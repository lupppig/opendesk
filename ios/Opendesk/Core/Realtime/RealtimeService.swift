import Foundation
import Observation

/// What a screen hears from realtime.
enum RealtimeUpdate: Sendable {
    case envelope(Envelope)
    /// Events were lost beyond retention (or the events API was unreachable): reload everything.
    case resync
}

struct PresenceViewer: Hashable, Sendable, Identifiable {
    enum State: String, Sendable {
        case replying, viewing, idle
        var rank: Int {
            switch self {
            case .replying: 2
            case .viewing: 1
            case .idle: 0
            }
        }
    }

    var userID: String
    var name: String
    var avatar: String?
    var state: State
    var id: String { userID }
}

struct TypingAgent: Hashable, Sendable {
    var userID: String
    var name: String
    var at: Date
}

/// The AI writing a chat reply as its bot (`bot` is the name), as it streams (contracts/realtime.md "Live events"). Never stored.
struct LiveReply: Hashable, Sendable {
    struct Tool: Hashable, Sendable {
        var name: String
        var status: String
    }

    var replyID: String
    var ticketID: String
    var bot: String
    var text: String
    var tools: [Tool]
    var messageID: String?
    var handoff = false
    var ended = false
    var at: Date

    /// Markdown so far, without `[n]` citation markers.
    var displayText: String {
        text.replacingOccurrences(of: #"\[\d+\]"#, with: "", options: .regularExpression)
    }

    var runningTool: String? { tools.last(where: { $0.status == "running" })?.name }
}

/// One socket per signed-in workspace: `workspace:<wid>` and `user:<uid>` for
/// events (kept contiguous by seq), and `ticket:<wid>:<id>` for presence,
/// typing and Ollie's live replies on the open ticket, plus quiet watches on
/// the tickets visible in the list.
@MainActor
@Observable
final class RealtimeService {
    enum Status: Equatable {
        case idle, connecting, live, reconnecting, offline
    }

    private(set) var status: Status = .idle
    private(set) var presence: [String: [PresenceViewer]] = [:]
    private(set) var typing: [String: [TypingAgent]] = [:]
    private(set) var customerTypingAt: [String: Date] = [:]
    private(set) var live: [String: LiveReply] = [:]
    /// Ticks once a second while someone is typing, so views re-evaluate expiry.
    private(set) var clock = Date.now

    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var socket: PhoenixSocket?
    @ObservationIgnored private var pump: Task<Void, Never>?
    @ObservationIgnored private var workspaceID = ""
    @ObservationIgnored private var userID = ""
    @ObservationIgnored private var everOpened = false
    @ObservationIgnored private var listeners: [UUID: (RealtimeUpdate) -> Void] = [:]
    @ObservationIgnored private var presenceRaw: [String: [String: [JSONValue]]] = [:]
    @ObservationIgnored private var ticketModes: [String: Bool] = [:] // ticket id → active
    @ObservationIgnored private var activeTicket: String?
    @ObservationIgnored private var activeContactID: String?
    @ObservationIgnored private var watched: [String] = []
    @ObservationIgnored private var lastTypingSent = Date.distantPast
    @ObservationIgnored private var clockTask: Task<Void, Never>?
    /// Socket operations run one after another, so a leave never overtakes the join before it.
    @ObservationIgnored private var operations: Task<Void, Never>?

    // Seq bookkeeping (web: realtime/seq.ts).
    @ObservationIgnored private var lastSeq: Int?
    @ObservationIgnored private var catching = false
    @ObservationIgnored private var buffer: [Envelope] = []
    @ObservationIgnored private var seen = Set<String>()
    @ObservationIgnored private var seenOrder: [String] = []

    /// A notification for the signed-in user arrived (user channel only).
    @ObservationIgnored var onNotification: ((AppNotification) -> Void)?
    /// The signed-in user was removed from the workspace or their role changed.
    @ObservationIgnored var onMembershipChanged: (() -> Void)?

    static let maxWatched = 30
    static let typingWindow: TimeInterval = 5
    static let liveStale: TimeInterval = 45

    // MARK: Listeners

    @discardableResult
    func subscribe(_ handler: @escaping (RealtimeUpdate) -> Void) -> UUID {
        let id = UUID()
        listeners[id] = handler
        return id
    }

