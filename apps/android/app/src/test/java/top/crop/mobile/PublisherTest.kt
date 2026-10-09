package top.crop.mobile

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.ByteArrayInputStream
import java.io.File
import java.io.IOException

class PublisherTest {
    @get:Rule val temporary = TemporaryFolder()
    private val site = Connection("k51qzi5uqu5dg9ufswxt229ntzdy7p4125xzv5rtyjso89ajdujg6csfxcj260", "https://crop.top")
    private val source = "retained screenshot bytes".toByteArray()

    private fun draft(store: DraftStore): Draft = store.intake(ByteArrayInputStream(source), "image/png", 1024)
        .copy(ipns = site.ipns, origin = site.origin, title = "Saved title", caption = "Saved caption")
        .also { store.save(it) }

    private fun signer(connection: Connection = site) = object : SiteSigner {
        override fun connection() = connection
        override fun <T> withKey(action: (ByteArray) -> T): T = error("Recovery must not require a signature.")
    }

    private fun operation(draft: Draft, state: String = "preparing"): JSONObject = JSONObject()
        .put("id", draft.id).put("postID", draft.id).put("ipns", draft.ipns)
        .put("title", draft.title).put("caption", draft.caption).put("state", state)
        .also { if (state == "published") it.put("url", "https://site.example/posts/${draft.id}/") }

    private class FakeTransport : MobileTransport {
        val requests = mutableListOf<String>()
        val uploaded = mutableListOf<Pair<Draft, ByteArray>>()
        var enabled = true
        var siteResponse = JSONObject()
        var operationResponse: JSONObject? = null
        var operationFailure: Exception? = null
        var uploadResponse: JSONObject? = null
        var prepareResponse: JSONObject? = null
        var loseUploadResponse = false
        override fun config(requireEnabled: Boolean): JSONObject {
            requests += "config:$requireEnabled"
            if (requireEnabled) require(enabled)
            return JSONObject().put("enabled", enabled).put("host", "crop.top").put("maxImageBytes", 1024)
                .put("maxTitleBytes", 1024).put("maxCaptionBytes", 16 * 1024).put("maxImagePixels", 40_000_000)
        }
        override fun site(): JSONObject { requests += "site"; return siteResponse }
        override fun operation(id: String): JSONObject {
            requests += "get:$id"
            operationFailure?.let { throw it }
            return operationResponse ?: throw ApiError(404, "Unknown publication")
        }
        override fun upload(draft: Draft, source: File): JSONObject {
            requests += "upload:${draft.id}"
            uploaded += draft to source.readBytes()
            val result = requireNotNull(uploadResponse)
            operationResponse = result // The server retained the operation before the response was lost.
            if (loseUploadResponse) throw IOException("Connection closed before the upload response arrived")
            return result
        }
        override fun prepare(id: String): JSONObject { requests += "prepare:$id"; return requireNotNull(prepareResponse) }
        override fun preview(id: String, maxBytes: Long): ByteArray = error("Unexpected preview")
        override fun commit(id: String, signatures: Protocol.Signatures): JSONObject = error("Unexpected commit")
    }

    private fun transport(draft: Draft) = FakeTransport().apply {
        siteResponse = JSONObject().put("ipns", draft.ipns).put("ready", true).put("enabled", true)
        uploadResponse = operation(draft)
    }

    @Test fun lostUploadResponseRecoversByIdentityAfterProcessRestartWithoutAnotherUpload() {
        val root = temporary.newFolder()
        val store = DraftStore(root)
        val original = draft(store)
        val api = transport(original).apply { loseUploadResponse = true }

        assertThrows(IOException::class.java) { Publisher(signer(), store, api).review(original) }
        val restoredStore = DraftStore(root)
        val retained = restoredStore.get(original.id)
        assertTrue(retained.submitted)
        assertEquals("unconfirmed", retained.state)
        api.requests.clear()

        val recovered = Publisher(signer(), restoredStore, api).review(retained)
        assertEquals("preparing", recovered.state)
        assertEquals(original.id, recovered.id)
        assertEquals(listOf("config:false", "get:${original.id}"), api.requests)
        assertEquals(1, api.uploaded.size)
        assertArrayEquals(source, restoredStore.source(original.id).readBytes())
    }

