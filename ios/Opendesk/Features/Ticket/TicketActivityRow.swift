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
        case "subject_changed", "alias_changed": "textformat"
        case "snoozed": "alarm"
        case "spam_marked", "spam": "xmark.octagon"
        case "ai_handoff": "hand.raised"
        case "reply_retracted": "lock"
        case "note_deleted", "message_deleted", "message_purged": "trash"
        case "message_restored": "arrow.uturn.backward"
        case "call": "phone"
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
        case "alias_changed":
            return line([(who, true), (" changed the ticket reference", false)])
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

// MARK: - Calls

/// A call on the ticket (contracts/phone.md "Timeline", `CallData`), read from its
/// activity's data. Only https links from the provider are kept.
struct TicketCall {
    struct Comment: Identifiable {
        var id: String
        var text: String
        var author: String
    }

    var provider: String
    var outbound: Bool
    var stage: String
    var outcome: String
    var missedReason: String
    var number: String
    var line: String
    var callerName: String
    var agentName: String
    var startedAt: Date?
    var answeredAt: Date?
    var endedAt: Date?
    var duration: Double
    var recording: Bool
    var voicemail: Bool
    var assetURL: URL?
    var recordingURL: URL?
    var voicemailURL: URL?
    var linksExpireAt: Date?
    var tags: [String]
    var comments: [Comment]
    var summary: String
    var transcript: String
    var outsideHours: Bool

    init(_ data: [String: JSONValue]) {
        func text(_ key: String) -> String { data[key]?.string ?? "" }
        func date(_ key: String) -> Date? { data[key]?.string.flatMap(Self.date) }
        provider = text("provider")
        outbound = text("direction") == "outbound"
        stage = text("stage")
        outcome = text("outcome")
        missedReason = text("missed_reason")
        number = text("number")
        line = text("line")
        callerName = text("caller_name")
        agentName = data["agent"]?["name"]?.string.flatMap { $0.isEmpty ? nil : $0 } ?? text("agent_name")
        startedAt = date("started_at")
        answeredAt = date("answered_at")
        endedAt = date("ended_at")
        duration = data["duration"]?.double ?? 0
        recording = data["recording"]?.bool ?? false
        voicemail = data["voicemail"]?.bool ?? false
        assetURL = Self.https(text("asset_url"))
        recordingURL = Self.https(text("recording_url"))
        voicemailURL = Self.https(text("voicemail_url"))
        linksExpireAt = date("links_expire_at")
        tags = data["tags"]?.array?.compactMap(\.string).filter { !$0.isEmpty } ?? []
        comments = (data["comments"]?.array ?? []).enumerated().compactMap { index, value in
            guard let body = value["text"]?.string, !body.isEmpty else { return nil }
            return Comment(id: value["id"]?.string ?? "\(index)", text: body, author: value["author"]?.string ?? "")
        }
        summary = text("summary")
        transcript = text("transcript")
        outsideHours = data["outside_hours"]?.bool ?? false
    }

    var live: Bool { stage != "ended" }

    var providerName: String { provider == "aircall" ? "Aircall" : provider.capitalizedFirst }

    /// How long the customer and the team spoke: from the answer to the end, else the provider's duration.
    var talkSeconds: Double {
        guard outcome == "answered" else { return 0 }
        if let answeredAt, let endedAt { return max(0, endedAt.timeIntervalSince(answeredAt)) }
        return duration
    }

    /// The link that plays a recording or voicemail now: the direct one while it lasts, else the provider's page.
    func playLink(_ direct: URL?, now: Date = AppClock.now) -> URL? {
        if let direct, let linksExpireAt, linksExpireAt > now.addingTimeInterval(60) { return direct }
        return assetURL
    }

