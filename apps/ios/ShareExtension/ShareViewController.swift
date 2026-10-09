import UIKit
import SwiftUI
import UniformTypeIdentifiers
import CroptopMobileCore

final class ShareViewController: UIViewController {
    private var host: UIViewController?
    private var intakeStarted = false
    private var retainedModel: ComposerModel?

    override func viewDidAppear(_ animated: Bool) {
        super.viewDidAppear(animated)
        guard !intakeStarted else { return }; intakeStarted = true
        show(ShareMessageView(message: "Saving screenshot…", done: nil))
        let items = extensionContext?.inputItems.compactMap { $0 as? NSExtensionItem } ?? []
        let attachments = items.flatMap { $0.attachments ?? [] }
        Task { @MainActor [weak self] in
            guard let self else { return }
            do {
                let model = ComposerModel()
                guard let store = model.store else { throw MobileError.storage("Shared draft storage is unavailable.") }
                let draft = try await ImageIntake.persist(providers: attachments, store: store, destination: model.connection)
                try model.reload(); model.select(draft.id)
                self.retainedModel = model
                self.show(ShareComposerHost(model: model, draftID: draft.id) { [weak self] in self?.finish() })
            } catch { self.showError("Screenshot was not saved: \(error.localizedDescription)") }
        }
    }

    private func showError(_ message: String) { show(ShareMessageView(message: message, done: { [weak self] in self?.finish() })) }
    private func finish() { extensionContext?.completeRequest(returningItems: nil) }

    private func show<Content: View>(_ content: Content) {
        if let host { host.willMove(toParent: nil); host.view.removeFromSuperview(); host.removeFromParent() }
        let controller = UIHostingController(rootView: content)
        addChild(controller); view.addSubview(controller.view)
        controller.view.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            controller.view.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            controller.view.trailingAnchor.constraint(equalTo: view.trailingAnchor),
            controller.view.topAnchor.constraint(equalTo: view.topAnchor),
            controller.view.bottomAnchor.constraint(equalTo: view.bottomAnchor)
        ])
        controller.didMove(toParent: self); host = controller
    }
}

private struct ShareComposerHost: View {
    @ObservedObject var model: ComposerModel
    let draftID: String
    let done: () -> Void

    var body: some View {
        NavigationStack {
            if let draft = model.selected {
                ComposerView(model: model, draft: draft, onSaved: done).id(draft.id)
            }
        }.tint(Color(red: 0.38, green: 0.22, blue: 0.77))
    }
}

private struct ShareMessageView: View {
    let message: String
    let done: (() -> Void)?
    var body: some View {
        VStack(spacing: 24) {
            Image(systemName: "photo.on.rectangle").font(.largeTitle)
            Text(message).multilineTextAlignment(.center)
            if let done { Button("Done", action: done).buttonStyle(.borderedProminent) }
            else { ProgressView() }
        }.padding(32).frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}
