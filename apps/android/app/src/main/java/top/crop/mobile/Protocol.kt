package top.crop.mobile

import org.bouncycastle.crypto.params.Ed25519PrivateKeyParameters
import org.bouncycastle.crypto.signers.Ed25519Signer
import org.json.JSONObject
import java.math.BigInteger
import java.net.URI
import java.security.MessageDigest
import java.time.Instant
import java.util.Base64

/** All bytes signed by this client pass through this protocol boundary. */
object Protocol {
    private val pkcs8Prefix = hexBytes("302e020100300506032b657004220420")
    private val ipnsPrefix = hexBytes("0172002408011220")
    fun decode(value: String): ByteArray = Base64.getDecoder().decode(value)
    fun encode(value: ByteArray): String = Base64.getEncoder().encodeToString(value)
    fun sha256(value: ByteArray): String = MessageDigest.getInstance("SHA-256").digest(value).joinToString("") { "%02x".format(it) }

    fun importPem(pem: String): ByteArray {
        val match = Regex("\\A\\s*-----BEGIN PRIVATE KEY-----\\s*([A-Za-z0-9+/=\\r\\n]+)\\s*-----END PRIVATE KEY-----\\s*\\z").matchEntire(pem)
            ?: error("Choose an unencrypted Ed25519 PKCS8 site key.")
        val der = decode(match.groupValues[1].replace(Regex("\\s"), ""))
        require(der.size == 48 && der.copyOfRange(0, 16).contentEquals(pkcs8Prefix)) { "This is not an Ed25519 PKCS8 site key." }
        return der
    }

    private fun key(der: ByteArray): Ed25519PrivateKeyParameters {
        require(der.size == 48 && der.copyOfRange(0, 16).contentEquals(pkcs8Prefix)) { "Invalid stored key." }
        return Ed25519PrivateKeyParameters(der, 16)
    }

    fun identity(der: ByteArray): String = "k" + BigInteger(1, ipnsPrefix + key(der).generatePublicKey().encoded).toString(36)
    fun sign(der: ByteArray, payload: ByteArray): String {
        val signer = Ed25519Signer()
        signer.init(true, key(der))
        signer.update(payload, 0, payload.size)
        return encode(signer.generateSignature())
    }

    fun origin(value: String): String {
        val uri = URI(value.trim().trimEnd('/'))
        require(uri.scheme == "https" && !uri.host.isNullOrEmpty() && uri.rawUserInfo == null && uri.rawQuery == null && uri.rawFragment == null && uri.rawPath.isNullOrEmpty()) { "Use an HTTPS publishing service address without a path or query." }
        return uri.toASCIIString()
    }

    fun challenge(origin: String, ipns: String, challenge: JSONObject, now: Long = Instant.now().epochSecond): ByteArray {
        val id = challenge.getString("id")
        val expires = challenge.getLong("expiresAt")
        require(id.matches(Regex("[A-Za-z0-9_-]{16,128}")) && expires > now && expires <= now + 600) { "The connection challenge expired. Check your phone clock and retry." }
        val expected = "croptop-mobile-session\n$origin\n$ipns\n$id\n$expires"
        require(challenge.getString("message") == expected) { "The service returned an invalid connection challenge." }
        return expected.toByteArray(Charsets.UTF_8)
    }

    data class Signatures(val proposalId: String, val recordSignature: String, val pushSignature: String)

    fun validateOperation(draft: Draft, operation: JSONObject) {
        require(operation.getString("id") == draft.id && operation.getString("postID") == draft.id && operation.getString("ipns") == draft.ipns) { "The publication destination or identity changed." }
        require(operation.getString("title") == draft.title && operation.getString("caption") == draft.caption) { "The prepared text differs from this draft." }
    }

    fun validateText(draft: Draft, config: JSONObject) {
        require(draft.title.toByteArray(Charsets.UTF_8).size <= config.getInt("maxTitleBytes")) { "The title is too long. Shorten it before publishing." }
        require(draft.caption.toByteArray(Charsets.UTF_8).size <= config.getInt("maxCaptionBytes")) { "The caption is too long. Shorten it before publishing." }
    }

    fun matchesParent(operation: JSONObject, site: JSONObject): Boolean {
        val proposal = operation.getJSONObject("proposal")
        val sequence = site.getString("sequence")
        require(sequence.matches(Regex("0|[1-9][0-9]{0,18}")) && sequence.toLong() < Long.MAX_VALUE && validCID(site.getString("cid"))) { "The site returned an invalid current publication." }
        return site.getString("ipns") == operation.getString("ipns") && proposal.getString("parent") == site.getString("cid") && proposal.getString("sequence") == (sequence.toLong() + 1).toString()
    }