    /// Why a call was missed, in plain words where we know the provider's reason.
    var reasonText: String {
        switch missedReason {
        case "": ""
        case "out_of_opening_hours": "Outside opening hours"
        case "short_abandoned": "Hung up quickly"
        case "abandoned_in_ivr": "Hung up in the menu"
        case "abandoned_in_classic": "Hung up while waiting"
        case "no_available_agent": "No teammate available"
        case "agents_did_not_answer": "Nobody answered"
        default: missedReason.replacingOccurrences(of: "_", with: " ").capitalizedFirst
        }
    }

    /// Seconds as 3:05, or 1:02:05 past an hour.
    static func clock(_ seconds: Double) -> String {
        let total = max(0, Int(seconds.rounded()))
        let hours = total / 3600, minutes = (total % 3600) / 60, rest = total % 60
        return hours > 0 ? String(format: "%d:%02d:%02d", hours, minutes, rest) : String(format: "%d:%02d", minutes, rest)
    }

    private static func https(_ raw: String) -> URL? {
        guard let url = URL(string: raw), url.scheme?.lowercased() == "https", url.host() != nil else { return nil }
        return url
    }

    private static func date(_ raw: String) -> Date? {
        (try? Date.ISO8601FormatStyle(includingFractionalSeconds: true).parse(raw)) ?? (try? Date.ISO8601FormatStyle().parse(raw))
    }
}

/// A call in the ticket's timeline: one entry per call, kept current as it rings, is
/// answered and ends, then gets its recording, the team's comments and the provider's summary.
struct TicketCallEntry: View {
    let activity: Activity
    @State private var transcript = false
    @Environment(\.openURL) private var openURL

