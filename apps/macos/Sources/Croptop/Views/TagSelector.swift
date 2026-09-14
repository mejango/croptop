import SwiftUI

// Compact tag buttons share the website's shaded chips and removal affordance.
struct TagChip: View {
    var title: String
    var selected = false
    var showsRemoveMark = true
    var action: () -> Void
    @State private var hovering = false

    var body: some View {
        Button(action: action) {
            HStack(spacing: 4) {
                Text(title)
                    .font(selected ? Theme.bold(13) : Theme.body(13))
                    .underline(hovering && !(selected && showsRemoveMark))
                    .lineLimit(1)
                if selected && showsRemoveMark {
                    Text("×").font(Theme.body(13)).underline(hovering)
                        .accessibilityHidden(true)
                }
            }
            .frame(height: 17)
            .padding(.vertical, 4).padding(.horizontal, 8)
            .foregroundColor(selected ? Theme.ink : Color(hex: 0x555555))
            .background(Theme.rule)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .onHover { hovering = $0 }
        .accessibilityLabel(title)
        .accessibilityValue(selected ? "Selected" : "Not selected")
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

struct TagSelector: View {
    @Binding var tags: String
    var choices: [String: String]
    @State private var newTag = ""

    private var selected: [String] { tags.split(separator: ",").map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.filter { !$0.isEmpty } }
    private var allTags: [String] { Array(Set(choices.keys).union(selected)).sorted { label($0).localizedStandardCompare(label($1)) == .orderedAscending } }
    private func label(_ tag: String) -> String { choices[tag].flatMap { $0.isEmpty ? nil : $0 } ?? tag }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            ChipFlowLayout(spacing: 4) {
                ForEach(allTags, id: \.self) { tag in
                    TagChip(title: label(tag), selected: selected.contains(tag)) { toggle(tag) }
                }
            }
            HStack(spacing: 8) {
                TextField("Add a tag", text: $newTag).field().onSubmit(addTag)
                    .frame(maxWidth: 300)
                Button("Add", action: addTag).buttonStyle(BorderedButton(kind: .quiet))
                    .disabled(newTag.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
    }

    private func toggle(_ tag: String) {
        var values = selected
        if values.contains(tag) { values.removeAll { $0 == tag } } else { values.append(tag) }
        tags = values.joined(separator: ", ")
    }
    private func addTag() {
        var values = selected
        for value in newTag.split(separator: ",") {
            let tag = value.trimmingCharacters(in: .whitespacesAndNewlines)
            if !tag.isEmpty && !values.contains(tag) { values.append(tag) }
        }
        tags = values.joined(separator: ", ")
        newTag = ""
    }
}

// Content-sized square chips wrap instead of disappearing off the right edge.
struct ChipFlowLayout: Layout {
    var spacing: CGFloat = 8
    private func arrangement(width: CGFloat, subviews: Subviews) -> (CGSize, [CGRect]) {
        var x: CGFloat = 0, y: CGFloat = 0, rowHeight: CGFloat = 0
        var frames: [CGRect] = []
        for view in subviews {
            let size = view.sizeThatFits(ProposedViewSize(width: width, height: nil))
            if x > 0 && x + size.width > width { x = 0; y += rowHeight + spacing; rowHeight = 0 }
            frames.append(CGRect(x: x, y: y, width: min(size.width, width), height: size.height))
            x += min(size.width, width) + spacing
            rowHeight = max(rowHeight, size.height)
        }
        return (CGSize(width: width, height: y + rowHeight), frames)
    }
    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        arrangement(width: proposal.width ?? 600, subviews: subviews).0
    }
    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        let frames = arrangement(width: bounds.width, subviews: subviews).1
        for (view, frame) in zip(subviews, frames) {
            view.place(at: CGPoint(x: bounds.minX + frame.minX, y: bounds.minY + frame.minY), proposal: ProposedViewSize(frame.size))
        }
    }
}
