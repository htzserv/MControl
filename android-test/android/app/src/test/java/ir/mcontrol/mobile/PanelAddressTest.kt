package ir.mcontrol.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class PanelAddressTest {
    @Test fun preservesPanelPrefixAndPort() {
        assertEquals("https://example.org:9443/private/api/setup/status",
            statusAddress(" https://example.org:9443/private/ ").toString())
    }
    @Test fun supportsRootPanel() {
        assertEquals("https://example.org/api/setup/status", statusAddress("https://example.org").toString())
    }
    @Test fun rejectsPlainHttpAndEmbeddedCredentials() {
        for (url in listOf("http://example.org", "https://user:pass@example.org")) {
            assertThrows(IllegalArgumentException::class.java) { statusAddress(url) }
        }
    }
}