    @Test fun confirmedMissingOperationRetriesWithTheSameIdentityMetadataAndRetainedSource() {
        val root = temporary.newFolder()
        val store = DraftStore(root)
        val original = draft(store).copy(submitted = true).also { store.save(it) }
        val restored = DraftStore(root)
        val api = transport(original)

        val result = Publisher(signer(), restored, api).review(restored.get(original.id))
        assertEquals(listOf("config:false", "get:${original.id}", "site", "upload:${original.id}"), api.requests)
        val (sent, bytes) = api.uploaded.single()
        assertEquals(original.id, sent.id)
        assertEquals(original.ipns, sent.ipns)
        assertEquals(original.origin, sent.origin)
        assertEquals(original.title, sent.title)
        assertEquals(original.caption, sent.caption)
        assertEquals(original.sourceSHA256, sent.sourceSHA256)
        assertArrayEquals(source, bytes)
        assertEquals(original.id, result.id)
    }

    @Test fun failedStatusLookupDoesNotGuessMissingOrStartAnotherUpload() {
        for (failure in listOf(IOException("Network unavailable"), ApiError(500, "Server unavailable"), ApiError(401, "Session expired"))) {
            val store = DraftStore(temporary.newFolder())
            val original = draft(store).copy(submitted = true).also { store.save(it) }
            val api = transport(original).apply { operationFailure = failure }
            assertThrows(Exception::class.java) { Publisher(signer(), store, api).review(original) }
            assertEquals(listOf("config:false", "get:${original.id}"), api.requests)
            assertTrue(api.uploaded.isEmpty())
            assertEquals(original, store.get(original.id))
        }
    }

    @Test fun publishedOperationRecoversWhileNewPublicationsAreDisabled() {
        val store = DraftStore(temporary.newFolder())
        val original = draft(store).copy(submitted = true).also { store.save(it) }
        val api = transport(original).apply { enabled = false; operationResponse = operation(original, "published") }
        val recovered = Publisher(signer(), store, api).review(original)
        assertEquals("published", recovered.state)
        assertEquals("https://site.example/posts/${original.id}/", recovered.publishedURL)
        assertEquals(listOf("config:false", "get:${original.id}"), api.requests)
        assertTrue(api.uploaded.isEmpty())
        assertEquals(recovered, store.get(original.id))
    }

    @Test fun missingOrNewOperationDoesNotUploadWhileNewPublicationsAreDisabled() {
        for (submitted in listOf(false, true)) {
            val store = DraftStore(temporary.newFolder())
            val original = draft(store).copy(submitted = submitted).also { store.save(it) }
            val api = transport(original).apply { enabled = false }
            assertThrows(Exception::class.java) { Publisher(signer(), store, api).review(original) }
            assertTrue(api.uploaded.isEmpty())
            assertEquals(original, store.get(original.id))
        }
    }

    @Test fun switchingSitesOrServicesRejectsBeforeAnyNetworkRequest() {
        val store = DraftStore(temporary.newFolder())
        val original = draft(store)
        for (connection in listOf(site.copy(ipns = "another-site"), site.copy(origin = "https://other.example"))) {
            val api = transport(original)
            assertThrows(Exception::class.java) { Publisher(signer(connection), store, api).review(original) }
            assertThrows(Exception::class.java) { Publisher(signer(connection), store, api).publish(original) }
            assertTrue(api.requests.isEmpty())
        }
    }

    @Test fun recoveredOperationCannotReplaceTheOriginalDestinationOrText() {
        val store = DraftStore(temporary.newFolder())
        val original = draft(store).copy(submitted = true).also { store.save(it) }
        for ((field, value) in listOf("id" to "another-operation", "postID" to "another-post", "ipns" to "another-site", "title" to "changed", "caption" to "changed")) {
            val api = transport(original).apply { operationResponse = operation(original).put(field, value) }
            assertThrows(Exception::class.java) { Publisher(signer(), store, api).review(original) }
            assertEquals(original, store.get(original.id))
            assertTrue(api.uploaded.isEmpty())
        }
    }

