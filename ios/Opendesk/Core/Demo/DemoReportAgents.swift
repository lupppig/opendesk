#if DEBUG
import Foundation

// The demo's agents report (`GET /v1/reports/agents`): figures that agree with the
// overview's (each agent's solved count and first reply are the overview's), with
// assignments, replies, notes, handle time and online time made up around them.
// Margin's owner reads every row; Lumen's agent reads their own.

extension DemoBackend {
    func reportAgents(_ query: [String: String], in ws: DemoWorkspace) -> ReportAgents {
        let overview = reportOverview(query, in: ws)
        let periodKey = (query["from"] ?? "") + (query["to"] ?? "") + (query["team_id"] ?? "all")
        let days = Double(max(1, overview.byDay.count))
        let rows = overview.byAgent.map { agent -> ReportAgentWork in
            let id = agent.user.id
            let unit = { (salt: String) in DemoReports.unit(salt + id + periodKey) }
            let solved = agent.solved
            let assigned = Int((Double(solved) * (1.02 + 0.22 * unit("assigned"))).rounded())
            let perSolved = solved > 0 ? ((1.6 + 1.4 * unit("per")) * 10).rounded() / 10 : nil
            let replies = Int((Double(solved) * (perSolved ?? 1.2) * (1.05 + 0.2 * unit("replies"))).rounded())
            let notes = Int((Double(solved) * (0.25 + 0.5 * unit("notes"))).rounded())
            let handle = solved > 0 ? (190 + 420 * unit("handle")).rounded() : nil
            // About five hours a working day, less for part-timers.
            let online = (days * 5 / 7 * 60 * (3.2 + 2.6 * unit("online"))).rounded()
            return ReportAgentWork(
                user: agent.user, assigned: assigned, solved: solved, replies: replies, notes: notes,
                repliesPerSolved: perSolved, firstResponseP50: agent.firstResponseP50, handleP50: handle, onlineMinutes: online
            )
        }
        if ws.role.isAdmin { return ReportAgents(data: rows, onlineTime: .all) }
        return ReportAgents(data: rows.filter { $0.id == world.user.id }, onlineTime: .own)
    }
}
#endif
