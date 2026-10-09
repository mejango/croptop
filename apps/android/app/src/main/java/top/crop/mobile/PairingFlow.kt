package top.crop.mobile

import android.app.Activity
import android.app.AlertDialog
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/** An ephemeral receiver. If Android kills setup, create a fresh connection link. */
class PairingFlow(
    private val activity: Activity,
    private val keys: SiteKeyStore,
    private val complete: (String) -> Unit,
    background: (String, () -> Unit) -> Boolean,
) {
    private val tasks = PairingTasks(background, ::cancel)
    private lateinit var origin: String
    private lateinit var id: String
    private lateinit var capability: String
    private lateinit var protocol: PairingProtocol
    private lateinit var claim: PairingProtocol.Claim
    private lateinit var originalConnection: ConnectionSnapshot
    private var dialog: AlertDialog? = null
    @Volatile private var canceled = false
    @Volatile private var completed = false

    fun isActive() = !canceled && !completed

    @Synchronized fun cancel() {
        canceled = true
        capability = ""
        if (::protocol.isInitialized) protocol.close()
        activity.runOnUiThread { dialog?.dismiss(); dialog = null }
    }

    private fun requireActive() {
        require(isActive() && !activity.isFinishing && !activity.isDestroyed) { "Connection setup was closed. Create a fresh Connect phone link; your draft is still saved." }
        keys.requireCurrent(originalConnection)
    }

    private fun schedule(message: String, task: () -> Unit) {
        if (!tasks.submit(message, task)) {
            complete("Connection setup closed while Croptop was busy. Your screenshot is saved; open a fresh Connect phone link when saving finishes.")
        }
    }

    fun open(parsed: PairingLink) {
        PublishingService.requireOrigin(parsed.origin)
        origin = parsed.origin; id = parsed.id; capability = parsed.capability
        originalConnection = keys.snapshot()
        val confirmation = {
            schedule("Connecting to your publisher…") {
                requireActive()
                protocol = PairingProtocol()
                val response = post("claim", JSONObject().put("receiverPublicKey", protocol.publicKey))
                requireActive()
                claim = protocol.claim(origin, id, response)
                requireActive()
                activity.runOnUiThread { if (!canceled && !activity.isFinishing && !activity.isDestroyed) confirm() }
            }
        }
        dialog = AlertDialog.Builder(activity).setTitle("Connect your Croptop site?")
            .setMessage("This service will prepare your posts. The connection transfers full control of the site to this phone.${if (keys.connection() != null) " It replaces this phone’s current connection; saved drafts retain their original site." else ""}")
            .setPositiveButton("Connect") { _, _ -> confirmation() }.setNegativeButton("Cancel") { _, _ -> cancel() }.setOnCancelListener { cancel() }.show()
    }

    private fun confirm() {
        val formatted = claim.confirmationCode.chunked(4).joinToString(" ")
        dialog = AlertDialog.Builder(activity).setTitle(formatted)
            .setMessage("Enter this code on your original publisher, then confirm the transfer there.\n\nSite: ${claim.ipns}\n\nConfirm only if this is your site and the codes match.")
            .setPositiveButton("Codes match · Connect") { _, _ -> consume() }
            .setNegativeButton("Cancel") { _, _ -> cancel() }.setOnCancelListener { cancel() }.show()
    }

    private fun consume() {
        schedule("Receiving your encrypted site key…") {
            try {
                requireActive()
                val response = post("consume", JSONObject().put("confirmed", true))
                requireActive()
                val pem = protocol.decrypt(response)
                synchronized(this@PairingFlow) {
                    requireActive()
                    keys.import(pem, origin, originalConnection)
                    completed = true
                }
                capability = ""
                protocol.close()
                activity.runOnUiThread { if (!canceled && !activity.isFinishing && !activity.isDestroyed) complete("Site connected. Your key is protected on this phone. Check Site settings before publishing.") }
            } catch (error: ApiError) {
                if (error.status == 409 && error.code == "pairing_pending") {
                    activity.runOnUiThread {
                        if (!canceled && !activity.isFinishing && !activity.isDestroyed) dialog = AlertDialog.Builder(activity)
                            .setTitle("Waiting for your publisher")
                            .setMessage("Finish confirming the connection on the original publisher, then try again. Your screenshot remains saved.")
                            .setPositiveButton("Try again") { _, _ -> consume() }.setNegativeButton("Cancel") { _, _ -> cancel() }.setOnCancelListener { cancel() }.show()
                    }
                } else throw error
            }
        }
    }

    private fun post(action: String, body: JSONObject): JSONObject {
        requireActive()
        val http = URL("$origin/v0/mobile/pairings/$id/$action").openConnection() as HttpURLConnection
        try {
            http.requestMethod = "POST"
            http.instanceFollowRedirects = false
            http.useCaches = false
            http.connectTimeout = 20_000; http.readTimeout = 30_000
            http.doOutput = true
            http.setRequestProperty("Content-Type", "application/json")
            http.setRequestProperty("X-Croptop-Pairing", capability)
            http.setRequestProperty("Cache-Control", "no-store")
            val encoded = body.toString().toByteArray(Charsets.UTF_8)
            http.setFixedLengthStreamingMode(encoded.size)
            http.outputStream.use { it.write(encoded) }
            val status = http.responseCode
            val bytes = (if (status in 200..299) http.inputStream else http.errorStream)?.use { MobileApi.readBounded(it, 16 * 1024) }
                ?: error("The pairing service did not respond.")
            val json = JSONObject(String(bytes, Charsets.UTF_8))
            if (status !in 200..299) throw ApiError(status, json.optString("error", "Create a fresh connection link on your original publisher."), json.optString("code"))
            return json
        } finally { http.disconnect() }
    }
}
