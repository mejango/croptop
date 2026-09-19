// docs/design/tokens.md, in Swift. Change tokens there first.
import SwiftUI
import CoreText

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

    static func heading(_ size: CGFloat) -> Font { bold(size) }
    static func body(_ size: CGFloat = 16) -> Font { .custom("Simplon", size: size) }
    static func bold(_ size: CGFloat = 16) -> Font { .custom("Simplon Bold", size: size) }
    static var tab: Font { bold(16) }
    static var formLabel: Font { bold(14) }
    static var formSection: Font { formLabel }
    static var formText: Font { body(14) }
    static var formHelp: Font { body(13) }
    static let code = Font.system(size: 14, design: .monospaced)
}

// Native controls and WebKit use the same trusted, bundled Simplon files.
enum AppFonts {
    enum Face: String, CaseIterable {
        case regular = "SimplonNorm-Regular-WebXL.ttf"
        case bold = "SimplonNorm-Bold-WebXL.ttf"
        var weight: Int { self == .bold ? 700 : 400 }
    }

    private static let directories: [URL] = {
        var result: [URL] = []
        if let resources = Bundle.main.resourceURL {
            result.append(resources.appendingPathComponent("fonts"))
        }
        let source = URL(fileURLWithPath: #filePath)
        result.append(source.deletingLastPathComponent().deletingLastPathComponent().deletingLastPathComponent()
            .deletingLastPathComponent().deletingLastPathComponent().appendingPathComponent("installer/fonts"))
        return result
    }()

    static func url(for face: Face) -> URL? {
        directories.lazy.map { $0.appendingPathComponent(face.rawValue) }
            .first { FileManager.default.fileExists(atPath: $0.path) }
    }

    // Register once before SwiftUI resolves fonts for the first scene.
    static func register() { _ = registration }

    private static let registration: Void = {
        for face in Face.allCases {
            guard let font = url(for: face) else { continue }
            CTFontManagerRegisterFontsForURL(font as CFURL, .process, nil)
        }
    }()

    // Cache the data URIs once, without granting WebKit filesystem or network access.
    static let previewFontFaces: String = Face.allCases.compactMap { face -> String? in
        guard let url = url(for: face), let data = try? Data(contentsOf: url) else { return nil }
        return "@font-face {font-family:'Simplon';font-style:normal;font-weight:\(face.weight);src:url('data:font/ttf;base64,\(data.base64EncodedString())') format('truetype');}"
    }.joined(separator: "\n")
}

extension Color {
    init(hex: UInt32) {
        self.init(red: Double((hex >> 16) & 0xFF) / 255, green: Double((hex >> 8) & 0xFF) / 255, blue: Double(hex & 0xFF) / 255)
    }
}

// The template's four-character loading cycle, without a persistent timer.
struct LoadingTicker: View {
    var accessibilityLabel = "Loading"
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var startedAt = Date()
    private let frames = ["|", "/", "-", "\\"]

    var body: some View {
        Group {
            if reduceMotion {
                Text("|")
            } else {
                TimelineView(.periodic(from: startedAt, by: 0.1)) { context in
                    let step = Int(max(0, context.date.timeIntervalSince(startedAt)) / 0.1)
                    Text(frames[step % frames.count])
                }
            }
        }
        .font(Theme.body(16)).foregroundColor(Theme.ink)
        .frame(width: 16, height: 20)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(accessibilityLabel)
    }
}

// The bordered button: 2 px ink, paper, Simplon 14 in every state. Variants: hot, quiet.
// Every button gets a subtle hover state: bordered ones tint their background, plain ones dim.
struct HoverState<Content: View>: View {
    @State private var hovering = false
    @ViewBuilder var content: (Bool) -> Content
    var body: some View { content(hovering).onHover { hovering = $0 } }
}

struct HoverButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        HoverState { hovering in configuration.label.opacity(configuration.isPressed ? 0.5 : hovering ? 0.7 : 1) }
    }
}
extension ButtonStyle where Self == HoverButtonStyle { static var hover: HoverButtonStyle { HoverButtonStyle() } }

struct BorderedButton: ButtonStyle {
    enum Kind { case plain, hot, quiet }
    var kind: Kind = .plain
    var accent: Color = Theme.hot
    var current = false   // a selected state, like the Feed button in the rail
    func makeBody(configuration: Configuration) -> some View {
        HoverState { hovering in
            let inverted = current || configuration.isPressed
            configuration.label
                .font(Theme.body(14))
                .foregroundColor(inverted ? Theme.paper : (kind == .hot ? accent : Theme.ink))
                .padding(.vertical, 8).padding(.horizontal, 14)
                .background(inverted ? Theme.ink : (hovering ? (kind == .hot ? Theme.hotWash : Theme.rule.opacity(0.35)) : Theme.paper))
                .overlay(Rectangle().stroke(inverted ? Theme.ink : (kind == .quiet ? (hovering ? Theme.ink : Theme.rule) : (kind == .hot ? accent : Theme.ink)), lineWidth: Theme.border))
                .contentShape(Rectangle())
        }
    }
}

