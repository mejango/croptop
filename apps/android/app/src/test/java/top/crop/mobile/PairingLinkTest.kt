package top.crop.mobile

import org.junit.Assert.*
import org.junit.Test
import java.util.Base64

class PairingLinkTest {
    private val origin = BuildConfig.MOBILE_SERVICE_ORIGIN
    private val id = Base64.getUrlEncoder().withoutPadding().encodeToString(ByteArray(32) { it.toByte() })
    private val secret = Base64.getUrlEncoder().withoutPadding().encodeToString(ByteArray(32) { (it + 32).toByte() })
    private val fragment = "pair=$id.$secret"

    @Test fun acceptsOnlyCanonicalApprovedConnectionLink() {
        val parsed = PairingLink.parse("$origin/#$fragment", origin)
        assertEquals(origin, parsed.origin)
        assertEquals(id, parsed.id)
        assertEquals(secret, parsed.capability)
        assertFalse(parsed.toString().contains(secret))
    }

    @Test fun rejectsWrongOriginPathQueryUserinfoPortAndEncodingWithoutReflectingCapability() {
        val host = java.net.URI(origin).host
        val invalid = listOf(
            "https://example.com/#$fragment", "http://$host/#$fragment", "$origin.evil.example/#$fragment",
            "https://$host@evil.example/#$fragment", "https://me@$host/#$fragment", "https://$host:443/#$fragment",
            "$origin/another/#$fragment", "$origin/%2f#$fragment", "$origin/?source=test#$fragment", "$origin#$fragment",
            "$origin/#$fragment&extra=1", "$origin/#pair=$id.%32$secret", "$origin/#pair=$id.$secret=",
            "https://[$secret/#$fragment", "$origin/%bad%#$fragment", "$origin/#pair=$id",
            "$origin/#pair=$id.${secret.dropLast(1)}_",
        )
        for (link in invalid) {
            val error = assertThrows(Exception::class.java) { PairingLink.parse(link, origin) }
            assertFalse("A malformed link exposed its capability", error.message.orEmpty().contains(secret))
        }
    }

    @Test fun configuredServiceOwnsManualImportDestinationToo() {
        PublishingService.requireOrigin(origin)
        assertThrows(IllegalArgumentException::class.java) { PublishingService.requireOrigin("https://example.com") }
        assertThrows(IllegalArgumentException::class.java) { PublishingService.requireOrigin("$origin/") }
    }
}
