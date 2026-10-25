import SwiftUI

/// One headline number of the report and how it moved against the period before.
struct ReportKPI: Identifiable {
    enum Change {
        case percent(Double)
        case points(Double)
    }

    let id: String
    let title: String
    let symbol: String
    let tone: Tone
    let value: String
    var change: Change?
    /// Whether a rise is good news; nil when it is neither (tickets created).
    var upIsGood: Bool?
    var series: [Double] = []
    var note: String?

    var changeText: String? {
        switch change {
        case let .percent(value)?:
            let percent = Int((value * 100).rounded())
            if percent == 0 { return "0%" }
            return (percent > 0 ? "+" : "\u{2212}") + "\(abs(percent))%"
        case let .points(value)?:
            let rounded = (value * 10).rounded() / 10
            if rounded == 0 { return "0.0" }
            return (rounded > 0 ? "+" : "\u{2212}") + String(format: "%.1f", abs(rounded))
        case nil:
            return nil
        }
    }

    var changeTone: Tone {
        let value: Double = switch change {
        case let .percent(v)?: (v * 100).rounded()
        case let .points(v)?: (v * 10).rounded()
        case nil: 0
        }
        guard value != 0, let upIsGood else { return .gray }
        return (value > 0) == upIsGood ? .green : .red
    }

    var changeSymbol: String? {
        let value: Double = switch change {
        case let .percent(v)?: (v * 100).rounded()
        case let .points(v)?: (v * 10).rounded()
        case nil: 0
        }
        if value == 0 { return nil }
        return value > 0 ? "arrow.up.right" : "arrow.down.right"
    }
}

/// A labelled count for the bar and donut cards.
struct ReportSlice: Identifiable, Hashable {
    let id: String
    let label: String
    let value: Double
    let tone: Tone
    var symbol: String?
    var display: String?
}

/// Everything the screen shows, worked out once from the store and the directory.
@MainActor
struct ReportFigures {
    let overview: ReportOverview
    let previous: ReportOverview?
    let open: [Ticket]
    let openPartial: Bool
    let directory: DirectoryStore

    var kpis: [ReportKPI] {
        let o = overview, p = previous
        func pct(_ now: Double?, _ before: Double?) -> ReportKPI.Change? {
            guard let now, let before, before > 0 else { return nil }
            return .percent((now - before) / before)
        }
        let created = o.byDay.map { Double($0.created) }
        let solved = o.byDay.map { Double($0.solved) }
        return [
            ReportKPI(id: "created", title: "Created", symbol: "tray.and.arrow.down", tone: .blue, value: o.created.formatted(),
                      change: pct(Double(o.created), p.map { Double($0.created) }), upIsGood: nil, series: created),
            ReportKPI(id: "solved", title: "Solved", symbol: "checkmark.circle", tone: .green, value: o.solved.formatted(),
                      change: pct(Double(o.solved), p.map { Double($0.solved) }), upIsGood: true, series: solved),
            ReportKPI(id: "first", title: "First response", symbol: "bolt", tone: .violet, value: o.firstResponseP50.map(Format.minutes) ?? "None",
                      change: pct(o.firstResponseP50, p?.firstResponseP50), upIsGood: false, note: "median"),
            ReportKPI(id: "resolution", title: "Resolution", symbol: "timer", tone: .teal, value: o.resolutionP50.map(Format.minutes) ?? "None",
                      change: pct(o.resolutionP50, p?.resolutionP50), upIsGood: false, note: "median"),
            ReportKPI(id: "csat", title: "CSAT", symbol: "star", tone: .amber, value: o.csatAvg.map { String(format: "%.1f", $0) } ?? "None",
                      change: (o.csatAvg != nil && p?.csatAvg != nil) ? .points(o.csatAvg! - p!.csatAvg!) : nil, upIsGood: true,
                      note: o.csatCount > 0 ? Format.plural(o.csatCount, "rating") : nil),
            ReportKPI(id: "sla", title: "SLA breached", symbol: "exclamationmark.triangle", tone: .red, value: o.slaBreached.formatted(),
                      change: pct(Double(o.slaBreached), p.map { Double($0.slaBreached) }), upIsGood: false),
            ReportKPI(id: "open", title: "Open now", symbol: "circle.dashed", tone: .orange, value: o.openNow.formatted(), note: "right now"),
            ReportKPI(id: "rate", title: "Solve rate", symbol: "percent", tone: .indigo,
                      value: o.solveRate.map { "\(Int(($0 * 100).rounded()))%" } ?? "None",
                      change: (o.solveRate != nil && p?.solveRate != nil) ? .percent(o.solveRate! - p!.solveRate!) : nil, upIsGood: true,
                      note: "solved per created"),
        ]
    }