    func unsubscribe(_ id: UUID?) {
        guard let id else { return }
        listeners[id] = nil
    }

    private func broadcast(_ update: RealtimeUpdate) {
        for handler in listeners.values { handler(update) }
    }

    // MARK: Lifecycle

    func start(api: APIClient, workspaceID: String, userID: String) {
        stop()
        self.api = api
        self.workspaceID = workspaceID
        self.userID = userID
        lastSeq = nil
        #if DEBUG
        if DemoMode.isOn { return startDemo() }
        #endif
        status = .connecting
        let instance = api.instance
        let socket = PhoenixSocket { [api] in
            let token: RealtimeToken = try await api.post("v1/realtime/token")
            return Self.socketURL(instance: instance, token: token)
        }
        self.socket = socket
        pump = Task { [weak self] in
            for await signal in socket.signals {
                guard let self else { return }
                self.handle(signal)
            }
        }
        enqueue { socket in
            await socket.join("workspace:\(workspaceID)")
            await socket.join("user:\(userID)")
            await socket.connect()
        }
        reconcileTickets()
    }

    func stop() {
        pump?.cancel()
        pump = nil
        clockTask?.cancel()
        clockTask = nil
        if let socket { Task { await socket.finish() } }
        socket = nil
        operations = nil
        ticketModes.removeAll()
        presence.removeAll()
        presenceRaw.removeAll()
        typing.removeAll()
        customerTypingAt.removeAll()
        live.removeAll()
        activeTicket = nil
        watched.removeAll()
        everOpened = false
        buffer.removeAll()
        catching = false
        status = .idle
    }

    #if DEBUG
    /// Demo mode has no socket: the demo backend hands over the events its own
    /// changes publish, and teammates sit on a few tickets as on a live server.
    private func startDemo() {
        status = .live
        let workspace = workspaceID
        let deliver: @Sendable (Envelope) -> Void = { [weak self] envelope in
            Task { @MainActor in
                guard let self, self.workspaceID == workspace, envelope.workspaceID == workspace else { return }
                self.applyOne(envelope)
            }
        }
        Task { [weak self] in
            await DemoBackend.shared.listen(deliver)
            let viewers = await DemoBackend.shared.presence(workspace: workspace)
            guard let self, self.workspaceID == workspace, self.status == .live else { return }
            self.presence = viewers
        }
    }
    #endif

    /// The app came back to the foreground: sockets die while suspended.
    func resume() {
        guard let socket else { return }
        Task { await socket.nudge() }
        if lastSeq != nil { Task { await catchUp() } }
    }

    nonisolated static func socketURL(instance: Instance, token: RealtimeToken) -> URL {
        var base = instance.gatewayURL
        if base == nil, let raw = token.url, !raw.isEmpty {
            base = InstanceResolver.gatewayURL(raw, origin: instance.appOrigin)
        }
        let gateway = base ?? instance.resolvedGatewayURL
        var components = URLComponents(url: gateway.appending(path: "websocket"), resolvingAgainstBaseURL: false)!
        components.queryItems = [URLQueryItem(name: "token", value: token.token), URLQueryItem(name: "vsn", value: "2.0.0")]
        return components.url!
    }

    // MARK: Signals

    private func handle(_ signal: SocketSignal) {
        switch signal {
        case .connecting:
            status = everOpened ? .reconnecting : .connecting
        case .open:
            everOpened = true
            status = .live
        case .closed:
            if status != .idle { status = everOpened ? .reconnecting : .offline }
        case let .joined(topic, ok, _, rejoin):
            guard ok else { return }
            if topic == "workspace:\(workspaceID)" {
                if !rejoin, lastSeq == nil {
                    Task { await self.baseline() }
                } else {
                    Task { await self.catchUp() }
                }
            } else if let id = ticketID(from: topic), ticketModes[id] == true {
                push(id, event: "presence", payload: .object(["state": .string("viewing")]))
            }
        case let .message(frame):
            route(frame)
        }
    }

