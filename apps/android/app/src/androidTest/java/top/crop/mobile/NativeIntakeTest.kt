@file:Suppress("DEPRECATION", "OVERRIDE_DEPRECATION")

package top.crop.mobile

import android.content.Intent
import android.app.AlertDialog
import android.graphics.Rect
import android.net.Uri
import android.os.Build
import android.test.InstrumentationTestCase
import android.view.KeyEvent
import android.view.WindowInsets
import android.view.View
import android.text.InputType
import android.view.inputmethod.EditorInfo
import android.view.inputmethod.InputMethodManager
import android.widget.EditText
import android.widget.ScrollView
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit

/** Offline acceptance checks against the real Activity, URI resolver, storage, and Android UI. */
class NativeIntakeTest : InstrumentationTestCase() {
    private lateinit var session: NativeTestSession
    override fun setUp() { super.setUp(); session = NativeTestSession(instrumentation) }
    override fun tearDown() { try { session.close() } finally { super.tearDown() } }

    fun testSharedImageIsOwnedAndRepeatedIntentKeepsDraftIdentity() {
        val share = session.share()
        session.launch(share)
        val draft = session.retained()
        assertTrue(session.store.source(draft.id).readBytes().contentEquals(session.source.readBytes()))
        assertEquals("draft", draft.state)
        assertEquals("", draft.ipns)
        assertNotNull(session.text("Connect your site"))
        assertNull(requireNotNull(session.activity).intent.data)
        assertNull(requireNotNull(session.activity).intent.clipData)
        assertNull(requireNotNull(session.activity).intent.extras)
        session.edit("Retain this caption", "Synthetic screenshot")

        // A second real start delivers onNewIntent because the Activity is singleTop.
        val opened = SyntheticShareProvider.openCount.get()
        session.main { session.context.startActivity(Intent(share).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP)) }
        session.await("Redelivered share was never received") { SyntheticShareProvider.openCount.get() > opened }
        session.await("Redelivered share did not finish") { session.text("Screenshot saved. Connect your site to publish it.") != null }
        assertEquals(listOf(draft.id), session.store.all().filter { it.id !in session.originalIds }.map { it.id })
        assertEquals("Retain this caption", session.store.get(draft.id).caption)
        assertEquals("Synthetic screenshot", session.store.get(draft.id).title)