    fun validateProposal(draft: Draft, operation: JSONObject, image: ByteArray, expectedHost: String, now: Long = Instant.now().epochSecond): Pair<ByteArray, ByteArray> {
        validateOperation(draft, operation)
        require(operation.getString("state") == "needs_signature") { "This publication is not ready to sign." }
        require(operation.getString("mediaType") in setOf("image/png", "image/jpeg", "image/webp")) { "The prepared preview is not a supported still image." }
        require(operation.getString("mediaSHA256") == sha256(image)) { "The image preview does not match the publication." }
        val p = operation.getJSONObject("proposal")
        val cid = p.getString("cid")
        require(validCID(cid) && validCID(p.getString("parent"))) { "Invalid publication content address." }
        val sequence = p.getString("sequence")
        require(sequence.matches(Regex("0|[1-9][0-9]{0,18}"))) { "Invalid publication sequence." }
        val seq = sequence.toLong()
        val time = p.getLong("time")
        require(p.getString("host") == expectedHost && expectedHost.isNotBlank() && !expectedHost.contains('\n')) { "Unexpected publishing host." }
        require(p.getLong("expiresAt") > now && p.getLong("expiresAt") <= time + 600 && time <= now + 60 && time >= now - 600) { "The publication expired. Check your phone clock and prepare it again." }
        require(p.getString("id").matches(Regex("[A-Za-z0-9_-]{16,128}"))) { "Invalid proposal identity." }
        val push = decode(p.getString("pushPayload"))
        require(push.contentEquals("croptop-push\n$expectedHost\n${draft.ipns}\n$cid\n$sequence\n$time".toByteArray(Charsets.UTF_8))) { "Invalid host authorization." }
        val record = decode(p.getString("recordPayload"))
        validateRecord(record, cid, seq, now, time, p.getLong("expiresAt"))
        return record to push
    }

    fun signProposal(der: ByteArray, draft: Draft, operation: JSONObject, image: ByteArray, host: String): Signatures {
        require(identity(der) == draft.ipns) { "Reconnect the original site for this draft." }
        val (record, push) = validateProposal(draft, operation, image, host)
        return Signatures(operation.getJSONObject("proposal").getString("id"), sign(der, record), sign(der, push))
    }

    // Boxo's DAG-CBOR v2 map: exact keys/types, canonical integer/length encoding,
    // fixed TTL and validity type. Unknown/duplicate fields and trailing data fail.
    fun validateRecord(payload: ByteArray, cid: String, sequence: Long, now: Long, proposalTime: Long = now, proposalExpiry: Long = now) {
        val prefix = "ipns-signature:".toByteArray(Charsets.UTF_8)
        require(payload.size in prefix.size + 1..4096 && payload.copyOfRange(0, prefix.size).contentEquals(prefix)) { "Invalid IPNS signing domain." }
        val cbor = Cbor(payload.copyOfRange(prefix.size, payload.size))
        require(cbor.length(5) == 5L) { "Unexpected IPNS record fields." }
        val expectedKeys = listOf("TTL", "Value", "Sequence", "Validity", "ValidityType")
        var validity = ""
        for (key in expectedKeys) {
            require(cbor.text() == key) { "Noncanonical IPNS record fields." }
            when (key) {
                "TTL" -> require(cbor.length(0) == 60_000_000_000L) { "Unexpected IPNS TTL." }
                "Value" -> require(cbor.bytes().contentEquals("/ipfs/$cid".toByteArray(Charsets.UTF_8))) { "IPNS record points to another publication." }
                "Sequence" -> require(cbor.length(0) == sequence) { "IPNS sequence mismatch." }
                "Validity" -> validity = String(cbor.bytes(), Charsets.UTF_8)
                "ValidityType" -> require(cbor.length(0) == 0L) { "Unsupported IPNS validity." }
            }
        }
        require(cbor.done()) { "Trailing IPNS record data." }
        val validUntil = Instant.parse(validity).epochSecond
        require(validUntil > maxOf(now, proposalExpiry) && validUntil <= proposalTime + 7200L * 3600 + 60) { "Invalid IPNS expiration." }
    }

    fun validCID(value: String): Boolean {
        if (!value.matches(Regex("b[a-z2-7]{58}"))) return false
        val alphabet = "abcdefghijklmnopqrstuvwxyz234567"
        val decoded = ArrayList<Int>()
        var bits = 0
        var buffer = 0
        for (character in value.substring(1)) {
            buffer = (buffer shl 5) or alphabet.indexOf(character)
            bits += 5
            if (bits >= 8) {
                bits -= 8
                decoded.add((buffer ushr bits) and 255)
                buffer = buffer and ((1 shl bits) - 1)
            }
        }
        return buffer == 0 && decoded.size == 36 && decoded[0] == 1 && decoded[1] in setOf(0x55, 0x70, 0x71) && decoded[2] == 0x12 && decoded[3] == 32
    }

    private class Cbor(private val bytes: ByteArray) {
        private var index = 0
        private fun byte(): Int { require(index < bytes.size) { "Truncated IPNS record." }; return bytes[index++].toInt() and 255 }
        fun length(type: Int): Long {
            val header = byte()
            require(header ushr 5 == type) { "Invalid IPNS field type." }
            val small = header and 31
            if (small < 24) return small.toLong()
            val count = when (small) { 24 -> 1; 25 -> 2; 26 -> 4; 27 -> 8; else -> error("Indefinite IPNS fields are forbidden.") }
            var value = 0L
            repeat(count) { require(value <= Long.MAX_VALUE ushr 8) { "IPNS integer overflow." }; value = (value shl 8) or byte().toLong() }
            val minimum = when (count) { 1 -> 24L; 2 -> 256L; 4 -> 65536L; else -> 4294967296L }
            require(value >= minimum) { "Noncanonical IPNS encoding." }
            return value
        }
        private fun data(type: Int): ByteArray {
            val length = length(type)
            require(length <= bytes.size - index) { "Truncated IPNS field." }
            return bytes.copyOfRange(index, index + length.toInt()).also { index += length.toInt() }
        }
        fun text() = String(data(3), Charsets.UTF_8)
        fun bytes() = data(2)
        fun done() = index == bytes.size
    }

    private fun hexBytes(value: String): ByteArray = value.chunked(2).map { it.toInt(16).toByte() }.toByteArray()
}
