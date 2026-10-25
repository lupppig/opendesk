import SwiftUI

/// The opendesk "Module" mark (brand/): three rounded squares and a round
/// module on a 64 grid.
struct ODLogoMark: Shape {
    func path(in rect: CGRect) -> Path {
        let s = min(rect.width, rect.height) / 64
        let ox = rect.midX - 32 * s
        let oy = rect.midY - 32 * s
        var path = Path()
        for (x, y) in [(4.0, 4.0), (4.0, 34.0), (34.0, 34.0)] {
            path.addRoundedRect(in: CGRect(x: ox + x * s, y: oy + y * s, width: 26 * s, height: 26 * s), cornerSize: CGSize(width: 7 * s, height: 7 * s), style: .continuous)
        }
        path.addEllipse(in: CGRect(x: ox + 34 * s, y: oy + 4 * s, width: 26 * s, height: 26 * s))
        return path
    }
}

struct ODWordmark: View {
    var size: CGFloat = 22
    /// White over a painting.
    var color: Color = Theme.fg

    var body: some View {
        HStack(spacing: size * 0.36) {
            ODLogoMark().fill(color).frame(width: size, height: size)
            Text("opendesk")
                .font(ODFont.sans(size * 0.92, weight: .semibold))
                .tracking(-size * 0.03)
                .foregroundStyle(color)
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("opendesk")
    }
}

/// Ollie, the AI persona: a soft violet blob with light swirling inside and
/// two small eyes. It breathes at rest, swirls faster while working, hops in
/// amber while a change waits for approval, and cheers in green when done.
/// Reduce Motion stills all of it.
struct OllieMark: View {
    enum State { case idle, thinking, attention, done, muted }

    var size: CGFloat = 20
    var state: State = .idle
    var eyes: Bool?

    @Environment(\.colorScheme) private var scheme
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var palette: (Color, Color, Color) {
        let dark = scheme == .dark
        switch state {
        case .attention: return (Color(hex: 0xFDE68A), Color(hex: 0xF59E0B), Color(hex: 0xC2410C))
        case .done: return (Color(hex: 0xA7F3D0), Color(hex: 0x10B981), Color(hex: 0x047857))
        case .muted:
            return dark ? (Color(hex: 0x6B6680), Color(hex: 0x464157), Color(hex: 0x2F2B3C)) : (Color(hex: 0xE7E3F3), Color(hex: 0xA8A1BD), Color(hex: 0x7D7792))
        case .idle, .thinking:
            return dark ? (Color(hex: 0xDDD3FF), Color(hex: 0x8B6CF6), Color(hex: 0x5131D0)) : (Color(hex: 0xC9BCFF), Color(hex: 0x7048E8), Color(hex: 0x3F22A6))
        }
    }

    var body: some View {
        if reduceMotion {
            blob(time: 0)
        } else {
            TimelineView(.animation(minimumInterval: 1 / 30, paused: false)) { context in
                blob(time: context.date.timeIntervalSinceReferenceDate)
            }
        }
    }

