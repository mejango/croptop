package top.crop.mobile

import org.json.JSONObject
import java.io.File
import java.io.FileOutputStream
import java.io.InputStream
import java.util.UUID

data class Draft(
    val id: String = UUID.randomUUID().toString().uppercase(),
    val createdAt: Long = System.currentTimeMillis(),
    val ipns: String = "",
    val origin: String = "",
    val title: String = "",
    val caption: String = "",
    val mime: String = "",
    val sourceSHA256: String = "",
    val submitted: Boolean = false,
    val operation: String = "",
    val error: String = "",
    val authorized: Boolean = false,
) {
    fun json(): JSONObject = JSONObject().put("id", id).put("createdAt", createdAt).put("ipns", ipns).put("origin", origin)
        .put("title", title).put("caption", caption).put("mime", mime).put("sourceSHA256", sourceSHA256)
        .put("submitted", submitted).put("operation", operation).put("error", error).put("authorized", authorized)
    val state: String get() = if (operation.isEmpty()) if (submitted) "unconfirmed" else "draft" else JSONObject(operation).getString("state")
    val publishedURL: String get() = if (state == "published") JSONObject(operation).getString("url") else ""
    companion object {
        fun parse(value: JSONObject) = Draft(value.getString("id"), value.getLong("createdAt"), value.getString("ipns"), value.getString("origin"), value.getString("title"), value.getString("caption"), value.getString("mime"), value.getString("sourceSHA256"), value.getBoolean("submitted"), value.getString("operation"), value.optString("error"), value.optBoolean("authorized", false))
    }
}

/** Owned image bytes and stable operation identity survive URI expiry and process death. */
class DraftStore(private val root: File) {
    private var unreadableCount = 0
    init { require(root.mkdirs() || root.isDirectory) { "Draft storage is unavailable." } }
    private fun directory(id: String): File {
        require(id.matches(Regex("[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}"))) { "Invalid draft identity." }
        return File(root, id)
    }
    fun save(draft: Draft) = synchronized(lock) {
        val directory = directory(draft.id)
        require(directory.mkdirs() || directory.isDirectory) { "Could not save the draft." }
        val file = File(directory, "draft.json")
        val existing = if (file.exists()) get(draft.id) else null
        if (existing?.submitted == true) {
            require(draft.submitted && existing.ipns == draft.ipns && existing.origin == draft.origin && existing.title == draft.title && existing.caption == draft.caption && existing.mime == draft.mime && existing.sourceSHA256 == draft.sourceSHA256) { "This screenshot was already submitted. Check its retained status before changing it." }
        }
        require(existing?.state != "published" || draft.state == "published") { "This screenshot is already published. Open its saved post link." }
        val retained = draft.copy(authorized = draft.authorized || existing?.authorized == true)
        atomicWrite(file, retained.json().toString().toByteArray(Charsets.UTF_8))
    }

    fun resetAfterRejectedUpload(id: String, message: String) = synchronized(lock) {
        val retained = get(id)
        require(!retained.authorized && retained.operation.isEmpty()) { "This operation may already have been prepared. Check its retained status." }
        atomicWrite(File(directory(id), "draft.json"), retained.copy(submitted = false, error = message).json().toString().toByteArray(Charsets.UTF_8))
    }
    fun get(id: String): Draft = Draft.parse(JSONObject(File(directory(id), "draft.json").readText()))
    fun all(): List<Draft> = synchronized(lock) {
        unreadableCount = 0
        root.listFiles().orEmpty().filter { File(it, "draft.json").isFile }.mapNotNull { directory ->
            try {
                get(directory.name).also { draft ->
                    require(draft.id == directory.name) { "Draft identity does not match its storage." }
                    draft.state // Validate persisted operation JSON before displaying this draft.
                }
            } catch (_: Exception) {
                unreadableCount++
                null // Preserve the metadata and source image for recovery.
            }
        }.sortedByDescending { it.createdAt }
    }
    fun corruptedCount(): Int = synchronized(lock) { all(); unreadableCount }
    fun source(id: String) = File(directory(id), "source")
    fun preview(id: String) = File(directory(id), "preview")
    fun savePreview(id: String, data: ByteArray) = atomicWrite(preview(id), data)

    fun intake(stream: InputStream, mime: String, maxBytes: Long): Draft = synchronized(lock) {
        require(mime in setOf("image/png", "image/jpeg", "image/webp", "image/heic", "image/heif")) { "Choose one PNG, JPEG, WebP, HEIC or HEIF image." }
        val draft = Draft(mime = mime)
        val dir = directory(draft.id)
        require(dir.mkdirs()) { "Could not create a draft." }
        val target = source(draft.id)
        try {
            val digest = java.security.MessageDigest.getInstance("SHA-256")
            FileOutputStream(target).use { output ->
                var total = 0L
                val buffer = ByteArray(64 * 1024)
                while (true) {
                    val count = stream.read(buffer)
                    if (count < 0) break
                    total += count
                    require(total <= maxBytes) { "This image is too large for the publishing service." }
                    output.write(buffer, 0, count)
                    digest.update(buffer, 0, count)
                }
                require(total > 0) { "The shared image is empty." }
                output.fd.sync()
            }
            val hash = digest.digest().joinToString("") { "%02x".format(it) }
            // Re-delivered shares of an already retained unfinished image reopen it.
            val existing = all().firstOrNull { it.sourceSHA256 == hash && it.state != "published" }
            if (existing != null) { target.delete(); dir.delete(); return@synchronized existing }
            val retained = draft.copy(sourceSHA256 = hash)
            save(retained)
            retained
        } catch (error: Exception) {
            target.delete(); dir.delete()
            throw error
        }
    }

    fun verifySource(draft: Draft) {
        require(source(draft.id).isFile && Protocol.sha256(source(draft.id).readBytes()) == draft.sourceSHA256) { "The saved source image changed. Keep this draft and select the original screenshot again." }
    }

    fun renewExpired(draft: Draft): Draft = synchronized(lock) {
        val retained = get(draft.id)
        require(retained.state == "failed" && JSONObject(retained.operation).optString("code") == "draft_expired") { "Only a confirmed expired upload can become a new draft. Check the original publication's status first." }
        copyForEditing(retained)
    }

    fun reviseRejected(draft: Draft): Draft = synchronized(lock) {
        val retained = get(draft.id)
        val operation = JSONObject(retained.operation)
        require(!retained.authorized && retained.state == "failed" && operation.optJSONObject("proposal") == null) { "Only an unsigned, rejected preparation can become an editable draft. Keep checking any uncertain publication's status." }
        copyForEditing(retained)
    }

    private fun copyForEditing(retained: Draft): Draft {
        verifySource(retained)
        val renewed = retained.copy(id = UUID.randomUUID().toString().uppercase(), createdAt = System.currentTimeMillis(), submitted = false, operation = "", error = "", authorized = false)
        val directory = directory(renewed.id)
        require(directory.mkdirs()) { "Could not create a replacement draft." }
        atomicWrite(source(renewed.id), source(retained.id).readBytes())
        save(renewed)
        return renewed
    }

    companion object {
        private val lock = Any()
        fun atomicWrite(file: File, bytes: ByteArray) = synchronized(lock) {
            val temporary = File(file.parentFile, file.name + ".new")
            FileOutputStream(temporary).use { it.write(bytes); it.fd.sync() }
            require(temporary.renameTo(file)) { "Could not save changes. Check available storage." }
        }
    }
}