// Plain actions use the same simple icon-and-text treatment as the sidebar.
struct TextActionButtonStyle: ButtonStyle {
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .labelStyle(.titleAndIcon)
            .font(Theme.body(14))
            .foregroundColor(isEnabled ? Theme.ink : Theme.muted)
            .padding(.vertical, 8).padding(.horizontal, 4)
            .contentShape(Rectangle())
            .modifier(HoverDim(pressed: configuration.isPressed))
    }
}

struct HoverDim: ViewModifier {
    var pressed = false
    @State private var hovering = false
    func body(content: Content) -> some View { content.opacity(pressed ? 0.5 : hovering ? 0.7 : 1).onHover { hovering = $0 } }
}

// Secondary actions stay compact; tooltips and accessibility carry their names.
struct IconActionButton: View {
    private let systemImage: String
    private let helpText: String
    private let accessibilityText: String
    private let action: () -> Void
    @Environment(\.isEnabled) private var isEnabled

    init(_ title: String, systemImage: String, help: String? = nil,
         accessibilityLabel: String? = nil, action: @escaping () -> Void) {
        self.systemImage = systemImage
        helpText = help ?? title
        accessibilityText = accessibilityLabel ?? title
        self.action = action
    }

    var body: some View {
        Button(action: action) {
            Image(systemName: systemImage).frame(width: 20, height: 20)
                .font(Theme.body(14))
                .foregroundColor(isEnabled ? Theme.ink : Theme.muted)
                .padding(.vertical, 8).padding(.horizontal, 4)
                .contentShape(Rectangle())
        }
        .buttonStyle(.hover)
        .help(helpText)
        .accessibilityLabel(accessibilityText)
    }
}

// Shared by single-line fields and multiline editors, including Payments.
struct FieldOutline: ViewModifier {
    @FocusState private var focused: Bool

    func body(content: Content) -> some View {
        content
            .focused($focused)
            .background(Theme.paper)
            .overlay(Rectangle().strokeBorder(focused ? Theme.muted : Theme.rule, lineWidth: 1))
    }
}

// A text field with a quiet 1 pt inset border and 8 x 10 padding.
struct Field: ViewModifier {
    var compact = false
    func body(content: Content) -> some View {
        content
            .textFieldStyle(.plain)
            .font(Theme.formText)
            .padding(.vertical, compact ? 6 : 8).padding(.horizontal, 10)
            .fieldOutline()
    }
}

struct Labeled<Content: View>: View {
    var title: String
    var help: String?
    @ViewBuilder var content: Content
    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text(title).font(Theme.formLabel)
            VStack(alignment: .leading, spacing: 6) {
                content
                if let h = help, !h.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty { Text(h).font(Theme.formHelp).foregroundColor(Theme.muted).fixedSize(horizontal: false, vertical: true) }
            }.font(Theme.formText)
        }
    }
}

struct SetupStep<Content: View>: View {
    var number: Int
    var title: String
    @ViewBuilder var content: Content

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Text("\(number).").font(Theme.formLabel)
                .frame(width: 16, alignment: .leading)
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 8) {
                Text(title).font(Theme.formLabel)
                    .accessibilityLabel("Step \(number): \(title)")
                    .accessibilityAddTraits(.isHeader)
                content.font(Theme.formText)
            }
        }.fixedSize(horizontal: false, vertical: true)
    }
}

struct ScreenHead<Trailing: View>: View {
    var title: String
    var subtitle: String?
    @ViewBuilder var trailing: Trailing
    var body: some View {
        HStack(alignment: .top, spacing: 16) {
            VStack(alignment: .leading, spacing: 6) {
                Text(title).font(Theme.heading(28))
                if let s = subtitle, !s.isEmpty { Text(s).font(Theme.body()).foregroundColor(Theme.muted) }
            }
            Spacer()
            trailing
        }
        .padding(.bottom, 22)
    }
}

extension View {
    func field(compact: Bool = false) -> some View { modifier(Field(compact: compact)) }
    func fieldOutline() -> some View { modifier(FieldOutline()) }
}

struct FullRowDisclosureStyle: DisclosureGroupStyle {
    func makeBody(configuration: Configuration) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Button { configuration.isExpanded.toggle() } label: {
                HStack(spacing: 8) {
                    Image(systemName: "chevron.right")
                        .font(.system(size: 11, weight: .semibold))
                        .rotationEffect(.degrees(configuration.isExpanded ? 90 : 0))
                        .frame(width: 12, height: 12)
                        .accessibilityHidden(true)
                    configuration.label.font(Theme.formSection)
                    Spacer(minLength: 0)
                }.contentShape(Rectangle())
            }.buttonStyle(.plain)
            if configuration.isExpanded { configuration.content.font(Theme.formText).padding(.leading, 20) }
        }
    }
}
