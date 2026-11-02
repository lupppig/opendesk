import SwiftUI

/// How the ticket list shows who a ticket is assigned to, on this iPhone (the web keeps it
/// per browser): their photo (the default), their first name, or nothing. Teammates on a
/// ticket right now always show live in green, whatever this says.
enum AssigneeDisplay: String, CaseIterable, Identifiable, Sendable {
    case photo, name, none

    var id: String { rawValue }

    var title: String {
        switch self {
        case .photo: "Photo"
        case .name: "First name"
        case .none: "Nothing"
        }
    }

    static let key = "inbox.list.assignee"

    /// The app's defaults (a suite of their own in demo mode).
    static var store: UserDefaults { InboxDefaults.store }
}

/// One ticket in the list, as the web's compact row: status and priority in
/// a left column, contact and time on top, subject, then the preview and
/// pills. Awaiting a reply reads bolder; a waiting chat washes red. Who is on it
/// right now shows live in green; who it is assigned to sits still at the top's end.
struct TicketRow: View {
    let ticket: Ticket
    var viewers: [PresenceViewer] = []
    var agentOn = false
    var hideAssignee = false
    /// The bot streaming a reply on this chat right now, if any.
    var replyingBot: String?
    @AppStorage(AssigneeDisplay.key, store: AssigneeDisplay.store) private var assigneeDisplay: AssigneeDisplay = .photo

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { context in
            content(now: AppClock.now(at: context.date))
        }
    }

    private func content(now: Date) -> some View {
        let wait = ChatWait.of(ticket, agentOn: agentOn, now: now)
        let urgent = wait?.state == .urgent && ticket.assignee == nil
        let unread = ticket.awaitingReply || wait != nil
        let sla = SLAState.of(ticket, now: now)
        let aiSet = ticket.custom["ai_decisions"]?["fields"]?["priority"]?["applied"]?.bool ?? (ticket.custom["ai_triaged_at"] != nil)

        return HStack(alignment: .top, spacing: 11) {
            VStack(spacing: 8) {
                StatusIcon(status: ticket.status, size: 15)
                ZStack(alignment: .topTrailing) {
                    PriorityIcon(priority: ticket.priority, size: 15)
                    if aiSet {
                        OllieMark(size: 9, eyes: false)
                            .offset(x: 5, y: -5)
                            .accessibilityLabel("Set by Ollie")
                    }
                }
            }
            .padding(.top, 2)

            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(ticket.contactName)
                        .font(ODFont.sans(14, weight: unread ? .semibold : .regular))
                        .foregroundStyle(unread ? Theme.fg : Theme.fg2)
                        .lineLimit(1)
                    if ticket.channel == .chat {
                        LiveChatBadge(urgent: urgent)
                    } else if ticket.channel != .email {
                        Image(systemName: ticket.channel.symbol).font(.system(size: 10)).foregroundStyle(Theme.fg3)
                    }
                    Spacer(minLength: 4)
                    if !hideAssignee, let assignee = ticket.assignee {
                        AssigneeMark(assignee: assignee, display: assigneeDisplay)
                    }
                    Text(Format.relShort(ticket.lastMessageAt, now: now))
                        .font(ODFont.mono(11))
                        .monospacedDigit()
                        .foregroundStyle(Theme.fg3)
                }

                HStack(spacing: 6) {
                    if let severity = ticket.severity { SeverityChip(severity: severity) }
                    Text(ticket.displaySubject)
                        .font(ODFont.sans(14, weight: unread ? .medium : .regular))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                }

                if !ticket.lastMessagePreview.isEmpty {
                    Text(ticket.lastMessagePreview)
                        .font(ODFont.sans(13))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(2)
                }

                HStack(spacing: 5) {
                    TicketNumber(number: ticket.number)
                    if !viewers.isEmpty {
                        LiveViewersPill(viewers: viewers)
                            .transition(.scale(scale: 0.9).combined(with: .opacity))
                    }
                    if let wait { WaitPill(wait: wait, compact: true, botName: replyingBot) }
                    if let sla, sla.kind != .onTrack { SLAPill(state: sla, now: now) }
                    if ticket.isSnoozed, let until = ticket.snoozedUntil {
                        TonePill(text: Calendar.current.isDate(until, inSameDayAs: now) ? "Until \(Format.clock(until))" : "Until \(Format.shortDate(until, now: now))", tone: .violet) {
                            Image(systemName: "alarm").font(.system(size: 9))
                        }
                    }
                    ForEach(ticket.tags.prefix(2)) { tag in
                        LabelChip(name: tag.name, color: tag.color)
                    }
                    if ticket.tags.count > 2 {
                        Text("+\(ticket.tags.count - 2)").font(ODFont.mono(10.5)).foregroundStyle(Theme.fg3)
                    }
                }
                .padding(.top, 3)
            }
        }
        .padding(.leading, 14)
        .padding(.trailing, 14)
        .padding(.vertical, 12)
        .background {
            if urgent {
                HStack(spacing: 0) {
                    Rectangle().fill(Tone.red.solid).frame(width: 2.5)
                    Tone.red.soft
                }
            }
        }
        .overlay(alignment: .bottom) { Hairline(inset: 40) }
        .livePulse(ticket.updatedAt)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
        .accessibilityHint(unread ? "Waiting for a reply" : "")
    }
}

