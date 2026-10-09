package top.crop.mobile

import org.json.JSONObject
import java.math.BigInteger
import java.security.AlgorithmParameters
import java.security.KeyFactory
import java.security.KeyPair
import java.security.KeyPairGenerator
import java.security.MessageDigest
import java.security.interfaces.ECPublicKey
import java.security.spec.ECGenParameterSpec
import java.security.spec.ECParameterSpec
import java.security.spec.ECPoint
import java.security.spec.ECPublicKeySpec
import java.time.Instant
import java.util.Base64
import javax.crypto.Cipher
import javax.crypto.KeyAgreement
import javax.crypto.Mac
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

/** One ephemeral receiver for one pairing. No key material is sent to the relay. */
class PairingProtocol internal constructor(private val receiver: KeyPair, private val now: () -> Long) {
    constructor(now: () -> Long = { Instant.now().epochSecond }) : this(newKeyPair(), now)

    data class Claim(val transcript: String, val confirmationCode: String, val ipns: String, val expiresAt: Long)

    private data class Envelope(
        val origin: String, val id: String, val ipns: String, val expiresAt: Long,
        val senderPublicKey: String, val receiverPublicKey: String,
    ) {
        val transcript: String get() = listOf("croptop-pairing-v1", origin, id, ipns, expiresAt.toString(), senderPublicKey, receiverPublicKey).joinToString("\n")
    }

    val publicKey: String = Protocol.encode(sec1(receiver.public as ECPublicKey))
    private var claimed: Envelope? = null
    private var claimResult: Claim? = null
    private var encryptionKey: ByteArray? = null
    private var closed = false

    @Synchronized fun claim(origin: String, id: String, response: JSONObject): Claim {
        require(!closed) { "Connection setup was closed. Create a fresh Connect phone link." }
        val expectedOrigin = Protocol.origin(origin)
        require(origin == expectedOrigin && validIdentifier(id)) { "Invalid phone connection link." }
        require(response.getString("state") == "claimed") { "This phone connection is not ready for confirmation." }
        val envelope = envelope(response)
        require(envelope.origin == expectedOrigin && envelope.id == id) { "The phone connection destination changed." }
        require(envelope.receiverPublicKey == publicKey) { "Another phone claimed this connection. Create a new link." }
        validateExpiry(envelope.expiresAt)
        if (claimed != null) {
            require(envelope == claimed) { "The phone connection changed. Create a new link." }
            return requireNotNull(claimResult)
        }
        val peer = publicKey(envelope.senderPublicKey)
        val agreement = KeyAgreement.getInstance("ECDH")
        agreement.init(receiver.private)
        agreement.doPhase(peer, true)
        val secret = agreement.generateSecret()
        require(secret.size == 32) { "Invalid phone connection secret." }
        val transcriptBytes = envelope.transcript.toByteArray(Charsets.UTF_8)
        val salt = MessageDigest.getInstance("SHA-256").digest(transcriptBytes)
        val extracted = hmac(salt, secret)
        secret.fill(0)
        val key = expand(extracted, "croptop-pairing-key-v1", 32)
        val codeBytes = expand(extracted, "croptop-pairing-code-v1", 4)
        extracted.fill(0)
        val code = (BigInteger(1, codeBytes).toLong() % 100_000_000).toString().padStart(8, '0')
        val result = Claim(envelope.transcript, code, envelope.ipns, envelope.expiresAt)
        claimed = envelope
        encryptionKey = key
        claimResult = result
        return result
    }