    var body: some View {
        let call = TicketCall(activity.data)
        let look = look(call)
        VStack(alignment: .leading, spacing: 9) {
            HStack(alignment: .top, spacing: 10) {
                Image(systemName: look.symbol)
                    .font(.system(size: 11.5, weight: .semibold))
                    .foregroundStyle(look.tone.text)
                    .frame(width: 26, height: 26)
                    .background(look.tone.soft, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 4) {
                    Text(title(call))
                        .font(ODFont.sans(13.5, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .fixedSize(horizontal: false, vertical: true)
                    FlowLayout(spacing: 6) {
                        TonePill(text: look.status, tone: look.tone, height: 19) {
                            if call.live { PingDot(color: look.tone.solid, size: 5) }
                        }
                        if call.talkSeconds > 0 {
                            Text(TicketCall.clock(call.talkSeconds))
                                .font(ODFont.mono(11))
                                .monospacedDigit()
                                .foregroundStyle(Theme.fg3)
                                .frame(height: 19)
                        }
                        if call.outsideHours {
                            TonePill(text: "Outside business hours", tone: .amber, height: 19)
                        }
                    }
                    if let meta = meta(call) {
                        Text(meta)
                            .font(ODFont.sans(12))
                            .foregroundStyle(Theme.fg3)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 4)
                Text(Format.clock(call.startedAt ?? activity.createdAt))
                    .font(ODFont.mono(10.5))
                    .foregroundStyle(Theme.fg4)
            }
            .accessibilityElement(children: .combine)
            links(call)
            if !call.summary.isEmpty {
                box(Text(Self.joined(("Summary  ", ODFont.sans(10.5, weight: .semibold), Theme.fg3), (call.summary, ODFont.sans(13), Theme.fg2))))
            }
            if !call.comments.isEmpty {
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(call.comments) { comment in
                        Text(Self.joined((comment.author.isEmpty ? "" : comment.author + ": ", ODFont.sans(13, weight: .medium), Theme.fg), (comment.text, ODFont.sans(13), Theme.fg2)))
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
            }
            if !call.tags.isEmpty {
                FlowLayout(spacing: 5) {
                    ForEach(call.tags, id: \.self) { LabelChip(name: $0) }
                }
            }
            if !call.transcript.isEmpty {
                VStack(alignment: .leading, spacing: 6) {
                    Button {
                        Haptics.tap()
                        withAnimation(Motion.snappy) { transcript.toggle() }
                    } label: {
                        HStack(spacing: 4) {
                            Image(systemName: "chevron.right")
                                .font(.system(size: 9.5, weight: .bold))
                                .rotationEffect(.degrees(transcript ? 90 : 0))
                            Text(transcript ? "Hide transcript" : "Show transcript")
                                .font(ODFont.sans(12, weight: .medium))
                        }
                        .foregroundStyle(Theme.fg3)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    if transcript {
                        box(Text(call.transcript).font(ODFont.sans(13)).foregroundStyle(Theme.fg2))
                            .transition(.opacity)
                    }
                }
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control + 2, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control + 2, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }

    /// Pieces of text, each in its own font and colour, as one run.
    private static func joined(_ parts: (String, Font, Color)...) -> AttributedString {
        var out = AttributedString()
        for (text, font, color) in parts where !text.isEmpty {
            var piece = AttributedString(text)
            piece.font = font
            piece.foregroundColor = color
            out.append(piece)
        }
        return out
    }

    private func box(_ text: Text) -> some View {
        text
            .lineSpacing(2)
            .fixedSize(horizontal: false, vertical: true)
            .textSelection(.enabled)
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .background(Theme.sunken, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
    }

    private func look(_ call: TicketCall) -> (symbol: String, tone: Tone, status: String) {
        let arrow = call.outbound ? "phone.arrow.up.right" : "phone.arrow.down.left"
        if call.live { return (arrow, .blue, call.stage == "answered" ? "In progress" : "Ringing") }
        switch call.outcome {
        case "voicemail": return ("recordingtape", .amber, "Voicemail")
        case "missed": return ("phone.down", .red, call.outbound ? "No answer" : "Missed")
        default: return (arrow, .green, "Answered")
        }
    }

    private func title(_ call: TicketCall) -> String {
        let caller = !call.callerName.isEmpty ? call.callerName : !call.number.isEmpty ? call.number : "a withheld number"
        if call.outbound {
            return call.agentName.isEmpty ? "Call to \(caller)" : "\(call.agentName) called \(caller)"
        }
        if call.outcome == "answered", !call.agentName.isEmpty { return "\(caller) called, \(call.agentName) answered" }
        return "Call from \(caller)"
    }

    /// Why it was missed, the line, and the number when the title shows a name.
    private func meta(_ call: TicketCall) -> String? {
        var parts: [String] = []
        if !call.reasonText.isEmpty { parts.append(call.reasonText) }
        if !call.line.isEmpty { parts.append("Line \(call.line)") }
        if !call.number.isEmpty, !call.callerName.isEmpty { parts.append(call.number) }
        return parts.isEmpty ? nil : parts.joined(separator: " · ")
    }

    @ViewBuilder private func links(_ call: TicketCall) -> some View {
        let recording = call.recording ? call.playLink(call.recordingURL) : nil
        let voicemail = call.voicemail ? call.playLink(call.voicemailURL) : nil
        if recording != nil || voicemail != nil || call.assetURL != nil {
            FlowLayout(spacing: 6) {
                if let recording { link("Play recording", symbol: "play.fill", url: recording) }
                if let voicemail { link("Play voicemail", symbol: "recordingtape", url: voicemail) }
                // The provider's lasting page for the call's media is also where it opens there.
                if let asset = call.assetURL { link("Open in \(call.providerName)", symbol: "arrow.up.right.square", url: asset) }
            }
        }
    }

    private func link(_ title: String, symbol: String, url: URL) -> some View {
        Button {
            Haptics.tap()
            openURL(url)
        } label: {
            HStack(spacing: 5) {
                Image(systemName: symbol).font(.system(size: 10.5, weight: .semibold))
                Text(title).font(ODFont.sans(12.5, weight: .medium))
            }
        }
        .buttonStyle(ChipButtonStyle())
    }
}
