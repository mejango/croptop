import SwiftUI
import PhotosUI
import CroptopMobileCore

@main
struct CroptopApp: App {
    var body: some Scene { WindowGroup { HomeView() } }
}

struct HomeView: View {
    @StateObject private var model = ComposerModel()
    @State private var photo: PhotosPickerItem?
    @State private var presentation = MobilePresentation()
    @State private var showRemoveConfirmation = false
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        NavigationStack {
            List {
                if let fatal = model.fatalStorageError { Text(fatal).foregroundStyle(.red) }
                Section {
                    PhotosPicker(selection: $photo, matching: .images, preferredItemEncoding: .current) {
                        Label("Choose a screenshot", systemImage: "photo.badge.plus").font(.headline)
                            .frame(maxWidth: .infinity, minHeight: 48)
                    }.disabled(model.store == nil || model.busy)
                    Text("One image, a few words, your site.").foregroundStyle(.secondary)
                }
                Section("Your site") {
                    if let site = model.connection {
                        Text(site.name).font(.headline)
                        Text(site.ipns).font(.caption).foregroundStyle(.secondary).textSelection(.enabled)
                        if !site.enabled {
                            Text("Allow this service to prepare and host your posts. Your phone keeps the site key and approves each publication.")
                                .font(.callout)
                            Button("Enable phone posting") { Task { await model.enable(true) } }.disabled(model.busy)
                        }
                        Menu("Connection settings") {
                            Button("Check / reconnect site") { presentation.sheet = .connection }
                            if site.enabled { Button("Stop phone posting") { Task { await model.enable(false) } } }
                            Button("Remove site from this phone", role: .destructive) { showRemoveConfirmation = true }
                        }.disabled(model.busy || model.hasPairing || model.incomingPairingReady)
                    } else {
                        Text("Connect once. Post anywhere, even with your computer asleep.").foregroundStyle(.secondary)
                        Button("Connect your site") { presentation.sheet = .connection }.disabled(model.store == nil || model.busy)
                    }
                }
                if let message = model.message { Section { Text(message).font(.callout) } }
                Section("Drafts and posts") {
                    if model.drafts.isEmpty { Text("Saved screenshots appear here, including images shared before setup.").foregroundStyle(.secondary) }
                    ForEach(model.drafts) { draft in
                        Button {
                            model.select(draft.id); presentation.sheet = .composer
                        } label: {
                            HStack {
                                Image(systemName: draft.operation?.state == .published ? "checkmark.circle" : "photo")
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(draft.title.isEmpty ? (draft.caption.isEmpty ? "Screenshot" : draft.caption) : draft.title).lineLimit(1)
                                    Text(draft.statusLabel + " · " + draft.createdAt.formatted(date: .abbreviated, time: .shortened))
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                            }.padding(.vertical, 4)
                        }.disabled(model.busy)
                    }
                }
                Section("Share from anywhere") {
                    Text("In Screenshot, Photos or Files, tap Share, then Croptop. If it is hidden, scroll to More and add Croptop to your favorites.")
                        .font(.callout).foregroundStyle(.secondary)
                }
                Section {
                    NavigationLink("Privacy and your content") { PrivacyInformationView() }
                }
            }
            .navigationTitle("Croptop")
            .sheet(item: $presentation.sheet, onDismiss: { presentation.didDismiss() }) { sheet in
                switch sheet {
                case .connection: ConnectionView(model: model)
                case .composer:
                    NavigationStack {
                        if let draft = model.selected {
                            ComposerView(model: model, draft: draft, onSaved: { presentation.sheet = nil }).id(draft.id)
                        }
                    }
                }
            }
            .alert("Remove this phone's connection?", isPresented: $showRemoveConfirmation) {
                Button("Remove", role: .destructive) { model.removeConnection() }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("The local key is deleted. Drafts are kept, but need the same site's key to resume. This does not revoke other copies of your site key.")
            }
            .onChange(of: photo) { _, selection in
                Task {
                    do {
                        guard let image = try await selection?.loadTransferable(type: ScreenshotTransfer.self) else { return }
                        let mayPresent = presentation.mayPresentCapturedDraft && !model.busy && !model.hasPairing && !model.incomingPairingReady
                        try model.capture(image.data, selectAfterCapture: mayPresent); photo = nil
                        if mayPresent { presentation.sheet = .composer }
                    } catch { model.message = "Screenshot could not be saved: \(error.localizedDescription)" }
                }
            }
            .onChange(of: scenePhase) { _, phase in
                if phase == .active {
                    do { try model.reload() } catch { model.message = error.localizedDescription }
                }
            }
            .onOpenURL { url in
                if model.receivePairingURL(url) {
                    presentation.openPairing(hasSelectedDraft: model.selected != nil)
                }
            }
        }.tint(Color(red: 0.38, green: 0.22, blue: 0.77))
    }
}

