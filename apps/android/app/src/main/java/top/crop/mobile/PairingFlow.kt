package top.crop.mobile

import android.app.Activity
import android.app.AlertDialog
import android.net.Uri
import org.json.JSONObject
import java.net.HttpURLConnection
import java.net.URL

/** An ephemeral receiver. If Android kills setup, create a fresh connection link. */
class PairingFlow(
    private val activity: Activity,
    private val keys: SiteKeyStore,
    private val complete: (String) -> Unit,
    private val background: (String, () -> Unit) -> Unit,
) {
    private lateinit var origin: String
    private lateinit var id: String
    private lateinit var capability: String
    private lateinit var protocol: PairingProtocol
    private lateinit var claim: PairingProtocol.Claim

    fun open(link: String) {
        val uri = Uri.parse(link.trim())
        require(uri.scheme == "https" && uri.host != null && uri.encodedQuery == null && uri.path in listOf("", "/")) { "Paste the HTTPS Connect phone link from your existing Croptop publisher." }
        origin = Protocol.origin("${uri.scheme}://${uri.encodedAuthority}")
        val fragment = uri.fragment ?: error("This connection link is incomplete.")
        require(fragment.startsWith("pair=")) { "This is not a Connect phone link." }
        val parts = fragment.removePrefix("pair=").split('.')
        require(parts.size == 2 && parts.all { it.matches(Regex("[A-Za-z0-9_-]{43}")) }) { "This connection link is invalid." }
        id = parts[0]; capability = parts[1]
        val confirmation = {
            background("Connecting to your publisher…") {
                protocol = PairingProtocol()
                val response = post("claim", JSONObject().put("receiverPublicKey", protocol.publicKey))
                claim = protocol.claim(origin, id, response)
                activity.runOnUiThread { if (!activity.isFinishing && !activity.isDestroyed) confirm() }
            }
        }
        AlertDialog.Builder(activity).setTitle("Connect through ${uri.host}?")
            .setMessage("This service will prepare your posts. The connection transfers full control of the site to this phone.${if (keys.connection() != null) " It replaces this phone’s current connection; saved drafts retain their original site." else ""}")
            .setPositiveButton("Connect") { _, _ -> confirmation() }.setNegativeButton("Cancel", null).show()
    }

    private fun confirm() {
        val formatted = claim.confirmationCode.chunked(4).joinToString(" ")
        AlertDialog.Builder(activity).setTitle(formatted)
            .setMessage("Enter this code on your original publisher, then confirm the transfer there.\n\nSite: ${claim.ipns}\n\nConfirm only if this is your site and the codes match.")
            .setPositiveButton("Codes match · Connect") { _, _ -> consume() }
            .setNegativeButton("Cancel", null).show()
    }

    private fun consume() {
        background("Receiving your encrypted site key…") {
            try {
                val response = post("consume", JSONObject().put("confirmed", true))
                val pem = protocol.decrypt(response)
                keys.import(pem, origin)
                capability = ""
                activity.runOnUiThread { complete("Site connected. Your key is protected on this phone. Check Site settings before publishing.") }
            } catch (error: ApiError) {
                if (error.status == 409) {
                    activity.runOnUiThread {
                        if (!activity.isFinishing && !activity.isDestroyed) AlertDialog.Builder(activity)
                            .setTitle("Waiting for your publisher")
                            .setMessage("Finish confirming the connection on the original publisher, then try again. Your screenshot remains saved.")
                            .setPositiveButton("Try again") { _, _ -> consume() }.setNegativeButton("Cancel", null).show()
                    }
                } else throw error
            }
        }
    }

    private fun post(action: String, body: JSONObject): JSONObject {
        val http = URL("$origin/v0/mobile/pairings/$id/$action").openConnection() as HttpURLConnection
        try {
            http.requestMethod = "POST"
            http.instanceFollowRedirects = false
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
            if (status !in 200..299) throw ApiError(status, json.optString("error", "Create a fresh connection link on your original publisher."))
            return json
        } finally { http.disconnect() }
    }
}
