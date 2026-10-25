import SwiftUI

/// An empty conversation: Ollie says hello, and the asks that fit the
/// screen behind the sheet are one tap away.
struct AssistantGreeting: View {
    let assistantName: String
    let prompts: [AssistantPrompt]
    let onPick: (AssistantPrompt) -> Void

    @State private var appeared = false
    @State private var height: CGFloat = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                Spacer(minLength: 24)
                ZStack {
                    Circle()
                        .fill(RadialGradient(colors: [Tone.violet.solid.opacity(0.16), Tone.violet.solid.opacity(0)], center: .center, startRadius: 0, endRadius: 70))
                        .frame(width: 140, height: 140)
                        .scaleEffect(appeared ? 1 : 0.6)
                    OllieMark(size: 60)
                        .scaleEffect(appeared ? 1 : 0.7)
                }
                .frame(width: 60, height: 60)
                .padding(.bottom, 22)
                Text("Hi, I'm \(assistantName).")
                    .font(ODFont.sans(26, weight: .semibold))
                    .tracking(-0.6)
                    .foregroundStyle(Theme.fg)
                Text("I can read your queue, explain a ticket, pull numbers and prepare changes. Nothing changes until you approve it.")
                    .font(ODFont.sans(15.5))
                    .lineSpacing(3)
                    .foregroundStyle(Theme.fg3)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.top, 8)
                    .padding(.trailing, 12)

                SectionLabel("Try")
                    .padding(.top, 28)
                    .padding(.bottom, 8)
                VStack(spacing: 0) {
                    ForEach(Array(prompts.enumerated()), id: \.element) { index, prompt in
                        if index > 0 { Hairline(inset: 50) }
                        row(prompt)
                            .opacity(appeared ? 1 : 0)
                            .offset(y: appeared ? 0 : 8)
                            .animation(reduceMotion ? nil : Motion.smooth.delay(0.08 + Double(index) * 0.05), value: appeared)
                    }
                }
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                .clipShape(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                Text("Type / for quick asks.")
                    .font(ODFont.sans(12.5))
                    .foregroundStyle(Theme.fg4)
                    .padding(.top, 10)
                    .padding(.leading, 2)
            }
            .padding(.horizontal, 20)
            .padding(.bottom, 16)
            .frame(maxWidth: .infinity, alignment: .leading)
            .frame(minHeight: height, alignment: .bottom)
        }
        .onScrollGeometryChange(for: CGFloat.self) { $0.containerSize.height - $0.contentInsets.top - $0.contentInsets.bottom } action: { _, new in
            height = max(new, 0)
        }
        .defaultScrollAnchor(.bottom)
        .scrollBounceBehavior(.basedOnSize)
        .scrollDismissesKeyboard(.interactively)
        .onAppear {
            guard !appeared else { return }
            if reduceMotion {
                appeared = true
            } else {
                withAnimation(Motion.smooth) { appeared = true }
            }
        }
    }

    private func row(_ prompt: AssistantPrompt) -> some View {
        Button {
            Haptics.tap()
            onPick(prompt)
        } label: {
            HStack(spacing: 12) {
                Image(systemName: prompt.symbol)
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(Tone.violet.text)
                    .frame(width: 26, height: 26)
                    .background(Tone.violet.soft, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
                Text(prompt.chip)
                    .font(ODFont.sans(15))
                    .foregroundStyle(Theme.fg)
                    .lineLimit(1)
                Spacer(minLength: 6)
                Image(systemName: "arrow.up.right")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(Theme.fg4)
            }
            .padding(.horizontal, 12)
            .frame(height: 50)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
        .accessibilityHint(prompt.prompt)
    }
}

/// The short asks above the composer once a conversation is going.
struct AssistantSuggestionStrip: View {
    let prompts: [AssistantPrompt]
    let onPick: (AssistantPrompt) -> Void

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                ForEach(prompts) { prompt in
                    Button {
                        Haptics.tap()
                        onPick(prompt)
                    } label: {
                        Text(prompt.chip)
                            .font(ODFont.sans(13))
                            .foregroundStyle(Theme.fg2)
                            .padding(.horizontal, 12)
                            .frame(height: 32)
                            .glassEffect(.regular.interactive(), in: Capsule())
                    }
                    .buttonStyle(TapScaleStyle(scale: 0.96))
                    .accessibilityHint(prompt.prompt)
                }
            }
            .padding(.horizontal, 16)
        }
        .scrollClipDisabled()
    }
}
