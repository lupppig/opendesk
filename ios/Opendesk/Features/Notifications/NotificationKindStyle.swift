import SwiftUI

/// How each kind of notification looks and where tapping it goes, as on the web.
enum NotificationStyle {
    static func tone(for type: String) -> Tone {
        switch type {
        case "assigned", "collab_assigned": .blue
        case "new_ticket", "import_finished": .green
        case "mentioned", "collab_mention": .indigo
        case "customer_replied": .teal
        case "thread_replied", "team_joined": .cyan
        case "sla_breached", "ai_balance_out", "auto_recharge_failed", "ai_key_failed", "support_channel_unhealthy": .red
        case "ai_handoff", "ai_cap_reached": .violet
        case "ai_balance_low": .amber
        default: .gray
        }
    }

    static func tone(for notification: AppNotification) -> Tone {
        if notification.kind == .importFinished {
            switch notification.data["status"]?.string {
            case "completed": return .green
            case "needs_credentials": return .amber
            default: return .red
            }
        }
        return tone(for: notification.type)
    }

    static func symbol(for notification: AppNotification) -> String {
        notification.kind?.symbol ?? "bell"
    }

    /// The person behind it, when there is one (for the avatar).
    static func actor(of notification: AppNotification) -> (id: String, name: String)? {
        let data = notification.data
        for key in ["actor", "contact", "user", "from"] {
            if let name = data[key]?["name"]?.string, !name.isEmpty {
                return (data[key]?["id"]?.string ?? name, name)
            }
        }
        if notification.kind == .threadReplied, let email = data["from"]?["email"]?.string, !email.isEmpty { return (email, email) }
        return nil
    }

    /// Where a tap goes, as the web's `notificationPath`: the ticket, the mail conversation
    /// (or Mail when it names none), or the settings page it is about on the web.
    enum Target: Equatable {
        case ticket(String)
        case mailThread(String)
        case mail
        case web(String)
        case none
    }

    static func target(of notification: AppNotification) -> Target {
        let data = notification.data
        switch notification.kind {
        case .collabAssigned, .collabMention:
            if let id = data["thread_id"]?.string, !id.isEmpty { return .mailThread(id) }
            return .mail
        case .aiCapReached: return .web("/settings/ai/behaviour")
        case .aiKeyFailed: return .web("/settings/ai")
        case .aiBalanceLow, .aiBalanceOut, .autoRechargeFailed: return .web("/settings/billing")
        case .teamJoined:
            let id = data["team"]?["id"]?.string ?? data["team_id"]?.string
            return .web(id.flatMap(pathSegment).map { "/settings/teams/\($0)" } ?? "/settings/teams")
        case .importFinished:
            let id = data["import_id"]?.string.flatMap { $0.isEmpty ? nil : $0.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed.subtracting(CharacterSet(charactersIn: "&=+"))) }
            return .web(id.map { "/settings/import?id=\($0)" } ?? "/settings/import")
        case .supportChannelUnhealthy: return .web("/settings/support-channels")
        default:
            if let id = notification.ticket?.id { return .ticket(id) }
            return .none
        }
    }

    private static func pathSegment(_ id: String) -> String? {
        id.isEmpty ? nil : id.addingPercentEncoding(withAllowedCharacters: .urlPathAllowed.subtracting(CharacterSet(charactersIn: "/")))
    }

    /// The line under the title: the ticket or conversation it's about, then a preview.
    static func context(of notification: AppNotification) -> String? {
        if let ticket = notification.ticket { return "#\(ticket.number) \(ticket.subject)" }
        if let subject = notification.data["subject"]?.string, !subject.isEmpty { return subject }
        return nil
    }
}
