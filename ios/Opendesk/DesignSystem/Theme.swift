import SwiftUI
import UIKit

extension Color {
    /// A light/dark pair from hex values, resolved by the trait collection.
    static func dynamic(light: UInt32, dark: UInt32, alpha: CGFloat = 1) -> Color {
        Color(uiColor: UIColor { traits in
            UIColor(hex: traits.userInterfaceStyle == .dark ? dark : light, alpha: alpha)
        })
    }

    init(hex: UInt32, alpha: Double = 1) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255,
            opacity: alpha
        )
    }
}

extension UIColor {
    convenience init(hex: UInt32, alpha: CGFloat = 1) {
        self.init(
            red: CGFloat((hex >> 16) & 0xFF) / 255,
            green: CGFloat((hex >> 8) & 0xFF) / 255,
            blue: CGFloat(hex & 0xFF) / 255,
            alpha: alpha
        )
    }

    /// `#RRGGBB` for CSS.
    var cssHex: String {
        var r: CGFloat = 0, g: CGFloat = 0, b: CGFloat = 0, a: CGFloat = 0
        getRed(&r, green: &g, blue: &b, alpha: &a)
        return String(format: "#%02X%02X%02X", Int(r * 255), Int(g * 255), Int(b * 255))
    }
}

/// opendesk's palette (web `app.css` and `brand/tokens.css`): white-first,
/// layered surfaces (grey chrome behind white panels), brand greys, ink
/// actions, soft semantic tints.
enum Theme {
    // Surfaces
    static let chrome = Color.dynamic(light: 0xF5F5F5, dark: 0x141416)
    static let canvas = Color.dynamic(light: 0xF5F5F5, dark: 0x141416)
    static let surface = Color.dynamic(light: 0xFFFFFF, dark: 0x1B1C1F)
    static let raised = Color.dynamic(light: 0xFFFFFF, dark: 0x232427)
    static let sunken = Color.dynamic(light: 0xFAFAFA, dark: 0x17181A)
    static let hover = Color.dynamic(light: 0xF4F4F4, dark: 0x232427)
    static let active = Color.dynamic(light: 0xEDEDED, dark: 0x2B2C30)

    // Lines (hairlines over shadows)
    static let line = Color.dynamic(light: 0xE4E4E4, dark: 0x2E2F33)
    static let lineSoft = Color.dynamic(light: 0xF0F0F0, dark: 0x252629)
    static let lineStrong = Color.dynamic(light: 0xD6D6D6, dark: 0x3C3D42)

    // Ink
    static let fg = Color.dynamic(light: 0x0A0A0A, dark: 0xF7F7F7)
    static let fg2 = Color.dynamic(light: 0x4A4A4A, dark: 0xC4C4C4)
    static let fg3 = Color.dynamic(light: 0x737373, dark: 0x8F8F8F)
    static let fg4 = Color.dynamic(light: 0xA3A3A3, dark: 0x5F5F5F)
    static let inverse = Color.dynamic(light: 0x0A0A0A, dark: 0xF7F7F7)
    static let inverseFg = Color.dynamic(light: 0xFFFFFF, dark: 0x0A0A0A)

    // Timeline frames (web `--reply`, `--note`): the team's replies carry a blue tint,
    // internal notes an amber one, so who wrote a message reads before its words.
    static let reply = Color.dynamic(light: 0xF4F8FF, dark: 0x222630)
    static let replyLine = Color.dynamic(light: 0xBED5FF, dark: 0x364565)
    static let note = Color.dynamic(light: 0xFBF3EA, dark: 0x2F2B26)
    static let noteLine = Color.dynamic(light: 0xE7B882, dark: 0x7F6437)

    // States
    static let ok = Color.dynamic(light: 0x16A34A, dark: 0x3DD68C)
    static let warn = Color.dynamic(light: 0xD97706, dark: 0xF5B53D)
    static let danger = Color.dynamic(light: 0xDC2626, dark: 0xFF6B6B)

    /// The glass tint for floating bars.
    static let glassTint = Color.dynamic(light: 0xFFFFFF, dark: 0x191919, alpha: 0.55)

    /// Radii: slightly square.
    enum Radius {
        static let pill: CGFloat = 5
        static let chip: CGFloat = 6
        static let control: CGFloat = 10
        static let card: CGFloat = 14
        static let sheet: CGFloat = 22
    }

    static let hairline: CGFloat = 1 / 3

    /// Stable avatar tint from a seed.
    static func avatarTone(for seed: String) -> Tone {
        let tones: [Tone] = [.blue, .violet, .teal, .orange, .pink, .indigo, .green, .amber, .cyan, .rose]
        var hash: UInt64 = 5381
        for byte in seed.utf8 { hash = (hash &* 33) &+ UInt64(byte) }
        return tones[Int(hash % UInt64(tones.count))]
    }
}

