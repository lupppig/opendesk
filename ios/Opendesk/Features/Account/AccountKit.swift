import SwiftUI

// The pieces every Account page is built from: a page on the grey chrome with
// white grouped panels, rows with a soft tinted icon tile, switches, and a
// segmented control with a sliding tile.

/// A pushed settings page: grey chrome, an inline Geist title, grouped panels.
struct AccountPage<Content: View>: View {
    let title: String
    var spacing: CGFloat = 24
    @ViewBuilder var content: () -> Content

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: spacing) { content() }
                .padding(.horizontal, 16)
                .padding(.top, 8)
                .padding(.bottom, 120)
        }
        .scrollDismissesKeyboard(.interactively)
        .background(Theme.chrome.ignoresSafeArea())
        .accountTitle(title)
    }
}

extension View {
    /// The system navigation bar with the title in Geist.
    func accountTitle(_ title: String) -> some View {
        navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .principal) {
                    Text(title)
                        .font(ODFont.sans(16, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                        .accessibilityAddTraits(.isHeader)
                }
            }
    }
}

/// A titled group: a label, a white panel of rows, and an optional footnote.
struct AccountSection<Content: View>: View {
    var title: String?
    var footer: String?
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if let title {
                SectionLabel(title).padding(.horizontal, 4)
            }
            Panel { content() }
            if let footer {
                Text(footer)
                    .font(ODFont.footnote)
                    .foregroundStyle(Theme.fg3)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, 4)
            }
        }
    }
}

/// A rounded square with a symbol on a soft wash of its tone.
struct AccountIconTile: View {
    let symbol: String
    var tone: Tone = .gray
    var size: CGFloat = 30

    var body: some View {
        Image(systemName: symbol)
            .font(.system(size: size * 0.48, weight: .medium))
            .foregroundStyle(tone == .gray ? Theme.fg2 : tone.text)
            .frame(width: size, height: size)
            .background(tone == .gray ? Theme.active : tone.solid.opacity(0.13), in: RoundedRectangle(cornerRadius: size * 0.28, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: size * 0.28, style: .continuous).strokeBorder(tone == .gray ? Theme.line : tone.line, lineWidth: 0.5))
            .accessibilityHidden(true)
    }
}

/// One row: an icon tile, a title with an optional line under it, and what sits on the right.
struct AccountRow<Trailing: View>: View {
    var symbol: String?
    var tone: Tone = .gray
    let title: String
    var subtitle: String?
    var titleColor: Color = Theme.fg
    @ViewBuilder var trailing: () -> Trailing

