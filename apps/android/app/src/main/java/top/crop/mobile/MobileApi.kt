package top.crop.mobile

import org.json.JSONObject
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.InputStream
import java.net.HttpURLConnection
import java.net.URL
import java.time.Instant
import java.util.UUID

class ApiError(val status: Int, message: String, val code: String = "") : Exception(message)

interface MobileTransport {
    fun config(requireEnabled: Boolean = true): JSONObject
    fun site(): JSONObject
    fun operation(id: String): JSONObject
    fun prepare(id: String): JSONObject
    fun preview(id: String, maxBytes: Long): ByteArray
    fun commit(id: String, signatures: Protocol.Signatures): JSONObject
    fun upload(draft: Draft, source: File): JSONObject
}

class MobileApi(private val keys: SiteSigner, private val connect: (URL) -> HttpURLConnection = { it.openConnection() as HttpURLConnection }) : MobileTransport {
    private var token = ""
    private var tokenExpiry = 0L
    private var tokenIdentity = ""
    private fun connection() = keys.connection() ?: error("Connect your site to continue.")
    override fun config(requireEnabled: Boolean): JSONObject = json("GET", "/config", auth = false).also {
        require(it.getInt("version") == 1) { "Unsupported publishing service version." }
        require(!requireEnabled || it.getBoolean("enabled")) { "Phone publishing is not enabled on this service yet." }
        require(Protocol.origin(it.getString("origin")) == connection().origin) { "The service address does not match its configuration." }
        require(it.getInt("maxImages") == 1) { "Unsupported publishing service version." }
    }
    override fun site(): JSONObject = json("GET", "/site")
    fun consent(enabled: Boolean): JSONObject = json("PUT", "/connection", JSONObject().put("enabled", enabled))

    private fun session(expected: ConnectionSnapshot) {
        keys.requireCurrent(expected)
        val connection = expected.connection ?: error("Connect your site to continue.")
        val identity = connection.ipns + connection.origin + expected.generation
        if (token.isNotEmpty() && tokenExpiry > Instant.now().epochSecond + 30 && tokenIdentity == identity) return
        val challenge = json("POST", "/challenge", JSONObject().put("ipns", connection.ipns), false, expected)
        val payload = Protocol.challenge(connection.origin, connection.ipns, challenge)
        keys.requireCurrent(expected)
        val signature = keys.withKey { keys.requireCurrent(expected); require(Protocol.identity(it) == connection.ipns) { "The signing key changed during connection setup." }; Protocol.sign(it, payload) }
        val session = json("POST", "/session", JSONObject().put("id", challenge.getString("id")).put("signature", signature), false, expected)
        keys.requireCurrent(expected)
        token = session.getString("token")
        tokenExpiry = session.getLong("expiresAt")
        tokenIdentity = identity
    }

    override fun operation(id: String): JSONObject = json("GET", "/operations/$id")
    override fun prepare(id: String): JSONObject = json("POST", "/operations/$id/prepare", JSONObject())
    override fun preview(id: String, maxBytes: Long): ByteArray = request("GET", "/operations/$id/image", maxBytes = maxBytes)
    override fun commit(id: String, signatures: Protocol.Signatures): JSONObject = json("POST", "/operations/$id/commit", JSONObject()
        .put("proposalId", signatures.proposalId).put("recordSignature", signatures.recordSignature).put("pushSignature", signatures.pushSignature))

    override fun upload(draft: Draft, source: File): JSONObject {
        val boundary = "croptop-${UUID.randomUUID()}"
        val bytes = request("POST", "/operations", contentType = "multipart/form-data; boundary=$boundary", body = { http ->
            http.setChunkedStreamingMode(64 * 1024)
            http.outputStream.use { output ->
                fun text(value: String) = output.write(value.toByteArray(Charsets.UTF_8))
                for ((key, value) in listOf("id" to draft.id, "title" to draft.title, "caption" to draft.caption)) {
                    text("--$boundary\r\nContent-Disposition: form-data; name=\"$key\"\r\n\r\n$value\r\n")
                }
                text("--$boundary\r\nContent-Disposition: form-data; name=\"image\"; filename=\"screenshot\"\r\nContent-Type: ${draft.mime}\r\n\r\n")
                source.inputStream().use { it.copyTo(output) }
                text("\r\n--$boundary--\r\n")
            }
        })
        return JSONObject(String(bytes, Charsets.UTF_8))
    }

