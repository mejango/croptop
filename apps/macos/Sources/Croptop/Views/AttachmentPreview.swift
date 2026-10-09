import SwiftUI
import AppKit

struct PreviewAttachment: Identifiable {
    var name: String
    var url: URL
    var id: URL { url }
}

struct AttachmentImage: View {
    var url: URL
    var body: some View {
        if url.isFileURL, let image = NSImage(contentsOf: url) {
            Image(nsImage: image).resizable().scaledToFit()
        } else {
            AsyncImage(url: url) { phase in
                switch phase {
                case .success(let image): image.resizable().scaledToFit()
                case .failure: Text("Image unavailable").font(Theme.body(13)).foregroundColor(Theme.muted)
                default: LoadingTicker(accessibilityLabel: "Loading image")
                }
            }
        }
    }
}

struct AttachmentPreview: View {
    var attachment: PreviewAttachment
    @Environment(\.dismiss) private var dismiss
    @State private var zoom = 1.0

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            HStack(spacing: 12) {
                Text(attachment.name).font(Theme.heading(18)).lineLimit(1)
                Spacer()
                Text("Zoom").font(Theme.body(13))
                Slider(value: $zoom, in: 1...4).frame(width: 120).accessibilityLabel("Image zoom")
                Button("Fit") { zoom = 1 }.buttonStyle(BorderedButton(kind: .quiet))
                Button("Done") { dismiss() }.buttonStyle(BorderedButton()).keyboardShortcut(.cancelAction)
            }
            GeometryReader { geometry in
                ScrollView([.horizontal, .vertical]) {
                    AttachmentImage(url: attachment.url)
                        .frame(width: geometry.size.width * zoom, height: geometry.size.height * zoom)
                }
            }
            .background(Theme.paper)
            .overlay(Rectangle().stroke(Theme.rule, lineWidth: Theme.border))
        }
        .padding(Theme.content)
        .frame(minWidth: 700, idealWidth: 1000, minHeight: 560, idealHeight: 760)
        .background(Theme.paper)
    }
}
