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

interface SiteSigner {
    fun connection(): Connection?
    fun <T> withKey(action: (ByteArray) -> T): T
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
    override fun connection(): Connection? {
        if (!file.exists()) return null
        val json = JSONObject(file.readText())
        return Connection(json.getString("ipns"), json.getString("origin"), json.optString("name"))
    }
    fun import(pem: String, origin: String): Connection {
        val der = Protocol.importPem(pem)
        try {
            val connection = Connection(Protocol.identity(der), Protocol.origin(origin))
            val cipher = Cipher.getInstance("AES/GCM/NoPadding")
            cipher.init(Cipher.ENCRYPT_MODE, wrappingKey())
            cipher.updateAAD("${connection.origin}\n${connection.ipns}".toByteArray(Charsets.UTF_8))
            val ciphertext = cipher.doFinal(der)
            DraftStore.atomicWrite(file, JSONObject().put("ipns", connection.ipns).put("origin", connection.origin)
                .put("iv", Protocol.encode(cipher.iv)).put("ciphertext", Protocol.encode(ciphertext)).toString().toByteArray())
            return connection
        } finally { der.fill(0) }
    }
    override fun <T> withKey(action: (ByteArray) -> T): T {
        val json = JSONObject(file.readText())
        val connection = connection() ?: error("Connect your site first.")
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, wrappingKey(), GCMParameterSpec(128, Protocol.decode(json.getString("iv"))))
        cipher.updateAAD("${connection.origin}\n${connection.ipns}".toByteArray(Charsets.UTF_8))
        val der = cipher.doFinal(Protocol.decode(json.getString("ciphertext")))
        try { require(Protocol.identity(der) == connection.ipns); return action(der) } finally { der.fill(0) }
    }
    fun remove() {
        require(!file.exists() || file.delete()) { "Could not remove the local connection." }
        keystore().deleteEntry(alias)
    }
}