    var channels: [ReportSlice] {
        let order = ["email", "chat", "api", "portal"]
        let keys = overview.byChannel.keys.sorted { (order.firstIndex(of: $0) ?? 99, $0) < (order.firstIndex(of: $1) ?? 99, $1) }
        return keys.compactMap { key in
            guard let value = overview.byChannel[key], value > 0 else { return nil }
            let channel = Channel(rawValue: key)
            return ReportSlice(id: key, label: channel?.title ?? key.capitalized, value: Double(value), tone: channel?.tone ?? .gray, symbol: channel?.symbol)
        }
    }

    var severities: [ReportSlice] {
        guard let by = overview.bySeverity else { return [] }
        let rows: [ReportSlice] = Severity.allCases.map { severity in
            ReportSlice(id: severity.rawValue, label: severity.title, value: Double(by[severity.rawValue] ?? 0), tone: severity.tone)
        }
        return rows + [ReportSlice(id: "none", label: "No severity", value: Double(by["none"] ?? 0), tone: .gray)]
    }

    var priorities: [ReportSlice] {
        Priority.allCases.map { priority in
            ReportSlice(id: priority.rawValue, label: priority.title, value: Double(open.filter { $0.priority == priority }.count), tone: priority.tone, symbol: priority.symbol)
        }
    }

    var backlog: [ReportSlice] {
        [TicketStatus.open, .pending, .onHold].map { status in
            ReportSlice(id: status.rawValue, label: status.title, value: Double(open.filter { $0.status == status }.count), tone: status.tone)
        }
    }

    var unassigned: Int { open.filter { $0.assignee == nil }.count }
    var breaching: Int { open.filter { $0.sla?.breached == true }.count }
    var oldest: Ticket? { open.min { $0.createdAt < $1.createdAt } }

    var labels: [ReportSlice] {
        var counts: [String: (TagRef, Int)] = [:]
        for ticket in open {
            for tag in ticket.tags { counts[tag.name.lowercased(), default: (tag, 0)].1 += 1 }
        }
        return counts.values.sorted { $0.1 != $1.1 ? $0.1 > $1.1 : $0.0.name < $1.0.name }.prefix(8).map { tag, count in
            ReportSlice(id: tag.id, label: tag.name, value: Double(count), tone: Tone(label: tag.color ?? directory.tag(named: tag.name)?.color))
        }
    }

    var teams: [ReportTeamRow] {
        (overview.byTeam ?? []).filter { $0.created + $0.solved + $0.openNow > 0 }.sorted { $0.created > $1.created }
    }

    func teamName(_ id: String?) -> String {
        guard let id else { return "No team" }
        return directory.team(id)?.name ?? "Team"
    }

    var teamResponse: [ReportSlice] {
        teams.compactMap { row in
            guard let minutes = row.firstResponseP50 else { return nil }
            let index = directory.teams.firstIndex { $0.id == row.teamID } ?? 99
            return ReportSlice(id: row.teamID ?? "none", label: teamName(row.teamID), value: minutes, tone: row.teamID == nil ? .gray : ReportsPalette.series[index % ReportsPalette.series.count], display: Format.minutes(minutes))
        }
    }
}

enum ReportsPalette {
    static let series: [Tone] = [.blue, .violet, .teal, .amber, .pink, .green]
    static let created = Tone.blue
    static let solved = Tone.green
}