private struct PrivacyInformationView: View {
    var body: some View {
        List {
            Section("What leaves this phone") {
                Text("When you prepare a post, Croptop sends its image, title, caption and site identifier to the publishing service. They are used to prepare, host and publish your post.")
                Text(MobileEnvironment.productionOrigin.host ?? "Croptop publishing service").font(.caption).textSelection(.enabled)
            }
            Section("Your publishing key") {
                Text("Your site key stays in this phone's Keychain. The publishing service receives signatures authorizing a particular update. Keep the original publisher or a key backup for recovery.")
            }
            Section("Drafts and published content") {
                Text("Local drafts stay on this phone. The service keeps operation records indefinitely, including submitted titles, captions, site and post identifiers, image hashes and types, status and receipts, so retries can recover the same post.")
                Text("Seven-day cleanup removes private media and staging files only after publication has been reconciled. Uncertain operations keep their uploaded files.")
                Text("Published content is public. Copies on IPFS or other hosts may persist. Removing a connection from this phone deletes its local key; it does not erase published content or revoke other key copies.")
            }
        }.navigationTitle("Privacy").navigationBarTitleDisplayMode(.inline)
    }
}

private struct ConnectionView: View {
    @ObservedObject var model: ComposerModel
    @Environment(\.dismiss) private var dismiss
    @State private var link = ""
    @State private var pem = ""
    @State private var importing = false
    @Environment(\.scenePhase) private var scenePhase

    var body: some View {
        NavigationStack {
            Form {
                Section("Connect from your publisher") {
                    Text("Choose Connect phone in Croptop on your computer, then paste the connection link here.")
                    if model.incomingPairingReady {
                        Text("A connection link from Croptop is ready. Its private connection code stays in memory until you continue or close this screen.")
                        Button("Continue connection") { Task { await model.claimIncomingPairing() } }.disabled(model.busy)
                    } else if !model.hasPairing {
                        SecureField("Connection link", text: $link).keyboardType(.URL).textInputAutocapitalization(.never)
                            .autocorrectionDisabled().privacySensitive()
                        Button("Connect with link") {
                            let submittedLink = link; link = ""
                            Task { await model.claimPairing(link: submittedLink) }
                        }.disabled(link.isEmpty || model.busy || model.connection != nil)
                    }
                    if let code = model.pairingCode {
                        Text(code).font(.largeTitle.monospacedDigit()).textSelection(.enabled)
                        if let ipns = model.pairingIPNS { Text(ipns).font(.caption).textSelection(.enabled) }
                        Text("Enter this code on your publisher and confirm the same site there. The site's full publishing key will be stored on this phone.")
                        Button("Site and code match — finish connection") { Task { await model.confirmPairing() } }
                            .disabled(model.busy)
                    }
                }
                Section {
                    DisclosureGroup("Import an existing site key") {
                        Text("The key stays in this phone's Keychain. Keep your original backup; it controls the entire site.").font(.callout)
                        Text(MobileEnvironment.productionOrigin.host ?? "Croptop publishing service").font(.caption).foregroundStyle(.secondary)
                        SecureField("Paste PKCS8 site key", text: $pem)
                            .textInputAutocapitalization(.never).autocorrectionDisabled().privacySensitive()
                            .accessibilityLabel("PKCS8 Ed25519 private key")
                        Button("Choose key file") { importing = true }
                        Button("Import key locally") {
                            let value = pem; pem = ""
                            Task { await model.importKey(pem: value) }
                        }.disabled(pem.isEmpty || model.busy || model.hasPairing || model.incomingPairingReady)
                    }
                }
                if model.busy { ProgressView("Connecting…") }
                if let message = model.message { Section { Text(message) } }
            }
            .navigationTitle("Connect your site").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { pem = ""; link = ""; model.cancelPairing(); dismiss() }.disabled(model.busy) } }
            .interactiveDismissDisabled(model.busy)
            .onDisappear { pem = ""; link = ""; model.cancelPairing() }
            .onChange(of: scenePhase) { _, phase in if phase != .active { pem = ""; link = "" } }
            .fileImporter(isPresented: $importing, allowedContentTypes: [.data]) { result in
                do {
                    let url = try result.get(); let accessible = url.startAccessingSecurityScopedResource()
                    defer { if accessible { url.stopAccessingSecurityScopedResource() } }
                    let data = try BoundedFile.read(url: url, maxBytes: 8191)
                    guard data.count < 8192, let value = String(data: data, encoding: .utf8) else {
                        throw MobileError.invalid("Choose a small PKCS8 PEM site key file.")
                    }
                    pem = value
                } catch { model.message = error.localizedDescription }
            }
        }
    }
}
