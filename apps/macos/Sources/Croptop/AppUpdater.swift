import AppKit
import Combine
import Sparkle
import SwiftUI

// A pending install resumes once, after the user finishes editing and publishing.
@MainActor
final class UpdateRelaunchGate {
    var blocked = false { didSet { resumeIfReady() } }
    private var pending: (() -> Void)?
    func postpone(_ install: @escaping () -> Void) -> Bool {
        guard blocked else { return false }
        pending = install
        return true
    }
    func cancel() { pending = nil }
    private func resumeIfReady() {
        guard !blocked, let install = pending else { return }
        pending = nil
        install()
    }
}

@MainActor
final class AppUpdater: NSObject, ObservableObject, SPUUpdaterDelegate {
    @Published private(set) var canCheck = false
    @Published private(set) var availableVersion: String?
    @Published private(set) var waitingToRelaunch = false
    private var controller: SPUStandardUpdaterController?
    private var observations = Set<AnyCancellable>()
    private let gate = UpdateRelaunchGate()

    func start(model: AppModel) {
        // Tests and unbundled development executables have no update feed.
        guard controller == nil, Bundle.main.object(forInfoDictionaryKey: "SUFeedURL") != nil,
              Bundle.main.object(forInfoDictionaryKey: "SUPublicEDKey") != nil else { return }
        let controller = SPUStandardUpdaterController(startingUpdater: false, updaterDelegate: self, userDriverDelegate: nil)
        self.controller = controller
        controller.updater.publisher(for: \.canCheckForUpdates).assign(to: &$canCheck)
        Publishers.CombineLatest4(model.$screen, model.$sheet, model.$publishing, model.$collaborationOpen)
            .receive(on: RunLoop.main)
            .sink { [weak self] screen, sheet, publishing, collaborationOpen in
                self?.gate.blocked = Self.blocksRelaunch(screen: screen, sheet: sheet, publishing: publishing, collaborationOpen: collaborationOpen)
            }.store(in: &observations)
        controller.startUpdater()
    }

    static func blocksRelaunch(screen: Screen, sheet: Sheet?, publishing: Set<String>, collaborationOpen: Bool = false) -> Bool {
        if collaborationOpen || sheet != nil || !publishing.isEmpty { return true }
        switch screen {
        case .editor, .quick, .settings: return true
        default: return false
        }
    }

    func check() { controller?.checkForUpdates(nil) }
    func updater(_ updater: SPUUpdater, didFindValidUpdate item: SUAppcastItem) {
        availableVersion = item.displayVersionString + " (build " + item.versionString + ")"
    }
    func updaterDidNotFindUpdate(_ updater: SPUUpdater, error: Error) { availableVersion = nil }
    func updater(_ updater: SPUUpdater, shouldPostponeRelaunchForUpdate item: SUAppcastItem,
                 untilInvokingBlock installHandler: @escaping () -> Void) -> Bool {
        let postponed = gate.postpone { [weak self] in
            self?.waitingToRelaunch = false
            installHandler()
        }
        waitingToRelaunch = postponed
        return postponed
    }
    func updater(_ updater: SPUUpdater, didFinishUpdateCycleFor updateCheck: SPUUpdateCheck, error: Error?) {
        if error != nil { gate.cancel(); waitingToRelaunch = false }
    }
}

struct CheckForUpdates: View {
    @ObservedObject var updater: AppUpdater
    var body: some View {
        Button("Check for Updates…") { updater.check() }.disabled(!updater.canCheck)
    }
}

struct UpdateIndicator: View {
    @ObservedObject var updater: AppUpdater
    var body: some View {
        if updater.waitingToRelaunch {
            Text("Update ready. Finish or cancel editing to restart.")
                .font(Theme.body(11)).foregroundColor(Theme.muted)
                .fixedSize(horizontal: false, vertical: true)
        } else if let version = updater.availableVersion {
            Button("Update available") { updater.check() }
                .font(Theme.body(11)).foregroundColor(Theme.ink)
                .buttonStyle(.plain).disabled(!updater.canCheck)
                .help("Update Croptop to " + version)
                .accessibilityLabel("Update available: Croptop " + version)
        }
    }
}
