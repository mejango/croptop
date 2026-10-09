package top.crop.mobile

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Rule
import org.junit.Test
import org.junit.rules.TemporaryFolder
import java.io.ByteArrayInputStream
import java.io.IOException
import java.io.InputStream

class DraftStoreTest {
    @get:Rule val temporary = TemporaryFolder()
    private val image = "image retained before the original URI expires".toByteArray()

    @Test fun retryKeepsIdentitySourceAndSubmittedStateAcrossStoreRecreation() {
        val root = temporary.newFolder()
        val initialStore = DraftStore(root)
        val draft = initialStore.intake(ByteArrayInputStream(image), "image/png", image.size.toLong())
        val submitted = draft.copy(ipns = "the-original-site", origin = "https://crop.top", title = "Title", caption = "Caption", submitted = true, error = "Connection interrupted")
        initialStore.save(submitted)

        val restoredStore = DraftStore(root)
        val restored = restoredStore.get(draft.id)
        assertEquals(submitted, restored)
        assertEquals("unconfirmed", restored.state)
        assertEquals(draft.id, restoredStore.all().single().id)
        assertArrayEquals(image, restoredStore.source(restored.id).readBytes())
        assertEquals(Protocol.sha256(image), restored.sourceSHA256)
        restoredStore.verifySource(restored)

        val operation = JSONObject().put("id", restored.id).put("state", "needs_signature").toString()
        restoredStore.save(restored.copy(operation = operation, error = ""))
        assertEquals(draft.id, DraftStore(root).get(draft.id).id)
        assertEquals("needs_signature", DraftStore(root).get(draft.id).state)
    }

    @Test fun redeliveredImageReopensExistingUnpublishedDraftWithoutLosingText() {
        val root = temporary.newFolder()
        val store = DraftStore(root)
        val first = store.intake(ByteArrayInputStream(image), "image/png", 1024)
        val retained = first.copy(title = "Saved title", caption = "Saved caption", submitted = true)
        store.save(retained)
        val duplicate = DraftStore(root).intake(ByteArrayInputStream(image), "image/png", 1024)
        assertEquals(retained, duplicate)
        assertEquals(1, store.all().size)
        assertEquals(1, root.listFiles()!!.size)
        assertArrayEquals(image, store.source(first.id).readBytes())
    }

    @Test fun previouslyPublishedImageCanBecomeANewPost() {
        val store = DraftStore(temporary.newFolder())
        val first = store.intake(ByteArrayInputStream(image), "image/png", 1024)
        store.save(first.copy(submitted = true, operation = JSONObject().put("state", "published").put("url", "https://site.example/post").toString()))
        val next = store.intake(ByteArrayInputStream(image), "image/png", 1024)
        assertNotEquals(first.id, next.id)
        assertEquals("draft", next.state)
        assertEquals(2, store.all().size)
        assertEquals("https://site.example/post", store.get(first.id).publishedURL)
    }

    @Test fun boundsRejectEmptyOversizeAndUnsupportedIntakeWithoutLeavingFiles() {
        for ((bytes, mime, limit) in listOf(
            Triple(byteArrayOf(), "image/png", 1024L),
            Triple(image, "image/png", image.size.toLong() - 1),
            Triple(image, "application/pdf", 1024L),
        )) {
            val root = temporary.newFolder()
            val store = DraftStore(root)
            assertThrows(Exception::class.java) { store.intake(ByteArrayInputStream(bytes), mime, limit) }
            assertTrue(store.all().isEmpty())
            assertTrue(root.listFiles()!!.isEmpty())
        }
    }

    @Test fun interruptedInputDoesNotCreateAPartialRecoverableDraft() {
        val root = temporary.newFolder()
        val store = DraftStore(root)
        val broken = object : InputStream() {
            private var reads = 0
            override fun read(): Int = if (reads++ < 5) 1 else throw IOException("Source permission expired")
        }
        assertThrows(IOException::class.java) { store.intake(broken, "image/jpeg", 1024) }
        assertTrue(store.all().isEmpty())
        assertTrue(root.listFiles()!!.isEmpty())
    }

    @Test fun sourceTamperingAndMissingSourceFailBeforeUpload() {
        val store = DraftStore(temporary.newFolder())
        val draft = store.intake(ByteArrayInputStream(image), "image/png", 1024)
        store.source(draft.id).writeBytes("changed bytes".toByteArray())
        assertThrows(Exception::class.java) { store.verifySource(draft) }
        assertTrue(store.source(draft.id).delete())
        assertThrows(Exception::class.java) { store.verifySource(draft) }
    }