    private fun json(method: String, path: String, body: JSONObject? = null, auth: Boolean = true, expected: ConnectionSnapshot = keys.snapshot()): JSONObject {
        val bytes = body?.toString()?.toByteArray(Charsets.UTF_8)
        return JSONObject(String(request(method, path, auth, expected = expected, body = bytes?.let { { http ->
            http.setFixedLengthStreamingMode(it.size)
            http.outputStream.use { output -> output.write(it) }
        } }), Charsets.UTF_8))
    }

    private fun request(method: String, path: String, auth: Boolean = true, contentType: String = "application/json", maxBytes: Long = 1024 * 1024, body: ((HttpURLConnection) -> Unit)? = null, reauthenticate: Boolean = true, expected: ConnectionSnapshot = keys.snapshot()): ByteArray {
        keys.requireCurrent(expected)
        val connection = expected.connection ?: error("Connect your site to continue.")
        PublishingService.requireOrigin(connection.origin)
        if (auth) session(expected)
        keys.requireCurrent(expected)
        val http = connect(URL(connection.origin + "/v0/mobile" + path))
        try {
            http.requestMethod = method
            http.instanceFollowRedirects = false
            http.useCaches = false
            http.connectTimeout = 20_000
            http.readTimeout = 60_000
            http.setRequestProperty("Accept", "application/json")
            http.setRequestProperty("Cache-Control", "no-store")
            if (auth) http.setRequestProperty("Authorization", "Bearer $token")
            keys.requireCurrent(expected)
            if (body != null) {
                http.doOutput = true
                http.setRequestProperty("Content-Type", contentType)
                body(http)
            }
            val status = http.responseCode
            if (status == 401 && auth && reauthenticate) {
                token = ""
                return request(method, path, auth, contentType, maxBytes, body, false, expected)
            }
            val bytes = (if (status in 200..299) http.inputStream else http.errorStream)?.use { readBounded(it, maxBytes) } ?: ByteArray(0)
            if (status !in 200..299) {
                val error = runCatching { JSONObject(String(bytes, Charsets.UTF_8)) }.getOrNull()
                val message = error?.optString("error")?.ifBlank { null } ?: "The service returned HTTP $status. Your draft is still saved."
                throw ApiError(status, message, error?.optString("code") ?: "")
            }
            return bytes
        } finally { http.disconnect() }
    }

    companion object {
        fun readBounded(input: InputStream, maxBytes: Long): ByteArray {
            val output = ByteArrayOutputStream()
            val buffer = ByteArray(64 * 1024)
            while (true) {
                val count = input.read(buffer)
                if (count < 0) break
                require(output.size().toLong() + count <= maxBytes) { "The service response is too large." }
                output.write(buffer, 0, count)
            }
            return output.toByteArray()
        }
    }
}

