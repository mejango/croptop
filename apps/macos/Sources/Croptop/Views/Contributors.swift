import SwiftUI

struct ContributorsView: View {
    @EnvironmentObject var model: AppModel
    var siteID: String
    @State private var state: CollaborationState?
    @State private var sources = ""
    @State private var adding = false
    @State private var busy = false
    @State private var error: String?
    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            if let error { Text(error).font(Theme.body(14)).foregroundColor(Theme.attention) }
            if let state {
                ForEach(state.contributors) { contributor in
                    HStack(spacing: 12) {
                        SiteAvatar(url: API.shared.url("/v0/croptop/sites/\(siteID)/collaboration/sources/\(contributor.ipns)/avatar"), name: contributor.label, size: 40)
                        VStack(alignment: .leading, spacing: 6) {
                            Text(contributor.label).font(Theme.formText)
                            if let problem = state.errors[contributor.ipns] {
                                Text("Couldn’t refresh this source. Its previous posts are still here.").font(Theme.formHelp).foregroundColor(Theme.muted).help(problem)
                            }
                        }
                        Spacer()
                    }
                }
                if state.contributors.isEmpty { Text("Nothing yet.").font(Theme.formHelp).foregroundColor(Theme.muted) }
                if adding {
                    Labeled(title: "Sites", help: "One ENS name or IPNS address per line.") {
                        TextEditor(text: $sources).font(Theme.formText).frame(height: 100).padding(8).fieldOutline()
                    }
                    HStack {
                        Button(busy ? "Adding…" : "Add sites") { Task { await add() } }.buttonStyle(BorderedButton()).disabled(busy || sourceNames.isEmpty)
                        Button("Cancel") { adding = false }.buttonStyle(BorderedButton()).disabled(busy)
                    }
                } else {
                    HStack {
                        Button("Add sites") { adding = true }.buttonStyle(BorderedButton())
                        Spacer()
                        if !state.contributors.isEmpty { Button(busy ? "Refreshing…" : "Refresh") { Task { await refresh() } }.buttonStyle(BorderedButton()).disabled(busy) }
                    }
                }
            } else if error == nil { LoadingTicker(accessibilityLabel: "Loading sites") }
        }
        .onAppear { model.collaborationOpen = true }
        .onDisappear { model.collaborationOpen = false }
        .task { await load() }
    }
    private var sourceNames: [String] { sources.components(separatedBy: .newlines).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty } }
    private func load() async {
        error = nil
        do { state = try await API.shared.collaboration(siteID) }
        catch { self.error = error.localizedDescription }
    }
    private func add() async {
        busy = true; error = nil; defer { busy = false }
        do { state = try await API.shared.mergeSources(siteID, names: sourceNames); adding = false; sources = ""; await model.load(); await model.loadPosts(siteID) }
        catch { self.error = error.localizedDescription }
    }
    private func refresh() async {
        busy = true; error = nil; defer { busy = false }
        do { state = try await API.shared.mergeSources(siteID); await model.loadPosts(siteID) }
        catch { self.error = error.localizedDescription }
    }
}

struct CurationDraft {
    var name = ""
    var sources = ""
}

struct CurateSheet: View {
    @EnvironmentObject var model: AppModel
    var embedded = false
    var onBusyChange: (Bool) -> Void = { _ in }
    var draft: Binding<CurationDraft>? = nil
    @State private var localDraft = CurationDraft()
    @State private var busy = false
    @State private var error: String?
    @FocusState private var nameFocused: Bool
    private var activeDraft: Binding<CurationDraft> { draft ?? $localDraft }
    private var name: String { activeDraft.wrappedValue.name }
    private var sources: String { activeDraft.wrappedValue.sources }
    var body: some View {
        VStack(alignment: .leading, spacing: 24) {
            if embedded {
                Text("Bring one or more sites together. Their posts stay credited to the original authors.").font(Theme.formHelp).foregroundColor(Theme.muted)
            } else {
                VStack(alignment: .leading, spacing: 8) {
                    HStack(alignment: .top) {
                        Text("Curate").font(Theme.heading(28))
                        Spacer()
                        Button { model.sheet = nil } label: {
                            Image(systemName: "xmark").font(.system(size: 16, weight: .medium))
                                .frame(width: 32, height: 32).contentShape(Rectangle())
                        }
                        .buttonStyle(.hover)
                        .accessibilityLabel("Close Curate")
                        .help("Close")
                        .keyboardShortcut(.cancelAction)
                        .disabled(busy)
                    }
                    Text("Bring one or more sites together. Their posts stay credited to the original authors.").font(Theme.formHelp).foregroundColor(Theme.muted)
                }
            }
            Labeled(title: "Curation name") { TextField("Name your curation", text: activeDraft.name).field(compact: true).focused($nameFocused) }
            Labeled(title: "Sites", help: "One ENS name or IPNS address per line.") {
                TextEditor(text: activeDraft.sources).font(Theme.body(14)).frame(height: 120).padding(8).fieldOutline()
            }
            if let error { Text(error).font(Theme.body(13)).foregroundColor(Theme.attention) }
            HStack {
                Spacer()
                if embedded {
                    Button("Cancel") { model.sheet = nil }.buttonStyle(BorderedButton(kind: .quiet))
                        .keyboardShortcut(.cancelAction).disabled(busy)
                }
                Button(busy ? "Creating…" : "Create curation") { Task { await create() } }.buttonStyle(BorderedButton()).disabled(busy || name.trimmingCharacters(in: .whitespaces).isEmpty || sources.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }.padding(embedded ? 0 : 24).frame(width: embedded ? nil : 500).interactiveDismissDisabled(busy)
            .defaultFocus($nameFocused, true)
            .task { nameFocused = true }
            .onChange(of: busy, perform: onBusyChange)
            .onDisappear { onBusyChange(false) }
    }
    private func create() async {
        busy = true; error = nil; defer { busy = false }
        do {
            let site = try await API.shared.createSite(name: name, about: "", sources: sources)
            await model.load(); model.sheet = nil; model.screen = .site(site.id)
        } catch { self.error = error.localizedDescription }
    }
}
