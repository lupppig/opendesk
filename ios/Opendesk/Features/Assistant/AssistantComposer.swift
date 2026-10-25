import SwiftUI

/// The message box on Liquid Glass: grows to six lines, `/` lists the quick
/// asks, ⌘Return sends, and a small pill says what Ollie can see.
struct AssistantComposer: View {
    @Binding var text: String
    var focused: FocusState<Bool>.Binding
    let running: Bool
    let waitingForApproval: Bool
    let assistantName: String
    let contextLabel: AssistantContextLabel?
    let onSend: (String) -> Void
    let onStop: () -> Void

    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var slash: [AssistantPrompt] { AssistantPrompt.slashMatches(text) }
    private var canSend: Bool { !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && !running }

    var body: some View {
        VStack(spacing: 8) {
            if !slash.isEmpty {
                slashList
                    .transition(.asymmetric(insertion: .scale(scale: 0.97, anchor: .bottom).combined(with: .opacity), removal: .opacity))
            }
            field
        }
        .onChange(of: text) { old, new in
            // The software keyboard's return while the quick asks are open picks the first one.
            guard new.hasSuffix("\n"), let first = AssistantPrompt.slashMatches(old).first else { return }
            pick(first)
        }
        .animation(reduceMotion ? nil : Motion.snappy, value: slash)
        .animation(reduceMotion ? nil : Motion.snappy, value: running)
        .animation(reduceMotion ? nil : Motion.snappy, value: canSend)
    }

    private var field: some View {
        VStack(alignment: .leading, spacing: 2) {
            if let contextLabel {
                HStack(spacing: 5) {
                    Image(systemName: contextLabel.symbol)
                        .font(.system(size: 10, weight: .semibold))
                    Text(contextLabel.title)
                        .font(ODFont.sans(11.5, weight: .medium))
                        .lineLimit(1)
                }
                .foregroundStyle(Tone.violet.text)
                .padding(.horizontal, 7)
                .frame(height: 21)
                .background(Tone.violet.soft, in: Capsule())
                .padding(.top, 10)
                .padding(.leading, 12)
                .accessibilityElement(children: .ignore)
                .accessibilityLabel("\(assistantName) sees \(contextLabel.title)")
            }
            HStack(alignment: .bottom, spacing: 8) {
                TextField(text: $text, axis: .vertical) {
                    Text(waitingForApproval ? "Approve the change, or ask something else" : "Ask \(assistantName) anything")
                        .foregroundStyle(Theme.fg4)
                }
                .font(ODFont.sans(16.5))
                .foregroundStyle(Theme.fg)
                .tint(Tone.violet.solid)
                .lineLimit(1 ... 6)
                .focused(focused)
                .submitLabel(.send)
                .padding(.vertical, 12)
                .padding(.leading, 16)
                .onKeyPress(.return, phases: .down) { press in
                    // Return sends; Shift-Return breaks the line (hardware keyboards).
                    guard !press.modifiers.contains(.shift) else { return .ignored }
                    if let first = slash.first {
                        pick(first)
                        return .handled
                    }
                    guard canSend else { return .handled }
                    submit()
                    return .handled
                }

                actionButton
                    .padding(.trailing, 7)
                    .padding(.bottom, 7)
            }
        }
        .glassEffect(.regular.interactive(), in: RoundedRectangle(cornerRadius: 24, style: .continuous))
        .contentShape(RoundedRectangle(cornerRadius: 24, style: .continuous))
        .onTapGesture { focused.wrappedValue = true }
    }

    @ViewBuilder
    private var actionButton: some View {
        if running {
            Button {
                Haptics.tap()
                onStop()
            } label: {
                Image(systemName: "square.fill")
                    .font(.system(size: 11, weight: .bold))
                    .foregroundStyle(Theme.inverseFg)
                    .frame(width: 34, height: 34)
                    .background(Theme.inverse, in: Circle())
            }
            .buttonStyle(TapScaleStyle(scale: 0.9))
            .accessibilityLabel("Stop")
            .transition(.scale(scale: 0.5).combined(with: .opacity))
        } else {
            Button(action: submit) {
                Image(systemName: "arrow.up")
                    .font(.system(size: 15, weight: .bold))
                    .foregroundStyle(canSend ? Theme.inverseFg : Theme.fg4)
                    .frame(width: 34, height: 34)
                    .background(canSend ? Theme.inverse : Theme.active, in: Circle())
            }
            .buttonStyle(TapScaleStyle(scale: 0.9))
            .disabled(!canSend)
            .keyboardShortcut(.return, modifiers: .command)
            .accessibilityLabel("Send")
            .transition(.scale(scale: 0.5).combined(with: .opacity))
        }
    }

    /// Four and a half rows at most, so the keyboard, the list and the
    /// conversation all fit; the half row says there is more.
    private var slashList: some View {
        ScrollView {
            slashRows
        }
        .scrollBounceBehavior(.basedOnSize)
        .frame(height: min(CGFloat(slash.count) * 42 + 8, 4.5 * 42 + 4))
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Quick asks")
    }

    private var slashRows: some View {
        VStack(spacing: 0) {
            ForEach(Array(slash.enumerated()), id: \.element) { index, prompt in
                if index > 0 { Hairline(inset: 46) }
                Button {
                    pick(prompt)
                } label: {
                    HStack(spacing: 10) {
                        Image(systemName: prompt.symbol)
                            .font(.system(size: 13, weight: .medium))
                            .foregroundStyle(Tone.violet.text)
                            .frame(width: 24)
                        Text("/" + prompt.rawValue)
                            .font(ODFont.mono(13, weight: .medium))
                            .foregroundStyle(Tone.violet.text)
                            .frame(width: 92, alignment: .leading)
                        Text(prompt.slashHint)
                            .font(ODFont.sans(14))
                            .foregroundStyle(index == 0 ? Theme.fg : Theme.fg2)
                            .lineLimit(1)
                        Spacer(minLength: 0)
                        if index == 0 {
                            Image(systemName: "return")
                                .font(.system(size: 11, weight: .semibold))
                                .foregroundStyle(Theme.fg4)
                        }
                    }
                    .padding(.horizontal, 12)
                    .frame(height: 42)
                    .contentShape(Rectangle())
                }
                .buttonStyle(RowPressStyle())
            }
        }
        .padding(.vertical, 4)
    }

    private func pick(_ prompt: AssistantPrompt) {
        Haptics.selection()
        text = ""
        onSend(prompt.prompt)
    }

    private func submit() {
        let value = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !value.isEmpty, !running else { return }
        text = ""
        onSend(value)
    }
}

/// What the composer's pill says Ollie is looking at.
struct AssistantContextLabel: Equatable {
    var title: String
    var symbol: String
}
