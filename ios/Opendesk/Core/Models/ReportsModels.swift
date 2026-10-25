import Foundation

// `GET /v1/reports/overview` (contracts/api.md). Every figure is optional or
// defaulted so an older server that omits a breakdown never fails the screen.

struct ReportDay: Codable, Hashable, Sendable, Identifiable {
    var date: String
    var created: Int
    var solved: Int

    var id: String { date }
    var day: Date { ReportRange.parse(date) ?? .distantPast }
}

struct ReportAgentRow: Codable, Hashable, Sendable, Identifiable {
    var user: UserRef
    var solved: Int
    var firstResponseP50: Double?

    var id: String { user.id }

    enum CodingKeys: String, CodingKey {
        case user, solved
        case firstResponseP50 = "first_response_p50_minutes"
    }

    init(user: UserRef, solved: Int, firstResponseP50: Double?) {
        self.user = user
        self.solved = solved
        self.firstResponseP50 = firstResponseP50
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        user = try c.decode(UserRef.self, forKey: .user)
        solved = c.lenient(Int.self, forKey: .solved) ?? 0
        firstResponseP50 = c.lenient(Double.self, forKey: .firstResponseP50)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(user, forKey: .user)
        try c.encode(solved, forKey: .solved)
        try c.encode(firstResponseP50, forKey: .firstResponseP50)
    }
}

/// One team's row of the overview; `teamID` nil counts tickets without a team.
struct ReportTeamRow: Codable, Hashable, Sendable {
    var teamID: String?
    var created: Int
    var solved: Int
    var openNow: Int
    var firstResponseP50: Double?

    enum CodingKeys: String, CodingKey {
        case created, solved
        case teamID = "team_id"
        case openNow = "open_now"
        case firstResponseP50 = "first_response_p50_minutes"
    }

    init(teamID: String?, created: Int, solved: Int, openNow: Int, firstResponseP50: Double?) {
        self.teamID = teamID
        self.created = created
        self.solved = solved
        self.openNow = openNow
        self.firstResponseP50 = firstResponseP50
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        teamID = c.lenient(String.self, forKey: .teamID)
        created = c.lenient(Int.self, forKey: .created) ?? 0
        solved = c.lenient(Int.self, forKey: .solved) ?? 0
        openNow = c.lenient(Int.self, forKey: .openNow) ?? 0
        firstResponseP50 = c.lenient(Double.self, forKey: .firstResponseP50)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(teamID, forKey: .teamID)
        try c.encode(created, forKey: .created)
        try c.encode(solved, forKey: .solved)
        try c.encode(openNow, forKey: .openNow)
        try c.encode(firstResponseP50, forKey: .firstResponseP50)
    }
}

struct ReportOverview: Codable, Hashable, Sendable {
    var created: Int
    var solved: Int
    var openNow: Int
    var firstResponseP50: Double?
    var resolutionP50: Double?
    var csatAvg: Double?
    var csatCount: Int
    var slaBreached: Int
    var byDay: [ReportDay]
    var byChannel: [String: Int]
    var bySeverity: [String: Int]?
    var byAgent: [ReportAgentRow]
    var byTeam: [ReportTeamRow]?

    enum CodingKeys: String, CodingKey {
        case created, solved
        case openNow = "open_now"
        case firstResponseP50 = "first_response_p50_minutes"
        case resolutionP50 = "resolution_p50_minutes"
        case csatAvg = "csat_avg"
        case csatCount = "csat_count"
        case slaBreached = "sla_breached"
        case byDay = "by_day"
        case byChannel = "by_channel"
        case bySeverity = "by_severity"
        case byAgent = "by_agent"
        case byTeam = "by_team"
    }

    init(created: Int, solved: Int, openNow: Int, firstResponseP50: Double?, resolutionP50: Double?, csatAvg: Double?, csatCount: Int, slaBreached: Int, byDay: [ReportDay], byChannel: [String: Int], bySeverity: [String: Int]?, byAgent: [ReportAgentRow], byTeam: [ReportTeamRow]?) {
        self.created = created
        self.solved = solved
        self.openNow = openNow
        self.firstResponseP50 = firstResponseP50
        self.resolutionP50 = resolutionP50
        self.csatAvg = csatAvg
        self.csatCount = csatCount
        self.slaBreached = slaBreached
        self.byDay = byDay
        self.byChannel = byChannel
        self.bySeverity = bySeverity
        self.byAgent = byAgent
        self.byTeam = byTeam
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        created = c.lenient(Int.self, forKey: .created) ?? 0
        solved = c.lenient(Int.self, forKey: .solved) ?? 0
        openNow = c.lenient(Int.self, forKey: .openNow) ?? 0
        firstResponseP50 = c.lenient(Double.self, forKey: .firstResponseP50)
        resolutionP50 = c.lenient(Double.self, forKey: .resolutionP50)
        csatAvg = c.lenient(Double.self, forKey: .csatAvg)
        csatCount = c.lenient(Int.self, forKey: .csatCount) ?? 0
        slaBreached = c.lenient(Int.self, forKey: .slaBreached) ?? 0
        byDay = c.lenient([ReportDay].self, forKey: .byDay) ?? []
        byChannel = c.lenient([String: Int].self, forKey: .byChannel) ?? [:]
        bySeverity = c.lenient([String: Int].self, forKey: .bySeverity)
        byAgent = c.lenient([ReportAgentRow].self, forKey: .byAgent) ?? []
        byTeam = c.lenient([ReportTeamRow].self, forKey: .byTeam)
    }

