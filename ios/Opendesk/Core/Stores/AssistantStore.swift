import Foundation
import Observation

/// A conversation with Ollie: streamed answers, tool steps, and approvals
/// for anything that would change data (contracts/ai.md "Ollie"). Each
/// assistant turn keeps its text and steps in the order they happened, as
/// the web's blocks, so a transcript reads the way the run went.
@MainActor
@Observable
final class AssistantStore {
    enum Role: String, Hashable, Sendable { case user, assistant }

    /// A change the user decided on, kept in the turn as the card's result.
    struct Decision: Hashable, Sendable {
        var approval: AssistantApproval
        var approved: Bool
    }

    struct Block: Identifiable, Hashable, Sendable {
        enum Kind: Hashable, Sendable {
            case text(String)
            case step(AssistantStep)
            case decision(Decision)
        }

        var id: String
        var kind: Kind

        var step: AssistantStep? {
            if case let .step(step) = kind { return step }
            return nil
        }

        var text: String? {
            if case let .text(text) = kind { return text }
            return nil
        }
    }

    struct Turn: Identifiable, Hashable, Sendable {
        var id = UUID()
        var role: Role
        /// What the user wrote (assistant turns read their blocks).
        var text = ""
        var blocks: [Block] = []
        /// Why this turn's run failed, shown with a retry.
        var error: String?

        var answer: String {
            blocks.compactMap(\.text).joined(separator: "\n\n")
        }

        var refs: [AssistantRef] {
            blocks.flatMap { $0.step?.refs ?? [] }
        }
    }

    enum Outcome: Equatable, Sendable { case none, done, failed }

    private(set) var session: AssistantSession?
    private(set) var turns: [Turn] = []
    private(set) var approval: AssistantApproval?
    private(set) var running = false
    private(set) var deciding = false
    private(set) var loading = false
    /// Why the conversation picked from the history didn't open.
    private(set) var loadError: String?
    /// How the last run ended, for the blob's cheer and the haptics.
    private(set) var outcome: Outcome = .none
    /// Bumps when a run ends, so views can react once per run.
    private(set) var finishedRuns = 0
    /// Bumps when the session list may have changed (a new title, a new conversation).
    private(set) var sessionsChanged = 0

    var title: String? {
        guard let title = session?.title?.trimmingCharacters(in: .whitespacesAndNewlines), !title.isEmpty else { return nil }
        return title
    }

    var isEmpty: Bool { turns.isEmpty && session == nil }

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private var runTask: Task<Void, Never>?
    @ObservationIgnored private var runID = 0

    init(api: APIClient) {
        self.api = api
    }

    // MARK: Conversations

    /// A fresh, empty conversation; the session is created with the first message.
    func newChat() {
        cancelRun()
        session = nil
        turns = []
        approval = nil
        loading = false
        loadError = nil
        outcome = .none
    }

    /// Opens a past conversation from the history.
    func open(_ target: AssistantSession) async {
        guard target.id != session?.id || turns.isEmpty || loadError != nil else { return }
        cancelRun()
        session = target
        turns = []
        approval = nil
        outcome = .none
        loadError = nil
        loading = true
        defer { if session?.id == target.id { loading = false } }
        do {
            let transcript: AssistantTranscript = try await api.get("v1/assistant/sessions/\(target.id)")
            guard session?.id == target.id else { return }
            session = transcript.session
            turns = Self.turns(from: transcript)
            approval = transcript.approval
        } catch {
            guard session?.id == target.id, !error.isCancellation else { return }
            loadError = error.userMessage
        }
    }

    /// The history renamed the open conversation.
    func renamed(_ updated: AssistantSession) {
        guard updated.id == session?.id else { return }
        session = updated
    }

    /// The history deleted a conversation; the open one starts over.
    func deleted(_ id: String) {
        guard id == session?.id else { return }
        newChat()
    }

    /// Transcript items to turns: consecutive assistant items merge into one.
    static func turns(from transcript: AssistantTranscript) -> [Turn] {
        var out: [Turn] = []
        for item in transcript.items {
            if item.role == Role.user.rawValue {
                out.append(Turn(role: .user, text: item.text))
                continue
            }
            if out.last?.role != .assistant { out.append(Turn(role: .assistant)) }
            let last = out.count - 1
            if !item.text.isEmpty { out[last].blocks.append(Block(id: UUID().uuidString, kind: .text(item.text))) }
            for step in item.steps ?? [] {
                out[last].blocks.append(Block(id: step.id, kind: .step(step)))
            }
        }
        return out
    }

    // MARK: Runs

    func send(_ text: String, context: AssistantPageContext?) {
        let trimmed = String(text.trimmingCharacters(in: .whitespacesAndNewlines).prefix(8000))
        guard !trimmed.isEmpty, !running, !loading else { return }
        // A new message expires a waiting proposal on the server; the card goes with it.
        approval = nil
        turns.append(Turn(role: .user, text: trimmed))
        turns.append(Turn(role: .assistant))
        start { store in
            if store.session == nil {
                struct NewSession: Encodable, Sendable {}
                let created: AssistantSession = try await store.api.post("v1/assistant/sessions", body: NewSession())
                store.session = created
                store.sessionsChanged += 1
            }
            guard let id = store.session?.id else { return }
            struct Body: Encodable, Sendable {
                let text: String
                let context: AssistantPageContext?
            }
            let stream = try await store.api.stream("v1/assistant/sessions/\(id)/messages", body: Body(text: trimmed, context: context))
            try await store.consume(stream)
        }
    }

