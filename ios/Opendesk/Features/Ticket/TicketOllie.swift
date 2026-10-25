import SwiftUI

/// Ollie's read of the ticket: the summary, key points and sentiment, and the triage
/// suggestions waiting for a decision (Apply, Skip, Apply all).
struct TicketOllieCard: View {
    let summary: AISummary?
    let suggestions: [AISuggestion]
    let summarizing: Bool
    let triaging: Bool
    let canWrite: Bool
    let teamName: (String) -> String?
    @Binding var expanded: Bool
    let onSummarize: () -> Void
    let onApply: (AISuggestion) -> Void
    let onSkip: (AISuggestion) -> Void
    let onApplyAll: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            if expanded {
                VStack(alignment: .leading, spacing: 12) {
                    if let summary {
                        Text(summary.text)
                            .font(ODFont.sans(14.5))
                            .foregroundStyle(Theme.fg)
                            .lineSpacing(2.5)
                            .fixedSize(horizontal: false, vertical: true)
                            .textSelection(.enabled)
                        if !summary.keyPoints.isEmpty {
                            VStack(alignment: .leading, spacing: 6) {
                                ForEach(Array(summary.keyPoints.enumerated()), id: \.offset) { _, point in
                                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                                        Circle().fill(Tone.violet.solid.opacity(0.7)).frame(width: 4.5, height: 4.5).alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + 4 }
                                        Text(point).font(ODFont.sans(13.5)).foregroundStyle(Theme.fg2).fixedSize(horizontal: false, vertical: true)
                                    }
                                }
                            }
                        }
                    } else if summarizing {
                        Text("Reading the conversation\u{2026}").font(ODFont.sans(13.5)).foregroundStyle(Theme.fg3)
                    } else {
                        HStack {
                            Text("A short summary of the conversation so far.").font(ODFont.sans(13.5)).foregroundStyle(Theme.fg3)
                            Spacer()
                            Button("Summarize", action: onSummarize).buttonStyle(ChipButtonStyle(tone: .violet))
                        }
                    }
                    if !suggestions.isEmpty, canWrite { suggestionList }
                }
                .padding(.horizontal, 14)
                .padding(.bottom, 14)
                .transition(.opacity)
            }
        }
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .background(
            LinearGradient(colors: [Tone.violet.soft, .clear], startPoint: .topLeading, endPoint: .center)
                .clipShape(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        )
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Tone.violet.line, lineWidth: 1))
    }

    private var header: some View {
        Button {
            Haptics.tap()
            withAnimation(Motion.snappy) { expanded.toggle() }
        } label: {
            HStack(spacing: 8) {
                OllieMark(size: 20, state: summarizing || triaging ? .thinking : .idle)
                Text(summary == nil && !suggestions.isEmpty ? "Ollie suggests" : "Summary")
                    .font(ODFont.sans(14, weight: .semibold))
                    .foregroundStyle(Theme.fg)
                if let sentiment = summary?.sentiment {
                    TonePill(text: sentiment.title, tone: sentiment.tone, height: 19)
                }
                if !expanded, !suggestions.isEmpty {
                    TonePill(text: Format.plural(suggestions.count, "suggestion"), tone: .violet, height: 19)
                }
                Spacer(minLength: 4)
                if let updated = summary?.updatedAt {
                    Text(Format.relShort(updated)).font(ODFont.mono(10.5)).foregroundStyle(Theme.fg4)
                }
                if summary != nil, canWrite {
                    Button(action: onSummarize) {
                        Image(systemName: "arrow.clockwise").font(.system(size: 11.5, weight: .semibold)).foregroundStyle(Theme.fg3)
                            .frame(width: 26, height: 26)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .disabled(summarizing)
                    .accessibilityLabel("Summarize again")
                }
                Image(systemName: "chevron.down")
                    .font(.system(size: 10.5, weight: .bold))
                    .foregroundStyle(Theme.fg4)
                    .rotationEffect(.degrees(expanded ? 0 : -90))
            }
            .padding(.horizontal, 14)
            .frame(height: 46)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .accessibilityHint(expanded ? "Collapses Ollie's summary" : "Shows Ollie's summary")
    }

    private var suggestionList: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("Ollie suggests").font(ODFont.sans(12, weight: .medium)).foregroundStyle(Theme.fg3)
                Spacer()
                if suggestions.count > 1 {
                    Button("Apply all", action: onApplyAll)
                        .font(ODFont.sans(12.5, weight: .semibold))
                        .foregroundStyle(Tone.violet.text)
                }
            }
            .padding(.bottom, 6)
            VStack(spacing: 0) {
                ForEach(Array(suggestions.enumerated()), id: \.element.id) { index, suggestion in
                    if index > 0 { Hairline() }
                    HStack(spacing: 8) {
                        Text(fieldTitle(suggestion.field)).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).frame(width: 64, alignment: .leading)
                        value(suggestion)
                        if let confidence = suggestion.confidence {
                            Text("\(Int((confidence * 100).rounded()))%").font(ODFont.mono(10.5)).foregroundStyle(Theme.fg4)
                        }
                        Spacer(minLength: 4)
                        Button("Skip") { onSkip(suggestion) }
                            .font(ODFont.sans(12.5, weight: .medium))
                            .foregroundStyle(Theme.fg3)
                            .padding(.horizontal, 4)
                        Button("Apply") { onApply(suggestion) }
                            .buttonStyle(ChipButtonStyle(tone: .violet))
                    }
                    .frame(minHeight: 42)
                }
            }
            .padding(.horizontal, 10)
            .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.lineSoft, lineWidth: 1))
        }
    }

    private func fieldTitle(_ field: SuggestionField) -> String {
        switch field {
        case .priority: "Priority"
        case .severity: "Severity"
        case .labels: "Labels"
        case .team: "Team"
        case .spam: "Spam"
        }
    }

    @ViewBuilder private func value(_ suggestion: AISuggestion) -> some View {
        switch suggestion.field {
        case .priority:
            if let priority = suggestion.value.string.flatMap(Priority.init(rawValue:)) { PriorityPill(priority: priority) }
        case .severity:
            if let severity = suggestion.value.string.flatMap(Severity.init(rawValue:)) { SeverityChip(severity: severity) }
        case .team:
            Text(suggestion.value.string.flatMap(teamName) ?? "A team").font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg)
        case .labels:
            let names = suggestion.value.array?.compactMap(\.string) ?? suggestion.value.string.map { [$0] } ?? []
            HStack(spacing: 4) { ForEach(names.prefix(2), id: \.self) { LabelChip(name: $0) } }
        case .spam:
            TonePill(text: "Mark as spam", tone: .red)
        }
    }
}

