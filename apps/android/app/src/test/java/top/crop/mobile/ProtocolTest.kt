package top.crop.mobile

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import java.nio.charset.StandardCharsets.UTF_8

class ProtocolTest {
    private val fixture: JSONObject = JSONObject(
        requireNotNull(javaClass.getResourceAsStream("/mobile-protocol-fixture.json")) {
            "The shared mobile protocol fixture must be included in test resources."
        }.bufferedReader().use { it.readText() },
    )
    private val clock = fixture.getLong("time")
    private val image = "retained screenshot bytes".toByteArray(UTF_8)
    private val draft = Draft(
        id = "AE62FABD-EF7B-4053-8AE4-423D40875A5C",
        ipns = fixture.getString("ipns"),
        origin = fixture.getString("origin"),
        title = "A screenshot",
        caption = "Shared from my phone",
        mime = "image/png",
        sourceSHA256 = Protocol.sha256(image),
    )

    private fun operation() = JSONObject()
        .put("id", draft.id).put("postID", draft.id).put("ipns", draft.ipns)
        .put("title", draft.title).put("caption", draft.caption)
        .put("state", "needs_signature").put("mediaSHA256", Protocol.sha256(image)).put("mediaType", "image/png")
        .put("proposal", JSONObject()
            .put("id", "mobile-fixture-proposal-00000001")
            .put("cid", fixture.getString("cid")).put("parent", fixture.getString("cid"))
            .put("host", fixture.getString("host")).put("sequence", fixture.getString("sequence"))
            .put("time", clock).put("expiresAt", fixture.getLong("expiresAt"))
            .put("recordPayload", fixture.getString("recordPayload"))
            .put("pushPayload", fixture.getString("pushPayload")))

    private fun validate(operation: JSONObject, bytes: ByteArray = image) =
        Protocol.validateProposal(draft, operation, bytes, fixture.getString("host"), clock)

    private fun rejected(block: () -> Unit) = assertThrows(Exception::class.java) { block() }

    @Test fun importedKeyMatchesSharedIdentityAndSignatures() {
        val der = Protocol.importPem(fixture.getString("privateKeyPEM"))
        assertEquals(fixture.getString("ipns"), Protocol.identity(der))
        val (record, push) = validate(operation())
        assertEquals(fixture.getString("recordSignature"), Protocol.sign(der, record))
        assertEquals(fixture.getString("pushSignature"), Protocol.sign(der, push))
    }

    @Test fun importedKeyRejectsAnotherAlgorithmWrongLengthAndWrappedText() {
        val pem = fixture.getString("privateKeyPEM")
        rejected { Protocol.importPem("unrelated text\n$pem") }
        rejected { Protocol.importPem(pem.replace("PRIVATE KEY", "ENCRYPTED PRIVATE KEY")) }
        val der = Protocol.importPem(pem)
        for (invalid in listOf(der.copyOf(47), der.copyOf(49), der.copyOf().also { it[11] = 0x71 })) {
            val candidate = "-----BEGIN PRIVATE KEY-----\n${Protocol.encode(invalid)}\n-----END PRIVATE KEY-----"
            rejected { Protocol.importPem(candidate) }
        }
    }

    @Test fun sessionChallengeMatchesExactCrossPlatformBytesAndSignature() {
        val challenge = fixture.getJSONObject("sessionChallenge")
        val payload = Protocol.challenge(draft.origin, draft.ipns, challenge, clock)
        assertEquals(challenge.getString("message"), String(payload, UTF_8))
        assertEquals(challenge.getString("signature"), Protocol.sign(Protocol.importPem(fixture.getString("privateKeyPEM")), payload))
    }

    @Test fun sessionChallengeRejectsExpiredReboundAndModifiedMessages() {
        val original = fixture.getJSONObject("sessionChallenge")
        rejected { Protocol.challenge(draft.origin, draft.ipns, original, original.getLong("expiresAt")) }
        rejected { Protocol.challenge("https://another.example", draft.ipns, original, clock) }
        rejected { Protocol.challenge(draft.origin, "another-site", original, clock) }
        rejected { Protocol.challenge(draft.origin, draft.ipns, JSONObject(original.toString()).put("message", original.getString("message") + "\n"), clock) }
        rejected { Protocol.challenge(draft.origin, draft.ipns, JSONObject(original.toString()).put("id", "bad\nid"), clock) }
        rejected { Protocol.challenge(draft.origin, draft.ipns, original, clock - 1) }
    }

