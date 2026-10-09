@file:Suppress("DEPRECATION", "OVERRIDE_DEPRECATION")

package top.crop.mobile

import android.app.Activity
import android.os.Bundle
import android.os.Process
import android.test.InstrumentationTestRunner
import org.json.JSONObject
import java.io.File

/** Normal JUnit suite, or two explicit recovery phases separated by adb force-stop. */
class NativeInstrumentationRunner : InstrumentationTestRunner() {
    private var phase: String? = null
    override fun onCreate(arguments: Bundle?) {
        phase = arguments?.getString("phase")
        if (phase == null) super.onCreate(arguments) else start()
    }
    override fun onStart() {
        if (phase == null) { super.onStart(); return }
        val result = Bundle()
        var session: NativeTestSession? = null
        try {
            session = NativeTestSession(this)
            val marker = File(targetContext.noBackupFilesDir, "native-process-death-fixture.json")
            when (phase) {
                "seed" -> {
                    check(!marker.exists()) { "A previous recovery fixture needs verification before another run." }
                    session.launch(session.share())
                    val draft = session.retained()
                    session.edit("Caption retained across force-stop", "Process death fixture")
                    check(session.source.delete())
                    DraftStore.atomicWrite(marker, JSONObject().put("id", draft.id).put("sha256", draft.sourceSHA256)
                        .put("pid", Process.myPid()).put("originalIds", org.json.JSONArray(session.originalIds.toList()))
                        .toString().toByteArray())
                    session.close(cleanup = false)
                    result.putString("stream", "PASS: synthetic draft retained; external URI removed.\n")
                }
                "verify" -> {
                    check(marker.isFile) { "No prepared recovery fixture." }
                    val fixture = JSONObject(marker.readText())
                    check(fixture.getInt("pid") != Process.myPid()) { "Verification must run in a new application process." }
                    session.launch()
                    session.awaitComposer()
                    val id = fixture.getString("id")
                    val draft = session.store.get(id)
                    check(targetContext.getSharedPreferences("composer", 0).getString("draft", null) == id)
                    check(draft.caption == "Caption retained across force-stop" && draft.title == "Process death fixture")
                    check(draft.sourceSHA256 == fixture.getString("sha256"))
                    check(!session.source.exists())
                    session.store.verifySource(draft)
                    val previous = fixture.getJSONArray("originalIds")
                    val previousIds = (0 until previous.length()).map { previous.getString(it) }.toSet()
                    check(session.store.all().filter { it.id !in previousIds }.map { it.id } == listOf(id))
                    session.close(cleanup = false)
                    check(marker.delete())
                    // Retain the synthetic draft so before/after screenshots and manual inspection remain available.
                    result.putString("stream", "PASS: new process recovered the same draft, caption, title, and owned image.\n")
                }
                else -> error("Use phase=seed or phase=verify via scripts/emulator-smoke.sh")
            }
            finish(Activity.RESULT_OK, result)
        } catch (error: Throwable) {
            runCatching { session?.close(cleanup = false) }
            result.putString("stream", "FAIL: ${error.stackTraceToString()}\n")
            finish(Activity.RESULT_CANCELED, result)
        }
    }
}
