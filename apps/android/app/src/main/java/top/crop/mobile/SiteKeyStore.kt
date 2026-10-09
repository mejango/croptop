package top.crop.mobile

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import org.json.JSONObject
import java.io.File
import java.security.KeyStore
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

data class Connection(val ipns: String, val origin: String, val name: String = "")
data class ConnectionSnapshot(val connection: Connection?, val generation: Long)

interface SiteSigner {
    fun connection(): Connection?
    fun <T> withKey(action: (ByteArray) -> T): T
    fun snapshot(): ConnectionSnapshot = ConnectionSnapshot(connection(), 0)
    fun requireCurrent(expected: ConnectionSnapshot) {
        require(snapshot() == expected) { "The site connection changed. Reopen the saved draft and confirm its destination." }
    }
}

/** The imported Ed25519 key is encrypted by a non-exportable Android Keystore AES key. */
class SiteKeyStore(context: Context) : SiteSigner {
    private val file = File(context.noBackupFilesDir, "site-key.json")
    private val alias = "top.crop.mobile.site-key.v1"
    private fun keystore() = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
    private fun wrappingKey(): SecretKey {
        val store = keystore()
        (store.getKey(alias, null) as? SecretKey)?.let { return it }
        val generator = KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore")
        generator.init(KeyGenParameterSpec.Builder(alias, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
            .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
            .setKeySize(256).setRandomizedEncryptionRequired(true).build())
        return generator.generateKey()
    }
    override fun connection(): Connection? = synchronized(keyLock) {
        if (!file.exists()) return@synchronized null
        val json = JSONObject(file.readText())
        Connection(json.getString("ipns"), json.getString("origin"), json.optString("name"))
    }
    override fun snapshot(): ConnectionSnapshot = synchronized(keyLock) { ConnectionSnapshot(connection(), generation) }
    fun import(pem: String, origin: String, expected: ConnectionSnapshot = snapshot()): Connection = synchronized(keyLock) {
        requireCurrent(expected)
        PublishingService.requireOrigin(origin)
        val der = Protocol.importPem(pem)
        try {
            val connection = Connection(Protocol.identity(der), Protocol.origin(origin))
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.ENCRYPT_MODE, wrappingKey())
            cipher.updateAAD("${connection.origin}\n${connection.ipns}".toByteArray(Charsets.UTF_8))
            val ciphertext = cipher.doFinal(der)
            DraftStore.atomicWrite(file, JSONObject().put("ipns", connection.ipns).put("origin", connection.origin)
                .put("iv", Protocol.encode(cipher.iv)).put("ciphertext", Protocol.encode(ciphertext)).toString().toByteArray())
            generation++
            connection
        } finally { der.fill(0) }
    }
    override fun <T> withKey(action: (ByteArray) -> T): T = synchronized(keyLock) {
        val json = JSONObject(file.readText())
        val connection = connection() ?: error("Connect your site first.")
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, wrappingKey(), GCMParameterSpec(128, Protocol.decode(json.getString("iv"))))
        cipher.updateAAD("${connection.origin}\n${connection.ipns}".toByteArray(Charsets.UTF_8))
        val der = cipher.doFinal(Protocol.decode(json.getString("ciphertext")))
        try { require(Protocol.identity(der) == connection.ipns); action(der) } finally { der.fill(0) }
    }
    fun remove(expected: ConnectionSnapshot = snapshot()) = synchronized(keyLock) {
        requireCurrent(expected)
        require(!file.exists() || file.delete()) { "Could not remove the local connection." }
        keystore().deleteEntry(alias)
        generation++
    }
    companion object {
        // Every Activity in this single-process app shares the mutation/signing boundary.
        // A process restart also removes all stale UI callbacks, so no persisted counter is needed.
        private val keyLock = Any()
        private var generation = 0L
    }
}
