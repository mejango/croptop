/// The explicit publishing choice stored by the local console.
enum SiteStorage: String {
    case p2p
    case hosted

    init(savedValue: String?) {
        self = savedValue == Self.hosted.rawValue ? .hosted : .p2p
    }

    func effectiveGateway(_ configured: String?) -> String {
        let gateway = configured.flatMap { $0.isEmpty ? nil : $0 } ?? "crop.top"
        return self == .p2p && gateway == "crop.top" ? "sucks" : gateway
    }

    // Saving unrelated settings must not silently acknowledge an upgrade
    // notice. A changed toggle or the explicit publish choice supplies consent.
    func settingChange(original: Site, explicitlyChanged: Bool) -> String? {
        if original.croptopStorageNeedsReview == true && self == original.storage && !explicitlyChanged { return nil }
        return rawValue
    }
}
