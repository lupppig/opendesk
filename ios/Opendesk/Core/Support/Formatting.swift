import Foundation
import Observation
import Synchronization

/// The clock a person reads times on (`User.time_format`), account-wide: "auto"
/// keeps the iPhone's own, "12h" reads 2:05 PM and "24h" 14:05.
enum TimeFormat: String, CaseIterable, Identifiable, Sendable {
    case auto
    case twelveHour = "12h"
    case twentyFourHour = "24h"

    var id: Self { self }

    init(_ raw: String?) {
        self = raw.flatMap(TimeFormat.init(rawValue:)) ?? .auto
    }

    var title: String {
        switch self {
        case .auto: "Automatic"
        case .twelveHour: "12-hour"
        case .twentyFourHour: "24-hour"
        }
    }

    /// A time read on this clock, for the picker: "2:05 PM" or "14:05".
    var example: String {
        let sample = Calendar.current.date(from: DateComponents(year: 2026, month: 1, day: 1, hour: 14, minute: 5)) ?? .now
        return Format.clock(sample, on: self)
    }
}

/// The signed-in person's clock, which every time of day the app shows follows.
/// Observable, so a view that formats a time redraws when the person changes it.
final class ClockSetting: Observable, Sendable {
    static let shared = ClockSetting()

    private struct State {
        var format: TimeFormat = .auto
        var locale: Locale?
        var base = ""
    }

    private let state = Mutex(State())
    private let registrar = ObservationRegistrar()

    var format: TimeFormat {
        get {
            registrar.access(self, keyPath: \.format)
            return state.withLock { $0.format }
        }
        set {
            guard newValue != state.withLock({ $0.format }) else { return }
            registrar.withMutation(of: self, keyPath: \.format) {
                state.withLock { $0 = State(format: newValue) }
            }
        }
    }

    /// The iPhone's locale with the hour cycle of the person's clock.
    var locale: Locale {
        registrar.access(self, keyPath: \.format)
        let current = Locale.autoupdatingCurrent
        return state.withLock { cached in
            if let locale = cached.locale, cached.base == current.identifier { return locale }
            let locale = Self.locale(current, on: cached.format)
            cached.locale = locale
            cached.base = current.identifier
            return locale
        }
    }

    static func locale(_ base: Locale, on format: TimeFormat) -> Locale {
        guard format != .auto else { return base }
        var components = Locale.Components(locale: base)
        components.hourCycle = format == .twelveHour ? .oneToTwelve : .zeroToTwentyThree
        return Locale(components: components)
    }
}

/// Display formatting, matching the web's `lib/format.ts`. Times of day read on
/// the person's clock (`ClockSetting`), everything else on the iPhone's locale.
enum Format {
    /// The iPhone's locale on the person's clock, for formatting and time pickers.
    static var locale: Locale { ClockSetting.shared.locale }

    /// Compact age for dense lists: "now", "5m", "2h", "3d", then a short date.
    static func relShort(_ date: Date?, now: Date = AppClock.now) -> String {
        guard let date else { return "" }
        let seconds = now.timeIntervalSince(date)
        let minutes = Int((seconds / 60).rounded())
        let hours = Int((Double(minutes) / 60).rounded())
        let days = Int((Double(hours) / 24).rounded())
        if days < 7 {
            if seconds < 45 { return "now" }
            if minutes < 60 { return "\(minutes)m" }
            if hours < 24 { return "\(hours)h" }
            return "\(days)d"
        }
        return shortDate(date, now: now)
    }

    /// "just now", "5 minutes ago".
    static func relLong(_ date: Date?, now: Date = AppClock.now) -> String {
        guard let date else { return "" }
        if now.timeIntervalSince(date) < 45 { return "just now" }
        let formatter = RelativeDateTimeFormatter()
        formatter.unitsStyle = .full
        return formatter.localizedString(for: date, relativeTo: now)
    }

    /// "Oct 6", or "Oct 6, 2024" outside the current year.
    static func shortDate(_ date: Date, now: Date = AppClock.now) -> String {
        let calendar = Calendar.current
        if calendar.component(.year, from: date) == calendar.component(.year, from: now) {
            return date.formatted(.dateTime.month(.abbreviated).day())
        }
        return date.formatted(.dateTime.month(.abbreviated).day().year())
    }

