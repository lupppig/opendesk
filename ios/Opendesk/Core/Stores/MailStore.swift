import Foundation
import Observation

/// Where the mail list looks: a box (or one folder or label) across every account or in one.
struct MailScope: Hashable, Sendable {
    var box: MailBox = .inbox
    var folderID: String?
    var accountID: String?
}

/// Collab mail: accounts, folders, counts and the conversation list of one scope,
/// patched in place by actions and `collab.*` events. One for the app, started for
/// the workspace on screen, so the list and the conversation pushed on it share it.
@MainActor
@Observable
final class MailStore {
    static let shared = MailStore()

    private(set) var status: CollabStatus?
    private(set) var accounts: [MailAccount] = []
    private(set) var accountsLoaded = false
    private(set) var folders: [String: [MailFolder]] = [:]
    private(set) var counts = MailCounts()
    private(set) var prefs = MailSidebarPrefs()

    private(set) var scope = MailScope()
    private(set) var search = ""
    private(set) var threads: [MailThread] = []
    private(set) var nextCursor: String?
    private(set) var loading = false
    private(set) var loadingMore = false
    private(set) var error: String?
    private(set) var loadedOnce = false
    /// Bumped when a conversation changes live, for the row's pulse.
    private(set) var pulses: [String: Date] = [:]

    @ObservationIgnored private(set) var api: APIClient?
    @ObservationIgnored private(set) var me: String?
    @ObservationIgnored private var key: String?
    @ObservationIgnored private weak var realtime: RealtimeService?
    @ObservationIgnored private var listener: UUID?
    @ObservationIgnored private var generation = 0
    @ObservationIgnored private var reloadTask: Task<Void, Never>?
    @ObservationIgnored private var searchTask: Task<Void, Never>?
    @ObservationIgnored private var quietTask: Task<Void, Never>?
    @ObservationIgnored private var foldersTask: Task<Void, Never>?
    /// Called with what `GET /v1/collab` answered, so the menu's Mail row follows it.
    @ObservationIgnored var onStatus: ((CollabStatus) -> Void)?

    // MARK: Lifecycle

    /// Starts for the workspace on screen; a second call for the same one keeps what is loaded.
    func start(api: APIClient, realtime: RealtimeService, workspaceID: String, me: String) {
        let key = "\(ObjectIdentifier(api).hashValue)/\(workspaceID)/\(me)"
        guard key != self.key else { return }
        stop()
        self.key = key
        self.api = api
        self.me = me
        self.realtime = realtime
        listener = realtime.subscribe { [weak self] update in self?.handle(update) }
        Task { await loadAll() }
    }

    func stop() {
        realtime?.unsubscribe(listener)
        listener = nil
        generation += 1
        key = nil
        api = nil
        status = nil
        accounts = []
        accountsLoaded = false
        folders = [:]
        counts = MailCounts()
        prefs = MailSidebarPrefs()
        scope = MailScope()
        search = ""
        threads = []
        nextCursor = nil
        error = nil
        loadedOnce = false
        pulses = [:]
    }

    func loadAll() async {
        guard let api else { return }
        async let status: CollabStatus? = try? api.get("v1/collab")
        async let prefs: MailSidebarPrefs? = try? api.get("v1/collab/sidebar")
        if let value = await status {
            self.status = value
            onStatus?(value)
        }
        if let value = await prefs { self.prefs = value }
        guard self.status?.enabled != false else {
            accountsLoaded = true
            loadedOnce = true
            return
        }
        await loadAccounts()
        await refresh()
        await refreshCounts()
    }

    func loadAccounts() async {
        guard let api else { return }
        do {
            let list: [MailAccount] = try await api.get("v1/collab/accounts")
            accounts = list
            accountsLoaded = true
            if let id = scope.accountID, !list.contains(where: { $0.id == id }) { setScope(MailScope(box: scope.box)) }
            await loadFolders()
        } catch {
            accountsLoaded = true
        }
    }

    func loadFolders() async {
        guard let api else { return }
        var next: [String: [MailFolder]] = [:]
        await withTaskGroup(of: (String, [MailFolder]?).self) { group in
            for account in accounts {
                group.addTask {
                    let list: [MailFolder]? = try? await api.get("v1/collab/accounts/\(account.id)/folders")
                    return (account.id, list)
                }
            }
            for await (id, list) in group { next[id] = list ?? folders[id] ?? [] }
        }
        folders = next
    }

