// New site and Follow: the two small forms that come up as sheets.
import SwiftUI

struct NewSiteSheet: View {
    private enum Mode: String, CaseIterable {
        case fresh = "Start fresh"
        case curate = "Curate"
    }

    @EnvironmentObject var model: AppModel
    @State private var mode: Mode = .fresh
    @State private var name = ""
    @State private var about = ""
    @State private var logo: URL?
    @State private var sources = ""
    @State private var busy = false
    @State private var error: String?
    @FocusState private var nameFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            Text("New site").font(Theme.heading(28))
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    Labeled(title: "Name") {
                        TextField("Name", text: $name).field(compact: true).focused($nameFocused)
                    }
                    Labeled(title: "About") {
                        TextField("A line about it (optional)", text: $about).field(compact: true)
                    }
                    Labeled(title: "Logo") { SiteLogoPicker(selection: $logo) }
                    Picker("Site type", selection: $mode) {
                        ForEach(Mode.allCases, id: \.self) { option in
                            Text(option.rawValue).tag(option)
                        }
                    }
                    .pickerStyle(.radioGroup).horizontalRadioGroupLayout()
                    .controlSize(.small).labelsHidden().font(Theme.formText)
                    .fixedSize(horizontal: true, vertical: true)
                    if mode == .curate {
                        Labeled(title: "Sites", help: "Combine posts from these sites, with credit to the original authors. Enter one ENS name or IPNS address per line.") {
                            TextEditor(text: $sources)
                                .font(Theme.formText)
                                .scrollContentBackground(.hidden)
                                .frame(height: 88)
                                .padding(6)
                                .fieldOutline()
                                .accessibilityLabel("Sites")
                        }
                    }
                }
                .padding(1)
                .frame(maxWidth: .infinity, alignment: .leading)
            }
            .frame(maxHeight: mode == .curate ? 500 : 320)
            if let error {
                Text(error).font(Theme.formHelp).foregroundColor(Theme.attention)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack(spacing: 8) {
                Spacer()
                Button("Cancel") { model.sheet = nil }
                    .buttonStyle(BorderedButton(kind: .quiet))
                    .keyboardShortcut(.cancelAction)
                Button(busy ? "Creating…" : "Create", action: create)
                    .buttonStyle(BorderedButton(kind: .hot))
                    .disabled(!canCreate)
                    .keyboardShortcut(.defaultAction)
            }
        }
        .disabled(busy)
        .padding(Theme.content)
        .frame(width: 520)
        .background(Theme.paper)
        .interactiveDismissDisabled(busy)
        .defaultFocus($nameFocused, true)
        .task { nameFocused = true }
        .onChange(of: mode) { _ in error = nil }
    }

    private var canCreate: Bool {
        !busy && !name.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
            && (mode == .fresh || !sources.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
    }

    private func create() {
        guard canCreate else { return }
        let submittedName = name
        let submittedAbout = about
        let submittedLogo = logo
        let submittedSources = mode == .curate ? sources : nil
        error = nil
        busy = true
        Task {
            do {
                let site = try await API.shared.createSite(name: submittedName, about: submittedAbout, avatar: submittedLogo, sources: submittedSources)
                var gatewayError: Error?
                do { try await API.shared.saveSite(site.id, values: ["gateway": "crop.top"]) }
                catch { gatewayError = error }
                await model.load()
                model.sheet = nil
                model.screen = .site(site.id)
                if let gatewayError { model.show(gatewayError) }
            } catch { self.error = error.localizedDescription }
            busy = false
        }
    }
}

struct FollowSheet: View {
    @EnvironmentObject var model: AppModel
    @State private var name = ""
    @State private var busy = false

    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            Text("Follow a site").font(Theme.heading(28))
            Labeled(title: "IPNS name or ENS domain") { TextField("k51… or theirsite.eth", text: $name).field() }
            HStack(spacing: 8) {
                Spacer()
                Button("Cancel") { model.sheet = nil }.buttonStyle(BorderedButton(kind: .quiet)).keyboardShortcut(.cancelAction)
                Button(busy ? "Following…" : "Follow") {
                    busy = true
                    Task {
                        do {
                            try await API.shared.follow(name.trimmingCharacters(in: .whitespaces))
                            await model.load()
                            model.sheet = nil
                            model.screen = .feed
                            await model.refreshFeed()
                        } catch { model.show(error) }
                        busy = false
                    }
                }.buttonStyle(BorderedButton(kind: .hot)).disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || busy).keyboardShortcut(.defaultAction)
            }
        }
        .padding(Theme.content)
        .frame(width: 520)
        .background(Theme.paper)
    }
}
