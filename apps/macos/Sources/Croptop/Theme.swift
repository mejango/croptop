// docs/design/tokens.md, in Swift. Change tokens there first.
import SwiftUI

enum Theme {
    static let ink = Color(hex: 0x171717)
    static let paper = Color.white
    static let hot = Color(hex: 0xF056C1)
    static let hotWash = Color(hex: 0xFFF0FA)
    static let live = Color(hex: 0x3BB273)
    static let attention = Color(hex: 0xEFAB1D)
    static let rule = Color(hex: 0xE2E2E2)
    static let muted = Color(hex: 0xADADAF)

    static let rail: CGFloat = 220
    static let content: CGFloat = 28
    static let border: CGFloat = 2

    static func pixel(_ size: CGFloat) -> Font { .custom("Capsules", size: size) }
    static func body(_ size: CGFloat = 16) -> Font { .custom("Simplon", size: size) }
    static func bold(_ size: CGFloat = 16) -> Font { .custom("Simplon Bold", size: size) }
    static let code = Font.system(size: 14, design: .monospaced)
}

extension Color {
    init(hex: UInt32) {
        self.init(red: Double((hex >> 16) & 0xFF) / 255, green: Double((hex >> 8) & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }
}

// The bordered button: 2 px ink, paper, Capsules 14. Variants: hot, quiet.
struct BorderedButton: ButtonStyle {
    enum Kind { case plain, hot, quiet }
    var kind: Kind = .plain
    var current = false   // a selected state, like the Feed button in the rail
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(kind == .quiet ? Theme.body(14) : Theme.pixel(14))
            .foregroundColor(kind == .hot ? Theme.hot : Theme.ink)
            .padding(.vertical, 8).padding(.horizontal, 14)
            .background(configuration.isPressed || current ? Theme.hotWash : Theme.paper)
            .overlay(Rectangle().stroke(kind == .quiet ? Theme.rule : (kind == .hot ? Theme.hot : Theme.ink), lineWidth: Theme.border))
            .contentShape(Rectangle())
    }
}

// A text field or editor with the 2 px ink border and 8 x 10 padding.
struct Field: ViewModifier {
    func body(content: Content) -> some View {
        content
            .textFieldStyle(.plain)
            .font(Theme.body())
            .padding(.vertical, 8).padding(.horizontal, 10)
            .background(Theme.paper)
            .overlay(Rectangle().stroke(Theme.ink, lineWidth: Theme.border))
    }
}

struct Labeled<Content: View>: View {
    var title: String
    var help: String?
    @ViewBuilder var content: Content
    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(title).font(Theme.body(14))
            content
            if let h = help { Text(h).font(Theme.body(13)).foregroundColor(Theme.muted) }
        }
    }
}

struct ScreenHead<Trailing: View>: View {
    var title: String
    var subtitle: String?
    @ViewBuilder var trailing: Trailing
    var body: some View {
        HStack(alignment: .top, spacing: 16) {
            VStack(alignment: .leading, spacing: 6) {
                Text(title).font(Theme.pixel(28))
                if let s = subtitle, !s.isEmpty { Text(s).font(Theme.body()).foregroundColor(Theme.muted) }
            }
            Spacer()
            trailing
        }
        .padding(.bottom, 22)
    }
}

extension View {
    func field() -> some View { modifier(Field()) }
}
