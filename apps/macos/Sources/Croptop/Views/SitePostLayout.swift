import SwiftUI

// Raw values are the template's postViewMode setting, so the site opens in the owner's view.
enum SitePostViewMode: String, CaseIterable, Identifiable {
    case single
    case tiles = "frames"
    case more
    case list

    var id: String { rawValue }

    var title: String {
        switch self {
        case .single: return "Single"
        case .tiles: return "Tiles"
        case .more: return "More"
        case .list: return "List"
        }
    }

    var spacing: CGFloat {
        switch self {
        case .single, .tiles: return 20
        case .more: return 16
        case .list: return 0
        }
    }

    var help: String {
        switch self {
        case .single: return "Single: show one post at a time, up to the window height"
        case .tiles: return "Tiles: show posts at their natural proportions"
        case .more: return "More: show smaller square tiles"
        case .list: return "List: show posts in rows"
        }
    }

    func columnCount(for width: CGFloat) -> Int {
        guard self != .list, self != .single, width.isFinite else { return 1 }
        let minimumWidth: CGFloat = self == .tiles ? 260 : 150
        let maximumColumns: CGFloat = self == .tiles ? 3 : 5
        return Int(min(maximumColumns, max(1, floor((width + spacing) / (minimumWidth + spacing)))))
    }
}

enum SitePostColumns {
    /// Reading across the columns recovers the input order, including creation tiles.
    static func distribute<T>(_ items: [T], count: Int) -> [[T]] {
        let columnCount = max(1, count)
        var columns = Array(repeating: [T](), count: columnCount)
        for (index, item) in items.enumerated() {
            columns[index % columnCount].append(item)
        }
        return columns
    }
}

struct SitePostViewPicker: View {
    @Binding var selection: SitePostViewMode
    @Environment(\.sitePalette) private var palette

    var body: some View {
        HStack(spacing: 3) {
            ForEach(SitePostViewMode.allCases) { mode in
                if mode != .single {
                    Rectangle().fill(Theme.muted).frame(width: 1, height: 16)
                        .accessibilityHidden(true)
                }
                Button { selection = mode } label: {
                    SitePostViewGlyph(mode: mode)
                        .fill(selection == mode ? palette.ink : Theme.muted, style: FillStyle(eoFill: true))
                        .frame(width: 14, height: 16)
                        .frame(width: 28, height: 28)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .help(mode.help)
                .accessibilityLabel(mode.title)
                .accessibilityAddTraits(selection == mode ? .isSelected : [])
            }
        }
        .fixedSize()
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Post view")
    }
}

private struct SitePostViewGlyph: Shape {
    let mode: SitePostViewMode

    func path(in rect: CGRect) -> Path {
        if mode == .single {
            var path = Path()
            path.addRoundedRect(in: CGRect(x: rect.midX - 7, y: rect.midY - 7, width: 14, height: 14), cornerSize: CGSize(width: 1.5, height: 1.5))
            path.addRoundedRect(in: CGRect(x: rect.midX - 5.5, y: rect.midY - 5.5, width: 11, height: 11), cornerSize: CGSize(width: 1, height: 1))
            return path
        }
        if mode == .list {
            var path = Path()
            for row in 0..<3 {
                let line = CGRect(x: rect.midX - 7, y: rect.midY - 5.5 + CGFloat(row) * 4.5, width: 14, height: 2)
                path.addRoundedRect(in: line, cornerSize: CGSize(width: 1, height: 1))
            }
            return path
        }
        let rows = mode == .tiles ? 2 : 3
        let columns = rows
        let size: CGFloat = mode == .tiles ? 6 : 2.5
        let gap: CGFloat = mode == .tiles ? 2 : 1.5
        let width = CGFloat(columns) * size + CGFloat(columns - 1) * gap
        let height = CGFloat(rows) * size + CGFloat(rows - 1) * gap
        var path = Path()
        for row in 0..<rows {
            for column in 0..<columns {
                let cell = CGRect(
                    x: rect.midX - width / 2 + CGFloat(column) * (size + gap),
                    y: rect.midY - height / 2 + CGFloat(row) * (size + gap),
                    width: size,
                    height: size
                )
                path.addRoundedRect(in: cell, cornerSize: CGSize(width: 1.25, height: 1.25))
            }
        }
        return path
    }
}
