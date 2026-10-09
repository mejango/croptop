import AppKit

enum StorageReviewChoice { case settings, publishP2P, cancel }
enum StorageReviewResult: Equatable { case publish, settings, cancelled }

/// All native publish commands pass through this gate. The server owns the
/// legacy-site predicate; the client never infers consent from a saved host.
@MainActor
enum StoragePublishReview {
    static let explanation = "Hosting now requires a choice for each site. This site's next publish uses peer-to-peer storage unless you enable hosting in Storage settings. P2P does not update the reliable host or its crop.top address, and this Mac must stay online unless other peers keep a copy."

    static func prepare(
        siteID: String,
        read: (String) async throws -> Site,
        saveP2P: (String) async throws -> Void,
        choose: (Site) async -> StorageReviewChoice,
        isCurrent: () -> Bool
    ) async throws -> StorageReviewResult {
        let site = try await read(siteID)
        guard site.id == siteID, isCurrent() else { return .cancelled }
        guard site.croptopStorageNeedsReview == true else { return .publish }
        let choice = await choose(site)
        guard isCurrent() else { return .cancelled }
        switch choice {
        case .cancel: return .cancelled
        case .settings: return .settings
        case .publishP2P:
            // Another window may have changed the choice while the dialog was
            // open. Never overwrite it or apply this choice to another site.
            let latest = try await read(siteID)
            guard latest.id == siteID, isCurrent() else { return .cancelled }
            guard latest.croptopStorageNeedsReview == true else { return .publish }
            try await saveP2P(siteID)
            let saved = try await read(siteID)
            guard saved.id == siteID, isCurrent() else { return .cancelled }
            guard saved.croptopStorage == SiteStorage.p2p.rawValue,
                  saved.croptopStorageNeedsReview == false else {
                throw APIError(message: "Your P2P choice was not saved. Review Storage settings before publishing.")
            }
            return .publish
        }
    }

    static func alert(for site: Site) -> NSAlert {
        let alert = NSAlert()
        alert.messageText = "Review storage for “\(site.name)”"
        alert.informativeText = explanation + "\n\nSaved changes stay on this Mac if you cancel."
        alert.addButton(withTitle: "Open Storage Settings")
        alert.addButton(withTitle: "Publish with P2P")
        alert.addButton(withTitle: "Cancel")
        alert.buttons[2].keyEquivalent = "\u{1b}"
        return alert
    }

    static func choose(_ site: Site) async -> StorageReviewChoice {
        let alert = alert(for: site)
        let response: NSApplication.ModalResponse
        if let window = NSApp.keyWindow {
            guard window.attachedSheet == nil else { return .cancel }
            response = await withCheckedContinuation { continuation in
                alert.beginSheetModal(for: window) { continuation.resume(returning: $0) }
            }
        } else {
            response = alert.runModal()
        }
        switch response {
        case .alertFirstButtonReturn: return .settings
        case .alertSecondButtonReturn: return .publishP2P
        default: return .cancel
        }
    }
}
