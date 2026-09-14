import SwiftUI

struct CaptureShortcutPrompt: View {
    @EnvironmentObject var model: AppModel

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            Text("Make your first post").font(Theme.heading(22))
            Text(model.captureShortcutEnabled
                 ? "Press ⌘⇧C anywhere to select a screenshot. Croptop opens a new post with a site selector."
                 : "Turn a screenshot into a post. Enable ⌘⇧C to capture a region from anywhere while Croptop is open.")
                .font(Theme.body(14)).foregroundColor(Theme.muted)
                .fixedSize(horizontal: false, vertical: true)
            if model.captureShortcutEnabled {
                Button("Take a screenshot") { model.captureScreenshot() }
                    .buttonStyle(BorderedButton()).disabled(model.capturing)
            } else {
                Button("Enable ⌘⇧C") { model.enableCaptureShortcut() }
                    .buttonStyle(BorderedButton())
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(24)
        .overlay(Rectangle().strokeBorder(Theme.rule, lineWidth: 1))
    }
}