    @Test fun previewAndDraftUpdatesSurviveAReopenWithoutTemporaryFiles() {
        val root = temporary.newFolder()
        val store = DraftStore(root)
        val draft = store.intake(ByteArrayInputStream(image), "image/heic", 1024)
        store.savePreview(draft.id, "first preview".toByteArray())
        store.savePreview(draft.id, "updated preview".toByteArray())
        store.save(draft.copy(title = "Updated title"))
        val reopened = DraftStore(root)
        assertEquals("Updated title", reopened.get(draft.id).title)
        assertEquals("updated preview", reopened.preview(draft.id).readText())
        assertFalse(root.walkTopDown().any { it.name.endsWith(".new") })
    }

    @Test fun draftIdsCannotEscapeOwnedStorage() {
        val store = DraftStore(temporary.newFolder())
        for (id in listOf("../another", "/tmp/another", "lowercase-invalid", "")) {
            assertThrows(Exception::class.java) { store.source(id) }
            assertThrows(Exception::class.java) { store.get(id) }
            assertThrows(Exception::class.java) { store.save(Draft(id = id)) }
        }
    }

    @Test fun corruptDraftMetadataDoesNotBlockOtherDraftsOrDestroyRecoverableFiles() {
        val root = temporary.newFolder()
        val store = DraftStore(root)
        val corrupt = store.intake(ByteArrayInputStream(image), "image/png", 1024)
        val metadata = java.io.File(store.source(corrupt.id).parentFile, "draft.json")
        metadata.writeText("{interrupted metadata")
        val next = store.intake(ByteArrayInputStream("another screenshot".toByteArray()), "image/png", 1024)
        assertEquals(listOf(next.id), store.all().map { it.id })
        assertEquals(1, store.corruptedCount())
        assertArrayEquals(image, store.source(corrupt.id).readBytes())
        assertEquals("{interrupted metadata", metadata.readText())

        metadata.writeText(corrupt.copy(operation = "invalid operation json").json().toString())
        assertEquals(1, store.corruptedCount())
        metadata.writeText(corrupt.json().toString())
        assertEquals(0, store.corruptedCount())
        assertEquals(2, store.all().size)
    }

    @Test fun confirmedExpiredDraftCreatesAFreshIdentityAndPreservesOriginalReceiptAndSource() {
        val store = DraftStore(temporary.newFolder())
        val initial = store.intake(ByteArrayInputStream(image), "image/png", 1024)
            .copy(ipns = "original-site", origin = "https://crop.top", title = "Original title", caption = "Original caption", submitted = true)
        val receipt = JSONObject().put("id", initial.id).put("postID", initial.id).put("ipns", initial.ipns)
            .put("title", initial.title).put("caption", initial.caption).put("state", "failed").put("code", "draft_expired")
        val expired = initial.copy(operation = receipt.toString(), error = "Draft retention expired")
        store.save(expired)

        val renewed = store.renewExpired(expired)
        assertNotEquals(expired.id, renewed.id)
        assertEquals(expired.ipns, renewed.ipns)
        assertEquals(expired.origin, renewed.origin)
        assertEquals(expired.title, renewed.title)
        assertEquals(expired.caption, renewed.caption)
        assertEquals(expired.mime, renewed.mime)
        assertEquals(expired.sourceSHA256, renewed.sourceSHA256)
        assertFalse(renewed.submitted)
        assertEquals("", renewed.operation)
        assertEquals("draft", renewed.state)
        assertArrayEquals(image, store.source(renewed.id).readBytes())
        assertArrayEquals(image, store.source(expired.id).readBytes())
        assertEquals(expired, store.get(expired.id))
        assertEquals(2, store.all().size)
    }

    @Test fun genericFailuresAndUncertainPublicationNeverCreateANewIdentity() {
        for ((state, code) in listOf("failed" to "publish_failed", "committing" to "draft_expired", "published" to "draft_expired", "needs_signature" to "")) {
            val store = DraftStore(temporary.newFolder())
            val initial = store.intake(ByteArrayInputStream(image), "image/png", 1024)
            val operation = JSONObject().put("state", state).put("code", code).toString()
            val retained = initial.copy(submitted = true, operation = operation)
            store.save(retained)
            assertThrows(Exception::class.java) { store.renewExpired(retained) }
            assertEquals(1, store.all().size)
            assertEquals(retained, store.get(initial.id))
        }
        val store = DraftStore(temporary.newFolder())
        val initial = store.intake(ByteArrayInputStream(image), "image/png", 1024)
        val uncertain = initial.copy(submitted = true)
        store.save(uncertain)
        assertThrows(Exception::class.java) { store.renewExpired(uncertain) }
        assertEquals(1, store.all().size)
    }