    private func route(_ frame: PhoenixFrame) {
        if frame.topic == "workspace:\(workspaceID)", frame.event == "event" {
            guard let envelope = try? frame.payload.decode(Envelope.self) else { return }
            receive(envelope)
            return
        }
        if frame.topic == "user:\(userID)", frame.event == "event" {
            guard let envelope = try? frame.payload.decode(Envelope.self), envelope.workspaceID == workspaceID else { return }
            if envelope.type == "notification.created" {
                apply(envelope, viaUser: true)
                return
            }
            if seen.contains(envelope.id) { return }
            markSeen(envelope.id)
            apply(envelope, viaUser: true)
            return
        }
        guard let ticketID = ticketID(from: frame.topic) else { return }
        switch frame.event {
        case "presence_state":
            var state: [String: [JSONValue]] = [:]
            for (key, value) in frame.payload.object ?? [:] { state[key] = value["metas"]?.array ?? [] }
            presenceRaw[ticketID] = state
            publishPresence(ticketID)
        case "presence_diff":
            var state = presenceRaw[ticketID] ?? [:]
            for (key, value) in frame.payload["joins"]?.object ?? [:] {
                let metas = value["metas"]?.array ?? []
                let refs = Set(metas.compactMap { $0["phx_ref"]?.string })
                state[key] = (state[key] ?? []).filter { !refs.contains($0["phx_ref"]?.string ?? "") } + metas
            }
            for (key, value) in frame.payload["leaves"]?.object ?? [:] {
                let refs = Set((value["metas"]?.array ?? []).compactMap { $0["phx_ref"]?.string })
                let left = (state[key] ?? []).filter { !refs.contains($0["phx_ref"]?.string ?? "") }
                state[key] = left.isEmpty ? nil : left
            }
            presenceRaw[ticketID] = state
            publishPresence(ticketID)
        case "typing":
            guard let user = frame.payload["user_id"]?.string, user != userID else { return }
            let name = frame.payload["name"]?.string ?? "Someone"
            var list = (typing[ticketID] ?? []).filter { $0.userID != user && Date.now.timeIntervalSince($0.at) < Self.typingWindow }
            list.append(TypingAgent(userID: user, name: name, at: .now))
            typing[ticketID] = list
            runClock()
        case "customer_typing":
            // contact_id comes from the visitor's token: only the ticket's own contact counts.
            guard let contact = frame.payload["contact_id"]?.string, ticketID == activeTicket, contact == activeContactID else { return }
            customerTypingAt[ticketID] = .now
            runClock()
        case "live":
            guard let type = frame.payload["type"]?.string, let data = frame.payload["data"] else { return }
            if let tid = data["ticket_id"]?.string, tid != ticketID { return }
            applyLive(ticketID, type: type, data: data)
        default:
            break
        }
    }

    private func publishPresence(_ ticketID: String) {
        var out: [PresenceViewer] = []
        for (key, metas) in presenceRaw[ticketID] ?? [:] {
            let viewers = metas.map { meta in
                PresenceViewer(
                    userID: meta["user_id"]?.string ?? key,
                    name: meta["name"]?.string ?? "Someone",
                    avatar: meta["avatar"]?.string,
                    state: PresenceViewer.State(rawValue: meta["state"]?.string ?? "") ?? .viewing
                )
            }
            guard let best = viewers.max(by: { $0.state.rank < $1.state.rank }), best.userID != userID else { continue }
            out.append(best)
        }
        out.sort { $0.state.rank != $1.state.rank ? $0.state.rank > $1.state.rank : $0.name < $1.name }
        presence[ticketID] = out.isEmpty ? nil : out
    }

    private func applyLive(_ ticketID: String, type: String, data: JSONValue) {
        guard let replyID = data["reply_id"]?.string else { return }
        var base = live[ticketID].flatMap { $0.replyID == replyID ? $0 : nil }
            ?? LiveReply(replyID: replyID, ticketID: ticketID, bot: "", text: "", tools: [], at: .now)
        switch type {
        case "ai.reply.started":
            base.bot = data["bot"]?["name"]?.string ?? ""
        case "ai.reply.delta":
            base.text += data["text"]?.string ?? ""
        case "ai.reply.tool":
            let tool = data["tool"]?.string ?? ""
            let status = data["status"]?.string ?? "running"
            if let index = base.tools.firstIndex(where: { $0.name == tool && $0.status == "running" }) {
                base.tools[index].status = status
            } else {
                base.tools.append(.init(name: tool, status: status))
            }
        case "ai.reply.ended":
            guard let messageID = data["message_id"]?.string else {
                live[ticketID] = nil
                return
            }
            base.ended = true
            base.messageID = messageID
            base.handoff = data["handoff"]?.bool ?? false
        default:
            return
        }
        base.at = .now
        live[ticketID] = base
        Task { [weak self] in
            try? await Task.sleep(for: .seconds(Self.liveStale + 0.1))
            guard let self, let current = self.live[ticketID], current.replyID == replyID,
                  Date.now.timeIntervalSince(current.at) >= Self.liveStale
            else { return }
            self.live[ticketID] = nil
        }
    }

