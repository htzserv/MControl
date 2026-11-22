package ir.mcontrol.mobile

import java.net.URI
import java.net.URL

internal fun statusAddress(input: String): URL {
    val uri = URI(input.trim())
    require(uri.scheme.equals("https", ignoreCase = true) && uri.host != null &&
        uri.rawUserInfo == null && uri.rawQuery == null && uri.rawFragment == null &&
        (uri.port == -1 || uri.port in 1..65535))
    return URL(uri.toASCIIString().trimEnd('/') + "/api/setup/status")
}
