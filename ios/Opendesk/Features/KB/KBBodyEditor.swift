import SwiftUI

/// The article body: markdown with a formatting bar (heading, bold, italic,
/// lists, link, code, quote) and a preview, or the HTML itself when the
/// article holds something the text form can't show.
struct KBBodyEditor: View {
    @Binding var form: KBArticleForm
    @Binding var preview: Bool
    var focused: FocusState<Bool>.Binding
    let onSwitchFailed: () -> Void

    @State private var selection: TextSelection?
    @State private var previewHeight: CGFloat = 160
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Hairline()
            if preview {
                Group {
                    if form.bodyHTML.isEmpty {
                        Text("Nothing to preview yet.")
                            .font(ODFont.sans(14))
                            .foregroundStyle(Theme.fg4)
                            .frame(maxWidth: .infinity, minHeight: 120, alignment: .topLeading)
                    } else {
                        KBArticleWebView(html: form.bodyHTML, height: $previewHeight, fontSize: 15.5)
                            .frame(height: max(previewHeight, 60))
                    }
                }
                .padding(16)
                .transition(.opacity)
            } else if form.raw {
                VStack(alignment: .leading, spacing: 10) {
                    Notice(text: "This article has formatting the text editor can't show, like a table or an image, so you're editing its HTML. Nothing is lost.", tone: .blue, symbol: "chevron.left.forwardslash.chevron.right")
                    editor(text: $form.html, mono: true, prompt: "<p>Write HTML here</p>")
                }
                .padding(.horizontal, 12)
                .padding(.top, 12)
                .transition(.opacity)
            } else {
                KBFormatBar { apply($0) }
                Hairline()
                editor(text: $form.markdown, mono: false, prompt: "Write the article. Start a line with ## for a heading, - for a list.")
                    .padding(.horizontal, 12)
                    .transition(.opacity)
            }
        }
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        .animation(reduceMotion ? nil : Motion.fade, value: preview)
        .animation(reduceMotion ? nil : Motion.fade, value: form.raw)
    }

    private var header: some View {
        HStack(spacing: 10) {
            Text("Body")
                .font(ODFont.sans(13, weight: .semibold))
                .foregroundStyle(Theme.fg2)
            if form.raw {
                TonePill(text: "HTML", tone: .blue, height: 18)
            }
            Spacer(minLength: 4)
            KBSegment(options: ["Write", "Preview"], selection: Binding(get: { preview ? 1 : 0 }, set: { value in
                Haptics.selection()
                if value == 1 { focused.wrappedValue = false }
                preview = value == 1
            }))
            .frame(width: 150)
        }
        .padding(.horizontal, 14)
        .frame(height: 46)
    }

    private func editor(text: Binding<String>, mono: Bool, prompt: String) -> some View {
        ZStack(alignment: .topLeading) {
            if text.wrappedValue.isEmpty {
                Text(prompt)
                    .font(mono ? ODFont.mono(13.5, relativeTo: .body) : ODFont.sans(16))
                    .foregroundStyle(Theme.fg4)
                    .padding(.top, 8)
                    .padding(.leading, 5)
                    .allowsHitTesting(false)
            }
            TextEditor(text: text, selection: $selection)
                .font(mono ? ODFont.mono(13.5, relativeTo: .body) : ODFont.sans(16))
                .foregroundStyle(Theme.fg)
                .lineSpacing(mono ? 3 : 5)
                .scrollContentBackground(.hidden)
                .scrollDisabled(true)
                .textInputAutocapitalization(mono ? .never : .sentences)
                .autocorrectionDisabled(mono)
                .focused(focused)
                .frame(minHeight: 320)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.vertical, 6)
    }

    /// Applies a format at the selection, or at the end when nothing is selected yet.
    func apply(_ format: KBFormat) {
        Haptics.tap()
        let text = form.markdown
        let range = offsets(in: text)
        let (updated, selected) = format.apply(to: text, range: range)
        form.markdown = updated
        let start = updated.index(updated.startIndex, offsetBy: min(selected.lowerBound, updated.count))
        let end = updated.index(updated.startIndex, offsetBy: min(selected.upperBound, updated.count))
        selection = TextSelection(range: start ..< end)
        focused.wrappedValue = true
    }

    private func offsets(in text: String) -> Range<Int> {
        guard let selection else { return text.count ..< text.count }
        let range: Range<String.Index>? = switch selection.indices {
        case let .selection(range): range
        case let .multiSelection(set): set.ranges.first
        @unknown default: nil
        }
        guard let range, range.lowerBound >= text.startIndex, range.upperBound <= text.endIndex else { return text.count ..< text.count }
        let lower = text.distance(from: text.startIndex, to: range.lowerBound)
        let upper = text.distance(from: text.startIndex, to: range.upperBound)
        return lower ..< max(lower, upper)
    }
}

/// The row of formatting buttons over the text.
struct KBFormatBar: View {
    let apply: (KBFormat) -> Void

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 2) {
                ForEach(KBFormat.allCases) { format in
                    Button { apply(format) } label: {
                        Image(systemName: format.symbol)
                            .font(.system(size: 14, weight: .medium))
                            .foregroundStyle(Theme.fg2)
                            .frame(width: 38, height: 34)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(KBToolButtonStyle())
                    .accessibilityLabel(format.label)
                    if format == .italic || format == .numbers {
                        Rectangle().fill(Theme.line).frame(width: 1, height: 18).padding(.horizontal, 3)
                    }
                }
            }
            .padding(.horizontal, 8)
        }
        .frame(height: 42)
    }
}

struct KBToolButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .background(configuration.isPressed ? Theme.active : .clear, in: RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous))
            .scaleEffect(configuration.isPressed ? 0.94 : 1)
            .animation(Motion.press, value: configuration.isPressed)
    }
}

/// A small two or three way segmented control with a sliding pill.
struct KBSegment: View {
    let options: [String]
    @Binding var selection: Int
    @Namespace private var pill

    var body: some View {
        HStack(spacing: 2) {
            ForEach(options.indices, id: \.self) { index in
                let on = selection == index
                Button {
                    withAnimation(Motion.snappy) { selection = index }
                } label: {
                    Text(options[index])
                        .font(ODFont.sans(12.5, weight: on ? .semibold : .medium))
                        .foregroundStyle(on ? Theme.fg : Theme.fg3)
                        .frame(maxWidth: .infinity)
                        .frame(height: 26)
                        .background {
                            if on {
                                RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous)
                                    .fill(Theme.raised)
                                    .shadow(color: .black.opacity(0.06), radius: 2, y: 1)
                                    .matchedGeometryEffect(id: "pill", in: pill)
                            }
                        }
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(on ? .isSelected : [])
            }
        }
        .padding(2)
        .background(Theme.active.opacity(0.7), in: RoundedRectangle(cornerRadius: Theme.Radius.chip + 2, style: .continuous))
    }
}

/// The violet action for anything Ollie does.
struct KBOllieButtonStyle: ButtonStyle {
    var height: CGFloat = 46
    @Environment(\.isEnabled) private var isEnabled

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(ODFont.sans(15.5, weight: .semibold))
            .foregroundStyle(.white)
            .frame(maxWidth: .infinity)
            .frame(height: height)
            .background(Tone.violet.solid.opacity(isEnabled ? 1 : 0.4), in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .opacity(configuration.isPressed ? 0.88 : 1)
            .animation(Motion.press, value: configuration.isPressed)
    }
}
