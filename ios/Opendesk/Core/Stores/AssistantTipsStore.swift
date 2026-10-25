import Foundation
import Observation

/// Ollie's nudges under the header blob (`GET /v1/assistant/tips`): counts
/// from the queue, never the model. Each one shows at most once a day once
/// it is answered, and all of them can be paused for a week, as on the web.
@MainActor
@Observable
final class AssistantTipsStore {
    private(set) var tip: AssistantTip?

    @ObservationIgnored private let api: APIClient
    @ObservationIgnored private var fetchedAt: Date?
    @ObservationIgnored private var tips: [AssistantTip] = []

    private static let again: TimeInterval = 24 * 3600
    private static let pause: TimeInterval = 7 * 24 * 3600
    private static let fresh: TimeInterval = 60

    init(api: APIClient) {
        self.api = api
    }

    /// Fetches at most once a minute (the route's own cache window) and picks the tip to show.
    func refresh() async {
        if let fetchedAt, Date.now.timeIntervalSince(fetchedAt) < Self.fresh {
            pick()
            return
        }
        guard let answer: AssistantTips = try? await api.get("v1/assistant/tips") else { return }
        fetchedAt = .now
        tips = answer.tips
        pick()
    }

    /// The tip was acted on or put away: not again today.
    func dismiss() {
        guard let tip else { return }
        var memory = Memory.read()
        memory.seen[tip.key] = Date.now.timeIntervalSince1970
        memory.write()
        self.tip = nil
    }

    /// No tips for a week.
    func pauseForAWeek() {
        var memory = Memory.read()
        memory.pausedUntil = Date.now.addingTimeInterval(Self.pause).timeIntervalSince1970
        if let tip { memory.seen[tip.key] = Date.now.timeIntervalSince1970 }
        memory.write()
        tip = nil
    }

    private func pick() {
        let memory = Memory.read()
        let now = Date.now.timeIntervalSince1970
        if let paused = memory.pausedUntil, now < paused {
            tip = nil
            return
        }
        tip = tips.first { tip in
            guard let seen = memory.seen[tip.key] else { return true }
            return now - seen >= Self.again
        }
    }

    /// What was dismissed and when, on this device.
    private struct Memory: Codable {
        var pausedUntil: TimeInterval?
        var seen: [String: TimeInterval] = [:]

        private static let key = "assistant.tips"

        private static var defaults: UserDefaults {
            #if DEBUG
            if DemoMode.isOn { return DemoMode.defaults }
            #endif
            return .standard
        }

        static func read() -> Memory {
            guard let data = defaults.data(forKey: key), let memory = try? JSONDecoder().decode(Memory.self, from: data) else { return Memory() }
            return memory
        }

        func write() {
            if let data = try? JSONEncoder().encode(self) { Self.defaults.set(data, forKey: Self.key) }
        }
    }
}
