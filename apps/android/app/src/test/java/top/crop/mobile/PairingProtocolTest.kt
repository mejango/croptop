package top.crop.mobile

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import java.math.BigInteger
import java.security.AlgorithmParameters
import java.security.KeyFactory
import java.security.KeyPair
import java.security.spec.ECGenParameterSpec
import java.security.spec.ECParameterSpec
import java.security.spec.ECPoint
import java.security.spec.ECPrivateKeySpec
import java.security.spec.ECPublicKeySpec
import javax.crypto.Cipher
import javax.crypto.spec.GCMParameterSpec
import javax.crypto.spec.SecretKeySpec

class PairingProtocolTest {
    private val fixture = JSONObject(requireNotNull(javaClass.getResourceAsStream("/mobile-pairing-v1.json")).bufferedReader().use { it.readText() })
    private val info = fixture.getJSONObject("info")
    private var clock = 2_000_000_000L

    private fun receiver(): PairingProtocol {
        val params = AlgorithmParameters.getInstance("EC").apply { init(ECGenParameterSpec("secp256r1")) }.getParameterSpec(ECParameterSpec::class.java)
        val keyFactory = KeyFactory.getInstance("EC")
        val privateKey = keyFactory.generatePrivate(ECPrivateKeySpec(BigInteger(1, Protocol.decode(fixture.getString("receiverPrivateKey"))), params))
        val sec1 = Protocol.decode(info.getString("receiverPublicKey"))
        val publicKey = keyFactory.generatePublic(ECPublicKeySpec(ECPoint(BigInteger(1, sec1.copyOfRange(1, 33)), BigInteger(1, sec1.copyOfRange(33, 65))), params))
        return PairingProtocol(KeyPair(publicKey, privateKey)) { clock }
    }

    private fun claimJSON() = JSONObject(info.toString()).put("state", "claimed")
    private fun response() = JSONObject(info.toString()).put("nonce", fixture.getString("nonce")).put("ciphertext", fixture.getString("ciphertext"))
    private fun claim(protocol: PairingProtocol, json: JSONObject = claimJSON()) = protocol.claim(info.getString("origin"), info.getString("id"), json)
    private fun rejected(block: () -> Unit) = assertThrows(Exception::class.java) { block() }

    @Test fun crossPlatformFixtureMatchesPublicKeyTranscriptCodeAndDecryptedSite() {
        val protocol = receiver()
        assertEquals(info.getString("receiverPublicKey"), protocol.publicKey)
        val claimed = claim(protocol)
        assertEquals(fixture.getString("transcript"), claimed.transcript)
        assertEquals(fixture.getString("confirmationCode"), claimed.confirmationCode)
        assertEquals(info.getString("ipns"), claimed.ipns)
        assertEquals(info.getLong("expiresAt"), claimed.expiresAt)
        val pem = protocol.decrypt(response())
        assertEquals(JSONObject(fixture.getString("plaintext")).getString("pem"), pem)
        assertEquals(claimed.ipns, Protocol.identity(Protocol.importPem(pem)))
    }

    @Test fun newlyGeneratedReceiversUseIndependentUncompressedP256Keys() {
        val first = PairingProtocol().publicKey
        val second = PairingProtocol().publicKey
        assertNotEquals(first, second)
        val raw = Protocol.decode(first)
        assertEquals(65, raw.size)
        assertEquals(4.toByte(), raw[0])
    }

    @Test fun repeatedClaimIsIdempotentButCannotReplaceAClaimedTranscript() {
        val protocol = receiver()
        assertEquals(claim(protocol), claim(protocol))
        rejected { claim(protocol, claimJSON().put("expiresAt", clock + 599)) }
        rejected { claim(protocol, claimJSON().put("ipns", info.getString("ipns").dropLast(1) + "a")) }
    }

    @Test fun closedReceiverCannotReclaimOrDecryptAfterCancellation() {
        val protocol = receiver()
        claim(protocol)
        protocol.close()
        rejected { claim(protocol) }
        rejected { protocol.decrypt(response()) }
        val unopened = receiver()
        unopened.close()
        rejected { claim(unopened) }
    }

