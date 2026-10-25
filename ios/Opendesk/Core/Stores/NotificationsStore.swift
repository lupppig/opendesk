import Foundation
import Observation

/// The signed-in person's notifications in the open workspace, and the
/// unread count across every workspace for the app icon badge.
@MainActor
@Observable
final class NotificationsStore {
    enum Filter: String, CaseIterable, Identifiable {
        case all, unread
        var id: String { rawValue }
        var title: String { self == .all ? "All" : "Unread" }
    }

    private(set) var items: [AppNotification] = []
    private(set) var nextCursor: String?
    private(set) var loading = false
    private(set) var loadingMore = false
    private(set) var error: String?
    private(set) var loadedOnce = false
    /// Unread here (the tab badge).
    private(set) var unread = 0
    /// Unread across the user's workspaces (the app icon badge).
    private(set) var badge = 0
    /// Who hears about what in this workspace, and how email is paced.
    private(set) var preferences: NotificationPreferences?
    private(set) var preferencesError: String?
    var filter: Filter = .all {
        didSet { if filter != oldValue { Task { await load() } } }
    }

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var memberships: [Membership] = []
    @ObservationIgnored private var currentWorkspace: String?
    @ObservationIgnored private var badgeTask: Task<Void, Never>?
    /// Mirrors the badge to the app icon (set by the environment).
    @ObservationIgnored var onBadge: ((Int) -> Void)?

    func start(api: APIClient, workspaceID: String, memberships: [Membership]) {
        self.api = api
        self.memberships = memberships
        currentWorkspace = workspaceID
        items = []
        nextCursor = nil
        loadedOnce = false
        preferences = nil
        preferencesError = nil
        Task {
            await load()
            await refreshBadge()
        }
    }

    func stop() {
        api = nil
        preferences = nil
        items = []
        unread = 0
        badge = 0
        onBadge?(0)
    }

    func load() async {
        guard let api else { return }
        loading = true
        defer { loading = false }
        var query = [URLQueryItem(name: "limit", value: "50")]
        if filter == .unread { query.append(URLQueryItem(name: "unread", value: "true")) }
        do {
            let page: Page<AppNotification> = try await api.get("v1/notifications", query: query)
            items = page.data
            nextCursor = page.nextCursor
            error = nil
        } catch {
            if !error.isCancellation { self.error = error.userMessage }
        }
        loadedOnce = true
        await refreshUnread()
    }

    func loadMore() async {
        guard let api, let cursor = nextCursor, !loadingMore else { return }
        loadingMore = true
        defer { loadingMore = false }
        var query = [URLQueryItem(name: "limit", value: "50"), URLQueryItem(name: "cursor", value: cursor)]
        if filter == .unread { query.append(URLQueryItem(name: "unread", value: "true")) }
        guard let page: Page<AppNotification> = try? await api.get("v1/notifications", query: query) else { return }
        let known = Set(items.map(\.id))
        items += page.data.filter { !known.contains($0.id) }
        nextCursor = page.nextCursor
    }

    private func refreshUnread() async {
        guard let api else { return }
        if let page: Page<AppNotification> = try? await api.get("v1/notifications", query: [URLQueryItem(name: "unread", value: "true"), URLQueryItem(name: "limit", value: "100")]) {
            unread = page.data.count
        }
    }

    /// The icon badge counts unread notifications in every workspace, like the server's push badge.
    func refreshBadge() async {
        guard let api else { return }
        var total = 0
        for membership in memberships {
            if membership.id == currentWorkspace {
                total += unread
                continue
            }
            if let page: Page<AppNotification> = try? await api.get("v1/notifications", query: [URLQueryItem(name: "unread", value: "true"), URLQueryItem(name: "limit", value: "100")], workspace: .id(membership.id)) {
                total += page.data.count
            }
        }
        if memberships.isEmpty { total = unread }
        badge = total
        onBadge?(total)
    }

    private func scheduleBadge() {
        badgeTask?.cancel()
        badgeTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(500))
            guard !Task.isCancelled, let self else { return }
            await self.refreshUnread()
            await self.refreshBadge()
        }
    }

    // MARK: Read state

    func markRead(_ notification: AppNotification) async {
        guard notification.isUnread else { return }
        setRead(ids: [notification.id])
        struct Body: Encodable, Sendable { let ids: [String] }
        let _: EmptyBody? = try? await api?.post("v1/notifications/read", body: Body(ids: [notification.id]))
    }

    func markRead(ticketID: String) async {
        let ids = items.filter { $0.isUnread && $0.ticket?.id == ticketID }.map(\.id)
        guard !ids.isEmpty else { return }
        setRead(ids: ids)
        struct Body: Encodable, Sendable { let ids: [String] }
        let _: EmptyBody? = try? await api?.post("v1/notifications/read", body: Body(ids: ids))
    }

    func markAllRead() async {
        let now = AppClock.now
        for index in items.indices where items[index].isUnread { items[index].readAt = now }
        if filter == .unread { items = [] }
        unread = 0
        scheduleBadge()
        struct Body: Encodable, Sendable { let all: Bool }
        let _: EmptyBody? = try? await api?.post("v1/notifications/read", body: Body(all: true))
    }

    private func setRead(ids: [String]) {
        let now = AppClock.now
        var changed = 0
        for index in items.indices where ids.contains(items[index].id) && items[index].isUnread {
            items[index].readAt = now
            changed += 1
        }
        unread = max(0, unread - changed)
        scheduleBadge()
    }

    // MARK: Preferences

    func loadPreferences() async {
        guard let api else { return }
        do {
            preferences = try await api.get("v1/me/notification-preferences")
            preferencesError = nil
        } catch {
            if !error.isCancellation { preferencesError = error.userMessage }
        }
    }

    /// Shows `change` at once and saves `patch`; puts things back if the save fails.
    func updatePreferences(_ patch: NotificationPreferencesPatch, change: (inout NotificationPreferences) -> Void) async throws {
        guard let api, var next = preferences else { return }
        let before = next
        change(&next)
        preferences = next
        do {
            preferences = try await api.patch("v1/me/notification-preferences", body: patch)
        } catch {
            preferences = before
            throw error
        }
    }

    func setPreference(_ type: String, channel: NotificationChannel, on: Bool) async throws {
        var change = NotificationPreferencesPatch.TypeChange(type: type)
        switch channel {
        case .email: change.email = on
        case .push: change.push = on
        case .desktop: change.desktop = on
        }
        try await updatePreferences(NotificationPreferencesPatch(types: [change])) { prefs in
            if let index = prefs.types.firstIndex(where: { $0.type == type }) { prefs.types[index].set(channel, on) }
        }
    }

    /// `POST /v1/me/notification-preferences/test` (a few an hour).
    func sendTestEmail() async throws {
        guard let api else { return }
        let _: EmptyBody = try await api.post("v1/me/notification-preferences/test")
    }

    /// Undoes an unsubscribe link: email comes back with the choices from before.
    func resubscribe() async throws {
        guard let api else { return }
        preferences = try await api.post("v1/me/notification-preferences/resubscribe")
    }

    // MARK: Realtime

    /// A new notification for the signed-in user (from the user channel).
    func received(_ notification: AppNotification) {
        guard !items.contains(where: { $0.id == notification.id }) else { return }
        items.insert(notification, at: 0)
        if notification.isUnread { unread += 1 }
        scheduleBadge()
    }

    func handle(_ update: RealtimeUpdate) {
        if case .resync = update { Task { await load() } }
    }

    /// Pushes changed the badge while the app was in the background.
    func foregrounded() {
        Task {
            await refreshUnread()
            await refreshBadge()
        }
    }
}