    @Test fun staleParentRepreparesTheSameOperationBeforeThePhoneCanSign() {
        val store = DraftStore(temporary.newFolder())
        val original = draft(store).copy(submitted = true).also { store.save(it) }
        val cid = "bafkreigh2akiscaildcxk7zhwlm4conbh6x5xjvdcz5cmnz7x5e5ckihxa"
        val stale = operation(original, "needs_signature").put("proposal", JSONObject()
            .put("id", "stale-proposal-00000001").put("parent", cid).put("sequence", "42")
            .put("expiresAt", java.time.Instant.now().epochSecond + 300))
        val api = transport(original).apply {
            operationResponse = stale
            siteResponse.put("cid", cid).put("sequence", "42")
            prepareResponse = operation(original, "preparing")
        }
        val result = Publisher(signer(), store, api).review(original)
        assertEquals(original.id, result.id)
        assertEquals("preparing", result.state)
        assertEquals(1, api.requests.count { it == "prepare:${original.id}" })
        assertTrue(api.uploaded.isEmpty())
        assertArrayEquals(source, store.source(original.id).readBytes())
    }

    @Test fun confirmedExpiredUploadIsRetainedWithoutAutomaticPrepareOrReplacement() {
        val store = DraftStore(temporary.newFolder())
        val original = draft(store).copy(submitted = true).also { store.save(it) }
        val expired = operation(original, "failed").put("code", "draft_expired")
        val api = transport(original).apply { enabled = false; operationResponse = expired }
        val recovered = Publisher(signer(), store, api).review(original)
        assertEquals(original.id, recovered.id)
        assertEquals("failed", recovered.state)
        assertEquals(expired.toString(), recovered.operation)
        assertEquals(listOf("config:false", "get:${original.id}"), api.requests)
        assertEquals(1, store.all().size)
    }

    @Test fun undecodablePreviewCannotReachSigningOrCommit() {
        val store = DraftStore(temporary.newFolder())
        val original = draft(store).copy(submitted = true)
        val cid = "bafkreigh2akiscaildcxk7zhwlm4conbh6x5xjvdcz5cmnz7x5e5ckihxa"
        val pending = operation(original, "needs_signature").put("proposal", JSONObject()
            .put("id", "proposal-00000000000001").put("parent", cid).put("sequence", "42"))
        val retained = original.copy(operation = pending.toString())
        store.save(retained)
        val invalid = "not an image".toByteArray()
        store.savePreview(retained.id, invalid)
        val api = transport(retained).apply {
            operationResponse = pending
            siteResponse.put("cid", cid).put("sequence", "41")
        }
        var checked = false
        val publisher = Publisher(signer(), store, api) { bytes, maximumPixels ->
            checked = true
            assertArrayEquals(invalid, bytes)
            assertEquals(40_000_000L, maximumPixels)
            throw IllegalArgumentException("The preview cannot be decoded")
        }
        assertThrows(IllegalArgumentException::class.java) { publisher.publish(retained) }
        assertTrue(checked)
        assertFalse(store.get(retained.id).authorized)
        assertEquals(listOf("config:true", "get:${retained.id}", "site"), api.requests)
    }

    @Test fun newlyDiscoveredFailureStaysVisibleUntilAnExplicitRetry() {
        val store = DraftStore(temporary.newFolder())
        val original = draft(store).copy(submitted = true).also { store.save(it) }
        val failure = operation(original, "failed").put("code", "preparation_failed")
        val api = transport(original).apply {
            operationResponse = failure
            prepareResponse = operation(original, "preparing")
        }
        val first = Publisher(signer(), store, api).review(original)
        assertEquals("failed", first.state)
        assertEquals(listOf("config:false", "get:${original.id}"), api.requests)
        assertTrue(api.uploaded.isEmpty())

        api.requests.clear()
        val retried = Publisher(signer(), store, api).review(store.get(original.id))
        assertEquals("preparing", retried.state)
        assertEquals(original.id, retried.id)
        assertEquals(1, api.requests.count { it == "prepare:${original.id}" })
        assertTrue(api.uploaded.isEmpty())
    }
}
