import Foundation
import os

/// One Phoenix frame (JSON serializer v2: `[join_ref, ref, topic, event, payload]`).
struct PhoenixFrame: Sendable {
    var joinRef: String?
    var ref: String?
    var topic: String
    var event: String
    var payload: JSONValue

    init(joinRef: String?, ref: String?, topic: String, event: String, payload: JSONValue) {
        self.joinRef = joinRef
        self.ref = ref
        self.topic = topic
        self.event = event
        self.payload = payload
    }

    init?(text: String) {
        guard let value = try? JSONValue.parse(Data(text.utf8)), let parts = value.array, parts.count == 5,
              let topic = parts[2].string, let event = parts[3].string
        else { return nil }
        joinRef = parts[0].string
        ref = parts[1].string
        self.topic = topic
        self.event = event
        payload = parts[4]
    }

    var text: String {
        let array: JSONValue = .array([
            joinRef.map(JSONValue.string) ?? .null,
            ref.map(JSONValue.string) ?? .null,
            .string(topic),
            .string(event),
            payload,
        ])
        let data = (try? APICoding.encoder.encode(array)) ?? Data("[]".utf8)
        return String(decoding: data, as: UTF8.self)
    }
}

/// What the socket tells its owner.
enum SocketSignal: Sendable {
    case connecting
    case open
    case closed
    case joined(topic: String, ok: Bool, response: JSONValue, rejoin: Bool)
    case message(PhoenixFrame)
}

/// Where a socket connects and the token it presents. The token rides in
/// `Sec-WebSocket-Protocol` as the `phoenix` client's `authToken` does, never in the
/// URL (contracts/realtime.md): `phoenix, base64url.bearer.phx.<unpadded base64>`.
struct SocketEndpoint: Sendable {
    let url: URL
    let token: String

    var protocols: [String] {
        let encoded = Data(token.utf8).base64EncodedString().replacingOccurrences(of: "=", with: "")
        return ["phoenix", "base64url.bearer.phx." + encoded]
    }
}