    private func blob(time: TimeInterval) -> some View {
        let (a, b, c) = palette
        let breathePeriod = state == .thinking ? 1.7 : state == .muted ? 9 : 4.8
        let turnPeriod = state == .thinking ? 1.1 : 8
        let phase = (time.truncatingRemainder(dividingBy: breathePeriod)) / breathePeriod * 2 * .pi
        let sx = 1 + 0.035 * sin(phase)
        let sy = 1 - 0.035 * sin(phase)
        let turn = (time.truncatingRemainder(dividingBy: turnPeriod)) / turnPeriod * 360
        let hop = state == .attention ? -abs(sin(time * .pi / 1.3)) * size * 0.08 : 0
        let blinkCycle = time.truncatingRemainder(dividingBy: 5.2) / 5.2
        let blink = (state == .idle || state == .muted) && blinkCycle > 0.44 && blinkCycle < 0.48 ? 0.15 : 1.0
        let look = state == .thinking ? sin(time * 2 * .pi / 1.6) * size * 0.03 : 0
        let showEyes = eyes ?? (size >= 14)

        return ZStack {
            ZStack {
                AngularGradient(colors: [a, b, c, b, a], center: UnitPoint(x: 0.54, y: 0.56), angle: .degrees(turn))
                RadialGradient(colors: [a.mix(with: .white, by: 0.15), .clear], center: UnitPoint(x: 0.3, y: 0.28), startRadius: 0, endRadius: size * 0.42)
            }
            .blur(radius: size * 0.04)
            .clipShape(Ellipse())
            .overlay(Ellipse().strokeBorder(c.opacity(0.35), lineWidth: max(0.5, size * 0.03)).blur(radius: size * 0.04).clipShape(Ellipse()))
            .shadow(color: b.opacity(0.45), radius: size * 0.12, y: size * 0.05)
            .scaleEffect(x: sx, y: sy)
            .padding(size * 0.05)

            Ellipse()
                .fill(RadialGradient(colors: [.white.opacity(0.92), .white.opacity(0)], center: .center, startRadius: 0, endRadius: size * 0.17))
                .frame(width: size * 0.34, height: size * 0.24)
                .rotationEffect(.degrees(-24))
                .offset(x: -size * 0.16, y: -size * 0.24)
                .opacity(state == .muted ? 0.55 : 1)

            if showEyes {
                HStack(spacing: size * 0.15) {
                    eye(blink: blink)
                    eye(blink: blink)
                }
                .offset(x: look, y: size * 0.02)
            }
        }
        .frame(width: size, height: size)
        .offset(y: hop)
        .accessibilityHidden(true)
    }

    private func eye(blink: Double) -> some View {
        let height = state == .done ? size * 0.06 : state == .attention ? size * 0.17 : size * 0.15
        return RoundedRectangle(cornerRadius: size * 0.05, style: .continuous)
            .fill(.white.opacity(0.94))
            .frame(width: size * 0.1, height: height)
            .scaleEffect(x: 1, y: blink, anchor: UnitPoint(x: 0.5, y: 0.6))
            .shadow(color: Color(red: 40 / 255, green: 16 / 255, blue: 120 / 255).opacity(0.35), radius: size * 0.02)
    }
}

/// Small "by Ollie" attribution: the still mark and the name, in violet.
struct OllieAttribution: View {
    var text = "Ollie"

    var body: some View {
        HStack(spacing: 4) {
            OllieMark(size: 13, eyes: false)
            Text(text).font(ODFont.sans(11.5, weight: .medium)).foregroundStyle(Tone.violet.text)
        }
    }
}

/// A customer-facing bot's face on its replies: an emoji look drawn natively
/// on a violet tint, else the server-rendered PNG of its look.
struct BotAvatar: View {
    let bot: BotRef
    var size: CGFloat = 28

    var body: some View {
        if let emoji = bot.nativeEmoji {
            ZStack {
                Circle().fill(Tone.violet.soft)
                Circle().strokeBorder(Tone.violet.line, lineWidth: 1)
                Text(emoji).font(.system(size: size * 0.56))
            }
            .frame(width: size, height: size)
            .accessibilityLabel(bot.name)
        } else {
            Avatar(name: bot.name, url: bot.avatarURL, seed: bot.id, size: size)
                .accessibilityLabel(bot.name)
        }
    }
}

/// The small violet "AI" tag beside a bot's name, so agents can tell the AI wrote it.
struct AITag: View {
    var body: some View {
        Text("AI")
            .font(ODFont.sans(10, weight: .semibold))
            .foregroundStyle(Tone.violet.text)
            .padding(.horizontal, 5)
            .frame(height: 16)
            .background(Tone.violet.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
            .accessibilityLabel("AI reply")
    }
}
