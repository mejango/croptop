import SwiftUI

enum FeedScope: Hashable {
    case following
    case owned
    case site(String)

    var source: String { self == .owned ? "owned" : "following" }
    var ipns: String? { if case .site(let ipns) = self { return ipns }; return nil }

    func contains(_ item: FeedItem) -> Bool {
        switch self {
        case .following: return item.siteID == nil
        case .owned: return item.siteID?.isEmpty == false
        case .site(let ipns): return item.siteID == nil && item.ipns == ipns
        }
    }
}

struct FeedView: View {
    @EnvironmentObject var model: AppModel
    var scope: FeedScope = .following
    @AppStorage("sitePostViewMode") private var viewMode: SitePostViewMode = .tiles
    @FocusState private var focusedPost: String?
    @State private var items: [FeedItem] = []
    @State private var loading = true
    @State private var checking = false
    @State private var error: String?
    @State private var selected: FeedItem?
    @State private var nextOffset = 0
    @State private var canLoadMore = false
    @State private var requestID = UUID()
    @State private var mediaVersions: [String: String] = [:]

    private var unfollowing: Bool {
        scope.ipns.map { model.unfollowing.contains($0) } ?? false
    }

    private var followedSite: Following? {
        guard let ipns = scope.ipns else { return nil }
        return model.following.first { $0.ipns == ipns }
    }

    private var title: String {
        switch scope {
        case .following: return "Following"
        case .owned: return "Your sites"
        case .site: return followedSite?.label ?? items.first?.site ?? "Following site"
        }
    }

    private var subtitle: String? {
        switch scope {
        case .following: return model.following.isEmpty ? nil : "Latest posts from the sites you follow."
        case .owned: return "Latest posts from all your sites."
        case .site: return followedSite.map { $0.name == $0.label ? "Posts from this site." : $0.name }
        }
    }