    /// Sends the failed message again, in place of the failed answer.
    func retry(context: AssistantPageContext?) {
        guard !running, let failed = turns.lastIndex(where: { $0.error != nil }) else { return }
        let question = turns[..<failed].last { $0.role == .user }
        turns.removeSubrange(max(0, failed - (question == nil ? 0 : 1))...)
        if let question { send(question.text, context: context) }
    }

    func stop() {
        guard running else { return }
        cancelRun()
        // Whatever arrived stays; the turn just ends here.
        if let last = turns.indices.last, turns[last].role == .assistant, turns[last].blocks.isEmpty, turns[last].error == nil {
            turns.removeLast()
        }
    }

    func decide(approve: Bool) {
        guard let pending = approval, !deciding, !running else { return }
        approval = nil
        deciding = true
        // The result sits where the proposal was: after its step, else at the end of the turn.
        if turns.last?.role != .assistant { turns.append(Turn(role: .assistant)) }
        let last = turns.count - 1
        let block = Block(id: "decision-" + pending.actionID, kind: .decision(Decision(approval: pending, approved: approve)))
        if let at = turns[last].blocks.lastIndex(where: { $0.step?.actionID == pending.actionID }) {
            turns[last].blocks.insert(block, at: at + 1)
        } else {
            turns[last].blocks.append(block)
        }
        start { store in
            defer { store.deciding = false }
            struct Body: Encodable, Sendable {
                let decision: String
                let token: String
            }
            let stream = try await store.api.stream("v1/assistant/actions/\(pending.actionID)/decision", body: Body(decision: approve ? "approve" : "deny", token: pending.token))
            try await store.consume(stream)
        }
    }

    private func cancelRun() {
        runTask?.cancel()
        runTask = nil
        runID += 1
        running = false
        deciding = false
    }

    private func start(_ work: @escaping @MainActor (AssistantStore) async throws -> Void) {
        cancelRun()
        let id = runID
        running = true
        outcome = .none
        runTask = Task { [weak self] in
            guard let self else { return }
            var failed = false
            do {
                try await work(self)
            } catch {
                guard !error.isCancellation, self.runID == id else { return }
                failed = true
                self.fail(error.userMessage)
            }
            guard self.runID == id else { return }
            if self.turns.last?.error != nil { failed = true }
            self.running = false
            self.deciding = false
            self.outcome = failed ? .failed : (self.approval == nil ? .done : .none)
            self.finishedRuns += 1
            self.sessionsChanged += 1
        }
    }

    private func fail(_ message: String) {
        if turns.last?.role != .assistant { turns.append(Turn(role: .assistant)) }
        turns[turns.count - 1].error = message
    }

    /// The assistant turn the stream writes into.
    private func answerIndex() -> Int {
        if turns.last?.role != .assistant { turns.append(Turn(role: .assistant)) }
        return turns.count - 1
    }

    private func consume(_ stream: AsyncThrowingStream<SSEEvent, Error>) async throws {
        for try await event in stream {
            try Task.checkCancellation()
            guard let json = event.json() else { continue }
            switch event.event {
            case "session":
                if let value = try? json.decode(AssistantSession.self) {
                    session = value
                    sessionsChanged += 1
                }
            case "text":
                let delta = json["text"]?.string ?? ""
                guard !delta.isEmpty else { continue }
                let index = answerIndex()
                if let last = turns[index].blocks.indices.last, case let .text(existing) = turns[index].blocks[last].kind {
                    turns[index].blocks[last].kind = .text(existing + delta)
                } else {
                    turns[index].blocks.append(Block(id: UUID().uuidString, kind: .text(delta)))
                }
            case "tool":
                guard let step = try? json.decode(AssistantStep.self) else { continue }
                let index = answerIndex()
                if let existing = turns[index].blocks.firstIndex(where: { $0.id == step.id && $0.step != nil }) {
                    turns[index].blocks[existing].kind = .step(step)
                } else {
                    turns[index].blocks.append(Block(id: step.id, kind: .step(step)))
                }
            case "approval":
                approval = try? json.decode(AssistantApproval.self)
            case "error":
                fail(APIError.parse(status: json["status"]?.int ?? 500, data: Data(event.data.utf8)).userMessage)
            case "result":
                // The server answered in one piece.
                guard let result = try? json.decode(AssistantRunResult.self) else { continue }
                if let value = result.session { session = value }
                let index = answerIndex()
                var blocks: [Block] = []
                if let text = result.text, !text.isEmpty { blocks.append(Block(id: UUID().uuidString, kind: .text(text))) }
                blocks += (result.steps ?? []).map { Block(id: $0.id, kind: .step($0)) }
                turns[index].blocks = blocks
                approval = result.approval
            default:
                break
            }
        }
        // A run that wrote nothing and asked nothing still answers something.
        if let last = turns.indices.last, turns[last].role == .assistant, turns[last].blocks.isEmpty, turns[last].error == nil, approval == nil {
            turns.removeLast()
        }
    }

    #if DEBUG
    /// The demo's next run fails partway, to show the error and its retry.
    func demoFailNext() async {
        let _: EmptyBody? = try? await api.post("v1/assistant/demo/fail-next")
    }
    #endif
}
