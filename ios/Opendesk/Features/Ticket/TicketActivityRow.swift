import SwiftUI

/// A compact line between messages that says what changed, in a human sentence
/// (web `activity.*`): "Maya marked this Solved", "Ollie triaged this".
struct TicketActivityRow: View {
    let activity: Activity
    let sentence: AttributedString
    let symbol: String
    var tone: Tone?

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Group {
                if activity.isAI {
                    OllieMark(size: 13, eyes: false)
                } else {
                    Image(systemName: symbol)
                        .font(.system(size: 10.5, weight: .semibold))
                        .foregroundStyle(tone?.text ?? Theme.fg3)
                }
            }
            .frame(width: 18, height: 18)
            .background(Circle().fill(tone?.soft ?? (activity.isAI ? Tone.violet.soft : Theme.surface)))
            .overlay(Circle().strokeBorder(tone?.line ?? (activity.isAI ? Tone.violet.line : Theme.line), lineWidth: 1))
            .alignmentGuide(.firstTextBaseline) { $0[VerticalAlignment.center] + 4 }

            Text(sentence)
                .font(ODFont.sans(12.5))
                .foregroundStyle(Theme.fg3)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 6)
            Text(Format.clock(activity.createdAt))
                .font(ODFont.mono(10.5))
                .foregroundStyle(Theme.fg4)
        }
        .padding(.horizontal, 6)
        .padding(.vertical, 4)
        .accessibilityElement(children: .combine)
    }
}

/// The sentence, glyph and tone of an activity.
@MainActor
struct ActivityPhrasing {
    let directory: DirectoryStore
    let me: String?
    let contactName: String

    func symbol(_ activity: Activity) -> String {
        switch activity.type {
        case "status_changed": "circle.dashed"
        case "priority_changed": "chart.bar"
        case "severity_changed": "exclamationmark.triangle"
        case "assigned": "person.crop.circle"
        case "team_changed": "person.2"
        case "tagged", "untagged": "tag"
        case "sla_breached": "clock.badge.exclamationmark"
        case "merged": "arrow.triangle.merge"
        case "csat_received": "star"
        case "linked", "unlinked": "link"
        case "contact_changed": "person.text.rectangle"
        case "thread_started", "thread_closed", "thread_reopened": "arrow.triangle.branch"
        case "subject_changed": "textformat"
        case "snoozed": "alarm"
        case "spam_marked", "spam": "xmark.octagon"
        case "ai_handoff": "hand.raised"
        case "reply_retracted": "lock"
        case "note_deleted", "message_deleted", "message_purged": "trash"
        case "message_restored": "arrow.uturn.backward"
        default: "circle"
        }
    }

    func tone(_ activity: Activity) -> Tone? {
        switch activity.type {
        case "sla_breached": return .red
        case "csat_received":
            let score = activity.data["score"]?.int ?? 0
            return score >= 4 ? .green : score == 3 ? .amber : .red
        case "ai_handoff": return .amber
        case "status_changed":
            return activity.data["to"]?.string.flatMap(TicketStatus.init(rawValue:)).map(\.tone)
        default: return nil
        }
    }

    private func who(_ activity: Activity) -> String {
        switch activity.actor.type {
        case "ai": return "Ollie"
        case "contact": return contactName
        case "automation":
            if let name = activity.data["automation"]?["name"]?.string, !name.isEmpty { return "Automation \u{201C}\(name)\u{201D}" }
            return "An automation"
        case "system", "api": return "opendesk"
        default:
            if let user = activity.actor.user {
                return user.id == me ? "You" : user.displayName
            }
            return "Someone"
        }
    }

    private func name(of value: JSONValue?) -> String? {
        guard let value, !value.isNull else { return nil }
        if let text = value.string { return text }
        if let name = value["name"]?.string, !name.isEmpty { return name }
        return value["email"]?.string
    }

    /// Builds "<b>who</b> did <b>what</b>" from parts; `strong` parts are medium and in ink.
    private func line(_ parts: [(String, Bool)]) -> AttributedString {
        var out = AttributedString()
        for (text, strong) in parts {
            var piece = AttributedString(text)
            if strong {
                piece.font = ODFont.sans(12.5, weight: .medium)
                piece.foregroundColor = Theme.fg2
            }
            out.append(piece)
        }
        return out
    }