/// Ollie's suggested reply to the last customer message, waiting to be sent, edited or discarded.
struct TicketSuggestedReplyCard: View {
    let suggestion: SuggestedReply
    let canWrite: Bool
    var sending = false
    let onSend: () -> Void
    let onEdit: () -> Void
    let onDiscard: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 8) {
                OllieMark(size: 20, state: sending ? .thinking : .idle)
                Text("Ollie drafted a reply").font(ODFont.sans(14, weight: .semibold)).foregroundStyle(Theme.fg)
                Spacer()
                if let created = suggestion.createdAt {
                    Text(Format.relShort(created)).font(ODFont.mono(10.5)).foregroundStyle(Theme.fg4)
                }
            }
            Text(TicketRichText.attributed(suggestion.bodyHTML, style: TicketRichText.Style(font: ODFont.sans(14.5))))
                .lineSpacing(2.5)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
            if canWrite {
                HStack(spacing: 8) {
                    Button(action: onSend) { ButtonLabel(title: "Send", busy: sending, symbol: "paperplane.fill") }
                        .buttonStyle(InkButtonStyle(fullWidth: false, height: 34))
                        .disabled(sending)
                    Button(action: onEdit) { Text("Edit") }
                        .buttonStyle(OutlineButtonStyle(fullWidth: false, height: 34))
                    Spacer()
                    Button("Discard", action: onDiscard)
                        .font(ODFont.sans(13.5, weight: .medium))
                        .foregroundStyle(Theme.fg3)
                }
            }
        }
        .padding(14)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Tone.violet.solid.opacity(0.45), style: StrokeStyle(lineWidth: 1.2, dash: [5, 3])))
    }
}

