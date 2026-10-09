package top.crop.mobile

/** Release-selected service. Imported keys and incoming links use this same trust boundary. */
object PublishingService {
    const val origin = BuildConfig.MOBILE_SERVICE_ORIGIN
    fun requireOrigin(value: String) {
        require(value == origin) { "This connection uses a different publishing service. Reconnect through this Croptop app's Connect phone link." }
    }
}