    func sentence(_ activity: Activity) -> AttributedString {
        let who = who(activity)
        let data = activity.data
        switch activity.type {
        case "status_changed":
            let to = data["to"]?.string.flatMap(TicketStatus.init(rawValue:))?.title ?? "updated"
            switch data["reason"]?.string {
            case "sender_unverified": return line([("Status kept at ", false), (to, true), (": the reply failed sender verification", false)])
            case "thread_replied": return line([("An answer in a side conversation opened this ticket again", false)])
            default: return line([(who, true), (" marked this ", false), (to, true)])
            }
        case "priority_changed":
            let to = data["to"]?.string.flatMap(Priority.init(rawValue:))?.title ?? "updated"
            return line([(who, true), (" set priority to ", false), (to, true)])
        case "severity_changed":
            guard let to = data["to"]?.string.flatMap(Severity.init(rawValue:)) else { return line([(who, true), (" cleared the severity", false)]) }
            return line([(who, true), (" set severity to ", false), (to.title, true)])
        case "assigned":
            guard let to = data["to"], !to.isNull else { return line([(who, true), (" unassigned this ticket", false)]) }
            let toID = to["id"]?.string
            if let actor = activity.actor.user, actor.id == toID { return line([(who, true), (" took this ticket", false)]) }
            let target = toID == me ? "you" : (name(of: to) ?? "someone")
            switch data["via"]?.string {
            case "round_robin", "load_balanced":
                return line([("Assigned to ", false), (target == "you" ? "you" : target, true), (" by rotation", false)])
            default:
                return line([(who, true), (" assigned this to ", false), (target, true)])
            }
        case "team_changed":
            guard let team = name(of: data["to"]) else { return line([(who, true), (" removed the team", false)]) }
            if let from = name(of: data["from"]) { return line([(who, true), (" moved this from ", false), (from, true), (" to ", false), (team, true)]) }
            return line([(who, true), (" moved this to ", false), (team, true)])
        case "tagged":
            return line([(who, true), (" added ", false), (data["tag"]?.string ?? "a label", true)])
        case "untagged":
            return line([(who, true), (" removed ", false), (data["tag"]?.string ?? "a label", true)])
        case "sla_breached":
            return line([(data["kind"]?.string == "first_response" ? "First response target was missed" : "Resolution target was missed", false)])
        case "merged":
            if let number = data["into_number"]?.int { return line([(who, true), (" merged this into ", false), ("#\(number)", true)]) }
            if let number = data["from_number"]?.int { return line([(who, true), (" merged ", false), ("#\(number)", true), (" into this ticket", false)]) }
            return line([(who, true), (" merged this into another ticket", false)])
        case "csat_received":
            return line([("The customer rated this ", false), ("\(data["score"]?.int ?? 0)/5", true)])
        case "linked":
            return line([(who, true), (" linked ", false), (data["title"]?.string ?? data["url"]?.string ?? "a record", true)])
        case "unlinked":
            return line([(who, true), (" removed the link to ", false), (data["title"]?.string ?? data["url"]?.string ?? "a record", true)])
        case "contact_changed":
            return line([(who, true), (" changed the contact from ", false), (name(of: data["from"]) ?? "no contact", true), (" to ", false), (name(of: data["to"]) ?? "no contact", true)])
        case "thread_started":
            let to = data["to"]?.array?.compactMap(\.string).joined(separator: ", ") ?? ""
            return line([(who, true), (" started the side conversation ", false), (data["subject"]?.string ?? "", true)] + (to.isEmpty ? [] : [(" with ", false), (to, false)]))
        case "thread_closed":
            return line([(who, true), (" closed the side conversation ", false), (data["subject"]?.string ?? "", true)])
        case "thread_reopened":
            return line([(who, true), (" reopened the side conversation ", false), (data["subject"]?.string ?? "", true)])
        case "subject_changed":
            return line([(who, true), (" changed the subject", false)])
        case "snoozed":
            if let raw = data["until"]?.string, let date = try? Date.ISO8601FormatStyle().parse(raw) {
                return line([(who, true), (" snoozed this until ", false), (Format.shortDate(date) + ", " + Format.clock(date), true)])
            }
            return line([(who, true), (" snoozed this", false)])
        case "unsnoozed": return line([("Snooze ended", false)])
        case "spam_marked", "spam":
            return line([(who, true), (data["spam"]?.bool == false ? " moved this out of spam" : " marked this as spam", false)])
        case "ai_triaged":
            if let category = data["category"]?.string, !category.isEmpty {
                return line([(who, true), (" triaged this as ", false), (category.replacingOccurrences(of: "_", with: " ").capitalizedFirst, true)])
            }
            return line([(who, true), (" triaged this", false)])
        case "ai_answered": return line([(who, true), (" answered from the knowledge base", false)])
        case "ai_handoff":
            return activity.actor.type == "contact" ? line([("The customer asked for a person", false)]) : line([(who, true), (" handed this to the team", false)])
        case "reply_retracted": return line([(who, true), (" turned a sent reply into an internal note", false)])
        case "note_deleted": return line([(who, true), (" deleted an internal note", false)])
        case "message_deleted": return line([(who, true), (" deleted a message", false)])
        case "message_restored": return line([(who, true), (" restored a deleted message", false)])
        case "message_purged": return line([(who, true), (" permanently deleted a message\u{2019}s content", false)])
        default:
            return line([(who, true), (" updated this ticket", false)])
        }
    }
}

extension String {
    var capitalizedFirst: String { prefix(1).uppercased() + dropFirst() }
}
