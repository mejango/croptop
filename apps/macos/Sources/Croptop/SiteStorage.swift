import Foundation

/// The explicit publishing choice stored by the local console.
enum SiteStorage: String {
    case p2p
    case hosted

    /// The Mac app offers crop.top; custom publishers remain supported by the CLI.
    static let publishingHost = "https://crop.top"

    static func isCroptopHost(_ configured: String?) -> Bool {
        let host = configured?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return host.isEmpty || host == publishingHost || host == publishingHost + "/"
    }

    func usesCroptop(original: Site, explicitlyChanged: Bool = false) -> Bool {
        self == .hosted && (explicitlyChanged || Self.isCroptopHost(original.croptopHost))
    }

    static func choosingCroptop(_ enabled: Bool, original: Site) -> (storage: SiteStorage, changed: Bool) {
        let changed = enabled != original.storage.usesCroptop(original: original)
        return (changed ? (enabled ? .hosted : .p2p) : original.storage, changed)
    }

    /// Merely saving another setting cannot move an existing site's content to
    /// a different host. An explicit checkbox change supplies that permission.
    static func hostSettingChange(original: Site, explicitlyChanged: Bool) -> String? {
        explicitlyChanged || isCroptopHost(original.croptopHost) ? publishingHost : nil
    }

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
