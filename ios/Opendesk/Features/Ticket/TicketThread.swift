import SwiftUI

/// The conversation in order: day separators, message cards, and runs of activity lines.
struct TicketThreadList<MessageView: View>: View {
    let items: [ThreadItem]
    let phrasing: ActivityPhrasing
    @ViewBuilder let message: (Message) -> MessageView

    private enum Block: Identifiable {
        case day(Date)
        case message(Message)
        case activities([Activity])

        var id: String {
            switch self {
            case let .day(date): "d-\(Int(date.timeIntervalSince1970))"
            case let .message(message): "m-" + message.id
            case let .activities(list): "a-" + (list.first?.id ?? "")
            }
        }
    }

    private var blocks: [Block] {
        var out: [Block] = []
        var run: [Activity] = []
        var day: Date?
        let calendar = Calendar.current
        func flush() {
            if !run.isEmpty { out.append(.activities(run)) }
            run = []
        }
        for item in items {
            let start = calendar.startOfDay(for: item.date)
            if day != start {
                flush()
                out.append(.day(start))
                day = start
            }
            switch item {
            case let .message(value):
                flush()
                out.append(.message(value))
            case let .activity(value):
                run.append(value)
            }
        }
        flush()
        return out
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            ForEach(blocks) { block in
                switch block {
                case let .day(date):
                    TicketDaySeparator(date: date)
                case let .message(value):
                    message(value)
                        .id("m-" + value.id)
                        .transition(.asymmetric(insertion: .opacity.combined(with: .move(edge: .bottom)), removal: .opacity))
                case let .activities(list):
                    VStack(alignment: .leading, spacing: 0) {
                        ForEach(list) { activity in
                            TicketActivityRow(activity: activity, sentence: phrasing.sentence(activity), symbol: phrasing.symbol(activity), tone: phrasing.tone(activity))
                        }
                    }
                    .padding(.horizontal, 4)
                }
            }
        }
    }
}

/// "Priya is typing" or the customer typing, as three soft dots.
struct TicketTypingRow: View {
    let text: String
    var customer = false

    var body: some View {
        HStack(spacing: 8) {
            TypingDots(color: customer ? Theme.fg3 : Tone.amber.solid)
                .padding(.horizontal, 9)
                .frame(height: 22)
                .background(customer ? Theme.surface : Tone.amber.soft, in: Capsule())
                .overlay(Capsule().strokeBorder(customer ? Theme.line : Tone.amber.line, lineWidth: 1))
            Text(text)
                .font(ODFont.sans(12.5))
                .foregroundStyle(Theme.fg3)
        }
        .padding(.horizontal, 4)
        .transition(.opacity)
        .accessibilityElement(children: .combine)
    }
}

/// Three dots that rise in turn; still under Reduce Motion.
struct TypingDots: View {
    var color: Color = Theme.fg3
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        TimelineView(.animation(minimumInterval: 1 / 20, paused: reduceMotion)) { context in
            let t = context.date.timeIntervalSinceReferenceDate
            HStack(spacing: 3) {
                ForEach(0 ..< 3, id: \.self) { index in
                    let phase = reduceMotion ? 0 : max(0, sin((t * 2 * .pi / 1.1) - Double(index) * 0.7))
                    Circle()
                        .fill(color)
                        .frame(width: 5, height: 5)
                        .opacity(0.45 + 0.55 * phase)
                        .offset(y: -2.5 * phase)
                }
            }
        }
        .accessibilityHidden(true)
    }
}

/// A customer-facing bot writing a chat reply right now, as it streams.
struct TicketLiveReplyCard: View {
    let live: LiveReply
    var bot: BotRef?

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 10) {
                if let bot, bot.nativeEmoji != nil || !bot.avatarURL.isEmpty {
                    BotAvatar(bot: bot, size: 30)
                } else {
                    OllieMark(size: 30, state: .thinking)
                }
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(live.bot.isEmpty ? "Ollie" : live.bot).font(ODFont.sans(14, weight: .semibold)).foregroundStyle(Theme.fg)
                        AITag()
                    }
                    HStack(spacing: 5) {
                        PingDot(color: Tone.violet.solid, size: 5)
                        Text(live.runningTool.map(toolLabel) ?? (live.handoff ? "Handing this to the team" : "Replying in the chat"))
                            .font(ODFont.sans(12))
                            .foregroundStyle(Tone.violet.text)
                    }
                }
                Spacer()
            }
            if !live.displayText.isEmpty {
                Text((try? AttributedString(markdown: live.displayText, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace))) ?? AttributedString(live.displayText))
                    .font(ODFont.body)
                    .foregroundStyle(Theme.fg)
                    .lineSpacing(3)
                    .fixedSize(horizontal: false, vertical: true)
                    .contentTransition(.opacity)
            } else {
                TypingDots(color: Tone.violet.solid)
            }
        }
        .padding(14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(alignment: .leading) { Rectangle().fill(Tone.violet.solid).frame(width: 3) }
        .clipShape(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Tone.violet.line, lineWidth: 1))
        .accessibilityElement(children: .combine)
    }

    private func toolLabel(_ tool: String) -> String {
        switch tool {
        case "search_kb", "search_articles", "kb_search": "Searching the knowledge base"
        case "handoff": "Handing this to the team"
        default: "Looking something up"
        }
    }
}

/// What the customer said when they rated the conversation.
struct TicketCSATCard: View {
    let csat: CSAT
    let contactName: String

    var body: some View {
        let tone: Tone = csat.score >= 4 ? .green : csat.score == 3 ? .amber : .red
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: csat.score >= 4 ? "face.smiling" : csat.score == 3 ? "face.dashed" : "hand.thumbsdown")
                .font(.system(size: 17, weight: .medium))
                .foregroundStyle(tone.text)
                .frame(width: 34, height: 34)
                .background(tone.soft, in: Circle())
            VStack(alignment: .leading, spacing: 5) {
                HStack(spacing: 8) {
                    Text("\(contactName) rated this").font(ODFont.sans(13.5, weight: .medium)).foregroundStyle(Theme.fg)
                    HStack(spacing: 2) {
                        ForEach(1 ... 5, id: \.self) { index in
                            Image(systemName: index <= csat.score ? "star.fill" : "star")
                                .font(.system(size: 10.5))
                                .foregroundStyle(index <= csat.score ? Tone.amber.solid : Theme.fg4)
                        }
                    }
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel("\(csat.score) out of 5")
                }
                if let comment = csat.comment, !comment.isEmpty {
                    Text("\u{201C}\(comment)\u{201D}")
                        .font(ODFont.sans(14))
                        .foregroundStyle(Theme.fg2)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(14)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}