    var body: some View {
        HStack(spacing: 12) {
            if let symbol { AccountIconTile(symbol: symbol, tone: tone) }
            VStack(alignment: .leading, spacing: 2) {
                Text(title)
                    .font(ODFont.sans(15.5))
                    .foregroundStyle(titleColor)
                if let subtitle, !subtitle.isEmpty {
                    Text(subtitle)
                        .font(ODFont.footnote)
                        .foregroundStyle(Theme.fg3)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: 8)
            trailing()
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
        .frame(minHeight: 52)
        .contentShape(Rectangle())
    }
}

extension AccountRow where Trailing == EmptyView {
    init(symbol: String? = nil, tone: Tone = .gray, title: String, subtitle: String? = nil, titleColor: Color = Theme.fg) {
        self.init(symbol: symbol, tone: tone, title: title, subtitle: subtitle, titleColor: titleColor, trailing: { EmptyView() })
    }
}

/// A value in grey and a chevron, for rows that open something.
struct AccountDisclosure: View {
    var value: String?

    var body: some View {
        HStack(spacing: 8) {
            if let value, !value.isEmpty {
                Text(value)
                    .font(ODFont.sans(14.5))
                    .foregroundStyle(Theme.fg3)
                    .lineLimit(1)
            }
            Chevron()
        }
    }
}

/// A row that pushes an Account page.
struct AccountLinkRow: View {
    let route: AccountRoute
    var value: String?
    var subtitle: String?

    var body: some View {
        NavigationLink(value: route) {
            AccountRow(symbol: route.symbol, tone: route.tone, title: route.title, subtitle: subtitle) {
                AccountDisclosure(value: value)
            }
        }
        .buttonStyle(RowPressStyle())
    }
}

/// A row with a switch.
struct AccountToggleRow: View {
    var symbol: String?
    var tone: Tone = .gray
    let title: String
    var subtitle: String?
    @Binding var isOn: Bool
    var disabled = false

    var body: some View {
        AccountRow(symbol: symbol, tone: tone, title: title, subtitle: subtitle) {
            Toggle(title, isOn: $isOn)
                .labelsHidden()
                .tint(Tone.green.solid)
                .disabled(disabled)
        }
        .opacity(disabled ? 0.5 : 1)
        .sensoryFeedback(.selection, trigger: isOn)
    }
}

/// A divider between rows, starting under the text when rows have tiles.
struct AccountDivider: View {
    var tiles = true

    var body: some View {
        Hairline(inset: tiles ? 56 : 14)
    }
}

/// A segmented control: options on a sunken track, the chosen one on a white tile that slides.
struct AccountSegmented<Value: Hashable>: View {
    let options: [Value]
    @Binding var selection: Value
    let label: (Value) -> String
    var dot: ((Value) -> Color?)? = nil
    var height: CGFloat = 34

    @Namespace private var tile
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 2) {
            ForEach(options, id: \.self) { option in
                let chosen = option == selection
                Button {
                    guard option != selection else { return }
                    Haptics.selection()
                    withAnimation(reduceMotion ? nil : Motion.snappy) { selection = option }
                } label: {
                    HStack(spacing: 6) {
                        if let color = dot?(option) {
                            Circle().fill(color).frame(width: 7, height: 7)
                        }
                        Text(label(option))
                            .font(ODFont.sans(13.5, weight: chosen ? .semibold : .medium))
                            .foregroundStyle(chosen ? Theme.fg : Theme.fg3)
                            .lineLimit(1)
                    }
                    .frame(maxWidth: .infinity)
                    .frame(height: height - 6)
                    .background {
                        if chosen {
                            RoundedRectangle(cornerRadius: Theme.Radius.control - 3, style: .continuous)
                                .fill(Theme.raised)
                                .shadow(color: .black.opacity(0.06), radius: 2, y: 1)
                                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control - 3, style: .continuous).strokeBorder(Theme.line, lineWidth: 0.5))
                                .matchedGeometryEffect(id: "tile", in: tile)
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(chosen ? .isSelected : [])
            }
        }
        .padding(3)
        .frame(height: height)
        .background(Theme.active.opacity(0.7), in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
    }
}

/// A choice in a list of options, with a check on the chosen one.
struct AccountChoiceRow: View {
    var symbol: String?
    var tone: Tone = .gray
    let title: String
    var subtitle: String?
    let chosen: Bool
    let action: () -> Void

    var body: some View {
        Button {
            Haptics.selection()
            action()
        } label: {
            AccountRow(symbol: symbol, tone: tone, title: title, subtitle: subtitle) {
                Image(systemName: "checkmark")
                    .font(.system(size: 14, weight: .semibold))
                    .foregroundStyle(Theme.fg)
                    .opacity(chosen ? 1 : 0)
                    .scaleEffect(chosen ? 1 : 0.6)
                    .motion(Motion.snappy, value: chosen)
            }
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(chosen ? .isSelected : [])
    }
}

/// A sheet's header: a title, an optional line, and a close button.
struct AccountSheetHeader: View {
    let title: String
    var subtitle: String?
    var closeLabel = "Close"
    let close: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text(title)
                    .font(ODFont.sans(20, weight: .semibold))
                    .tracking(-0.4)
                    .foregroundStyle(Theme.fg)
                if let subtitle {
                    Text(subtitle)
                        .font(ODFont.callout)
                        .foregroundStyle(Theme.fg3)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: 8)
            Button(action: close) {
                Image(systemName: "xmark")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Theme.fg2)
                    .frame(width: 32, height: 32)
                    .background(Theme.active, in: Circle())
            }
            .buttonStyle(TapScaleStyle(scale: 0.9))
            .accessibilityLabel(closeLabel)
        }
    }
}

