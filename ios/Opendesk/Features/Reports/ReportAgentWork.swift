import SwiftUI

/// A figure of the agents report, to sort by and to show.
enum ReportAgentColumn: String, CaseIterable, Identifiable {
    case name, assigned, solved, replies, notes, perSolved, firstResponse, handle, online

    var id: Self { self }

    var title: String {
        switch self {
        case .name: "Name"
        case .assigned: "Assigned"
        case .solved: "Solved"
        case .replies: "Replies"
        case .notes: "Notes"
        case .perSolved: "Replies per solve"
        case .firstResponse: "First response"
        case .handle: "Handle time"
        case .online: "Online"
        }
    }

    var hint: String {
        switch self {
        case .name: ""
        case .assigned: "Tickets assigned to them in the period, and new tickets created already assigned to them"
        case .solved: "Tickets solved in the period that are assigned to them"
        case .replies: "Replies they sent to customers in the period"
        case .notes: "Internal notes they wrote in the period"
        case .perSolved: "Their replies on the tickets they solved, per ticket"
        case .firstResponse: "Median time to the first reply on their tickets created in the period"
        case .handle: "Median time from the last assignment to them until the ticket was solved"
        case .online: "Time in opendesk with their status set to Online"
        }
    }

    func number(_ row: ReportAgentWork) -> Double? {
        switch self {
        case .name: nil
        case .assigned: Double(row.assigned)
        case .solved: Double(row.solved)
        case .replies: Double(row.replies)
        case .notes: Double(row.notes)
        case .perSolved: row.repliesPerSolved
        case .firstResponse: row.firstResponseP50
        case .handle: row.handleP50
        case .online: row.onlineMinutes
        }
    }

    func text(_ row: ReportAgentWork) -> String {
        switch self {
        case .name: row.user.name
        case .perSolved: row.repliesPerSolved.map { $0.formatted(.number.precision(.fractionLength(1))) } ?? "None"
        case .firstResponse, .handle, .online: number(row).map(Format.minutes) ?? "None"
        case .assigned, .solved, .replies, .notes: Int(number(row) ?? 0).formatted()
        }
    }

    /// The rows in this column's order: empty figures last whichever way it runs, then by name.
    static func sorted(_ rows: [ReportAgentWork], by column: ReportAgentColumn, ascending: Bool) -> [ReportAgentWork] {
        rows.sorted { a, b in
            let byName = a.user.name.localizedCaseInsensitiveCompare(b.user.name)
            if column == .name {
                if byName != .orderedSame { return ascending ? byName == .orderedAscending : byName == .orderedDescending }
            } else {
                switch (column.number(a), column.number(b)) {
                case let (x?, y?) where x != y: return ascending ? x < y : x > y
                case (nil, _?): return false
                case (_?, nil): return true
                default: break
                }
            }
            return byName == .orderedSame ? a.id < b.id : byName == .orderedAscending
        }
    }
}

/// Each member's work in the period (`/v1/reports/agents`): assigned and solved
/// tickets, replies, notes, replies per solve, first response and handle time
/// medians, and online time when the workspace records it. A member who is not
/// an admin sees their own row alone. A row opens to every figure.
struct ReportAgentWorkCard: View {
    let report: ReportAgents?
    let error: String?
    let isAdmin: Bool
    var shown = true
    let retry: () -> Void

    @State private var column: ReportAgentColumn = .solved
    @State private var ascending = false
    @State private var open: String?
    @State private var expanded = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// Only the caller's own row: their role sees no one else's.
    private var own: Bool {
        guard let report else { return !isAdmin }
        return report.onlineTime == .own || (report.onlineTime == .off && !isAdmin)
    }

    private var columns: [ReportAgentColumn] {
        ReportAgentColumn.allCases.filter { $0 != .name && ($0 != .online || report?.onlineTime.shown == true) }
    }

    private var rows: [ReportAgentWork] {
        ReportAgentColumn.sorted(report?.data ?? [], by: column, ascending: ascending)
    }

    private var visible: [ReportAgentWork] { expanded ? rows : Array(rows.prefix(6)) }

    private var note: String? {
        switch report?.onlineTime {
        case .off?: "Online time is off for this workspace, so none is recorded or shown. Admins can turn it on in the workspace settings on the web."
        case .all?: "Online counts the time each teammate had opendesk open with their status set to Online, whatever the team filter."
        case .own?: "You see your own work and online time. Admins see everyone's."
        default: nil
        }
    }

