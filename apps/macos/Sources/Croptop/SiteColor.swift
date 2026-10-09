import Foundation
import SwiftUI

// A site's own ink and paper, from its template settings; the app palette when unset.
struct SitePalette: Equatable {
    var ink = Theme.ink
    var paper = Theme.paper

    init() {}
    init(settings: [String: Any]) {
        let fg = SiteColor.hex(settings["foregroundColor"] as? String)
        let bg = SiteColor.hex(settings["backgroundColor"] as? String)
        if let fg { ink = Color(hex: fg) } else if let bg { ink = Color(hex: SiteColor.contrasting(bg)) }
        if let bg { paper = Color(hex: bg) } else if let fg { paper = Color(hex: SiteColor.contrasting(fg)) }
    }
}

private struct SitePaletteKey: EnvironmentKey { static let defaultValue = SitePalette() }
extension EnvironmentValues {
    var sitePalette: SitePalette {
        get { self[SitePaletteKey.self] }
        set { self[SitePaletteKey.self] = newValue }
    }
}

// Shared by the site color controls and their live preview.
enum SiteColor {
    static func hex(_ value: String?) -> UInt32? {
        guard let value = value?.trimmingCharacters(in: .whitespacesAndNewlines),
              value.range(of: "^#([A-Fa-f0-9]{3}|[A-Fa-f0-9]{6})$", options: .regularExpression) != nil else { return nil }
        let digits = String(value.dropFirst())
        let expanded = digits.count == 3 ? digits.map { String(repeating: String($0), count: 2) }.joined() : digits
        return UInt32(expanded, radix: 16)
    }

    // Match the template when just one of the two colors is set.
    static func contrasting(_ hex: UInt32) -> UInt32 {
        let channels = [Double((hex >> 16) & 255), Double((hex >> 8) & 255), Double(hex & 255)].map { channel -> Double in
            let value = channel / 255
            return value <= 0.04045 ? value / 12.92 : pow((value + 0.055) / 1.055, 2.4)
        }
        let luminance = channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722
        return luminance > 0.179 ? 0x000000 : 0xFFFFFF
    }
}
