package top.crop.mobile

import android.app.Instrumentation
import android.content.ClipData
import android.content.Intent
import android.graphics.Bitmap
import android.graphics.Color
import android.net.Uri
import android.os.SystemClock
import android.os.Looper
import android.view.View
import android.view.ViewGroup
import android.widget.EditText
import android.widget.ImageView
import android.widget.TextView
import java.io.File
import java.util.UUID
import java.util.concurrent.atomic.AtomicInteger

/** Tests touch only the fixture and drafts which they create; existing drafts are retained. */
internal class NativeTestSession(val instrumentation: Instrumentation) {
    val context = instrumentation.targetContext
    val store = DraftStore(File(context.noBackupFilesDir, "drafts"))
    val originalIds = store.all().map { it.id }.toSet()
    private val preferences = context.getSharedPreferences("composer", 0)
    private val originalSelection = preferences.getString("draft", null)
    val source = File(context.cacheDir, "native-test-share.png")
    var activity: MainActivity? = null

    init {
        check(SiteKeyStore(context).connection() == null) { "Use an isolated emulator without a connected site." }
    }

    fun main(task: () -> Unit) {
        if (Looper.myLooper() == Looper.getMainLooper()) task() else instrumentation.runOnMainSync(task)
    }
    fun launch(intent: Intent = Intent(Intent.ACTION_MAIN)): MainActivity {
        val launched = instrumentation.startActivitySync(intent.setClass(context, MainActivity::class.java).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)) as MainActivity
        activity = launched
        instrumentation.waitForIdleSync()
        return launched
    }
    fun share(clipOnly: Boolean = false): Intent {
        val bitmap = Bitmap.createBitmap(48, 48, Bitmap.Config.ARGB_8888)
        bitmap.eraseColor(Color.rgb(30, 110, 70))
        bitmap.setPixel(0, 0, UUID.randomUUID().hashCode() or 0xff000000.toInt())
        source.outputStream().use { check(bitmap.compress(Bitmap.CompressFormat.PNG, 100, it)) }
        bitmap.recycle()
        val uri = Uri.parse("content://${context.packageName}.native-test-share/screenshot.png")
        return Intent(Intent.ACTION_SEND).setType("image/png")
            .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION)
            .apply {
                clipData = ClipData.newUri(context.contentResolver, "Synthetic screenshot", uri)
                if (!clipOnly) putExtra(Intent.EXTRA_STREAM, uri)
            }
    }
    fun await(message: String, timeout: Long = 12_000, condition: () -> Boolean) {
        val end = SystemClock.uptimeMillis() + timeout
        do {
            instrumentation.waitForIdleSync()
            if (condition()) return
            SystemClock.sleep(50)
        } while (SystemClock.uptimeMillis() < end)
        runCatching {
            instrumentation.uiAutomation.takeScreenshot()?.let { bitmap ->
                File(context.cacheDir, "native-ui-failure.png").outputStream().use { bitmap.compress(Bitmap.CompressFormat.PNG, 100, it) }
                bitmap.recycle()
            }
        }
        throw AssertionError(message)
    }
    fun retained(): Draft {
        await("Screenshot was not retained") { store.all().any { it.id !in originalIds } }
        val draft = store.all().single { it.id !in originalIds }
        awaitComposer()
        return store.get(draft.id)
    }
    fun awaitComposer() {
        await("Composer did not render the saved screenshot") {
            var rendered = false
            main { rendered = text("Your screenshot") != null && views().filterIsInstance<ImageView>().any { it.contentDescription == "Screenshot preview" && it.drawable != null } }
            rendered
        }
    }
    fun views(root: View? = null): List<View> {
        var result = emptyList<View>()
        main {
            fun flatten(view: View): List<View> = listOf(view) + if (view is ViewGroup) (0 until view.childCount).flatMap { flatten(view.getChildAt(it)) } else emptyList()
            result = (root ?: activity?.window?.decorView)?.let(::flatten).orEmpty()
        }
        return result
    }
    fun text(value: String): TextView? {
        var result: TextView? = null
        main { result = views().filterIsInstance<TextView>().firstOrNull { it.text.toString() == value } }
        return result
    }
    fun input(hint: String): EditText {
        var result: EditText? = null
        main { result = views().filterIsInstance<EditText>().single { it.hint?.toString() == hint } }
        return requireNotNull(result)
    }
    fun click(value: String) {
        val view = text(value) ?: throw AssertionError("Missing action: $value")
        main { check(view.performClick()) }
        instrumentation.waitForIdleSync()
    }
    fun edit(caption: String, title: String) {
        main { input("Add a caption (optional)").setText(caption); input("Title (optional)").setText(title) }
    }
    fun recreate(waitForDraft: Boolean = true) {
        val old = requireNotNull(activity)
        val monitor = instrumentation.addMonitor(MainActivity::class.java.name, null, false)
        try {
            main { old.recreate() }
            activity = instrumentation.waitForMonitorWithTimeout(monitor, 12_000) as? MainActivity
                ?: throw AssertionError("Activity did not recreate")
        } finally {
            instrumentation.removeMonitor(monitor)
        }
        if (waitForDraft) awaitComposer()
    }
    fun close(cleanup: Boolean = true) {
        SyntheticShareProvider.openGate?.countDown()
        SyntheticShareProvider.openGate = null
        SyntheticShareProvider.openStarted = null
        // A failed lifecycle assertion must not leave its executor writing into the next test.
        val pending = Class.forName("top.crop.mobile.Work").getDeclaredField("pending").apply { isAccessible = true }.get(null) as AtomicInteger
        await("Image intake did not settle before test cleanup") { pending.get() == 0 }
        activity?.let { current -> main { if (!current.isDestroyed) current.finish() } }
        instrumentation.waitForIdleSync()
        if (cleanup) {
            store.all().filter { it.id !in originalIds }.forEach { draft ->
                check(File(context.noBackupFilesDir, "drafts/${draft.id}").deleteRecursively())
            }
            check(preferences.edit().apply {
                if (originalSelection == null) remove("draft") else putString("draft", originalSelection)
            }.commit())
        }
        source.delete()
    }
}