    @Test fun serviceAddressRequiresHttpsOriginWithoutCredentialsOrOtherComponents() {
        assertEquals("https://crop.top", Protocol.origin(" https://crop.top/ "))
        assertEquals("https://crop.top:8443", Protocol.origin("https://crop.top:8443"))
        for (value in listOf("http://crop.top", "file:///site", "https://me@crop.top", "https://crop.top/path", "https://crop.top?key=private", "https://crop.top#secret")) {
            rejected { Protocol.origin(value) }
        }
    }

    @Test fun proposalRejectsChangedDraftDestinationIdentityAndText() {
        for ((field, replacement) in listOf("id" to "another", "postID" to "another", "ipns" to "another-site", "title" to "Changed title", "caption" to "Changed caption")) {
            rejected { validate(operation().put(field, replacement)) }
        }
    }

    @Test fun proposalParentMustMatchTheCurrentSiteAndNextSequence() {
        val current = JSONObject().put("ipns", draft.ipns).put("cid", fixture.getString("cid")).put("sequence", "41")
        assertTrue(Protocol.matchesParent(operation(), current))
        assertFalse(Protocol.matchesParent(operation(), JSONObject(current.toString()).put("sequence", "42")))
        assertFalse(Protocol.matchesParent(operation(), JSONObject(current.toString()).put("ipns", "another-site")))
        assertFalse(Protocol.matchesParent(operation(), JSONObject(current.toString()).put("cid", "bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku")))
        for (sequence in listOf("041", "-1", "9223372036854775807", "9223372036854775808")) {
            rejected { Protocol.matchesParent(operation(), JSONObject(current.toString()).put("sequence", sequence)) }
        }
    }

    @Test fun textLimitsCountUtf8BytesWithoutRejectingTheExactBoundary() {
        val config = JSONObject().put("maxTitleBytes", 2).put("maxCaptionBytes", 4)
        Protocol.validateText(draft.copy(title = "é", caption = "🌱"), config)
        Protocol.validateText(draft.copy(title = "", caption = ""), config)
        rejected { Protocol.validateText(draft.copy(title = "éa", caption = ""), config) }
        rejected { Protocol.validateText(draft.copy(title = "", caption = "🌱a"), config) }
    }

    @Test fun contentAddressesRequireACanonicalSupportedCidEnvelope() {
        val cid = fixture.getString("cid")
        assertTrue(Protocol.validCID(cid))
        assertTrue(Protocol.validCID("bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku"))
        for (invalid in listOf("b" + "a".repeat(58), cid.dropLast(1) + "b", cid + "a", cid.uppercase(), "https://crop.top/$cid")) {
            assertFalse(Protocol.validCID(invalid))
            val operation = operation()
            operation.getJSONObject("proposal").put("cid", invalid)
            rejected { validate(operation) }
        }
    }

    @Test fun proposalRejectsChangedImageAndWrongState() {
        rejected { validate(operation(), "a different image".toByteArray(UTF_8)) }
        rejected { validate(operation().put("mediaSHA256", "0".repeat(64))) }
        rejected { validate(operation().put("state", "published")) }
        for (mime in listOf("text/html", "image/svg+xml", "image/heic", "image/gif")) {
            rejected { validate(operation().put("mediaType", mime)) }
        }
    }

    @Test fun proposalRejectsChangedPushAndRecordPayloads() {
        for (field in listOf("recordPayload", "pushPayload")) {
            val operation = operation()
            val proposal = operation.getJSONObject("proposal")
            val payload = Protocol.decode(proposal.getString(field))
            proposal.put(field, Protocol.encode(payload + byteArrayOf(0)))
            rejected { validate(operation) }
        }
    }