    var body: some View {
        ReportCard(title: "Agents", caption: own ? "Your work in the period" : "Each teammate's work in the period") {
            if let report, report.data.count > 1 { sortMenu }
        } content: {
            if let report {
                if report.data.isEmpty {
                    Text(own ? "Your work appears here once you work tickets in this period. Admins see everyone's." : "Agents appear here once they work tickets in this period.")
                        .font(ODFont.sans(14))
                        .foregroundStyle(Theme.fg3)
                        .fixedSize(horizontal: false, vertical: true)
                } else {
                    table
                }
                if let note {
                    Text(note)
                        .font(ODFont.sans(12))
                        .foregroundStyle(Theme.fg4)
                        .fixedSize(horizontal: false, vertical: true)
                }
            } else if let error {
                VStack(alignment: .leading, spacing: 10) {
                    Text("Agents couldn't load. " + error)
                        .font(ODFont.sans(14))
                        .foregroundStyle(Theme.fg3)
                        .fixedSize(horizontal: false, vertical: true)
                    Button("Try again", action: retry)
                        .buttonStyle(.plain)
                        .font(ODFont.sans(13.5, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                }
            } else {
                ProgressView().controlSize(.small).frame(maxWidth: .infinity, minHeight: 60)
            }
        }
    }

    private var sortMenu: some View {
        Menu {
            Picker("Sort by", selection: Binding(get: { column }, set: { pick($0) })) {
                ForEach([ReportAgentColumn.name] + columns) { option in
                    Text(option.title).tag(option)
                }
            }
            Divider()
            Button {
                Haptics.selection()
                withAnimation(reduceMotion ? nil : Motion.snappy) { ascending.toggle() }
            } label: {
                Label(ascending ? "Largest first" : "Smallest first", systemImage: "arrow.up.arrow.down")
            }
        } label: {
            HStack(spacing: 4) {
                Text(column.title).lineLimit(1)
                Image(systemName: ascending ? "arrow.up" : "arrow.down").font(.system(size: 10, weight: .semibold))
            }
            .font(ODFont.sans(12.5, weight: .medium))
            .foregroundStyle(Theme.fg2)
            .padding(.horizontal, 10)
            .frame(height: 28)
            .background(Theme.hover, in: Capsule())
            .contentShape(Capsule())
        }
        .accessibilityLabel("Sort by \(column.title)")
    }

    private func pick(_ next: ReportAgentColumn) {
        Haptics.selection()
        withAnimation(reduceMotion ? nil : Motion.snappy) {
            if next == column {
                ascending.toggle()
            } else {
                column = next
                ascending = next == .name
            }
        }
    }

    /// The figure on the right of each row: the sorted one, or solved when sorting by name.
    private var figure: ReportAgentColumn { column == .name ? .solved : column }

    private var table: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Text("Agent").frame(maxWidth: .infinity, alignment: .leading)
                Text(figure.title).lineLimit(1)
            }
            .font(ODFont.sans(11.5, weight: .medium))
            .foregroundStyle(Theme.fg3)
            .padding(.bottom, 6)
            .overlay(alignment: .bottom) { Hairline() }

            ForEach(Array(visible.enumerated()), id: \.element.id) { index, row in
                agentRow(row, isOpen: open == row.id || rows.count == 1)
                    .opacity(shown ? 1 : 0)
                    .animation(reduceMotion ? nil : Motion.smooth.delay(Double(index) * 0.03), value: shown)
                if index < visible.count - 1 { Hairline(inset: 35) }
            }
            if rows.count > 6 {
                Button {
                    Haptics.tap()
                    withAnimation(reduceMotion ? nil : Motion.snappy) { expanded.toggle() }
                } label: {
                    Text(expanded ? "Show fewer" : "Show all \(rows.count)")
                        .font(ODFont.sans(13, weight: .medium))
                        .foregroundStyle(Theme.fg2)
                        .frame(maxWidth: .infinity)
                        .frame(height: 36)
                }
                .buttonStyle(RowPressStyle())
                .padding(.top, 4)
            }
        }
    }

    private func agentRow(_ row: ReportAgentWork, isOpen: Bool) -> some View {
        let single = rows.count == 1
        return VStack(spacing: 0) {
            Button {
                guard !single else { return }
                Haptics.tap()
                withAnimation(reduceMotion ? nil : Motion.snappy) { open = open == row.id ? nil : row.id }
            } label: {
                HStack(spacing: 8) {
                    HStack(spacing: 9) {
                        Avatar(name: row.user.name, url: row.user.avatarURL, seed: row.user.id, size: 26)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(row.user.name)
                                .font(ODFont.sans(14, weight: .medium))
                                .foregroundStyle(Theme.fg)
                                .lineLimit(1)
                            Text("\(row.solved.formatted()) solved, \(Format.plural(row.replies, "reply", "replies"))")
                                .font(ODFont.sans(12))
                                .foregroundStyle(Theme.fg3)
                                .lineLimit(1)
                        }
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    Text(figure.text(row))
                        .font(ODFont.mono(13))
                        .foregroundStyle(figure.number(row) == nil ? Theme.fg4 : Theme.fg)
                        .lineLimit(1)
                        .contentTransition(.numericText())
                    if !single {
                        Image(systemName: "chevron.down")
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundStyle(Theme.fg4)
                            .rotationEffect(.degrees(isOpen ? 180 : 0))
                    }
                }
                .padding(.vertical, 9)
                .contentShape(Rectangle())
            }
            .buttonStyle(RowPressStyle())
            .accessibilityHint(single ? "" : (isOpen ? "Hides their figures" : "Shows all their figures"))
            if isOpen {
                details(row)
                    .padding(.leading, 35)
                    .padding(.bottom, 10)
                    .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
    }

    /// Every figure of one member, in a grid of small tiles.
    private func details(_ row: ReportAgentWork) -> some View {
        LazyVGrid(columns: [GridItem(.flexible(), spacing: 8), GridItem(.flexible(), spacing: 8)], spacing: 8) {
            ForEach(columns) { item in
                VStack(alignment: .leading, spacing: 3) {
                    Text(item.title)
                        .font(ODFont.sans(11.5, weight: .medium))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(1)
                    Text(item.text(row))
                        .font(ODFont.mono(14, weight: .medium))
                        .foregroundStyle(item.number(row) == nil ? Theme.fg4 : Theme.fg)
                        .lineLimit(1)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(.horizontal, 10)
                .padding(.vertical, 8)
                .background(item == column ? Theme.active : Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.lineSoft, lineWidth: 1))
                .accessibilityElement(children: .combine)
                .accessibilityHint(item.hint)
            }
        }
    }
}