    func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(created, forKey: .created)
        try c.encode(solved, forKey: .solved)
        try c.encode(openNow, forKey: .openNow)
        try c.encode(firstResponseP50, forKey: .firstResponseP50)
        try c.encode(resolutionP50, forKey: .resolutionP50)
        try c.encode(csatAvg, forKey: .csatAvg)
        try c.encode(csatCount, forKey: .csatCount)
        try c.encode(slaBreached, forKey: .slaBreached)
        try c.encode(byDay, forKey: .byDay)
        try c.encode(byChannel, forKey: .byChannel)
        try c.encodeIfPresent(bySeverity, forKey: .bySeverity)
        try c.encode(byAgent, forKey: .byAgent)
        try c.encodeIfPresent(byTeam, forKey: .byTeam)
    }

    var isEmpty: Bool { created == 0 && solved == 0 && openNow == 0 }
    var solveRate: Double? { created > 0 ? Double(solved) / Double(created) : nil }
}

/// The report's period: a preset ending today, or custom days. Days are calendar days in the
/// device's time zone, sent as `yyyy-MM-dd`.
struct ReportRange: Hashable, Sendable {
    enum Preset: Int, CaseIterable, Identifiable, Sendable {
        case week = 7, fortnight = 14, month = 30, quarter = 90
        var id: Int { rawValue }
        var title: String { "Last \(rawValue) days" }
        var short: String { "\(rawValue)d" }
    }

    var from: Date
    var to: Date
    var preset: Preset?

    static func preset(_ preset: Preset, now: Date = AppClock.now) -> ReportRange {
        let calendar = Calendar.current
        let today = calendar.startOfDay(for: now)
        let start = calendar.date(byAdding: .day, value: -(preset.rawValue - 1), to: today) ?? today
        return ReportRange(from: start, to: today, preset: preset)
    }

    static func custom(from: Date, to: Date) -> ReportRange {
        let calendar = Calendar.current
        let a = calendar.startOfDay(for: min(from, to))
        let b = calendar.startOfDay(for: max(from, to))
        return ReportRange(from: a, to: b, preset: nil)
    }

    var days: Int { (Calendar.current.dateComponents([.day], from: from, to: to).day ?? 0) + 1 }

    /// The same-length period just before this one, for deltas.
    var previous: ReportRange {
        let calendar = Calendar.current
        let end = calendar.date(byAdding: .day, value: -1, to: from) ?? from
        let start = calendar.date(byAdding: .day, value: -(days - 1), to: end) ?? end
        return ReportRange(from: start, to: end, preset: nil)
    }

    var title: String {
        if let preset { return preset.title }
        let sameYear = Calendar.current.component(.year, from: from) == Calendar.current.component(.year, from: to)
        let a = sameYear ? from.formatted(.dateTime.month(.abbreviated).day()) : from.formatted(.dateTime.month(.abbreviated).day().year())
        return "\(a) to \(to.formatted(.dateTime.month(.abbreviated).day().year()))"
    }

    var shortTitle: String {
        if let preset { return preset.title }
        return "\(from.formatted(.dateTime.month(.abbreviated).day())) to \(to.formatted(.dateTime.month(.abbreviated).day()))"
    }

    var query: [URLQueryItem] {
        [URLQueryItem(name: "from", value: Self.key(from)), URLQueryItem(name: "to", value: Self.key(to))]
    }

    static func key(_ date: Date) -> String {
        let c = Calendar.current.dateComponents([.year, .month, .day], from: date)
        return String(format: "%04d-%02d-%02d", c.year ?? 0, c.month ?? 0, c.day ?? 0)
    }

    static func parse(_ key: String) -> Date? {
        let parts = key.split(separator: "-").compactMap { Int($0) }
        guard parts.count == 3 else { return nil }
        return Calendar.current.date(from: DateComponents(year: parts[0], month: parts[1], day: parts[2]))
    }
}

/// The team filter: every team, one team, or tickets without a team (`none`).
enum ReportTeamFilter: Hashable, Sendable {
    case all
    case team(String)
    case none

    var queryValue: String? {
        switch self {
        case .all: nil
        case let .team(id): id
        case .none: "none"
        }
    }
}
