import AppKit
import CoreImage.CIFilterBuiltins
import SwiftUI

struct PhoneConnectionSheet: View {
    @EnvironmentObject private var app: AppModel
    @Environment(\.dismiss) private var dismiss
    @StateObject private var connection: PhoneConnectionModel
    @State private var closing = false
    @State private var cleanupStarted = false
    @State private var gateHeld = false

    init(site: Site) { _connection = StateObject(wrappedValue: PhoneConnectionModel(site: site)) }
    init(model: PhoneConnectionModel) { _connection = StateObject(wrappedValue: model) }

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 6) {
                    Text("Connect phone").font(Theme.heading(26))
                    Text(connection.site.name).font(Theme.body(16)).lineLimit(2)
                    Text(connection.site.ipns).font(.system(size: 10, design: .monospaced))
                        .foregroundColor(Theme.muted).lineLimit(1).truncationMode(.middle)
                }
                Spacer()
                Button(action: close) { Image(systemName: "xmark").frame(width: 28, height: 28) }
                    .buttonStyle(.hover).accessibilityLabel("Close Connect phone")
                    .keyboardShortcut(.cancelAction).disabled(closing)
            }
            Divider()
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    content
                    if let error = connection.error {
                        Text(error).font(Theme.formHelp).foregroundColor(Theme.ink)
                            .fixedSize(horizontal: false, vertical: true)
                            .accessibilityLabel("Connection problem: " + error)
                    }
                }.frame(maxWidth: .infinity, alignment: .leading).padding(1)
            }.frame(maxHeight: .infinity)
            if closing { Text("Stopping this connection…").font(Theme.formHelp) }
        }
        .padding(Theme.content)
        .frame(width: 560, height: 660)
        .foregroundColor(Theme.ink).background(Theme.paper)
        .interactiveDismissDisabled()
        .onAppear {
            // Hold before disappearance, so a queued sheet=nil update can
            // never briefly resume Sparkle ahead of its cleanup notification.
            if !gateHeld { app.phonePreparationCleanups += 1; gateHeld = true }
        }
        .task { await connection.monitor() }
        .onDisappear(perform: cleanup)
    }

    @ViewBuilder private var content: some View {
        switch connection.phase {
        case .consent, .publicationRequired, .failed, .expired:
            consent
        case .preparing:
            VStack(alignment: .leading, spacing: 16) {
                HStack(spacing: 8) {
                    LoadingTicker(accessibilityLabel: connection.stageTitle)
                    Text(connection.stageTitle).font(Theme.heading(20))
                }
                Text(connection.stageDetail).font(Theme.formText)
                HStack(spacing: 12) {
                    Text("\(connection.elapsed)s total")
                    Text("Keep this Mac awake")
                }
                .font(Theme.formHelp).foregroundColor(Theme.muted)
                .accessibilityElement(children: .combine)
                Text("You can close this window to stop preparation. Hosting permission and any publication already sent are not undone.")
                    .font(Theme.formHelp).foregroundColor(Theme.muted)
            }
        case .scan, .confirm:
            pairing
        case .sent:
            Label("Key sent securely", systemImage: "lock.shield").font(Theme.heading(20))
            Text("Finish connecting on your phone. Keep this window open while your phone receives the key.").font(Theme.formText)
            LoadingTicker(accessibilityLabel: "Waiting for your phone to receive the key")
        case .delivered:
            Label("Key delivered", systemImage: "checkmark.circle").font(Theme.heading(20))
            Text("Your phone will confirm once it has saved the connection. After that, you can post from your phone even while this Mac sleeps.").font(Theme.formText)
            Button("Done", action: close).buttonStyle(BorderedButton(kind: .hot))
        case .closed:
            EmptyView()
        case .stopping:
            Text("Stopping preparation").font(Theme.heading(20))
            Text("Waiting for this Mac’s publisher to stop. Hosting permission, uploaded content and any key already sent are not revoked.").font(Theme.formText)
        }
    }

    private var consent: some View {
        VStack(alignment: .leading, spacing: 16) {
            if connection.phase == .expired {
                Text("This connection expired").font(Theme.heading(20))
                Text("Create a new private link when your phone is ready.").font(Theme.formText)
            } else {
                Text("Post from your phone").font(Theme.heading(20))
            }
            if connection.needsHostingConsent {
                Toggle(isOn: $connection.consent) {
                    Text("Allow crop.top to host this site.")
                        .font(Theme.formText).fixedSize(horizontal: false, vertical: true)
                }.toggleStyle(.checkbox)
            }
            if connection.phase == .publicationRequired {
                Text("This site needs an update for phone posting. Publishing will make all saved changes on this Mac public.")
                    .font(Theme.formText).fixedSize(horizontal: false, vertical: true)
            }
            Text("Only connect a phone you trust: it receives a full copy of this site’s publishing key over an encrypted connection. You will confirm the phone’s code before the key is sent.")
                .font(Theme.formHelp).fixedSize(horizontal: false, vertical: true)
            if connection.phase == .publicationRequired {
                Button("Publish and connect") { connection.publishAndConnect() }
                    .buttonStyle(BorderedButton(kind: .hot)).disabled(!connection.canPublish)
                    .keyboardShortcut(.defaultAction)
            } else {
                Button("Connect") { connection.begin() }
                    .buttonStyle(BorderedButton(kind: .hot)).disabled(!connection.canStart)
                    .keyboardShortcut(.defaultAction)
            }
        }
    }

    @ViewBuilder private var pairing: some View {
        if let pairing = connection.pairing {
            VStack(alignment: .leading, spacing: 14) {
                Text(connection.phase == .confirm ? "Confirm your phone" : "Scan with your phone")
                    .font(Theme.heading(20))
                VStack(alignment: .leading, spacing: 8) {
                    if let image = PhoneConnectionQR.image(for: pairing.url) {
                        Image(nsImage: image).interpolation(.none).resizable().frame(width: 196, height: 196)
                            .accessibilityLabel("Private connection QR code. Scan with your phone.")
                    }
                    Text("Expires at \(Date(timeIntervalSince1970: pairing.expiresAt).formatted(date: .omitted, time: .shortened))")
                        .font(Theme.formHelp).foregroundColor(Theme.muted)
                }
                if connection.phase == .confirm {
                    Labeled(title: "Eight-digit code shown on your phone") {
                        TextField("00000000", text: $connection.code).field().font(Theme.code)
                            .accessibilityLabel("Eight-digit code shown on your phone")
                            .disableAutocorrection(true)
                            .onSubmit { connection.confirm() }
                    }
                    Button(connection.busy ? "Confirming…" : "Give this phone publishing access") { connection.confirm() }
                        .buttonStyle(BorderedButton(kind: .hot)).disabled(!connection.canConfirm)
                }
            }
        }
    }

    private func close() {
        guard !closing else { return }
        closing = true
        Task {
            if await connection.close() {
                dismiss()
            } else { closing = false }
        }
    }

    private func cleanup() {
        guard !cleanupStarted else { return }
        cleanupStarted = true
        // Menu commands can replace a sheet without pressing its Close button.
        // Keep the updater/quit gate aware of that now-hidden cancellation.
        if !gateHeld { app.phonePreparationCleanups += 1; gateHeld = true }
        Task {
            defer { app.phonePreparationCleanups -= 1; gateHeld = false }
            while !(await connection.close()) {
                app.toast("Phone preparation is still stopping. Keep Croptop open.")
                do { try await Task.sleep(nanoseconds: 5_000_000_000) }
                catch { return }
            }
            await app.load()
        }
    }
}

enum PhoneConnectionQR {
    static func image(for link: String) -> NSImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(link.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage,
              let image = CIContext().createCGImage(output, from: output.extent) else { return nil }
        return NSImage(cgImage: image, size: output.extent.size)
    }
}