/** Synchronous workflow run on one background executor. Disk is authoritative. */
class Publisher(private val keys: SiteSigner, private val store: DraftStore, private val api: MobileTransport, private val validateImage: (ByteArray, Long) -> Unit = ImagePreview::validate) {
    private fun checkedConnection(draft: Draft) {
        val connection = keys.connection() ?: error("Connect the site for this draft first.")
        require(draft.ipns == connection.ipns && draft.origin == connection.origin) { "This draft belongs to another site. Reconnect ${draft.ipns}." }
    }
    private fun saveOperation(draft: Draft, operation: JSONObject): Draft {
        Protocol.validateOperation(draft, operation)
        require(operation.getString("state") in setOf("preparing", "needs_signature", "committing", "published", "failed")) { "Unknown publication status." }
        if (operation.getString("state") == "published") {
            val link = java.net.URI(operation.getString("url"))
            require(link.scheme == "https" && !link.host.isNullOrEmpty() && link.rawUserInfo == null) { "The service has not returned a usable published post link yet. Check status again." }
        }
        return draft.copy(operation = operation.toString(), error = "").also { store.save(it) }
    }
    fun review(original: Draft): Draft {
        checkedConnection(original)
        val config = api.config(requireEnabled = false)
        store.verifySource(original)
        var draft = original
        fun upload(): JSONObject {
            checkedConnection(draft)
            Protocol.validateText(draft, config)
            try { return api.upload(draft, store.source(draft.id)) }
            catch (error: ApiError) {
                if (error.status == 400 && error.code == "invalid_text" && !original.submitted && !draft.authorized) store.resetAfterRejectedUpload(draft.id, error.message ?: "Edit your text and try again.")
                throw error
            }
        }
        fun requireReady(): JSONObject {
            require(config.getBoolean("enabled")) { "New phone publications are paused. Your draft is saved." }
            val site = api.site()
            require(site.getString("ipns") == original.ipns && site.getBoolean("ready") && site.getBoolean("enabled")) { site.optString("reason").ifBlank { "Enable phone publishing for this site first." } }
            return site
        }
        val operation = if (draft.submitted) {
            try { api.operation(draft.id) } catch (error: ApiError) {
                if (error.status != 404) throw error
                requireReady()
                upload()
            }
        } else {
            requireReady()
            Protocol.validateText(draft, config)
            draft = draft.copy(submitted = true, error = "")
            store.save(draft) // Persist intent before upload, including destination and source digest.
            upload()
        }
        draft = saveOperation(draft, operation)
        if (draft.state == "failed" && JSONObject(draft.operation).optString("code") == "draft_expired") return draft
        if (draft.state == "failed" && original.state != "failed") return draft // Show a newly reported failure before the user chooses retry or edit.
        if (draft.state == "failed" || (draft.state == "needs_signature" && (JSONObject(draft.operation).getJSONObject("proposal").getLong("expiresAt") <= Instant.now().epochSecond || !Protocol.matchesParent(JSONObject(draft.operation), requireReady())))) {
            requireReady()
            checkedConnection(draft)
            draft = saveOperation(draft, api.prepare(draft.id))
        }
        if (draft.state == "needs_signature") {
            val normalized = api.preview(draft.id, maxOf(config.getLong("maxImageBytes") * 4, 1024 * 1024))
            checkedConnection(draft)
            Protocol.validateProposal(draft, JSONObject(draft.operation), normalized, config.getString("host"))
            validateImage(normalized, config.getLong("maxImagePixels"))
            store.savePreview(draft.id, normalized)
        }
        return draft
    }
    fun publish(draft: Draft): Draft {
        checkedConnection(draft)
        val config = api.config()
        val fresh = api.operation(draft.id)
        val updated = saveOperation(draft, fresh)
        if (updated.state == "published" || updated.state == "committing") return updated
        require(fresh.getJSONObject("proposal").getString("id") == JSONObject(draft.operation).getJSONObject("proposal").getString("id")) { "The proposal changed. Review the updated preview before publishing." }
        val site = api.site()
        require(site.getBoolean("ready") && site.getBoolean("enabled") && Protocol.matchesParent(fresh, site)) { "Your site changed after preparing this screenshot. Check status to prepare it again, keeping the other publication." }
        val image = store.preview(draft.id).readBytes()
        validateImage(image, config.getLong("maxImagePixels"))
        checkedConnection(draft)
        val authorized = draft.copy(authorized = true)
        store.save(authorized) // Never offer editable replacement after any signing attempt.
        val signatures = keys.withKey { checkedConnection(draft); Protocol.signProposal(it, draft, fresh, image, config.getString("host")) }
        // Keep the last validated proposal if the response is lost. Recovery GET precedes retry.
        checkedConnection(draft)
        return saveOperation(authorized, api.commit(draft.id, signatures))
    }

    fun reviseRejected(draft: Draft): Draft {
        checkedConnection(draft)
        val fresh = saveOperation(draft, api.operation(draft.id))
        return store.reviseRejected(fresh)
    }
}
