import SwiftUI

/// Inline hosting action shares the settings screen's save/publish operation.
struct SiteHostingToggle: View {
    @Binding var isOn: Bool
    let changed: Bool
    let busy: Bool
    let canSave: Bool
    let save: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Toggle("Use crop.top", isOn: $isOn)
                .toggleStyle(.checkbox).font(Theme.formLabel).disabled(busy)
            Text("Add crop.top as a reliable, fast peer and let it serve your site’s content, including when this computer is offline.")
                .font(Theme.formHelp).foregroundColor(Theme.muted)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.leading, 20)
            if changed {
                Button("Save and publish", action: save)
                    .buttonStyle(BorderedButton(kind: .quiet))
                    .disabled(busy || !canSave)
                    .padding(.leading, 20)
            }
        }
    }
}