/// Teammates on the ticket right now, in a green pill with their photos and what they are
/// doing: "Ana viewing", "Ana replying…", "2 viewing", "Ana replying… +1".
struct LiveViewersPill: View {
    let viewers: [PresenceViewer]

    var body: some View {
        HStack(spacing: 4) {
            HStack(spacing: -4) {
                ForEach(viewers.prefix(3)) { viewer in
                    Avatar(name: viewer.name, url: viewer.avatar, seed: viewer.userID, size: 13)
                        .padding(1)
                        .background(Circle().fill(Tone.green.solid))
                }
            }
            Text(label)
                .font(ODFont.sans(11, weight: .medium))
                .lineLimit(1)
                .frame(maxWidth: 132, alignment: .leading)
                .fixedSize(horizontal: true, vertical: false)
        }
        .foregroundStyle(Tone.green.text)
        .padding(.leading, 3)
        .padding(.trailing, 7)
        .frame(height: 20)
        .background(Tone.green.soft, in: Capsule())
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(viewers.map { "\($0.name) is \($0.state == .replying ? "replying" : "viewing")" }.joined(separator: ", "))
    }

    private var label: String {
        let replying = viewers.first { $0.state == .replying }
        if viewers.count == 1 { return Self.first(viewers[0].name) + (replying == nil ? " viewing" : " replying\u{2026}") }
        if let replying { return Self.first(replying.name) + " replying\u{2026} +\(viewers.count - 1)" }
        return "\(viewers.count) viewing"
    }

    static func first(_ name: String) -> String {
        name.split(whereSeparator: \.isWhitespace).first.map(String.init) ?? name
    }
}

/// Who the ticket is assigned to, as Profile → Appearance chose: their photo, their first
/// name in a grey pill, or nothing.
struct AssigneeMark: View {
    let assignee: UserRef
    let display: AssigneeDisplay

    var body: some View {
        switch display {
        case .photo:
            Avatar(name: assignee.displayName, url: assignee.avatarURL, seed: assignee.id, size: 18)
                .accessibilityHidden(false)
                .accessibilityLabel("Assigned to \(assignee.displayName)")
        case .name:
            Text(LiveViewersPill.first(assignee.displayName))
                .font(ODFont.sans(11, weight: .medium))
                .foregroundStyle(Theme.fg2)
                .lineLimit(1)
                .padding(.horizontal, 7)
                .frame(maxWidth: 96)
                .frame(height: 19)
                .background(Theme.active, in: Capsule())
                .fixedSize(horizontal: true, vertical: false)
                .accessibilityLabel("Assigned to \(assignee.displayName)")
        case .none:
            EmptyView()
        }
    }
}