    /// "Sat, Oct 11, 2026, 2:05 PM".
    static func fullDate(_ date: Date?) -> String {
        guard let date else { return "" }
        return date.formatted(.dateTime.weekday(.abbreviated).month(.abbreviated).day().year().hour().minute().locale(locale))
    }

    /// Time of day on the person's clock: "2:05 PM" or "14:05".
    static func clock(_ date: Date) -> String {
        date.formatted(.dateTime.hour().minute().locale(locale))
    }

    /// Time of day on a given clock.
    static func clock(_ date: Date, on format: TimeFormat) -> String {
        date.formatted(.dateTime.hour().minute().locale(ClockSetting.locale(.autoupdatingCurrent, on: format)))
    }

    /// Weekday and time of day: "Mon 9:00 AM" or "Mon 09:00", for times within the coming week.
    static func weekdayClock(_ date: Date) -> String {
        date.formatted(.dateTime.weekday(.abbreviated).hour().minute().locale(locale))
    }

    /// A whole hour of the day on the person's clock: "8:00 AM" or "08:00".
    static func hour(_ hour: Int) -> String {
        clock(Calendar.current.date(from: DateComponents(hour: hour, minute: 0)) ?? .now)
    }

    /// "Today", "Yesterday", "Monday, Oct 6", or "Oct 6, 2024".
    static func dayLabel(_ date: Date, now: Date = AppClock.now) -> String {
        let calendar = Calendar.current
        if calendar.isDate(date, inSameDayAs: now) { return "Today" }
        if let yesterday = calendar.date(byAdding: .day, value: -1, to: now), calendar.isDate(date, inSameDayAs: yesterday) {
            return "Yesterday"
        }
        if calendar.component(.year, from: date) == calendar.component(.year, from: now) {
            return date.formatted(.dateTime.weekday(.wide).month(.abbreviated).day())
        }
        return date.formatted(.dateTime.month(.abbreviated).day().year())
    }

    /// 135 → "2h 15m", 2880 → "2d".
    static func minutes(_ value: Double) -> String {
        let total = Int(abs(value).rounded())
        if total < 60 { return "\(total)m" }
        let hours = total / 60
        let rest = total % 60
        if hours < 24 { return rest > 0 ? "\(hours)h \(rest)m" : "\(hours)h" }
        let days = hours / 24
        let restHours = hours % 24
        return restHours > 0 ? "\(days)d \(restHours)h" : "\(days)d"
    }

    static func bytes(_ count: Int) -> String {
        ByteCountFormatter.string(fromByteCount: Int64(count), countStyle: .file)
    }

    static func plural(_ count: Int, _ one: String, _ many: String? = nil) -> String {
        "\(count) \(count == 1 ? one : (many ?? one + "s"))"
    }

    /// Initials for an avatar: up to two letters.
    static func initials(_ name: String) -> String {
        let letters = name.split(whereSeparator: { $0 == " " || $0 == "." || $0 == "@" }).prefix(2).compactMap(\.first)
        return letters.isEmpty ? "?" : String(letters).uppercased()
    }
}

/// The SLA clock of a ticket, as the web's `slaState`.
struct SLAState: Equatable, Sendable {
    enum Kind: Equatable, Sendable { case breached, dueSoon, onTrack }

    var kind: Kind
    var due: Date
    var isFirstResponse: Bool

    static func of(_ ticket: Ticket, now: Date = AppClock.now) -> SLAState? {
        guard let sla = ticket.sla, !ticket.status.isDone else { return nil }
        let firstResponse = ticket.firstRespondedAt == nil && sla.firstResponseDueAt != nil
        guard let due = firstResponse ? sla.firstResponseDueAt : sla.resolutionDueAt else { return nil }
        let left = due.timeIntervalSince(now)
        let kind: Kind = sla.breached || left < 0 ? .breached : (left < 3600 ? .dueSoon : .onTrack)
        return SLAState(kind: kind, due: due, isFirstResponse: firstResponse)
    }

