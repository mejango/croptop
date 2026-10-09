package top.crop.mobile

import org.json.JSONObject
import org.junit.Assert.*
import org.junit.Test
import java.io.ByteArrayInputStream
import java.io.ByteArrayOutputStream
import java.net.HttpURLConnection
import java.net.URL
import java.time.Instant

class ApiConnectionTest {
    private val fixture = JSONObject(requireNotNull(javaClass.getResourceAsStream("/mobile-protocol-fixture.json")).bufferedReader().use { it.readText() })
    private val der = Protocol.importPem(fixture.getString("privateKeyPEM"))
    private val site = Connection(Protocol.identity(der), PublishingService.origin)
    private class Keys(var site: Connection?, val der: ByteArray) : SiteSigner {
        var generation = 0L
        var signs = 0
        override fun connection() = site
        override fun snapshot() = ConnectionSnapshot(site, generation)
        override fun <T> withKey(action: (ByteArray) -> T): T { signs++; return action(der) }
        fun remove() { site = null; generation++ }
    }
    private class Reply(url: URL, private val body: String, private val afterRead: () -> Unit = {}) : HttpURLConnection(url) {
        val output = ByteArrayOutputStream()
        override fun disconnect() {}
        override fun usingProxy() = false
        override fun connect() {}
        override fun getResponseCode() = 200
        override fun getOutputStream() = output
        override fun getInputStream(): ByteArrayInputStream {
            afterRead()
            return ByteArrayInputStream(body.toByteArray())
        }
    }

    private fun challenge(): JSONObject {
        val id = "test-session-challenge-identifier"
        val expires = Instant.now().epochSecond + 300
        return JSONObject().put("id", id).put("expiresAt", expires).put("message", "croptop-mobile-session\n${site.origin}\n${site.ipns}\n$id\n$expires")
    }

    @Test fun removalAfterChallengePreventsSigningAndSessionDispatch() {
        val keys = Keys(site, der)
        val urls = mutableListOf<URL>()
        val api = MobileApi(keys) { url ->
            urls += url
            Reply(url, challenge().toString()) { keys.remove() }
        }
        assertThrows(IllegalArgumentException::class.java) { api.site() }
        assertEquals(0, keys.signs)
        assertEquals(listOf("/v0/mobile/challenge"), urls.map { it.path })
    }

    @Test fun reconnectingSameIdentityCannotReuseAStaleGeneration() {
        val keys = Keys(site, der)
        val urls = mutableListOf<URL>()
        val api = MobileApi(keys) { url ->
            urls += url
            Reply(url, challenge().toString()) { keys.remove(); keys.site = site }
        }
        assertThrows(IllegalArgumentException::class.java) { api.site() }
        assertEquals(0, keys.signs)
        assertEquals(1, urls.size)
    }

    @Test fun removalAfterAuthenticationCannotDispatchBearerToAnotherSiteOrOrigin() {
        val keys = Keys(site, der)
        val urls = mutableListOf<URL>()
        val api = MobileApi(keys) { url ->
            urls += url
            if (url.path.endsWith("challenge")) Reply(url, challenge().toString())
            else Reply(url, JSONObject().put("token", "test-token").put("expiresAt", Instant.now().epochSecond + 600).toString()) {
                keys.remove()
                keys.site = site.copy(origin = "https://untrusted.example")
            }
        }
        assertThrows(IllegalArgumentException::class.java) { api.site() }
        assertEquals(1, keys.signs)
        assertEquals(listOf("/v0/mobile/challenge", "/v0/mobile/session"), urls.map { it.path })
        assertTrue(urls.all { it.host == java.net.URI(site.origin).host })
    }

    @Test fun anUnapprovedStoredOriginIsRejectedBeforeNetworkOrSigning() {
        val keys = Keys(site.copy(origin = "https://untrusted.example"), der)
        var requests = 0
        val api = MobileApi(keys) { url -> requests++; Reply(url, "{}") }
        assertThrows(IllegalArgumentException::class.java) { api.site() }
        assertEquals(0, requests)
        assertEquals(0, keys.signs)
    }
}
