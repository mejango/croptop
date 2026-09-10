// The window: rail left, screen right, toast on top. Drops anywhere become a post.
import SwiftUI
import UniformTypeIdentifiers

struct RootView: View {
    @EnvironmentObject var model: AppModel
    @State private var dropping = false

    var body: some View {
        ZStack(alignment: .bottom) {
            HStack(spacing: 0) {
                RailView()
                Rectangle().fill(Theme.ink).frame(width: Theme.border)
                content.frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
            }
            .frame(maxWidth: .infinity, maxHeight: .infinity)
            if let t = model.toast { ToastView(toast: t).padding(.bottom, 24).transition(.move(edge: .bottom).combined(with: .opacity)) }
            if dropping {
                Rectangle().stroke(Theme.hot, lineWidth: 4).background(Theme.hotWash.opacity(0.6)).allowsHitTesting(false)
            }
        }
        .animation(.easeOut(duration: 0.15), value: model.toast)
        .onDrop(of: [UTType.fileURL], isTargeted: $dropping) { providers in
            Task {
                var urls: [URL] = []
                for p in providers {
                    if let u = try? await p.loadItem(forTypeIdentifier: UTType.fileURL.identifier) as? Data, let url = URL(dataRepresentation: u, relativeTo: nil) { urls.append(url) }
                    else if let u = try? await p.loadItem(forTypeIdentifier: UTType.fileURL.identifier) as? URL { urls.append(u) }
                }
                model.postFiles(urls)
            }
            return true
        }
        .sheet(item: $model.sheet) { s in
            switch s {
            case .newSite: NewSiteSheet()
            case .follow: FollowSheet()
            }
        }
    }

    @ViewBuilder var content: some View {
        if let f = model.fatal {
            VStack(spacing: 12) {
                Text("Croptop could not start").font(Theme.pixel(28))
                Text(f).font(Theme.body()).foregroundColor(Theme.muted).multilineTextAlignment(.center)
            }.frame(maxWidth: .infinity, maxHeight: .infinity).padding(Theme.content)
        } else if !model.ready {
            VStack(spacing: 12) {
                ProgressView()
                Text("Starting your node…").font(Theme.body()).foregroundColor(Theme.muted)
            }.frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            switch model.screen {
            case .feed: FeedView()
            case .site(let id): SiteView(siteID: id).id(id)
            case .editor(let site, let post): EditorView(siteID: site, postID: post).id(site + (post ?? "new"))
            case .quick(let id): QuickView(groupID: id).id(id)
            }
        }
    }
}

struct ToastView: View {
    var toast: Toast
    var body: some View {
        Text(toast.text)
            .font(Theme.body(14))
            .padding(.vertical, 10).padding(.horizontal, 16)
            .background(Theme.paper)
            .overlay(Rectangle().stroke(toast.error ? Theme.hot : Theme.ink, lineWidth: Theme.border))
    }
}

struct EmptyState: View {
    var text: String
    var body: some View {
        Text(text).font(Theme.body()).foregroundColor(Theme.muted).frame(maxWidth: .infinity, alignment: .leading).padding(.top, 8)
    }
}
