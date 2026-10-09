import SwiftUI

/// Adopt hosting changes made by phone setup only where the user has no draft.
struct MobileSettingsSavedSiteMerge {
    let storage: SiteStorage
    let storageChoiceChanged: Bool
    let gateway: String

    init(saved: Site, previous: Site, storage: SiteStorage, storageChoiceChanged: Bool, gateway: String) {
        if storageChoiceChanged {
            let intended = storage.usesCroptop(original: previous, explicitlyChanged: true)
            let choice = SiteStorage.choosingCroptop(intended, original: saved)
            self.storage = choice.storage
            self.storageChoiceChanged = choice.changed
        } else {
            self.storage = saved.storage
            self.storageChoiceChanged = false
        }
        self.gateway = gateway == (previous.croptopGateway ?? "crop.top") ? (saved.croptopGateway ?? "crop.top") : gateway
    }
}

/// Loads a fresh saved site for each visit, after an earlier connection has
/// stopped. Unsaved settings are never treated as hosting permission.
@MainActor final class MobileSettingsModel: ObservableObject {
    @Published private(set) var connection: PhoneConnectionModel?
    @Published private(set) var error: String?
    @Published private(set) var waitingForCleanup = false
    private let loadSite: (String) async throws -> Site
    private let makeConnection: @MainActor (Site) -> PhoneConnectionModel

    init(loadSite: @escaping (String) async throws -> Site = { try await API.shared.site($0) },
         makeConnection: @escaping @MainActor (Site) -> PhoneConnectionModel = { PhoneConnectionModel(site: $0) }) {
        self.loadSite = loadSite
        self.makeConnection = makeConnection
    }

    func load(siteID: String, app: AppModel, onSavedSite: (Site) -> Void) async {
        guard connection == nil else { return }
        error = nil
        do {
            while app.phonePreparationCleanups > 0 {
                waitingForCleanup = true
                try await Task.sleep(nanoseconds: 100_000_000)
            }
            waitingForCleanup = false
            try Task.checkCancellation()
            let saved = try await loadSite(siteID)
            try Task.checkCancellation()
            guard saved.id == siteID else { throw APIError(message: "The saved site changed. Open Mobile again to retry.") }
            onSavedSite(saved)
            connection = makeConnection(saved)
        } catch is CancellationError {
            // A late response must not mount a connection after tab/site exit.
        } catch {
            guard !Task.isCancelled else { return }
            self.error = error.localizedDescription
        }
    }
}

struct MobileSettingsView: View {
    @EnvironmentObject private var app: AppModel
    @StateObject private var mobile: MobileSettingsModel
    @State private var attempt = 0
    let siteID: String
    let onSavedSite: (Site) -> Void
    let onDone: () -> Void
    let onCleanup: () async -> Void

    init(siteID: String, model: MobileSettingsModel? = nil,
         onSavedSite: @escaping (Site) -> Void, onDone: @escaping () -> Void,
         onCleanup: @escaping () async -> Void) {
        self.siteID = siteID
        _mobile = StateObject(wrappedValue: model ?? MobileSettingsModel())
        self.onSavedSite = onSavedSite
        self.onDone = onDone
        self.onCleanup = onCleanup
    }

    var body: some View {
        Group {
            if let connection = mobile.connection {
                PhoneConnectionView(model: connection, presentation: .settings,
                                    onClose: onDone, onCleanup: onCleanup)
            } else if let error = mobile.error {
                VStack(alignment: .leading, spacing: 12) {
                    Text(error).font(Theme.formHelp)
                    Button("Try again") { attempt += 1 }.buttonStyle(BorderedButton())
                }
            } else {
                LoadingTicker(accessibilityLabel: mobile.waitingForCleanup
                              ? "Stopping your previous phone connection" : "Loading Mobile settings")
            }
        }
        .task(id: attempt) { await mobile.load(siteID: siteID, app: app, onSavedSite: onSavedSite) }
    }
}
