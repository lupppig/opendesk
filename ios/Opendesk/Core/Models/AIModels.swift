import Foundation

// Shapes from contracts/ai.md and contracts/agent.md. Everything the AI
// does is shown as Ollie.

struct AIFeatures: Codable, Hashable, Sendable {
    var enabled: Bool?
    var assistant: Bool?
    var widgetAnswers: Bool?
    var autoTriage: Bool?

    enum CodingKeys: String, CodingKey {
        case enabled, assistant
        case widgetAnswers = "widget_answers"
        case autoTriage = "auto_triage"
    }
}

/// `GET /v1/ai/config`.
struct AIConfig: Codable, Hashable, Sendable {
    var enabled: Bool
    var assistantName: String?
    var features: AIFeatures?

    enum CodingKeys: String, CodingKey {
        case enabled, features
        case assistantName = "assistant_name"
    }

    static let off = AIConfig(enabled: false, assistantName: nil, features: nil)

    var assistantOn: Bool { enabled && (features?.enabled ?? true) && (features?.assistant ?? true) }
    var widgetAgentOn: Bool { enabled && (features?.widgetAnswers ?? false) }
}

enum Sentiment: String, LenientEnum {
    case positive, neutral, negative, frustrated
    static let fallback = Sentiment.neutral

    var title: String {
        switch self {
        case .positive: "Positive"
        case .neutral: "Neutral"
        case .negative: "Negative"
        case .frustrated: "Frustrated"
        }
    }
}

/// `custom.ai_summary`, and the answer of `POST /ai/summary`.
struct AISummary: Codable, Hashable, Sendable {
    var text: String
    var keyPoints: [String]
    var sentiment: Sentiment?
    var updatedAt: Date?

    enum CodingKeys: String, CodingKey {
        case text, summary, sentiment
        case keyPoints = "key_points"
        case updatedAt = "updated_at"
    }

    init(text: String, keyPoints: [String], sentiment: Sentiment?, updatedAt: Date?) {
        self.text = text
        self.keyPoints = keyPoints
        self.sentiment = sentiment
        self.updatedAt = updatedAt
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        text = c.lenient(String.self, forKey: .text) ?? c.lenient(String.self, forKey: .summary) ?? ""
        keyPoints = c.lenient([String].self, forKey: .keyPoints) ?? []
        sentiment = c.lenient(Sentiment.self, forKey: .sentiment)
        updatedAt = c.lenient(Date.self, forKey: .updatedAt)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(text, forKey: .text)
        try c.encode(keyPoints, forKey: .keyPoints)
        try c.encodeIfPresent(sentiment, forKey: .sentiment)
        try c.encodeIfPresent(updatedAt, forKey: .updatedAt)
    }
}

enum SuggestionField: String, CaseIterable, Sendable, Identifiable {
    case priority, severity, labels, team, spam
    var id: String { rawValue }
}

/// One pending entry of `custom.ai_suggestions`.
struct AISuggestion: Hashable, Sendable, Identifiable {
    var field: SuggestionField
    var value: JSONValue
    var confidence: Double?

    var id: String { field.rawValue }

    static func all(in custom: [String: JSONValue]) -> [AISuggestion] {
        guard let object = custom["ai_suggestions"]?.object else { return [] }
        return SuggestionField.allCases.compactMap { field in
            guard let entry = object[field.rawValue], let value = entry["value"], !value.isNull else { return nil }
            if case let .bool(flag) = value, field == .spam, !flag { return nil }
            return AISuggestion(field: field, value: value, confidence: entry["confidence"]?.double)
        }
    }
}

struct AIDraft: Decodable, Sendable {
    var bodyHTML: String
    var bodyText: String

    enum CodingKeys: String, CodingKey {
        case bodyHTML = "body_html"
        case bodyText = "body_text"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML) ?? ""
        bodyText = c.lenient(String.self, forKey: .bodyText) ?? ""
    }
}