    @Test fun proposalRejectsInvalidDestinationSequenceHostAndTimes() {
        val changes: List<Pair<String, Any>> = listOf(
            "cid" to "https://elsewhere.example", "parent" to "not-a-cid", "sequence" to "042",
            "sequence" to "-1", "sequence" to "9223372036854775808", "host" to "other.example",
            "time" to clock + 61, "time" to clock - 601, "expiresAt" to clock,
            "expiresAt" to clock + 601, "id" to "invalid\nproposal",
        )
        for ((field, value) in changes) {
            val operation = operation()
            operation.getJSONObject("proposal").put(field, value)
            rejected { validate(operation) }
        }
    }

    @Test fun cborRejectsTruncationTrailingBytesWrongDomainAndMapSize() {
        val original = Protocol.decode(fixture.getString("recordPayload"))
        val prefixLength = "ipns-signature:".toByteArray(UTF_8).size
        val invalid = listOf(
            original.copyOf(original.size - 1), original + byteArrayOf(0),
            original.copyOf().also { it[0] = 'x'.code.toByte() },
            original.copyOf().also { it[prefixLength] = 0xa4.toByte() },
            original.copyOf().also { it[prefixLength] = 0xa6.toByte() },
            original.copyOf().also { it[prefixLength] = 0xbf.toByte() },
        )
        for (payload in invalid) rejected { validateRecord(payload) }
    }

    @Test fun cborRejectsDuplicateUnknownReorderedAndNoncanonicalFields() {
        val original = Protocol.decode(fixture.getString("recordPayload"))
        // All replacements keep a syntactically readable CBOR value but violate
        // the signing boundary's exact schema or canonical byte representation.
        for (payload in listOf(
            replace(original, "Value".toByteArray(UTF_8), "Other".toByteArray(UTF_8)),
            replace(original, byteArrayOf(0x65) + "Value".toByteArray(UTF_8), byteArrayOf(0x63) + "TTL".toByteArray(UTF_8)),
            replace(original, "Sequence".toByteArray(UTF_8), "Validity".toByteArray(UTF_8)),
            replace(original, byteArrayOf(0x63) + "TTL".toByteArray(UTF_8), byteArrayOf(0x78, 0x03) + "TTL".toByteArray(UTF_8)),
            replace(original, byteArrayOf(0x18, 0x2a), byteArrayOf(0x19, 0x00, 0x2a)),
            replace(original, byteArrayOf(0x1b, 0, 0, 0, 0x0d, 0xf8.toByte(), 0x47, 0x58, 0), byteArrayOf(0x1b, 0, 0, 0, 0x0d, 0xf8.toByte(), 0x47, 0x58, 1)),
        )) rejected { validateRecord(payload) }
    }

    @Test fun cborRejectsRecordForAnotherCidSequenceOrValidity() {
        val payload = Protocol.decode(fixture.getString("recordPayload"))
        rejected { Protocol.validateRecord(payload, fixture.getString("cid") + "a", 42, clock, clock, fixture.getLong("expiresAt")) }
        rejected { Protocol.validateRecord(payload, fixture.getString("cid"), 43, clock, clock, fixture.getLong("expiresAt")) }
        for (date in listOf("2099-01-01T00:00:00Z", "2200-01-01T00:00:00Z")) {
            rejected { validateRecord(replace(payload, fixture.getString("recordValidity").toByteArray(UTF_8), date.toByteArray(UTF_8))) }
        }
        rejected { validateRecord(payload.copyOf().also { it[it.lastIndex] = 1 }) }
    }

    private fun validateRecord(payload: ByteArray) = Protocol.validateRecord(payload, fixture.getString("cid"), 42, clock, clock, fixture.getLong("expiresAt"))

    private fun replace(source: ByteArray, from: ByteArray, to: ByteArray): ByteArray {
        val offset = (0..source.size - from.size).firstOrNull { source.copyOfRange(it, it + from.size).contentEquals(from) }
        check(offset != null) { "Test fixture does not contain the expected mutation target." }
        return source.copyOfRange(0, offset) + to + source.copyOfRange(offset + from.size, source.size)
    }
}
