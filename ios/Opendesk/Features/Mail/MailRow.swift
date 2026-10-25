import SwiftUI

/// One conversation in the list: who, the subject and a line of it, when, and what is
/// attached to it (labels, files, sharing, assignees, team comments). Unread rows are bold
/// with a dot; a star sits under the time.
struct MailThreadRow: View {
    let thread: MailThread
    let own: Set<String>
    var account: MailAccount?
    var showAccount = false
    var labels: [MailFolder] = []
    var sentBox = false
    var pulse: Date?
    var onStar: (() -> Void)?

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            avatar
            VStack(alignment: .leading, spacing: 2.5) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(MailNames.line(thread, own: own, sentBox: sentBox))
                        .font(ODFont.sans(15, weight: thread.unread ? .semibold : .regular))
                        .foregroundStyle(thread.unread ? Theme.fg : Theme.fg2)
                        .lineLimit(1)
                    if thread.messageCount > 1 {
                        Text("\(thread.messageCount)")
                            .font(ODFont.sans(12.5, weight: .medium))
                            .foregroundStyle(Theme.fg4)
                            .monospacedDigit()
                    }
                    Spacer(minLength: 4)
                    if thread.hasAttachments {
                        Image(systemName: "paperclip")
                            .font(.system(size: 11.5, weight: .semibold))
                            .foregroundStyle(Theme.fg4)
                            .accessibilityLabel("Has attachments")
                    }
                    Text(Format.relShort(thread.lastMessageAt))
                        .font(ODFont.sans(12.5, weight: thread.unread ? .semibold : .regular))
                        .foregroundStyle(thread.unread ? Theme.fg : Theme.fg3)
                        .monospacedDigit()
                }
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(thread.displaySubject)
                        .font(ODFont.sans(14, weight: thread.unread ? .semibold : .medium))
                        .foregroundStyle(thread.unread ? Theme.fg : Theme.fg2)
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    star
                }
                Text(thread.snippet.isEmpty ? " " : thread.snippet)
                    .font(ODFont.sans(13.5))
                    .foregroundStyle(Theme.fg3)
                    .lineLimit(1)
                    .padding(.trailing, 22)
                if hasMarks { marks.padding(.top, 4) }
            }
        }
        .padding(.leading, 14)
        .padding(.trailing, 14)
        .padding(.vertical, 11)
        .overlay(alignment: .leading) {
            if thread.unread {
                Circle().fill(Tone.blue.solid).frame(width: 7, height: 7).offset(x: 4).accessibilityHidden(true)
            }
        }
        .overlay(alignment: .bottom) { Hairline(inset: 62) }
        .contentShape(Rectangle())
        .livePulse(pulse ?? .distantPast)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(accessibilityText)
    }

    private var lead: MailAddress? { MailNames.lead(thread, own: own) }

    private var avatar: some View {
        let person = lead
        return Avatar(name: person?.display ?? thread.displaySubject, seed: person?.email ?? thread.id, size: 38)
            .overlay(alignment: .bottomTrailing) {
                if showAccount, let account {
                    Circle().fill(Theme.surface).frame(width: 13, height: 13)
                        .overlay(MailAccountDot(color: account.color, size: 8))
                        .offset(x: 2, y: 2)
                        .accessibilityHidden(true)
                }
            }
            .padding(.top, 1)
    }

    @ViewBuilder
    private var star: some View {
        if let onStar {
            Button(action: onStar) {
                Image(systemName: thread.starred ? "star.fill" : "star")
                    .font(.system(size: 14, weight: .medium))
                    .foregroundStyle(thread.starred ? Tone.amber.solid : Theme.fg4.opacity(0.7))
                    .frame(width: 22, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(TapScaleStyle(scale: 0.8))
            .accessibilityLabel(thread.starred ? "Unstar" : "Star")
        } else if thread.starred {
            Image(systemName: "star.fill").font(.system(size: 13)).foregroundStyle(Tone.amber.solid)
        }
    }

    private var hasMarks: Bool {
        !labels.isEmpty || thread.shared || !thread.assignees.isEmpty || thread.commentCount > 0 || thread.toAddress != nil
    }

    private var marks: some View {
        HStack(spacing: 5) {
            if let alias = thread.toAddress, let account, alias.lowercased() != account.email.lowercased() {
                MarkPill(symbol: "at", text: alias)
            }
            ForEach(labels.prefix(2)) { label in
                MailLabelChip(name: label.name, color: label.color)
            }
            if labels.count > 2 {
                Text("+\(labels.count - 2)").font(ODFont.sans(11.5, weight: .medium)).foregroundStyle(Theme.fg3)
            }
            if thread.shared {
                MarkPill(symbol: "person.2.fill", text: "Shared", tone: .teal)
            }
            if thread.commentCount > 0 {
                MarkPill(symbol: "text.bubble.fill", text: "\(thread.commentCount)", tone: .amber)
            }
            Spacer(minLength: 0)
            if !thread.assignees.isEmpty {
                HStack(spacing: -5) {
                    ForEach(thread.assignees.prefix(3)) { person in
                        Avatar(name: person.name, url: person.avatarURL, seed: person.id, size: 18, ring: Theme.surface)
                    }
                }
                .accessibilityLabel("Assigned to " + thread.assignees.map(\.name).joined(separator: ", "))
            }
        }
        .lineLimit(1)
    }

    private var accessibilityText: String {
        var parts = [MailNames.line(thread, own: own, sentBox: sentBox), thread.displaySubject, thread.snippet, Format.relLong(thread.lastMessageAt)]
        if thread.unread { parts.insert("Unread", at: 0) }
        if thread.starred { parts.append("Starred") }
        if thread.hasAttachments { parts.append("Has attachments") }
        if thread.shared { parts.append("Shared") }
        if thread.commentCount > 0 { parts.append(Format.plural(thread.commentCount, "comment")) }
        return parts.joined(separator: ", ")
    }
}

/// A small mark on a row: an icon and a few words on a tinted wash.
struct MarkPill: View {
    let symbol: String
    let text: String
    var tone: Tone = .gray

    var body: some View {
        HStack(spacing: 4) {
            Image(systemName: symbol).font(.system(size: 9, weight: .bold))
            Text(text).font(ODFont.sans(11.5, weight: .medium)).lineLimit(1)
        }
        .foregroundStyle(tone == .gray ? Theme.fg2 : tone.text)
        .padding(.horizontal, 6)
        .frame(height: 20)
        .background(tone == .gray ? Theme.sunken : tone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous).strokeBorder(tone == .gray ? Theme.line : tone.line, lineWidth: 1))
    }
}