struct SuggestedReply: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var ticketID: String
    var messageID: String?
    var bodyHTML: String
    var status: String
    var createdAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, status
        case ticketID = "ticket_id"
        case messageID = "message_id"
        case bodyHTML = "body_html"
        case createdAt = "created_at"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        ticketID = c.lenient(String.self, forKey: .ticketID) ?? ""
        messageID = c.lenient(String.self, forKey: .messageID)
        bodyHTML = c.lenient(String.self, forKey: .bodyHTML) ?? ""
        status = c.lenient(String.self, forKey: .status) ?? "pending"
        createdAt = c.lenient(Date.self, forKey: .createdAt)
    }

    var isPending: Bool { status == "pending" }
}

struct SuggestedReplyEnvelope: Decodable, Sendable {
    var suggestedReply: SuggestedReply?

    enum CodingKeys: String, CodingKey {
        case suggestedReply = "suggested_reply"
    }
}

// MARK: - Ollie, the assistant

struct AssistantSession: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var title: String?
    var createdAt: Date?
    var updatedAt: Date?

    enum CodingKeys: String, CodingKey {
        case id, title
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}

struct AssistantRef: Codable, Hashable, Sendable {
    var kind: String
    var id: String
    var label: String
    var number: Int?
}

struct AssistantStep: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var tool: String?
    var label: String?
    var status: String
    var summary: String?
    var refs: [AssistantRef]?
    var actionID: String?
    /// An `ask_teammate` step: whose Ollie answered, and how.
    var peer: AssistantStepPeer?

    enum CodingKeys: String, CodingKey {
        case id, tool, label, status, summary, refs, peer
        case actionID = "action_id"
    }
}

struct AssistantStepPeer: Codable, Hashable, Sendable {
    struct Person: Codable, Hashable, Sendable {
        var id: String
        var name: String
        var avatarURL: String?

        enum CodingKeys: String, CodingKey {
            case id, name
            case avatarURL = "avatar_url"
        }
    }

    var user: Person
    /// `answered`, `declined`, `not_shared` or `failed`.
    var result: String
    var sources: Int?
}

struct PreviewChange: Codable, Hashable, Sendable {
    var field: String
    var from: String?
    var to: String?
}

struct ApprovalPreview: Codable, Hashable, Sendable {
    var title: String
    var summary: String?
    var changes: [PreviewChange]?
    var body: String?
    var targets: [AssistantRef]?
    var total: Int?
    var external: Bool?
    var system: String?
}

struct AssistantApproval: Codable, Hashable, Sendable, Identifiable {
    var actionID: String
    var callID: String?
    var tool: String?
    var preview: ApprovalPreview
    var token: String
    var expiresAt: Date?

    enum CodingKeys: String, CodingKey {
        case tool, preview, token
        case actionID = "action_id"
        case callID = "call_id"
        case expiresAt = "expires_at"
    }

    var id: String { actionID }
}

struct AssistantItem: Codable, Hashable, Sendable {
    var role: String
    var text: String
    var steps: [AssistantStep]?
    var context: AssistantPageContext?
}

/// `GET /v1/assistant/tips`: a nudge from a count query, never the model.
struct AssistantTip: Codable, Hashable, Sendable, Identifiable {
    var key: String
    var text: String
    var prompt: String
    /// `high`, `medium` or `low`.
    var severity: String
    var count: Int?

    var id: String { key }
}

struct AssistantTips: Codable, Sendable {
    var tips: [AssistantTip]
}

struct AssistantTranscript: Decodable, Sendable {
    var session: AssistantSession
    var items: [AssistantItem]
    var approval: AssistantApproval?
}

/// The non-streamed answer of a run.
struct AssistantRunResult: Decodable, Sendable {
    var session: AssistantSession?
    var text: String?
    var steps: [AssistantStep]?
    var approval: AssistantApproval?
    var status: String?
}

struct AssistantPageContext: Codable, Hashable, Sendable {
    var page: String
    var path: String?
    var ticketID: String?
    var folder: String?
    var viewID: String?

    enum CodingKeys: String, CodingKey {
        case page, path, folder
        case ticketID = "ticket_id"
        case viewID = "view_id"
    }
}