        assertTrue(session.source.delete()) // The sending app's URI is now unavailable.
        session.recreate()
        assertEquals("Retain this caption", session.input("Add a caption (optional)").text.toString())
        session.store.verifySource(session.store.get(draft.id))
    }

    fun testClipDataShareAndBackFromSetupKeepScreenshot() {
        session.launch(session.share(clipOnly = true))
        val draft = session.retained()
        session.edit("A draft before setup", "Saved through setup")
        session.click("Open connection link")
        session.await("Connection dialog did not receive focus") {
            var dialogFocused = false
            session.main { dialogFocused = !requireNotNull(session.activity).hasWindowFocus() }
            dialogFocused
        }
        instrumentation.sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
        session.await("Setup cancel did not return to composer") {
            var returned = false
            session.main { returned = requireNotNull(session.activity).hasWindowFocus() && !requireNotNull(session.activity).isFinishing }
            returned && session.text("Your screenshot") != null
        }
        session.recreate()
        assertEquals(draft.id, session.context.getSharedPreferences("composer", 0).getString("draft", null))
        assertEquals("A draft before setup", session.store.get(draft.id).caption)
        assertEquals("Saved through setup", session.store.get(draft.id).title)
        session.store.verifySource(session.store.get(draft.id))
    }

    fun testRecreationDuringShareEventuallyShowsOwnedDraft() {
        val started = CountDownLatch(1)
        val gate = CountDownLatch(1)
        SyntheticShareProvider.openStarted = started
        SyntheticShareProvider.openGate = gate
        session.launch(session.share())
        assertTrue("Share stream never opened", started.await(5, TimeUnit.SECONDS))
        session.recreate(waitForDraft = false)
        gate.countDown()
        val draft = session.retained()
        assertTrue(session.source.delete())
        session.store.verifySource(draft)
        assertEquals(draft.id, session.context.getSharedPreferences("composer", 0).getString("draft", null))
        assertNotNull(session.text("Connect your site"))
        assertFalse(session.views().any { it is android.widget.TextView && it.text.toString() == "Saving screenshot…" })
    }

    fun testRejectedPairingLinksAreScrubbedAndKeepSavedDraft() {
        session.launch(session.share())
        val draft = session.retained()
        session.edit("Retained around link errors", "Incoming link fixture")
        session.recreate()
        val capsule = "${"A".repeat(43)}.${"B".repeat(42)}A"
        val wrongService = "Use the Connect phone link from your Croptop publisher. This link does not match the approved publishing service."
        val invalidCapsule = "This connection link is incomplete or invalid. Create a new link in Croptop."
        val links = listOf(
            "https://wrong-origin.invalid/#pair=$capsule" to wrongService,
            "${BuildConfig.MOBILE_SERVICE_ORIGIN}/#pair=not-a-valid-capsule" to invalidCapsule,
            "${BuildConfig.MOBILE_SERVICE_ORIGIN}/unexpected#pair=$capsule" to wrongService,
            "${BuildConfig.MOBILE_SERVICE_ORIGIN}/?unexpected=true#pair=$capsule" to wrongService,
            "${BuildConfig.MOBILE_SERVICE_ORIGIN}/" to invalidCapsule,
        )
        for ((link, error) in links) {
            session.click("Incoming link fixture · Saved draft") // Clear the previous error before delivery.
            assertNull(session.text(error))
            session.main {
                session.context.startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(link)).setClass(session.context, MainActivity::class.java)
                    .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK or Intent.FLAG_ACTIVITY_SINGLE_TOP))
            }
            session.await("Pairing link was not visibly rejected") { session.text(error) != null }
            session.await("Pairing intent kept a capability after rejection") {
                var scrubbed = false
                session.main {
                    val intent = requireNotNull(session.activity).intent
                    scrubbed = intent.data == null && intent.clipData == null && intent.extras == null
                }
                scrubbed
            }
            assertNull(SiteKeyStore(session.context).connection())
            session.await("Rejected link opened an unexpected confirmation dialog") { requireNotNull(session.activity).hasWindowFocus() }
            assertNotNull(session.text("Your screenshot"))
            assertEquals(draft.id, session.context.getSharedPreferences("composer", 0).getString("draft", null))
            assertEquals("Retained around link errors", session.store.get(draft.id).caption)
            session.store.verifySource(session.store.get(draft.id))
        }
        session.recreate()
        assertNull(requireNotNull(session.activity).intent.data)
        assertEquals(1, session.store.all().count { it.id !in session.originalIds })
    }

    fun testColdMalformedPairingLaunchDoesNotRetainCapability() {
        session.launch(Intent(Intent.ACTION_VIEW, Uri.parse("${BuildConfig.MOBILE_SERVICE_ORIGIN}/#pair=invalid-secret")))
        session.await("Cold pairing launch kept a capability") {
            val intent = requireNotNull(session.activity).intent
            intent.data == null && intent.clipData == null && intent.extras == null
        }
        assertNull(SiteKeyStore(session.context).connection())
        assertNotNull(session.text("Connect your site"))
        assertEquals(session.originalIds, session.store.all().map { it.id }.toSet())
    }

    fun testStaleSetupCannotRestoreRemovedKeyOrRemoveReplacement() {
        val keys = SiteKeyStore(session.context)
        // Public deterministic Ed25519 seed used only by the repository's protocol fixtures.
        val pem = "-----BEGIN PRIVATE KEY-----\nMC4CAQAwBQYDK2VwBCIEIAABAgMEBQYHCAkKCwwNDg8QERITFBUWFxgZGhscHR4f\n-----END PRIVATE KEY-----\n"
        fun rejectsStale(action: () -> Unit) {
            var rejected = false
            try { action() } catch (_: IllegalArgumentException) { rejected = true }
            assertTrue("Stale connection mutation was accepted", rejected)
        }
        try {
            val initial = keys.snapshot()
            val original = keys.import(pem, PublishingService.origin, initial)
            val beforeRemoval = keys.snapshot()
            keys.remove(beforeRemoval)
            rejectsStale { keys.import(pem, PublishingService.origin, initial) }
            assertNull(keys.connection())
            val replacement = keys.import(pem, PublishingService.origin)
            assertEquals(original, replacement)
            rejectsStale { keys.remove(beforeRemoval) }
            assertEquals(replacement, keys.connection())
            assertEquals(replacement.ipns, keys.withKey(Protocol::identity))
        } finally {
            keys.remove()
        }
        assertNull(keys.connection())
    }

    fun testSensitiveSetupFieldsAreClearedAcrossRecreation() {
        session.launch(session.share())
        val draft = session.retained()
        session.edit("Keep my screenshot text", "Setup privacy fixture")
        val pem = session.input("Site key (PKCS8 PEM)")
        session.main { pem.setText("synthetic private-key placeholder") }
        session.click("Open connection link")
        lateinit var link: EditText
        session.main {
            val field = MainActivity::class.java.getDeclaredField("pairingInputDialog").apply { isAccessible = true }
            val dialog = field.get(session.activity) as AlertDialog
            link = session.views(requireNotNull(dialog.window).decorView).filterIsInstance<EditText>().single()
            link.setText("synthetic connection capability")
        }
        for (input in listOf(pem, link)) session.main {
            assertEquals(InputType.TYPE_TEXT_VARIATION_PASSWORD, input.inputType and InputType.TYPE_MASK_VARIATION)
            assertEquals(View.IMPORTANT_FOR_AUTOFILL_NO, input.importantForAutofill)
            assertFalse(input.isSaveEnabled)
            assertTrue(input.imeOptions and EditorInfo.IME_FLAG_NO_PERSONALIZED_LEARNING != 0)
        }
        session.recreate()
        session.main {
            assertEquals("", pem.text.toString())
            assertEquals("", link.text.toString())
            assertEquals("", session.input("Site key (PKCS8 PEM)").text.toString())
        }
        assertEquals(draft.id, session.context.getSharedPreferences("composer", 0).getString("draft", null))
        assertEquals("Keep my screenshot text", session.store.get(draft.id).caption)
        session.store.verifySource(session.store.get(draft.id))
    }

    fun testSystemBarsAndKeyboardLeaveComposerUsable() {
        if (Build.VERSION.SDK_INT < 30) return
        session.launch(session.share())
        val draft = session.retained()
        session.await("System insets not applied") {
            var safe = false
            session.main {
                val activity = requireNotNull(session.activity)
                val decor = activity.window.decorView
                val insets = decor.rootWindowInsets?.getInsets(WindowInsets.Type.systemBars())
                val scroll = session.views().filterIsInstance<ScrollView>().single()
                val location = IntArray(2).also(scroll::getLocationOnScreen)
                safe = insets != null && location[1] >= insets.top && location[1] + scroll.height <= decor.height - insets.bottom
            }
            safe
        }
        val input = session.input("Add a caption (optional)")
        session.main {
            input.requestFocus()
            input.requestRectangleOnScreen(Rect(0, 0, input.width, input.height), true)
            (session.context.getSystemService(InputMethodManager::class.java)).showSoftInput(input, InputMethodManager.SHOW_IMPLICIT)
            input.setText("Retained while the keyboard is open")
        }
        session.await("Keyboard did not become visible") {
            var visible = false
            var replaced = false
            session.main {
                replaced = session.input("Add a caption (optional)") !== input
                visible = requireNotNull(session.activity).window.decorView.rootWindowInsets.isVisible(WindowInsets.Type.ime())
            }
            if (replaced) throw AssertionError("Caption field was replaced while opening the keyboard")
            visible
        }
        var geometry = ""
        try {
            session.await("Caption is hidden behind the keyboard") {
                var accessible = false
                session.main {
                    val decor = requireNotNull(session.activity).window.decorView
                    val visible = Rect()
                    val ime = decor.rootWindowInsets.getInsets(WindowInsets.Type.ime()).bottom
                    val bottom = decor.height - ime
                    val top = decor.rootWindowInsets.getInsets(WindowInsets.Type.systemBars()).top
                    accessible = input.getGlobalVisibleRect(visible) && visible.height() == input.height && visible.top >= top && visible.bottom <= bottom
                    geometry = "caption=$visible fieldHeight=${input.height} decorHeight=${decor.height} ime=$ime safeTop=$top safeBottom=$bottom"
                }
                accessible
            }
        } catch (error: AssertionError) {
            throw AssertionError("Caption is hidden behind the keyboard: $geometry", error)
        }
        val save = requireNotNull(session.text("Save draft and close"))
        session.main { save.requestRectangleOnScreen(Rect(0, 0, save.width, save.height), true) }
        session.await("Save action cannot be brought above the keyboard") {
            var accessible = false
            session.main {
                val decor = requireNotNull(session.activity).window.decorView
                val visible = Rect()
                val bottom = decor.height - decor.rootWindowInsets.getInsets(WindowInsets.Type.ime()).bottom
                accessible = save.getGlobalVisibleRect(visible) && visible.height() == save.height && visible.bottom <= bottom
            }
            accessible
        }
        instrumentation.sendKeyDownUpSync(KeyEvent.KEYCODE_BACK)
        session.await("Back did not dismiss the keyboard") {
            var dismissed = false
            session.main { dismissed = !requireNotNull(session.activity).window.decorView.rootWindowInsets.isVisible(WindowInsets.Type.ime()) }
            dismissed
        }
        assertFalse(requireNotNull(session.activity).isFinishing)
        assertEquals("Retained while the keyboard is open", session.store.get(draft.id).caption)
        session.click("Save draft and close")
        session.launch()
        session.awaitComposer()
        assertEquals(draft.id, session.context.getSharedPreferences("composer", 0).getString("draft", null))
    }
}