/// Ask Ollie anything about the ticket; the answer cites the articles it used.
struct TicketAskSheet: View {
    let store: TicketStore
    let contactName: String
    let onInsert: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var question = ""
    @State private var asked = ""
    @State private var answer: TicketAnswer?
    @State private var error: String?
    @FocusState private var focused: Bool

    private var starters: [String] {
        ["What has \(contactName) already tried?", "What is the customer waiting for?", "Has this come up before?"]
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    HStack(alignment: .bottom, spacing: 8) {
                        TextField("Ask about this ticket", text: $question, axis: .vertical)
                            .font(ODFont.sans(15.5))
                            .lineLimit(1 ... 5)
                            .focused($focused)
                            .submitLabel(.send)
                            .onSubmit(ask)
                            .padding(.horizontal, 12)
                            .padding(.vertical, 10)
                        Button(action: ask) {
                            Image(systemName: "arrow.up")
                                .font(.system(size: 14, weight: .bold))
                                .foregroundStyle(Theme.inverseFg)
                                .frame(width: 32, height: 32)
                                .background(Theme.inverse.opacity(question.trimmingCharacters(in: .whitespaces).isEmpty ? 0.3 : 1), in: Circle())
                        }
                        .buttonStyle(TapScaleStyle(scale: 0.9))
                        .disabled(question.trimmingCharacters(in: .whitespaces).isEmpty || store.asking)
                        .padding(6)
                        .accessibilityLabel("Ask")
                    }
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control + 2, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control + 2, style: .continuous).strokeBorder(Tone.violet.line, lineWidth: 1))

                    if answer == nil, !store.asking {
                        FlowLayout(spacing: 7) {
                            ForEach(starters, id: \.self) { starter in
                                Button(starter) {
                                    question = starter
                                    ask()
                                }
                                .buttonStyle(ChipButtonStyle())
                            }
                        }
                    }

                    if store.asking {
                        HStack(spacing: 10) {
                            OllieMark(size: 24, state: .thinking)
                            Text("Reading the ticket\u{2026}").font(ODFont.sans(14)).foregroundStyle(Tone.violet.text)
                        }
                        .padding(.top, 6)
                    }
                    if let error { FormError(text: error) }
                    if let answer { answerView(answer) }
                }
                .padding(16)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Ask Ollie")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Done") { dismiss() } }
            }
            .onAppear { focused = answer == nil }
        }
    }

    private func answerView(_ answer: TicketAnswer) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(spacing: 8) {
                OllieMark(size: 20)
                Text(asked).font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg3).lineLimit(2)
            }
            Text(TicketRichText.attributed(answer.answerHTML, style: TicketRichText.Style(font: ODFont.sans(15))))
                .lineSpacing(3)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
            if !answer.sources.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    Text("From the knowledge base").font(ODFont.sans(11.5, weight: .medium)).foregroundStyle(Theme.fg3)
                    ForEach(answer.sources) { source in
                        if let url = URL(string: source.url), ["https", "http"].contains(url.scheme?.lowercased() ?? "") {
                            Link(destination: url) {
                                HStack(spacing: 6) {
                                    Image(systemName: "book.closed").font(.system(size: 11))
                                    Text(source.title).font(ODFont.sans(13, weight: .medium)).lineLimit(1)
                                    Image(systemName: "arrow.up.right").font(.system(size: 9, weight: .bold))
                                }
                                .foregroundStyle(Tone.blue.text)
                            }
                        } else {
                            Text(source.title).font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg2)
                        }
                    }
                }
            }
            HStack {
                Button {
                    onInsert(HTMLText.plain(from: answer.answerHTML))
                    dismiss()
                } label: { ButtonLabel(title: "Use in a note", symbol: "lock") }
                    .buttonStyle(OutlineButtonStyle(fullWidth: false, height: 36))
                Spacer()
            }
        }
        .padding(14)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }

    private func ask() {
        let text = question.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty, !store.asking else { return }
        focused = false
        error = nil
        asked = text
        question = ""
        Task {
            do {
                let result = try await store.ask(text)
                withAnimation(Motion.smooth) { answer = result }
                Haptics.success()
            } catch {
                self.error = error.userMessage
            }
        }
    }
}
