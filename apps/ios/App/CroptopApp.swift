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
    @State private var showConnection = false
    @State private var showComposer = false
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
                            Button("Check / reconnect site") { showConnection = true }
                            if site.enabled { Button("Stop phone posting") { Task { await model.enable(false) } } }
                            Button("Remove site from this phone", role: .destructive) { showRemoveConfirmation = true }
                        }
                    } else {
                        Text("Connect once. Post anywhere, even with your computer asleep.").foregroundStyle(.secondary)
                        Button("Connect your site") { showConnection = true }.disabled(model.store == nil)
                    }
                }
                if let message = model.message { Section { Text(message).font(.callout) } }
                Section("Drafts and posts") {
                    if model.drafts.isEmpty { Text("Saved screenshots appear here, including images shared before setup.").foregroundStyle(.secondary) }
                    ForEach(model.drafts) { draft in
                        Button {
                            model.select(draft.id); showComposer = true
                        } label: {
                            HStack {
                                Image(systemName: draft.operation?.state == .published ? "checkmark.circle" : "photo")
                                VStack(alignment: .leading, spacing: 4) {
                                    Text(draft.title.isEmpty ? (draft.caption.isEmpty ? "Screenshot" : draft.caption) : draft.title).lineLimit(1)
                                    Text(draft.statusLabel + " · " + draft.createdAt.formatted(date: .abbreviated, time: .shortened))
                                        .font(.caption).foregroundStyle(.secondary)
                                }
                            }.padding(.vertical, 4)
                        }
                    }
                }
                Section("Share from anywhere") {
                    Text("In Screenshot, Photos or Files, tap Share, then Croptop. If it is hidden, scroll to More and add Croptop to your favorites.")
                        .font(.callout).foregroundStyle(.secondary)
                }
            }
            .navigationTitle("Croptop")
            .sheet(isPresented: $showConnection) { ConnectionView(model: model) }
            .sheet(isPresented: $showComposer) {
                NavigationStack {
                    if let draft = model.selected {
                        ComposerView(model: model, draft: draft, onSaved: { showComposer = false }).id(draft.id)
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
                        guard let data = try await selection?.loadTransferable(type: Data.self) else { return }
                        try model.capture(data); photo = nil; showComposer = true
                    } catch { model.message = "Screenshot could not be saved: \(error.localizedDescription)" }
                }
            }
            .onChange(of: scenePhase) { _, phase in
                if phase == .active {
                    do { try model.reload() } catch { model.message = error.localizedDescription }
                }
            }
        }.tint(Color(red: 0.38, green: 0.22, blue: 0.77))
    }
}

private struct ConnectionView: View {
    @ObservedObject var model: ComposerModel
    @Environment(\.dismiss) private var dismiss
    @State private var link = ""
    @State private var origin = "https://app.crop.top"
    @State private var pem = ""
    @State private var importing = false

    var body: some View {
        NavigationStack {
            Form {
                Section("Connect from your publisher") {
                    Text("Choose Connect phone in Croptop on your computer, then paste the connection link here.")
                    TextField("Connection link", text: $link).keyboardType(.URL).textInputAutocapitalization(.never)
                        .autocorrectionDisabled().privacySensitive()
                    Button("Connect with link") {
                        let submittedLink = link; link = ""
                        Task { await model.claimPairing(link: submittedLink) }
                    }.disabled(link.isEmpty || model.busy)
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
                        TextField("Service address", text: $origin).keyboardType(.URL).textInputAutocapitalization(.never).autocorrectionDisabled()
                        SecureField("Paste PKCS8 site key", text: $pem)
                            .textInputAutocapitalization(.never).autocorrectionDisabled().privacySensitive()
                            .accessibilityLabel("PKCS8 Ed25519 private key")
                        Button("Choose key file") { importing = true }
                        Button("Import key locally") {
                            let value = pem; pem = ""
                            Task { await model.importKey(pem: value, originText: origin) }
                        }.disabled(pem.isEmpty || model.busy)
                    }
                }
                if model.busy { ProgressView("Connecting…") }
                if let message = model.message { Section { Text(message) } }
            }
            .navigationTitle("Connect your site").navigationBarTitleDisplayMode(.inline)
            .toolbar { ToolbarItem(placement: .confirmationAction) { Button("Done") { pem = ""; link = ""; dismiss() } } }
            .fileImporter(isPresented: $importing, allowedContentTypes: [.data]) { result in
                do {
                    let url = try result.get(); let accessible = url.startAccessingSecurityScopedResource()
                    defer { if accessible { url.stopAccessingSecurityScopedResource() } }
                    let data = try Data(contentsOf: url)
                    guard data.count < 8192, let value = String(data: data, encoding: .utf8) else {
                        throw MobileError.invalid("Choose a small PKCS8 PEM site key file.")
                    }
                    pem = value
                } catch { model.message = error.localizedDescription }
            }
        }
    }
}
