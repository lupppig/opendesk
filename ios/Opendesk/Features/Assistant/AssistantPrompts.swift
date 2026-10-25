import Foundation

/// Quick asks that fit the page (web `Suggestions.tsx` and the composer's `/`
/// list): a ticket gets "summarize" and "who should take this", the inbox
/// gets triage, reports get the week in numbers.
enum AssistantPrompt: String, CaseIterable, Identifiable, Sendable {
    case summarize, assign, reply, note, triage, unassigned, report, kb, label, breach, mine

    var id: String { rawValue }

    /// The short label on a chip.
    var chip: String {
        switch self {
        case .summarize: "Summarize this ticket"
        case .assign: "Who should take this?"
        case .reply: "Draft a reply"
        case .note: "Add an internal note"
        case .triage: "What needs attention now?"
        case .unassigned: "Suggest assignees for old tickets"
        case .report: "How are we doing this week?"
        case .kb: "Draft a help article"
        case .label: "Label this week's billing tickets"
        case .breach: "What breaches SLA soon?"
        case .mine: "What's waiting on me?"
        }
    }

    /// What is sent.
    var prompt: String {
        switch self {
        case .summarize: "Summarize this ticket"
        case .assign: "Who should take this?"
        case .reply: "Draft a reply to this customer"
        case .note: "Add an internal note with what we know so far"
        case .triage: "What needs my attention right now? Look at SLA and urgent tickets."
        case .unassigned: "Show unassigned tickets older than a day and suggest assignees"
        case .report: "How are we doing this week? Volume, response times, SLA and CSAT."
        case .kb: "Draft a help article from recently solved tickets about the most common question"
        case .label: "Label all billing tickets from this week"
        case .breach: "Which tickets breach SLA in the next hour?"
        case .mine: "Which of my tickets are waiting on me?"
        }
    }

    /// The line beside `/key` in the composer's list.
    var slashHint: String {
        switch self {
        case .summarize: "Summarize the open ticket"
        case .assign: "Find who should take it"
        case .reply: "Draft a reply"
        case .note: "Add an internal note"
        case .triage: "Triage what needs attention"
        case .unassigned: "Old unassigned tickets"
        case .report: "How the team is doing"
        case .kb: "Draft a help article"
        case .label: "Label billing tickets"
        case .breach: "What breaches SLA soon"
        case .mine: "What's waiting on me"
        }
    }

    var symbol: String {
        switch self {
        case .summarize: "text.alignleft"
        case .assign: "person.crop.circle.badge.checkmark"
        case .reply: "arrowshape.turn.up.left"
        case .note: "note.text"
        case .triage: "flame"
        case .unassigned: "tray"
        case .report: "chart.bar.xaxis"
        case .kb: "book.closed"
        case .label: "tag"
        case .breach: "timer"
        case .mine: "person.crop.circle"
        }
    }

    /// The `/` quick asks, in the web's order.
    static let slash: [AssistantPrompt] = [.summarize, .assign, .reply, .note, .triage, .unassigned, .report, .kb]

    static func suggestions(for page: String?) -> [AssistantPrompt] {
        switch page {
        case "ticket": [.summarize, .assign, .reply, .note]
        case "reports": [.report, .breach, .unassigned]
        case "kb": [.kb, .report]
        case "inbox", "view": [.triage, .unassigned, .label, .mine]
        default: [.triage, .breach, .mine, .report]
        }
    }

    /// The quick asks matching `text` while it is `/` and a word.
    static func slashMatches(_ text: String) -> [AssistantPrompt] {
        guard text.hasPrefix("/"), !text.contains(" "), !text.contains("\n") else { return [] }
        let word = text.dropFirst().lowercased()
        return slash.filter { $0.rawValue.hasPrefix(word) }
    }
}