/// The soft semantic tones (web `--tone-*`): a solid, a 10% wash, an 18% line, and text.
enum Tone: String, CaseIterable, Sendable {
    case gray, red, orange, amber, yellow, lime, green, teal, cyan, blue, indigo, violet, purple, pink, rose

    var solid: Color {
        switch self {
        case .gray: .dynamic(light: 0x737373, dark: 0xA3A3A3)
        case .red: .dynamic(light: 0xE03131, dark: 0xFF6B6B)
        case .orange: .dynamic(light: 0xE8590C, dark: 0xFF8A4C)
        case .amber: .dynamic(light: 0xD48806, dark: 0xF5B53D)
        case .yellow: .dynamic(light: 0xD9A404, dark: 0xFFD43B)
        case .lime: .dynamic(light: 0x5C940D, dark: 0xA9E34B)
        case .green: .dynamic(light: 0x12A150, dark: 0x3DD68C)
        case .teal: .dynamic(light: 0x0C9488, dark: 0x2DD4BF)
        case .cyan: .dynamic(light: 0x0C8599, dark: 0x66D9E8)
        case .blue: .dynamic(light: 0x2B6CF0, dark: 0x6B9BFF)
        case .indigo: .dynamic(light: 0x4C5FD5, dark: 0x91A7FF)
        case .violet: .dynamic(light: 0x7048E8, dark: 0x9D85FF)
        case .purple: .dynamic(light: 0x9C36B5, dark: 0xDA77F2)
        case .pink: .dynamic(light: 0xD6336C, dark: 0xF472B6)
        case .rose: .dynamic(light: 0xE64980, dark: 0xFF8FAB)
        }
    }

    var soft: Color { solid.opacity(0.10) }
    var line: Color { solid.opacity(0.18) }

    /// Text on the soft wash: the solid pulled toward the ink, as on the web.
    var text: Color {
        Color(uiColor: UIColor { traits in
            let base = UIColor(self.solid).resolvedColor(with: traits)
            let ink = UIColor(Theme.fg).resolvedColor(with: traits)
            return base.mixed(with: ink, amount: 0.18)
        })
    }

    /// A label colour name from the API (`LabelColor`), gray when unknown.
    init(label: String?) {
        self = label.flatMap { Tone(rawValue: $0.lowercased()) } ?? .gray
    }
}

extension UIColor {
    func mixed(with other: UIColor, amount: CGFloat) -> UIColor {
        var r1: CGFloat = 0, g1: CGFloat = 0, b1: CGFloat = 0, a1: CGFloat = 0
        var r2: CGFloat = 0, g2: CGFloat = 0, b2: CGFloat = 0, a2: CGFloat = 0
        getRed(&r1, green: &g1, blue: &b1, alpha: &a1)
        other.getRed(&r2, green: &g2, blue: &b2, alpha: &a2)
        let t = max(0, min(1, amount))
        return UIColor(red: r1 + (r2 - r1) * t, green: g1 + (g2 - g1) * t, blue: b1 + (b2 - b1) * t, alpha: a1)
    }
}

// MARK: - Semantic tones (web: lib/tones)

extension TicketStatus {
    var tone: Tone {
        switch self {
        case .open: .blue
        case .pending: .amber
        case .onHold: .violet
        case .solved: .green
        case .closed: .gray
        }
    }
}

extension Priority {
    var tone: Tone {
        switch self {
        case .urgent: .red
        case .high: .orange
        case .normal: .blue
        case .low: .gray
        }
    }

    var symbol: String {
        switch self {
        case .urgent: "exclamationmark.square.fill"
        case .high: "chart.bar.fill"
        case .normal: "chart.bar"
        case .low: "chart.bar"
        }
    }
}

extension Severity {
    var tone: Tone {
        switch self {
        case .sev1: .red
        case .sev2: .orange
        case .sev3: .amber
        case .sev4: .gray
        }
    }
}

extension Channel {
    var tone: Tone {
        switch self {
        case .email: .blue
        case .chat: .teal
        case .api: .violet
        case .portal: .indigo
        case .phone: .green
        }
    }

    var symbol: String {
        switch self {
        case .email: "envelope"
        case .chat: "bubble.left.and.bubble.right"
        case .api: "chevron.left.forwardslash.chevron.right"
        case .portal: "globe"
        case .phone: "phone"
        }
    }
}

extension Sentiment {
    var tone: Tone {
        switch self {
        case .positive: .green
        case .neutral: .gray
        case .negative: .orange
        case .frustrated: .red
        }
    }
}