    @Test fun unsignedRejectedPreparationCanBecomeEditableWhileKeepingTheOriginalReceipt() {
        val store = DraftStore(temporary.newFolder())
        val initial = store.intake(ByteArrayInputStream(image), "image/png", 1024)
        val rejected = initial.copy(title = "Saved title", submitted = true,
            operation = JSONObject().put("state", "failed").put("code", "preparation_failed").toString())
        store.save(rejected)
        val editable = store.reviseRejected(rejected)
        assertNotEquals(rejected.id, editable.id)
        assertEquals(rejected.title, editable.title)
        assertFalse(editable.submitted)
        assertFalse(editable.authorized)
        assertEquals("draft", editable.state)
        assertArrayEquals(image, store.source(editable.id).readBytes())
        assertEquals(rejected, store.get(rejected.id))
    }

    @Test fun aSigningAttemptOrOutstandingProposalPreventsAnEditableReplacement() {
        val failed = JSONObject().put("state", "failed")
        for (case in 0..3) {
            val store = DraftStore(temporary.newFolder())
            val initial = store.intake(ByteArrayInputStream(image), "image/png", 1024)
            val retained = when (case) {
                0 -> initial.copy(submitted = true, authorized = true, operation = failed.toString())
                1 -> initial.copy(submitted = true, operation = JSONObject(failed.toString()).put("proposal", JSONObject().put("id", "still-signable")).toString())
                2 -> initial.copy(submitted = true, operation = JSONObject().put("state", "committing").toString())
                else -> initial.copy(submitted = true)
            }
            store.save(retained)
            // A stale in-memory copy cannot weaken the authoritative on-disk decision.
            assertThrows(Exception::class.java) { store.reviseRejected(retained.copy(authorized = false)) }
            assertEquals(1, store.all().size)
            assertEquals(retained, store.get(initial.id))
        }
    }

    @Test fun staleStoreInstanceCannotChangeAnAlreadySubmittedBody() {
        val root = temporary.newFolder()
        val first = DraftStore(root)
        val second = DraftStore(root)
        val draft = first.intake(ByteArrayInputStream(image), "image/png", 1024)
        val stale = second.get(draft.id)
        val submitted = draft.copy(submitted = true, ipns = "original-site", origin = "https://crop.top", title = "Submitted title")
        first.save(submitted)
        assertThrows(Exception::class.java) { second.save(stale.copy(title = "Stale edit")) }
        for (changed in listOf(submitted.copy(title = "Another title"), submitted.copy(caption = "Another caption"), submitted.copy(ipns = "another-site"), submitted.copy(origin = "https://other.example"), submitted.copy(sourceSHA256 = "another-image"))) {
            assertThrows(Exception::class.java) { second.save(changed) }
        }
        assertEquals(submitted, first.get(draft.id))
    }

    @Test fun staleStoreInstanceCannotRollBackSigningAuthorization() {
        val root = temporary.newFolder()
        val first = DraftStore(root)
        val second = DraftStore(root)
        val submitted = first.intake(ByteArrayInputStream(image), "image/png", 1024).copy(submitted = true)
        first.save(submitted)
        val stale = second.get(submitted.id)
        first.save(submitted.copy(authorized = true))
        second.save(stale.copy(error = "A delayed network response arrived"))
        assertTrue(first.get(submitted.id).authorized)
        assertEquals("A delayed network response arrived", first.get(submitted.id).error)
    }

    @Test fun staleStoreInstanceCannotReplaceAPublishedReceiptWithAPendingState() {
        val root = temporary.newFolder()
        val first = DraftStore(root)
        val second = DraftStore(root)
        val submitted = first.intake(ByteArrayInputStream(image), "image/png", 1024).copy(submitted = true)
        first.save(submitted)
        val stale = second.get(submitted.id)
        val published = submitted.copy(authorized = true, operation = JSONObject().put("state", "published").put("url", "https://site.example/post").toString())
        first.save(published)
        assertThrows(Exception::class.java) { second.save(stale) }
        assertEquals(published, second.get(submitted.id))
    }

    @Test fun simultaneousSharesAcrossStoreInstancesRetainOnlyOneIdentity() {
        val root = temporary.newFolder()
        val start = java.util.concurrent.CyclicBarrier(2)
        val executor = java.util.concurrent.Executors.newFixedThreadPool(2)
        try {
            val futures = (1..2).map {
                executor.submit<Draft> {
                    start.await(5, java.util.concurrent.TimeUnit.SECONDS)
                    DraftStore(root).intake(ByteArrayInputStream(image), "image/png", 1024)
                }
            }
            val results = futures.map { it.get(5, java.util.concurrent.TimeUnit.SECONDS) }
            assertEquals(results[0].id, results[1].id)
            assertEquals(1, DraftStore(root).all().size)
            assertEquals(1, root.listFiles()!!.size)
        } finally { executor.shutdownNow() }
    }
}
