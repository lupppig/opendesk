import Foundation
import Observation

/// Ollie's past conversations (`GET /v1/assistant/sessions`): open one,
/// rename it, delete it. Renames and deletes show at once and roll back if
/// the server refuses.
@MainActor
@Observable
final class AssistantHistoryStore {
    private(set) var sessions: [AssistantSession] = []
    private(set) var loading = false
    private(set) var loaded = false
    private(set) var error: String?

    @ObservationIgnored private let api: APIClient

    init(api: APIClient) {
        self.api = api
    }

    func load() async {
        guard !loading else { return }
        loading = true
        defer { loading = false }
        struct Page: Decodable, Sendable { var data: [AssistantSession] }
        do {
            let page: Page = try await api.get("v1/assistant/sessions", query: [URLQueryItem(name: "limit", value: "50")])
            sessions = page.data.sorted { ($0.updatedAt ?? $0.createdAt ?? .distantPast) > ($1.updatedAt ?? $1.createdAt ?? .distantPast) }
            error = nil
            loaded = true
        } catch {
            guard !error.isCancellation else { return }
            self.error = error.userMessage
        }
    }

    /// The renamed session, or the error's message when the rename didn't stick.
    func rename(_ id: String, to title: String) async -> Result<AssistantSession, AssistantHistoryError> {
        let trimmed = String(title.trimmingCharacters(in: .whitespacesAndNewlines).prefix(120))
        guard let index = sessions.firstIndex(where: { $0.id == id }), !trimmed.isEmpty else { return .failure(AssistantHistoryError(message: "Give the conversation a name.")) }
        let before = sessions[index]
        sessions[index].title = trimmed
        struct Body: Encodable, Sendable { let title: String }
        do {
            let updated: AssistantSession = try await api.patch("v1/assistant/sessions/\(id)", body: Body(title: trimmed))
            if let at = sessions.firstIndex(where: { $0.id == id }) { sessions[at] = updated }
            return .success(updated)
        } catch {
            if let at = sessions.firstIndex(where: { $0.id == id }) { sessions[at] = before }
            return .failure(AssistantHistoryError(message: error.userMessage))
        }
    }

    func delete(_ id: String) async -> AssistantHistoryError? {
        guard let index = sessions.firstIndex(where: { $0.id == id }) else { return nil }
        let removed = sessions.remove(at: index)
        do {
            try await api.delete("v1/assistant/sessions/\(id)")
            return nil
        } catch {
            sessions.insert(removed, at: min(index, sessions.count))
            return AssistantHistoryError(message: error.userMessage)
        }
    }

    /// Conversations by when they were last used: Today, Yesterday, This week, then by month.
    func grouped(matching query: String, now: Date = AppClock.now, calendar: Calendar = .current) -> [(title: String, sessions: [AssistantSession])] {
        let needle = query.trimmingCharacters(in: .whitespacesAndNewlines)
        let matching = needle.isEmpty ? sessions : sessions.filter { ($0.title ?? "").localizedCaseInsensitiveContains(needle) }
        var groups: [(title: String, sessions: [AssistantSession])] = []
        for session in matching {
            let date = session.updatedAt ?? session.createdAt ?? now
            let title: String
            if calendar.isDate(date, inSameDayAs: now) {
                title = "Today"
            } else if let yesterday = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(date, inSameDayAs: yesterday) {
                title = "Yesterday"
            } else if now.timeIntervalSince(date) < 7 * 86400 {
                title = "This week"
            } else if now.timeIntervalSince(date) < 30 * 86400 {
                title = "Last 30 days"
            } else {
                title = date.formatted(.dateTime.month(.wide).year())
            }
            if groups.last?.title == title {
                groups[groups.count - 1].sessions.append(session)
            } else {
                groups.append((title, [session]))
            }
        }
        return groups
    }
}

struct AssistantHistoryError: Error, Sendable {
    var message: String
}