    @Synchronized fun decrypt(response: JSONObject): String {
        require(!closed) { "Connection setup was closed. Create a fresh Connect phone link." }
        val expected = requireNotNull(claimed) { "Confirm the connection before importing its site key." }
        require(response.getString("state") == "consumed" && envelope(response) == expected) { "The encrypted phone connection changed." }
        validateExpiry(expected.expiresAt)
        val nonce = canonicalBase64(response.getString("nonce"))
        val ciphertext = canonicalBase64(response.getString("ciphertext"))
        require(nonce.size == 12 && ciphertext.size in 17..65_552) { "Invalid encrypted phone connection." }
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.DECRYPT_MODE, SecretKeySpec(requireNotNull(encryptionKey), "AES"), GCMParameterSpec(128, nonce))
        cipher.updateAAD(expected.transcript.toByteArray(Charsets.UTF_8))
        val plaintext = cipher.doFinal(ciphertext)
        val json = JSONObject(String(plaintext, Charsets.UTF_8))
        plaintext.fill(0)
        require(integer(json, "version") == 1L && json.getString("origin") == expected.origin && json.getString("ipns") == expected.ipns) { "The received key belongs to another site or service." }
        require(json.get("name") is String && json.get("pem") is String) { "Invalid site key envelope." }
        val pem = json.getString("pem")
        val der = Protocol.importPem(pem)
        try {
            require(Protocol.identity(der) == expected.ipns) { "The received key does not control this site." }
        } finally {
            der.fill(0)
        }
        return pem
    }

    @Synchronized fun close() {
        closed = true
        encryptionKey?.fill(0)
        encryptionKey = null
        claimed = null
        claimResult = null
    }

    private fun envelope(response: JSONObject): Envelope {
        val origin = response.getString("origin")
        val id = response.getString("id")
        val ipns = response.getString("ipns")
        val sender = response.getString("senderPublicKey")
        val receiver = response.getString("receiverPublicKey")
        require(Protocol.origin(origin) == origin && validIdentifier(id) && ipns.matches(Regex("k[0-9a-z]{40,100}"))) { "Invalid phone connection identity." }
        publicKey(sender)
        publicKey(receiver)
        return Envelope(origin, id, ipns, integer(response, "expiresAt"), sender, receiver)
    }

    private fun validateExpiry(expiresAt: Long) {
        val current = now()
        require(expiresAt > current && expiresAt <= current + 600) { "The phone connection expired. Create a new link." }
    }

    companion object {
        private fun newKeyPair(): KeyPair = KeyPairGenerator.getInstance("EC").apply { initialize(ECGenParameterSpec("secp256r1")) }.generateKeyPair()

        private fun parameters(): ECParameterSpec = AlgorithmParameters.getInstance("EC").apply { init(ECGenParameterSpec("secp256r1")) }.getParameterSpec(ECParameterSpec::class.java)

        private fun sec1(key: ECPublicKey): ByteArray {
            fun coordinate(value: BigInteger): ByteArray {
                val bytes = value.toByteArray()
                return if (bytes.size > 32) bytes.copyOfRange(bytes.size - 32, bytes.size) else ByteArray(32 - bytes.size) + bytes
            }
            return byteArrayOf(4) + coordinate(key.w.affineX) + coordinate(key.w.affineY)
        }

        private fun publicKey(value: String): ECPublicKey {
            val bytes = canonicalBase64(value)
            require(bytes.size == 65 && bytes[0] == 4.toByte()) { "Invalid phone connection public key." }
            val x = BigInteger(1, bytes.copyOfRange(1, 33))
            val y = BigInteger(1, bytes.copyOfRange(33, 65))
            val params = parameters()
            val p = (params.curve.field as java.security.spec.ECFieldFp).p
            require(x < p && y < p && y.multiply(y).mod(p) == x.multiply(x).multiply(x).add(params.curve.a.multiply(x)).add(params.curve.b).mod(p)) { "Invalid phone connection public key." }
            return KeyFactory.getInstance("EC").generatePublic(ECPublicKeySpec(ECPoint(x, y), params)) as ECPublicKey
        }

        private fun canonicalBase64(value: String): ByteArray = Protocol.decode(value).also {
            require(Protocol.encode(it) == value) { "Invalid phone connection encoding." }
        }

        private fun validIdentifier(value: String): Boolean = value.matches(Regex("[A-Za-z0-9_-]{43}")) &&
            runCatching { Base64.getUrlEncoder().withoutPadding().encodeToString(Base64.getUrlDecoder().decode(value)) == value }.getOrDefault(false)

        private fun integer(value: JSONObject, key: String): Long {
            val number = value.get(key)
            require(number is Int || number is Long) { "Invalid phone connection number." }
            return (number as Number).toLong()
        }

        private fun hmac(key: ByteArray, data: ByteArray): ByteArray = Mac.getInstance("HmacSHA256").run {
            init(SecretKeySpec(key, "HmacSHA256"))
            doFinal(data)
        }

        private fun expand(extracted: ByteArray, info: String, length: Int): ByteArray =
            hmac(extracted, info.toByteArray(Charsets.UTF_8) + byteArrayOf(1)).copyOf(length)
    }
}