    var body: some View {
        GeometryReader { geometry in
            ScrollView {
                VStack(alignment: .leading, spacing: 0) {
                    ScreenHead(title: title, subtitle: subtitle) {
                        Button {
                            Task { await load(refresh: scope != .owned) }
                        } label: {
                            Label(checking ? "Refreshing…" : "Refresh", systemImage: "arrow.clockwise")
                        }
                        .buttonStyle(TextActionButtonStyle())
                        .disabled(loading || checking || unfollowing)
                        if let ipns = scope.ipns {
                            Button {
                                Task { await model.unfollow(ipns) }
                            } label: {
                                Label(unfollowing ? "Unfollowing…" : "Unfollow", systemImage: "minus.circle")
                            }
                            .buttonStyle(TextActionButtonStyle())
                            .disabled(loading || checking || unfollowing)
                        }
                    }
                    HStack {
                        Spacer(minLength: 0)
                        SitePostViewPicker(selection: $viewMode)
                    }.padding(.bottom, 20)
                    if let error {
                        HStack(alignment: .top, spacing: 12) {
                            Text(error).font(Theme.body(14)).fixedSize(horizontal: false, vertical: true)
                            Spacer(minLength: 0)
                            Button("Retry") { Task { await load(refresh: scope != .owned) } }
                                .buttonStyle(TextActionButtonStyle()).disabled(loading || checking || unfollowing)
                        }
                        .padding(12)
                        .overlay(Rectangle().strokeBorder(Theme.rule, lineWidth: 1))
                        .frame(maxWidth: 720, alignment: .leading)
                        .padding(.bottom, 16)
                    }
                    if loading && items.isEmpty {
                        LoadingTicker().padding(.vertical, 12)
                    } else if items.isEmpty {
                        if error == nil { emptyState }
                    } else {
                        postContent(width: max(1, geometry.size.width - 2 * Theme.content))
                            .id(viewMode)
                        if canLoadMore {
                            Button {
                                Task { await load(append: true) }
                            } label: {
                                if loading {
                                    LoadingTicker(accessibilityLabel: "Loading more posts")
                                } else {
                                    Label("Load more posts", systemImage: "arrow.down")
                                }
                            }
                            .buttonStyle(TextActionButtonStyle()).disabled(loading || checking || unfollowing)
                            .padding(.top, 16)
                        }
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(Theme.content)
            }
        }
        .id(scope)
        .sheet(item: $selected) { item in FeedPostReader(item: item).environmentObject(model) }
        .task(id: scope) {
            items = []; selected = nil; nextOffset = 0; canLoadMore = false; mediaVersions = [:]
            await load()
        }
        .onChange(of: model.following.map { $0.ipns + ":" + ($0.cid ?? "") }) { _ in
            if scope != .owned { Task { await load() } }
        }
        .onChange(of: model.sites.map { $0.id + ":" + String($0.updated ?? 0) }) { _ in
            if scope == .owned { Task { await load() } }
        }
    }

    @ViewBuilder private func postContent(width: CGFloat) -> some View {
        if viewMode == .list {
            LazyVStack(alignment: .leading, spacing: 0) {
                ForEach(items) { item in postButton(item) }
            }
        } else if viewMode == .more {
            LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: viewMode.spacing, alignment: .top),
                                     count: viewMode.columnCount(for: width)),
                      alignment: .leading, spacing: viewMode.spacing) {
                ForEach(items) { item in postButton(item) }
            }
        } else {
            let columns = SitePostColumns.distribute(items, count: viewMode.columnCount(for: width))
            HStack(alignment: .top, spacing: viewMode.spacing) {
                ForEach(columns.indices, id: \.self) { column in
                    LazyVStack(spacing: viewMode.spacing) {
                        ForEach(columns[column]) { item in postButton(item) }
                    }.frame(maxWidth: .infinity)
                }
            }
        }
    }

    private func imageRevision(for item: FeedItem) -> Double {
        if let siteID = item.siteID {
            return model.sites.first { $0.id == siteID }?.updated ?? item.created
        }
        // A followed site's CID versions its cached public files when it publishes.
        if let cid = mediaVersions[item.ipns] ?? model.following.first(where: { $0.ipns == item.ipns })?.cid {
            return Double(cid.hashValue)
        }
        return item.created
    }

    private func postButton(_ item: FeedItem) -> some View {
        Button { selected = item } label: {
            if viewMode == .list {
                FeedPostRow(item: item, revision: imageRevision(for: item))
            } else {
                FeedPostTile(item: item, square: viewMode == .more,
                             focused: focusedPost == item.id, revision: imageRevision(for: item))
            }
        }
        .buttonStyle(.plain)
        .focused($focusedPost, equals: item.id)
        .accessibilityLabel([item.title.isEmpty ? "Untitled post" : item.title, item.site,
                             item.date.formatted(date: .abbreviated, time: .omitted)].joined(separator: ", "))
        .accessibilityHint("Open this post in Croptop")
    }

    private var emptyState: some View {
        EmptyState(text: "Nothing yet.")
    }

    @MainActor private func load(refresh: Bool = false, append: Bool = false) async {
        let token = UUID()
        let requestedScope = scope
        let offset = append ? nextOffset : 0
        requestID = token
        loading = true; checking = refresh; error = nil
        defer {
            if requestID == token { loading = false; checking = false }
        }
        do {
            var checkErrors: [String] = []
            if refresh {
                let sites = requestedScope.ipns.map { [$0] } ?? model.following.map(\.ipns)
                for ipns in sites {
                    try Task.checkCancellation()
                    do { try await API.shared.refreshFollowing(ipns) }
                    catch is CancellationError { throw CancellationError() }
                    catch { checkErrors.append(error.localizedDescription) }
                }
            }
            let currentSources: [Following]?
            if requestedScope != .owned && !append {
                currentSources = try? await API.shared.following()
            } else { currentSources = nil }
            let loaded = try await API.shared.feed(limit: 60, source: requestedScope.source, ipns: requestedScope.ipns, offset: offset)
            try Task.checkCancellation()
            guard requestID == token, scope == requestedScope else { return }
            guard loaded.allSatisfy(requestedScope.contains) else {
                throw APIError(message: "The local engine returned a different feed. Update Croptop and try again.", status: nil)
            }
            if let currentSources {
                mediaVersions = Dictionary(currentSources.compactMap { source in
                    source.cid.map { (source.ipns, $0) }
                }, uniquingKeysWith: { _, latest in latest })
            }
            var seen = Set(append ? items.map(\.id) : [])
            let fresh = loaded.filter { seen.insert($0.id).inserted }
            items = append ? items + fresh : fresh
            nextOffset = offset + loaded.count
            canLoadMore = loaded.count == 60
            if requestedScope == .following { model.feed = items }
            if let first = checkErrors.first {
                error = "Couldn’t check \(checkErrors.count == 1 ? "one site" : "some sites"). Showing saved posts. " + first
            }
        } catch is CancellationError { }
        catch {
            if requestID == token, scope == requestedScope, !Task.isCancelled {
                self.error = "Couldn’t load posts. " + error.localizedDescription
            }
        }
    }
}
