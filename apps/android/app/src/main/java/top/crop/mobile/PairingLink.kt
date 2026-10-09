package top.crop.mobile

import java.net.URI
import java.util.Base64

/** Public entry points accept exactly the service chosen by this build. */
class PairingLink private constructor(val origin: String, val id: String, val capability: String) {
    // Capability must never appear in diagnostic representations.
    override fun toString(): String = "PairingLink(origin=$origin, capability=redacted)"

    companion object {
        fun parse(value: String, approvedOrigin: String): PairingLink {
            val expected = Protocol.origin(approvedOrigin)
            require(expected == approvedOrigin) { "The configured publishing address is invalid." }
            val uri = try { URI(value.trim()) } catch (_: Exception) { error("This connection link is invalid. Create a new link in Croptop.") }
            val service = URI(expected)
            require(uri.scheme == service.scheme && uri.rawAuthority == service.rawAuthority && uri.rawUserInfo == null && uri.port == -1 && uri.rawPath == "/" && uri.rawQuery == null) {
                "Use the Connect phone link from your Croptop publisher. This link does not match the approved publishing service."
            }
            val match = Regex("pair=([A-Za-z0-9_-]{43})\\.([A-Za-z0-9_-]{43})").matchEntire(uri.rawFragment ?: "")
                ?: error("This connection link is incomplete or invalid. Create a new link in Croptop.")
            val (id, capability) = match.destructured
            for (token in listOf(id, capability)) {
                val bytes = Base64.getUrlDecoder().decode(token)
                require(bytes.size == 32 && Base64.getUrlEncoder().withoutPadding().encodeToString(bytes) == token) { "This connection link is invalid. Create a new link in Croptop." }
            }
            return PairingLink(expected, id, capability)
        }
    }
}