    /// The posted message is in the thread: the preview gives way to it.
    func clearLive(ticketID: String, messageID: String) {
        if live[ticketID]?.messageID == messageID { live[ticketID] = nil }
    }

    private func runClock() {
        guard clockTask == nil else { return }
        clockTask = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(1))
                guard let self else { return }
                self.clock = .now
                let fresh = { (date: Date) in Date.now.timeIntervalSince(date) < Self.typingWindow }
                self.typing = self.typing.compactMapValues { list in
                    let kept = list.filter { fresh($0.at) }
                    return kept.isEmpty ? nil : kept
                }
                self.customerTypingAt = self.customerTypingAt.filter { fresh($0.value) }
                if self.typing.isEmpty, self.customerTypingAt.isEmpty {
                    self.clockTask = nil
                    return
                }
            }
        }
    }

    func typingAgents(_ ticketID: String) -> [TypingAgent] {
        _ = clock
        return (typing[ticketID] ?? []).filter { Date.now.timeIntervalSince($0.at) < Self.typingWindow }
    }

    func isCustomerTyping(_ ticketID: String) -> Bool {
        _ = clock
        guard let at = customerTypingAt[ticketID] else { return false }
        return Date.now.timeIntervalSince(at) < Self.typingWindow
    }

    // MARK: Tickets

    /// The ticket on screen: joined for presence, typing and live replies.
    func setActiveTicket(_ id: String?, contactID: String?) {
        activeTicket = id
        activeContactID = contactID
        reconcileTickets()
    }

    /// Tickets visible in the list: joined quietly for presence pulses.
    func setWatched(_ ids: [String]) {
        let next = Array(ids.prefix(Self.maxWatched))
        guard next != watched else { return }
        watched = next
        reconcileTickets()
    }

    func setPresenceState(_ state: PresenceViewer.State) {
        guard let activeTicket, ticketModes[activeTicket] == true else { return }
        push(activeTicket, event: "presence", payload: .object(["state": .string(state.rawValue)]))
    }

    /// Tells the team (the socket) and, on chat, the visitor (the API, which checks the ticket)
    /// that a reply is being written. Throttled to one per 2.5 s.
    func noteTyping() {
        guard let activeTicket, ticketModes[activeTicket] == true, Date.now.timeIntervalSince(lastTypingSent) >= 2.5 else { return }
        lastTypingSent = .now
        push(activeTicket, event: "typing", payload: .object([:]))
        if activeContactID != nil, let api {
            Task {
                let _: EmptyBody? = try? await api.post("v1/tickets/\(activeTicket)/typing")
            }
        }
    }

    private func reconcileTickets() {
        guard socket != nil else { return }
        var desired: [String: Bool] = [:]
        for id in watched { desired[id] = false }
        if let activeTicket { desired[activeTicket] = true }
        let wid = workspaceID
        var leaves: [String] = []
        var joins: [(String, JSONValue)] = []
        for (id, active) in ticketModes where desired[id] != active {
            ticketModes[id] = nil
            presence[id] = nil
            presenceRaw[id] = nil
            if desired[id] == nil {
                live[id] = nil
                typing[id] = nil
            }
            leaves.append("ticket:\(wid):\(id)")
        }
        for (id, active) in desired where ticketModes[id] == nil {
            ticketModes[id] = active
            let payload: JSONValue = active ? .object(["state": .string("viewing")]) : .object(["watch": .bool(true)])
            joins.append(("ticket:\(wid):\(id)", payload))
        }
        guard !leaves.isEmpty || !joins.isEmpty else { return }
        let leaving = leaves
        let joining = joins
        enqueue { socket in
            for topic in leaving { await socket.leave(topic) }
            for (topic, payload) in joining { await socket.join(topic, payload: payload) }
        }
    }

    private func push(_ ticketID: String, event: String, payload: JSONValue) {
        let topic = "ticket:\(workspaceID):\(ticketID)"
        enqueue { socket in await socket.push(topic, event: event, payload: payload) }
    }

    private func enqueue(_ operation: @escaping @Sendable (PhoenixSocket) async -> Void) {
        guard let socket else { return }
        let previous = operations
        operations = Task {
            await previous?.value
            await operation(socket)
        }
    }

    private func ticketID(from topic: String) -> String? {
        let prefix = "ticket:\(workspaceID):"
        guard topic.hasPrefix(prefix) else { return nil }
        return String(topic.dropFirst(prefix.count))
    }

    // MARK: Seq sync (web: realtime/seq.ts)

    private func baseline() async {
        guard let api else { return }
        let started = workspaceID
        guard let page: EventsPage = try? await api.get("v1/events", query: [URLQueryItem(name: "since", value: "0"), URLQueryItem(name: "limit", value: "1")]),
              started == workspaceID
        else { return }
        if lastSeq == nil {
            lastSeq = page.latestSeq
            // Lists loaded before the join can miss events published in between.
            broadcast(.resync)
        }
    }

    private func receive(_ envelope: Envelope) {
        if catching {
            buffer.append(envelope)
            return
        }
        guard let last = lastSeq else {
            applyOne(envelope)
            return
        }
        if envelope.seq <= last { return }
        if envelope.seq == last + 1 {
            applyOne(envelope)
            return
        }
        buffer.append(envelope)
        Task { await catchUp() }
    }

    private func catchUp() async {
        guard !catching, lastSeq != nil, let api else { return }
        catching = true
        let started = workspaceID
        do {
            for _ in 0 ..< 20 {
                guard let last = lastSeq else { break }
                let page: EventsPage = try await api.get("v1/events", query: [URLQueryItem(name: "since", value: String(last))])
                guard started == workspaceID else { return }
                if page.resync {
                    broadcast(.resync)
                    lastSeq = page.latestSeq
                    break
                }
                let fresh = page.data.filter { $0.seq > last }.sorted { $0.seq < $1.seq }
                for envelope in fresh { applyOne(envelope) }
                if fresh.isEmpty || (lastSeq ?? 0) >= page.latestSeq {
                    lastSeq = max(lastSeq ?? 0, page.latestSeq)
                    break
                }
            }
        } catch {
            // Can't reach the events API: reload rather than show stale data.
            broadcast(.resync)
        }
        catching = false
        let pending = buffer.sorted { $0.seq < $1.seq }
        buffer.removeAll()
        for envelope in pending { receive(envelope) }
    }

    private func applyOne(_ envelope: Envelope) {
        lastSeq = max(lastSeq ?? envelope.seq, envelope.seq)
        if seen.contains(envelope.id) { return }
        markSeen(envelope.id)
        apply(envelope, viaUser: false)
    }

    private func markSeen(_ id: String) {
        guard seen.insert(id).inserted else { return }
        seenOrder.append(id)
        if seenOrder.count > 1000 { seen.remove(seenOrder.removeFirst()) }
    }

    private func apply(_ envelope: Envelope, viaUser: Bool) {
        switch envelope.type {
        case "notification.created":
            // The workspace copy carries everyone's notifications; ours arrive on user:<me>.
            guard viaUser || (envelope.topics ?? []).contains("user:\(userID)") else { return }
            if let notification = envelope.member("notification", as: AppNotification.self) {
                onNotification?(notification)
            }
        case "member.removed":
            if envelope.string("user_id") == userID { onMembershipChanged?() }
        case "member.added", "member.updated":
            if let member = envelope.member("member", as: Member.self), member.id == userID {
                onMembershipChanged?()
            }
        case "message.created":
            if let message = envelope.member("message", as: Message.self) {
                let tid = envelope.string("ticket_id") ?? message.ticketID
                clearLive(ticketID: tid, messageID: message.id)
            }
        default:
            break
        }
        broadcast(.envelope(envelope))
    }
}