/// A multi-line field on a white panel, for signatures and reply bodies.
struct AccountTextEditor: View {
    let label: String
    @Binding var text: String
    var prompt = ""
    var minHeight: CGFloat = 120
    var mono = false

    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(label).font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg2)
            TextField(text: $text, axis: .vertical) { Text(prompt).foregroundStyle(Theme.fg4) }
                .font(mono ? ODFont.mono(14) : ODFont.sans(15.5))
                .foregroundStyle(Theme.fg)
                .lineLimit(4 ... 14)
                .focused($focused)
                .padding(.horizontal, 13)
                .padding(.vertical, 11)
                .frame(minHeight: minHeight, alignment: .topLeading)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous)
                        .strokeBorder(focused ? Theme.lineStrong : Theme.line, lineWidth: focused ? 1.5 : 1)
                )
                .motion(Motion.fade, value: focused)
        }
    }
}

/// A confirmation in the app's own style: a title, what happens, and two buttons.
struct AccountConfirm: Identifiable {
    let id = UUID()
    var title: String
    var message: String
    var confirmTitle: String
    var destructive = true
    var action: @MainActor () async -> Void
}

struct AccountConfirmSheet: View {
    let confirm: AccountConfirm
    @Environment(\.dismiss) private var dismiss
    @State private var busy = false
    @State private var height: CGFloat = 250

    var body: some View {
        VStack(alignment: .leading, spacing: 22) {
            VStack(alignment: .leading, spacing: 8) {
                Text(confirm.title)
                    .font(ODFont.sans(19, weight: .semibold))
                    .tracking(-0.3)
                    .foregroundStyle(Theme.fg)
                Text(confirm.message)
                    .font(ODFont.callout)
                    .foregroundStyle(Theme.fg2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            HStack(spacing: 10) {
                Button("Cancel") { dismiss() }
                    .buttonStyle(OutlineButtonStyle())
                Button {
                    busy = true
                    Task {
                        await confirm.action()
                        busy = false
                        dismiss()
                    }
                } label: {
                    ButtonLabel(title: confirm.confirmTitle, busy: busy)
                }
                .buttonStyle(AccountDangerButtonStyle(destructive: confirm.destructive))
                .disabled(busy)
            }
        }
        .padding(.horizontal, 24)
        .padding(.top, 28)
        .padding(.bottom, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .onGeometryChange(for: CGFloat.self) { $0.size.height } action: { height = $0 }
        .frame(maxHeight: .infinity, alignment: .top)
        .background(Theme.surface)
        .presentationDetents([.height(height)])
        .presentationCornerRadius(Theme.Radius.sheet)
    }
}

/// A red primary button for actions that remove something (ink when not destructive).
struct AccountDangerButtonStyle: ButtonStyle {
    var destructive = true
    var height: CGFloat = 46

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(ODFont.sans(15.5, weight: .semibold))
            .foregroundStyle(destructive ? Color.white : Theme.inverseFg)
            .frame(maxWidth: .infinity)
            .frame(height: height)
            .background(destructive ? Tone.red.solid : Theme.inverse, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .opacity(configuration.isPressed ? 0.88 : 1)
            .animation(Motion.press, value: configuration.isPressed)
    }
}

extension View {
    /// Presents the app's own confirmation for `item`.
    func accountConfirm(_ item: Binding<AccountConfirm?>) -> some View {
        sheet(item: item) { confirm in AccountConfirmSheet(confirm: confirm) }
    }
}