    @Test fun claimRejectsAnotherOriginIdReceiverOrState() {
        for ((field, value) in listOf(
            "origin" to "https://other.example", "id" to "B" + "A".repeat(42),
            "receiverPublicKey" to info.getString("senderPublicKey"), "state" to "waiting",
        )) rejected { claim(receiver(), claimJSON().put(field, value)) }
        rejected { receiver().claim("https://app.crop.top/", info.getString("id"), claimJSON()) }
        rejected { receiver().claim(info.getString("origin"), "../pairing", claimJSON()) }
    }

    @Test fun claimRejectsExpiredFarFutureAndNonintegerExpiry() {
        for (expiry in listOf<Any>(clock, clock - 1, clock + 601, (clock + 600).toString(), clock + 600.5)) {
            rejected { claim(receiver(), claimJSON().put("expiresAt", expiry)) }
        }
    }

    @Test fun claimRejectsMalformedNoncanonicalAndOffCurvePublicKeys() {
        val invalid = listOf(
            info.getString("senderPublicKey").trimEnd('='),
            Protocol.encode(byteArrayOf(4) + ByteArray(64)),
            Protocol.encode(byteArrayOf(4) + ByteArray(64) { 0xff.toByte() }),
            Protocol.encode(byteArrayOf(2) + ByteArray(32)),
            "not base64",
        )
        for (key in invalid) rejected { claim(receiver(), claimJSON().put("senderPublicKey", key)) }
    }

    @Test fun decryptionRequiresClaimAndAnUnchangedEnvelope() {
        rejected { receiver().decrypt(response()) }
        val changes: List<Pair<String, Any>> = listOf(
            "origin" to "https://other.example", "id" to "B" + "A".repeat(42),
            "ipns" to info.getString("ipns").dropLast(1) + "a", "expiresAt" to clock + 599,
            "senderPublicKey" to info.getString("receiverPublicKey"),
            "receiverPublicKey" to info.getString("senderPublicKey"), "state" to "ready",
        )
        for ((field, value) in changes) {
            val protocol = receiver()
            claim(protocol)
            rejected { protocol.decrypt(response().put(field, value)) }
        }
    }

    @Test fun expiredClaimCannotDecryptEvenAnAuthenticResponse() {
        val protocol = receiver()
        claim(protocol)
        clock = info.getLong("expiresAt")
        rejected { protocol.decrypt(response()) }
    }

    @Test fun decryptionRejectsModifiedCiphertextAndNonceAndBadSizes() {
        val modified = Protocol.decode(fixture.getString("ciphertext")).also { it[0] = (it[0].toInt() xor 1).toByte() }
        for ((field, value) in listOf(
            "ciphertext" to Protocol.encode(modified), "ciphertext" to Protocol.encode(ByteArray(16)),
            "ciphertext" to Protocol.encode(ByteArray(65_553)), "nonce" to Protocol.encode(ByteArray(12)),
            "nonce" to Protocol.encode(ByteArray(11)), "ciphertext" to "bad base64",
        )) {
            val protocol = receiver()
            claim(protocol)
            rejected { protocol.decrypt(response().put(field, value)) }
        }
    }

    @Test fun authenticatedPlaintextStillRequiresSupportedVersionDestinationAndKeyIdentity() {
        val plaintext = JSONObject(fixture.getString("plaintext"))
        val anotherKey = "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f\n-----END PRIVATE KEY-----\n"
        for ((field, value) in listOf(
            "version" to 2, "version" to "1", "origin" to "https://other.example",
            "ipns" to info.getString("ipns").dropLast(1) + "a", "pem" to anotherKey,
            "pem" to "not a private key", "name" to 42,
        )) {
            val protocol = receiver()
            claim(protocol)
            val invalid = JSONObject(plaintext.toString()).put(field, value)
            rejected { protocol.decrypt(encrypt(invalid)) }
        }
    }

    /** Sender-side encryption uses the independently generated fixture AES key. */
    private fun encrypt(plaintext: JSONObject): JSONObject {
        val nonce = Protocol.decode(fixture.getString("nonce"))
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        cipher.init(Cipher.ENCRYPT_MODE, SecretKeySpec(Protocol.decode(fixture.getString("aesKey")), "AES"), GCMParameterSpec(128, nonce))
        cipher.updateAAD(fixture.getString("transcript").toByteArray(Charsets.UTF_8))
        return response().put("ciphertext", Protocol.encode(cipher.doFinal(plaintext.toString().toByteArray(Charsets.UTF_8))))
    }
}
