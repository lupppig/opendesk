import SwiftUI

/// Light, dark, or whatever the iPhone uses, each with a small preview of the app.
struct AccountAppearanceView: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        AccountPage(title: "Appearance") {
            HStack(spacing: 12) {
                ForEach(Appearance.allCases) { option in
                    AccountAppearanceTile(option: option, chosen: env.appearance == option) {
                        Haptics.selection()
                        withAnimation(Motion.smooth) { env.appearance = option }
                    }
                }
            }
            .padding(.top, 8)
            Text(footer)
                .font(ODFont.footnote)
                .foregroundStyle(Theme.fg3)
                .fixedSize(horizontal: false, vertical: true)
                .padding(.horizontal, 4)
                .contentTransition(.opacity)
                .motion(Motion.fade, value: env.appearance)
        }
    }

    private var footer: String {
        switch env.appearance {
        case .system: "opendesk follows your iPhone, light by day and dark at night if you set it to change."
        case .light: "opendesk stays light, whatever your iPhone uses."
        case .dark: "opendesk stays dark, whatever your iPhone uses."
        }
    }
}

private struct AccountAppearanceTile: View {
    let option: Appearance
    let chosen: Bool
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            VStack(spacing: 10) {
                preview
                    .frame(height: 150)
                    .clipShape(RoundedRectangle(cornerRadius: 12, style: .continuous))
                    .overlay(
                        RoundedRectangle(cornerRadius: 12, style: .continuous)
                            .strokeBorder(chosen ? Theme.fg : Theme.line, lineWidth: chosen ? 2 : 1)
                    )
                    .padding(chosen ? 0 : 1)
                HStack(spacing: 6) {
                    ZStack {
                        Circle().strokeBorder(chosen ? Theme.fg : Theme.lineStrong, lineWidth: 1.5)
                        Circle().fill(Theme.fg).padding(4).scaleEffect(chosen ? 1 : 0)
                    }
                    .frame(width: 16, height: 16)
                    Text(option == .system ? "Automatic" : option.title)
                        .font(ODFont.sans(13.5, weight: chosen ? .semibold : .medium))
                        .foregroundStyle(chosen ? Theme.fg : Theme.fg2)
                }
            }
            .frame(maxWidth: .infinity)
            .motion(Motion.snappy, value: chosen)
        }
        .buttonStyle(TapScaleStyle(scale: 0.96))
        .accessibilityLabel(option.title)
        .accessibilityAddTraits(chosen ? .isSelected : [])
    }

    @ViewBuilder
    private var preview: some View {
        switch option {
        case .light: AccountMiniApp(dark: false)
        case .dark: AccountMiniApp(dark: true)
        case .system:
            HStack(spacing: 0) {
                AccountMiniApp(dark: false).frame(minWidth: 0, maxWidth: .infinity).clipped()
                AccountMiniApp(dark: true).frame(minWidth: 0, maxWidth: .infinity).clipped()
            }
        }
    }
}

/// A tiny inbox drawn in fixed colours of one theme.
private struct AccountMiniApp: View {
    let dark: Bool

    private var chrome: Color { dark ? Color(white: 0.08) : Color(white: 0.96) }
    private var surface: Color { dark ? Color(white: 0.11) : .white }
    private var ink: Color { dark ? Color(white: 0.9) : Color(white: 0.1) }
    private var faint: Color { dark ? Color(white: 0.24) : Color(white: 0.88) }

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 5) {
                Circle().fill(faint).frame(width: 12, height: 12)
                RoundedRectangle(cornerRadius: 2).fill(ink).frame(width: 34, height: 6)
                Spacer()
            }
            VStack(alignment: .leading, spacing: 0) {
                ForEach(0 ..< 4, id: \.self) { index in
                    HStack(spacing: 5) {
                        Circle().fill([Tone.blue, .amber, .green, .violet][index].solid).frame(width: 6, height: 6)
                        VStack(alignment: .leading, spacing: 3) {
                            RoundedRectangle(cornerRadius: 1.5).fill(ink.opacity(0.85)).frame(width: [40, 30, 46, 26][index], height: 4)
                            RoundedRectangle(cornerRadius: 1.5).fill(faint).frame(height: 3).padding(.trailing, [8, 18, 4, 14][index])
                        }
                    }
                    .padding(.vertical, 6)
                    .padding(.horizontal, 6)
                    if index < 3 { Rectangle().fill(faint.opacity(0.6)).frame(height: 0.5) }
                }
            }
            .background(surface, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
            Spacer(minLength: 0)
            Capsule().fill(surface).frame(width: 46, height: 12).frame(maxWidth: .infinity)
                .overlay(Capsule().strokeBorder(faint, lineWidth: 0.5).frame(width: 46, height: 12))
        }
        .padding(9)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
        .background(chrome)
    }
}