    func refreshCounts() async {
        guard let api else { return }
        if let value: MailCounts = try? await api.get("v1/collab/counts") { counts = value }
    }

    /// Reloads accounts, status, folders, the list and counts (pull to refresh).
    func refreshEverything() async {
        guard let api else { return }
        if let value: CollabStatus = try? await api.get("v1/collab") {
            status = value
            onStatus?(value)
        }
        await loadAccounts()
        await refresh()
        await refreshCounts()
    }

    // MARK: Scope and search

    func setScope(_ next: MailScope) {
        guard next != scope else { return }
        scope = next
        threads = []
        nextCursor = nil
        loadedOnce = false
        error = nil
        reload()
    }

    func setBox(_ box: MailBox) {
        setScope(MailScope(box: box, folderID: nil, accountID: box.spansAccounts ? nil : scope.accountID))
    }

    func setAccount(_ id: String?) {
        setScope(MailScope(box: scope.box.spansAccounts ? .inbox : scope.box, folderID: nil, accountID: id))
    }

    func setFolder(_ folder: MailFolder) {
        setScope(MailScope(box: scope.box, folderID: folder.id, accountID: folder.accountID))
    }

    func setSearch(_ text: String) {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed != search else { return }
        search = trimmed
        searchTask?.cancel()
        searchTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(280))
            guard !Task.isCancelled else { return }
            self?.reload()
        }
    }

    private func query(limit: Int = 50) -> [URLQueryItem] {
        var items: [URLQueryItem] = []
        if let folder = scope.folderID {
            items.append(URLQueryItem(name: "folder_id", value: folder))
        } else {
            items.append(URLQueryItem(name: "box", value: scope.box.rawValue))
        }
        if let account = scope.accountID, scope.folderID != nil || !scope.box.spansAccounts {
            items.append(URLQueryItem(name: "account_id", value: account))
        }
        if !search.isEmpty { items.append(URLQueryItem(name: "q", value: search)) }
        items.append(URLQueryItem(name: "limit", value: String(limit)))
        return items
    }

    func reload() {
        reloadTask?.cancel()
        reloadTask = Task { await self.refresh() }
    }

    func refresh() async {
        guard let api else { return }
        generation += 1
        let current = generation
        loading = true
        defer { if current == generation { loading = false } }
        do {
            let page: Page<MailThread> = try await api.get("v1/collab/threads", query: query())
            guard current == generation else { return }
            threads = page.data
            nextCursor = page.nextCursor
            error = nil
            loadedOnce = true
        } catch {
            guard current == generation, !error.isCancellation else { return }
            self.error = error.userMessage
            loadedOnce = true
        }
    }

    /// The first page again without the loading state, as many as are shown (live changes).
    private func quietRefresh() async {
        guard let api, loadedOnce else { return }
        let current = generation
        let limit = min(100, max(50, threads.count))
        guard let page: Page<MailThread> = try? await api.get("v1/collab/threads", query: query(limit: limit)), current == generation else { return }
        let before = Dictionary(uniqueKeysWithValues: threads.map { ($0.id, $0) })
        for thread in page.data where before[thread.id] != nil && before[thread.id] != thread {
            pulses[thread.id] = .now
        }
        threads = page.data
        if threads.count >= limit { nextCursor = page.nextCursor } else { nextCursor = nil }
    }

    func loadMore() async {
        guard let api, let cursor = nextCursor, !loadingMore, !loading else { return }
        loadingMore = true
        defer { loadingMore = false }
        let current = generation
        var items = query()
        items.append(URLQueryItem(name: "cursor", value: cursor))
        guard let page: Page<MailThread> = try? await api.get("v1/collab/threads", query: items), current == generation else { return }
        let known = Set(threads.map(\.id))
        threads += page.data.filter { !known.contains($0.id) }
        nextCursor = page.nextCursor
    }

    // MARK: Lookups

    func account(_ id: String?) -> MailAccount? {
        guard let id else { return nil }
        return accounts.first { $0.id == id }
    }

    func folder(_ id: String?) -> MailFolder? {
        guard let id else { return nil }
        for list in folders.values { if let folder = list.first(where: { $0.id == id }) { return folder } }
        return nil
    }

    func folders(of accountID: String) -> [MailFolder] { folders[accountID] ?? [] }

    func folder(role: MailFolderRole, in accountID: String) -> MailFolder? {
        folders(of: accountID).first { $0.role == role }
    }

    /// The user's folders and labels of an account, in path order.
    func customFolders(of accountID: String) -> [MailFolder] {
        folders(of: accountID).filter(\.isCustom).sorted { $0.path.localizedCaseInsensitiveCompare($1.path) == .orderedAscending }
    }

    /// Labels on a conversation worth a pill: the account's own labels, not the one being viewed.
    func labels(on thread: MailThread) -> [MailFolder] {
        let list = folders(of: thread.accountID)
        return thread.folderIDs.compactMap { id in list.first { $0.id == id } }
            .filter { $0.isLabel && $0.isCustom && $0.id != scope.folderID }
    }

    /// Every address of every account the person sees: never "the other person".
    var ownEmails: Set<String> {
        accounts.reduce(into: Set<String>()) { $0.formUnion($1.addresses) }
    }

    /// Accounts the list spans.
    var accountsInView: [MailAccount] {
        if let id = scope.accountID, let account = account(id) { return [account] }
        return accounts
    }

    var spansAccounts: Bool { scope.accountID == nil && accounts.count > 1 }

    /// The box or folder's name.
    var title: String {
        if let folder = folder(scope.folderID) { return folder.name }
        if scope.box == .inbox, scope.accountID == nil, accounts.count > 1 { return "All inboxes" }
        return scope.box.title
    }

    /// Unread conversations for a box tab or row, nil when it shows no number.
    func unread(for box: MailBox) -> Int? {
        switch box {
        case .inbox:
            if let id = scope.accountID { return counts.accounts[id] ?? account(id)?.unread }
            return counts.boxes["inbox"]
        case .unread, .shared, .assigned, .drafts:
            return scope.accountID == nil || box.spansAccounts ? counts.boxes[box.rawValue] : nil
        default:
            return nil
        }
    }

    func unread(folder: MailFolder) -> Int { counts.folders[folder.id] ?? folder.unread }

    /// Whether a box makes sense for the account in scope: one account whose provider lacks the folder hides it.
    func offers(_ box: MailBox) -> Bool {
        guard let id = scope.accountID, let role = box.role else { return true }
        if role == .archive, account(id)?.provider == .google { return true }
        return folders(of: id).isEmpty || folder(role: role, in: id) != nil
    }

    /// The oldest mail every account in view has here, for "Searching mail since".
    var searchCoveredFrom: Date? {
        accountsInView.compactMap(\.oldestMessageAt).max()
    }

    /// The conversations before and after `id` in the list.
    func neighbours(of id: String) -> (previous: MailThread?, next: MailThread?) {
        guard let index = threads.firstIndex(where: { $0.id == id }) else { return (nil, nil) }
        let previous = index > 0 ? threads[index - 1] : nil
        let next = index + 1 < threads.count ? threads[index + 1] : nil
        return (previous, next)
    }

    func thread(_ id: String) -> MailThread? { threads.first { $0.id == id } }

    // MARK: Actions

    /// Applies an action at once and sends it; puts the list back if the server refuses.
    @discardableResult
    func act(_ ids: [String], _ action: MailAction, folderID: String? = nil, known: [MailThread] = []) async throws -> [MailThread] {
        guard let api else { throw APIError.signedOut }
        let before = threads
        let beforeCounts = counts
        for id in ids {
            guard let thread = threads.first(where: { $0.id == id }) ?? known.first(where: { $0.id == id }) else { continue }
            place(applied(action, folderID: folderID, to: thread))
        }
        struct Body: Encodable, Sendable {
            let thread_ids: [String]
            let action: String
            let folder_id: String?
        }
        struct Answer: Decodable, Sendable { let threads: [MailThread] }
        do {
            let answer: Answer = try await api.post("v1/collab/threads/actions", body: Body(thread_ids: ids, action: action.rawValue, folder_id: folderID))
            for thread in answer.threads { place(thread) }
            scheduleCounts()
            return answer.threads
        } catch {
            threads = before
            counts = beforeCounts
            throw error
        }
    }

    /// What a conversation looks like after an action, before the server answers.
    func applied(_ action: MailAction, folderID: String?, to thread: MailThread) -> MailThread {
        var next = thread
        let account = thread.accountID
        func role(_ role: MailFolderRole) -> String? { folder(role: role, in: account)?.id }
        func drop(_ roles: [MailFolderRole]) {
            let ids = Set(roles.compactMap(role))
            next.folderIDs.removeAll { ids.contains($0) }
        }
        func add(_ id: String?) {
            if let id, !next.folderIDs.contains(id) { next.folderIDs.append(id) }
        }
        switch action {
        case .read: next.unread = false
        case .unread: next.unread = true
        case .star: next.starred = true
        case .unstar: next.starred = false
        case .archive:
            drop([.inbox])
            if self.account(account)?.provider != .google { add(role(.archive)) }
        case .inbox, .notSpam:
            drop([.trash, .spam, .archive])
            add(role(.inbox))
        case .trash:
            drop([.inbox, .spam, .archive])
            add(role(.trash))
        case .spam:
            drop([.inbox, .trash, .archive])
            add(role(.spam))
        case .move:
            guard let folderID else { break }
            if self.folder(folderID)?.isLabel != true || self.account(account)?.provider == .google {
                drop([.inbox, .archive, .trash, .spam])
                if self.account(account)?.provider != .google {
                    let custom = Set(customFolders(of: account).filter { !$0.isLabel }.map(\.id))
                    next.folderIDs.removeAll { custom.contains($0) }
                }
            }
            add(folderID)
        case .label: add(folderID)
        case .unlabel: next.folderIDs.removeAll { $0 == folderID }
        }
        return next
    }

    /// Puts a fresh copy where it belongs: updated in place, or dropped when it left this list.
    func place(_ thread: MailThread) {
        guard let index = threads.firstIndex(where: { $0.id == thread.id }) else { return }
        if belongs(thread) {
            threads[index] = thread
        } else {
            threads.remove(at: index)
        }
    }

    /// Whether a conversation still fits the scope, as far as the app can tell.
    func belongs(_ thread: MailThread) -> Bool {
        if let id = scope.accountID, thread.accountID != id, !scope.box.spansAccounts { return false }
        if let folderID = scope.folderID { return thread.folderIDs.contains(folderID) }
        let roles = Set(thread.folderIDs.compactMap { folder($0)?.role })
        let trashOrSpam = roles.contains(.trash) || roles.contains(.spam)
        switch scope.box {
        case .inbox: return roles.contains(.inbox)
        case .unread: return thread.unread && !trashOrSpam
        case .starred: return thread.starred && !roles.contains(.trash)
        case .sent: return roles.contains(.sent)
        case .drafts: return roles.contains(.drafts)
        case .archive:
            if account(thread.accountID)?.provider == .google { return !roles.contains(.inbox) && !trashOrSpam && !roles.contains(.drafts) }
            return roles.contains(.archive)
        case .trash: return roles.contains(.trash)
        case .spam: return roles.contains(.spam)
        case .all: return !trashOrSpam
        case .shared: return thread.shared && !trashOrSpam
        case .assigned: return thread.assignees.contains { $0.id == me } && !trashOrSpam
        }
    }

    /// A conversation the open thread changed (shares, assignees, comments).
    func update(_ thread: MailThread) {
        guard let index = threads.firstIndex(where: { $0.id == thread.id }) else { return }
        threads[index] = thread
    }

    func setStatus(_ value: CollabStatus) { status = value }

    // MARK: Realtime

    func handle(_ update: RealtimeUpdate) {
        guard api != nil else { return }
        switch update {
        case .resync:
            Task { await refreshEverything() }
        case let .envelope(envelope):
            switch envelope.type {
            case "collab.changed":
                if envelope.data["folders"]?.bool == true { scheduleFolders() }
                scheduleQuiet()
                scheduleCounts()
            case "collab.accounts":
                Task { [weak self] in
                    guard let self, let api = self.api else { return }
                    if let value: CollabStatus = try? await api.get("v1/collab") {
                        self.status = value
                        self.onStatus?(value)
                    }
                    await self.loadAccounts()
                }
            default:
                break
            }
        }
    }

    private func scheduleQuiet() {
        guard quietTask == nil else { return }
        quietTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(450))
            guard let self else { return }
            self.quietTask = nil
            await self.quietRefresh()
        }
    }

    @ObservationIgnored private var countsTask: Task<Void, Never>?

    func scheduleCounts() {
        guard countsTask == nil else { return }
        countsTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(400))
            guard let self else { return }
            self.countsTask = nil
            await self.refreshCounts()
        }
    }

    private func scheduleFolders() {
        guard foldersTask == nil else { return }
        foldersTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(500))
            guard let self else { return }
            self.foldersTask = nil
            await self.loadFolders()
        }
    }
}