    func label(now: Date = AppClock.now) -> String {
        let left = due.timeIntervalSince(now) / 60
        switch kind {
        case .breached: return left < 0 ? "Overdue \(Format.minutes(left))" : "Breached"
        case .dueSoon, .onTrack: return "Due in \(Format.minutes(left))"
        }
    }
}

/// Snooze presets, as on the web.
enum SnoozePreset: CaseIterable, Identifiable {
    case oneHour, fourHours, tomorrow, nextWeek

    var id: Self { self }

    var title: String {
        switch self {
        case .oneHour: "In 1 hour"
        case .fourHours: "In 4 hours"
        case .tomorrow: "Tomorrow"
        case .nextWeek: "Next week"
        }
    }

    func date(from now: Date = AppClock.now, calendar: Calendar = .current) -> Date {
        let startOfHour = calendar.dateInterval(of: .hour, for: now)?.start ?? now
        switch self {
        case .oneHour:
            return calendar.date(byAdding: .hour, value: 2, to: startOfHour) ?? now.addingTimeInterval(3600)
        case .fourHours:
            return calendar.date(byAdding: .hour, value: 5, to: startOfHour) ?? now.addingTimeInterval(4 * 3600)
        case .tomorrow:
            let tomorrow = calendar.date(byAdding: .day, value: 1, to: calendar.startOfDay(for: now)) ?? now
            return calendar.date(bySettingHour: 9, minute: 0, second: 0, of: tomorrow) ?? tomorrow
        case .nextWeek:
            var components = DateComponents()
            components.weekday = 2
            components.hour = 9
            let next = calendar.nextDate(after: now, matching: components, matchingPolicy: .nextTime) ?? now.addingTimeInterval(7 * 86400)
            return next
        }
    }

    func subtitle(from now: Date = AppClock.now) -> String {
        let date = date(from: now)
        let calendar = Calendar.current
        if calendar.isDate(date, inSameDayAs: now) { return Format.clock(date) }
        return Format.weekdayClock(date)
    }
}

/// A live chat visitor waiting for a reply (the web's `chatWait`): calm and
/// violet while Ollie is on it, urgent red once a person must answer.
struct ChatWait: Equatable, Sendable {
    enum State: Equatable, Sendable { case ollie, urgent }

    var since: Date
    var state: State
    var handedOff: Bool

    /// How long Ollie may take before a waiting visitor needs a person.
    static let ollieGrace: TimeInterval = 60

    static func of(_ ticket: Ticket, agentOn: Bool, now: Date = AppClock.now) -> ChatWait? {
        guard ticket.channel == .chat, !ticket.spam, ticket.status == .open || ticket.status == .pending else { return nil }
        let handedOff = ticket.aiHandedOff
        let handoffAt = ticket.custom["ai_handoff_at"]?.string.flatMap(parseDate)
        var since = ticket.waitingSince
        // Ollie's handoff line answers the visitor, but they now wait for the team, unless a person already replied after it.
        if since == nil, handedOff, let handoffAt {
            let teamReplied = ticket.firstRespondedAt.map { $0 >= handoffAt } ?? false
            if !teamReplied { since = handoffAt }
        }
        guard let since else { return nil }
        let ollieOn = agentOn && !handedOff && ticket.assignee == nil
        let state: State = ollieOn && now.timeIntervalSince(since) < ollieGrace ? .ollie : .urgent
        return ChatWait(since: since, state: state, handedOff: handedOff)
    }

    /// 45s, 3m 12s, 1h 04m, 2d 3h.
    static func format(_ interval: TimeInterval) -> String {
        let s = max(0, Int(interval))
        if s < 60 { return "\(s)s" }
        let m = s / 60
        if m < 60 { return "\(m)m " + String(format: "%02d", s % 60) + "s" }
        let h = m / 60
        if h < 24 { return "\(h)h " + String(format: "%02d", m % 60) + "m" }
        return "\(h / 24)d \(h % 24)h"
    }

    private static func parseDate(_ raw: String) -> Date? {
        (try? Date.ISO8601FormatStyle(includingFractionalSeconds: true).parse(raw)) ?? (try? Date.ISO8601FormatStyle().parse(raw))
    }
}