/// A Phoenix Channels client over `URLSessionWebSocketTask`.
/// Heartbeat every 25 s with an 8 s watchdog; reconnects with backoff
/// [120, 350, 800, 1500, 3000, 5000, 10000] ms ±25% and rejoins every topic,
/// asking for a fresh endpoint (a fresh realtime token) each time.
actor PhoenixSocket {
    private struct Channel {
        var payload: JSONValue
        var joinRef: String?
        var joinedOnce = false
    }

    private let endpointProvider: @Sendable () async throws -> SocketEndpoint
    private let continuation: AsyncStream<SocketSignal>.Continuation
    nonisolated let signals: AsyncStream<SocketSignal>

    private let log = Logger(subsystem: "sh.opendesk.app", category: "realtime")
    private let session = URLSession(configuration: .default)
    private var task: URLSessionWebSocketTask?
    private var generation = 0
    private var running = false
    private var opened = false
    private var refCounter = 0
    private var attempt = 0
    private var channels: [String: Channel] = [:]
    private var pendingJoins: [String: String] = [:]
    private var heartbeatRef: String?
    private var heartbeatTask: Task<Void, Never>?

    private static let backoff: [Double] = [120, 350, 800, 1500, 3000, 5000, 10000]

    init(endpointProvider: @escaping @Sendable () async throws -> SocketEndpoint) {
        self.endpointProvider = endpointProvider
        (signals, continuation) = AsyncStream.makeStream(of: SocketSignal.self, bufferingPolicy: .unbounded)
    }

    // MARK: Lifecycle

    func connect() {
        guard !running else { return }
        running = true
        attempt = 0
        Task { await open() }
    }

    func disconnect() {
        running = false
        channels.removeAll()
        pendingJoins.removeAll()
        teardown()
        continuation.yield(.closed)
    }

    func finish() {
        disconnect()
        continuation.finish()
    }

    /// After the app comes back: check the socket with a heartbeat now, or reconnect at once.
    func nudge() {
        guard running else { return }
        if let task, task.state == .running, opened {
            sendHeartbeat()
        } else {
            attempt = 0
            teardown()
            Task { await open() }
        }
    }

    private func open() async {
        guard running, task == nil else { return }
        generation += 1
        let current = generation
        continuation.yield(.connecting)
        let endpoint: SocketEndpoint
        do {
            endpoint = try await endpointProvider()
        } catch {
            if current == generation { scheduleReconnect() }
            return
        }
        guard running, current == generation, task == nil else { return }
        let task = session.webSocketTask(with: endpoint.url, protocols: endpoint.protocols)
        task.maximumMessageSize = 1 << 22
        self.task = task
        opened = false
        task.resume()
        for topic in channels.keys { sendJoin(topic) }
        startHeartbeat(generation: current)
        receive(on: task, generation: current)
    }

    private func teardown() {
        // Anything still running for the old socket sees a newer generation and stops.
        generation += 1
        heartbeatTask?.cancel()
        heartbeatTask = nil
        heartbeatRef = nil
        task?.cancel(with: .goingAway, reason: nil)
        task = nil
        opened = false
        pendingJoins.removeAll()
        for key in channels.keys { channels[key]?.joinRef = nil }
    }

    private func scheduleReconnect() {
        guard running else { return }
        teardown()
        continuation.yield(.closed)
        let base = Self.backoff[min(attempt, Self.backoff.count - 1)]
        attempt += 1
        let delay = (base + base * 0.25 * Double.random(in: -1 ... 1)) / 1000
        let scheduled = generation
        Task {
            try? await Task.sleep(for: .seconds(delay))
            await self.reopen(ifGeneration: scheduled)
        }
    }

    private func reopen(ifGeneration scheduled: Int) async {
        guard scheduled == generation else { return }
        await open()
    }

    // MARK: Channels

    func join(_ topic: String, payload: JSONValue = .object([:])) {
        if channels[topic] != nil { return }
        channels[topic] = Channel(payload: payload)
        if task != nil { sendJoin(topic) }
    }

    func leave(_ topic: String) {
        guard let channel = channels.removeValue(forKey: topic) else { return }
        if task != nil, let joinRef = channel.joinRef {
            send(PhoenixFrame(joinRef: joinRef, ref: nextRef(), topic: topic, event: "phx_leave", payload: .object([:])))
        }
    }

    func push(_ topic: String, event: String, payload: JSONValue) {
        guard let joinRef = channels[topic]?.joinRef, task != nil else { return }
        send(PhoenixFrame(joinRef: joinRef, ref: nextRef(), topic: topic, event: event, payload: payload))
    }

    func joinedTopics() -> Set<String> { Set(channels.keys) }

    private func sendJoin(_ topic: String) {
        guard let channel = channels[topic] else { return }
        let ref = nextRef()
        channels[topic]?.joinRef = ref
        pendingJoins[ref] = topic
        send(PhoenixFrame(joinRef: ref, ref: ref, topic: topic, event: "phx_join", payload: channel.payload))
    }

    // MARK: Wire

    private func nextRef() -> String {
        refCounter += 1
        return String(refCounter)
    }

    private func send(_ frame: PhoenixFrame) {
        guard let task else { return }
        let text = frame.text
        task.send(.string(text)) { _ in }
    }

    private func receive(on task: URLSessionWebSocketTask, generation current: Int) {
        Task {
            while true {
                do {
                    let message = try await task.receive()
                    guard self.handle(message, generation: current) else { return }
                } catch {
                    self.dropped(generation: current)
                    return
                }
            }
        }
    }

    private func dropped(generation current: Int) {
        guard current == generation else { return }
        log.info("realtime socket closed; reconnecting")
        scheduleReconnect()
    }

    /// Returns false once this socket generation is stale.
    private func handle(_ message: URLSessionWebSocketTask.Message, generation current: Int) -> Bool {
        guard current == generation else { return false }
        let text: String
        switch message {
        case let .string(value): text = value
        case let .data(value): text = String(decoding: value, as: UTF8.self)
        @unknown default: return true
        }
        guard let frame = PhoenixFrame(text: text) else { return true }
        if !opened {
            opened = true
            attempt = 0
            continuation.yield(.open)
        }
        if frame.event == "phx_reply" {
            if frame.topic == "phoenix" {
                if frame.ref == heartbeatRef { heartbeatRef = nil }
                return true
            }
            if let ref = frame.ref, let topic = pendingJoins.removeValue(forKey: ref) {
                let ok = frame.payload["status"]?.string == "ok"
                let rejoin = channels[topic]?.joinedOnce ?? false
                if ok { channels[topic]?.joinedOnce = true }
                continuation.yield(.joined(topic: topic, ok: ok, response: frame.payload["response"] ?? .null, rejoin: rejoin))
            }
            return true
        }
        if frame.event == "phx_error" || frame.event == "phx_close" {
            // The server dropped the channel: join it again after a breath.
            let topic = frame.topic
            if channels[topic] != nil, frame.joinRef == channels[topic]?.joinRef {
                channels[topic]?.joinRef = nil
                Task {
                    try? await Task.sleep(for: .seconds(1))
                    self.rejoin(topic)
                }
            }
            return true
        }
        continuation.yield(.message(frame))
        return true
    }

    private func rejoin(_ topic: String) {
        guard task != nil, channels[topic] != nil, channels[topic]?.joinRef == nil else { return }
        sendJoin(topic)
    }

    // MARK: Heartbeat

    private func startHeartbeat(generation current: Int) {
        heartbeatTask?.cancel()
        heartbeatTask = Task {
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(25))
                if Task.isCancelled { return }
                self.heartbeatTick(generation: current)
            }
        }
    }

    private func heartbeatTick(generation current: Int) {
        guard current == generation, task != nil else { return }
        sendHeartbeat()
    }

    private func sendHeartbeat() {
        let ref = nextRef()
        heartbeatRef = ref
        send(PhoenixFrame(joinRef: nil, ref: ref, topic: "phoenix", event: "heartbeat", payload: .object([:])))
        let current = generation
        Task {
            try? await Task.sleep(for: .seconds(8))
            self.checkHeartbeat(ref: ref, generation: current)
        }
    }

    private func checkHeartbeat(ref: String, generation current: Int) {
        guard current == generation, heartbeatRef == ref else { return }
        log.warning("realtime heartbeat went unanswered; reconnecting")
        scheduleReconnect()
    }
}
