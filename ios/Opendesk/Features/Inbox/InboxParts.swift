import SwiftUI

/// A team inbox's tabs: Unassigned, Open, Pending, All, with an underline that glides.
struct InboxTeamTabs: View {
    let tab: TeamTab
    var counts: TicketCounts.TeamCount?
    let onTab: (TeamTab) -> Void
    @Namespace private var underline
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 4) {
            ForEach(TeamTab.allCases) { item in
                let active = item == tab
                Button {
                    guard !active else { return }
                    Haptics.selection()
                    withAnimation(reduceMotion ? nil : Motion.snappy) { onTab(item) }
                } label: {
                    HStack(spacing: 5) {
                        Text(item.title)
                            .font(ODFont.sans(14, weight: active ? .semibold : .regular))
                            .foregroundStyle(active ? Theme.fg : Theme.fg3)
                        if let count = count(item), count > 0 {
                            Text("\(count)")
                                .font(ODFont.mono(11))
                                .monospacedDigit()
                                .foregroundStyle(Theme.fg4)
                                .contentTransition(.numericText())
                        }
                    }
                    .padding(.horizontal, 10)
                    .frame(height: 36)
                    .overlay(alignment: .bottom) {
                        if active {
                            Capsule().fill(Theme.fg).frame(height: 2)
                                .padding(.horizontal, 8)
                                .matchedGeometryEffect(id: "underline", in: underline)
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(active ? [.isSelected, .isButton] : .isButton)
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Team inbox")
    }

    private func count(_ item: TeamTab) -> Int? {
        switch item {
        case .unassigned: counts?.unassigned
        case .open: counts?.open
        default: nil
        }
    }
}

/// What needs attention in this list (the web's `SummaryStrip`): urgent narrows the
/// list, the SLA counts sort it by what's due first. Select sits at the end.
struct InboxSummaryStrip: View {
    let tickets: [Ticket]
    let total: String
    let urgentOnly: Bool
    let byDue: Bool
    var canSelect = true
    let onUrgent: () -> Void
    let onDue: () -> Void
    let onSelect: () -> Void

    var body: some View {
        let summary = InboxSummary(tickets)
        HStack(spacing: 2) {
            ScrollView(.horizontal) {
                HStack(spacing: 2) {
                    if !total.isEmpty {
                        Text(total)
                            .font(ODFont.sans(12.5))
                            .foregroundStyle(Theme.fg4)
                            .monospacedDigit()
                            .padding(.trailing, 6)
                    }
                    if summary.awaiting > 0 {
                        item(n: summary.awaiting, label: "awaiting reply", active: false, action: nil) {
                            Circle().fill(Tone.blue.solid).frame(width: 6, height: 6)
                        }
                    }
                    if summary.urgent > 0 || urgentOnly {
                        item(n: summary.urgent, label: "urgent", active: urgentOnly, hint: urgentOnly ? "Show all priorities" : "Show urgent only", action: onUrgent) {
                            PriorityIcon(priority: .urgent, size: 12)
                        }
                    }
                    if summary.overdue > 0 {
                        item(n: summary.overdue, label: "overdue", active: byDue, hint: byDue ? "Sort by last activity" : "Sort by SLA due", action: onDue) {
                            PingDot(color: Tone.red.solid, size: 6)
                        }
                    }
                    if summary.dueSoon > 0 {
                        item(n: summary.dueSoon, label: "due soon", active: byDue, hint: byDue ? "Sort by last activity" : "Sort by SLA due", action: onDue) {
                            Circle().fill(Tone.amber.solid).frame(width: 6, height: 6)
                        }
                    }
                }
                .padding(.leading, 14)
                .padding(.trailing, 12)
            }
            .scrollIndicators(.hidden)
            .mask {
                HStack(spacing: 0) {
                    Color.black
                    LinearGradient(colors: [.black, .clear], startPoint: .leading, endPoint: .trailing).frame(width: 18)
                }
            }
            if canSelect {
                Button {
                    Haptics.tap()
                    onSelect()
                } label: {
                    Text("Select")
                        .font(ODFont.sans(13, weight: .medium))
                        .foregroundStyle(Theme.fg2)
                        .padding(.horizontal, 10)
                        .frame(height: 28)
                        .contentShape(Rectangle())
                }
                .buttonStyle(TapScaleStyle())
                .padding(.trailing, 6)
                .accessibilityHint("Choose tickets to change together")
            }
        }
        .frame(height: 40)
        .overlay(alignment: .bottom) { Hairline() }
    }

    private func item<Mark: View>(n: Int, label: String, active: Bool, hint: String? = nil, action: (() -> Void)?, @ViewBuilder mark: () -> Mark) -> some View {
        let content = HStack(spacing: 5) {
            mark()
            Text("\(n)")
                .font(ODFont.mono(12, weight: .medium))
                .monospacedDigit()
                .foregroundStyle(Theme.fg2)
                .contentTransition(.numericText())
            Text(label).font(ODFont.sans(12.5)).foregroundStyle(active ? Theme.fg : Theme.fg3)
        }
        .padding(.horizontal, 8)
        .frame(height: 26)
        .background(active ? Theme.active : .clear, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
        return Group {
            if let action {
                Button {
                    Haptics.selection()
                    action()
                } label: { content }
                .buttonStyle(TapScaleStyle())
                .accessibilityHint(hint ?? "")
                .accessibilityAddTraits(active ? .isSelected : [])
            } else {
                content
            }
        }
    }
}

/// Counts over the loaded rows, as the web computes them.
struct InboxSummary {
    var awaiting = 0
    var urgent = 0
    var overdue = 0
    var dueSoon = 0

    init(_ tickets: [Ticket], now: Date = AppClock.now) {
        for ticket in tickets {
            if ticket.awaitingReply { awaiting += 1 }
            if ticket.priority == .urgent { urgent += 1 }
            if let sla = SLAState.of(ticket, now: now) {
                switch sla.kind {
                case .breached: overdue += 1
                case .dueSoon: dueSoon += 1
                case .onTrack: break
                }
            }
        }
    }
}

/// One removable chip per active filter, under the header.
struct InboxActiveFilters: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let chips = self.chips
        if !chips.isEmpty {
            ScrollView(.horizontal) {
                HStack(spacing: 6) {
                    ForEach(chips) { chip in
                        HStack(spacing: 5) {
                            chip.mark
                            Text(chip.title)
                                .font(ODFont.sans(12.5, weight: .medium))
                                .foregroundStyle(Theme.fg2)
                                .lineLimit(1)
                            Button {
                                Haptics.selection()
                                chip.remove()
                            } label: {
                                Image(systemName: "xmark")
                                    .font(.system(size: 9, weight: .bold))
                                    .foregroundStyle(Theme.fg3)
                                    .frame(width: 22, height: 26)
                                    .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .accessibilityLabel("Remove filter \(chip.title)")
                        }
                        .padding(.leading, 9)
                        .padding(.trailing, 2)
                        .frame(height: 28)
                        .background(Theme.hover, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
                        .transition(.scale(scale: 0.94).combined(with: .opacity))
                    }
                    Button("Clear") {
                        Haptics.selection()
                        env.inbox.filters = InboxFilters()
                    }
                    .font(ODFont.sans(12.5, weight: .medium))
                    .foregroundStyle(Theme.fg3)
                    .padding(.horizontal, 6)
                }
                .padding(.horizontal, 14)
                .padding(.vertical, 7)
            }
            .scrollIndicators(.hidden)
            .overlay(alignment: .bottom) { Hairline() }
            .motion(Motion.snappy, value: chips.map(\.id))
        }
    }

    struct Chip: Identifiable {
        let id: String
        let title: String
        let mark: AnyView
        let remove: () -> Void
    }

    private var chips: [Chip] {
        let inbox = env.inbox
        let filters = inbox.filters
        var out: [Chip] = []
        func update(_ change: @escaping (inout InboxFilters) -> Void) -> () -> Void {
            { change(&env.inbox.filters) }
        }
        for priority in Priority.allCases where filters.priorities.contains(priority) {
            out.append(Chip(id: "p." + priority.rawValue, title: priority.title, mark: AnyView(PriorityIcon(priority: priority, size: 12)), remove: update { $0.priorities.remove(priority) }))
        }
        for value in (Severity.allCases.map(\.rawValue) + [InboxFilters.noSeverity]) where filters.severities.contains(value) {
            let severity = Severity(rawValue: value)
            out.append(Chip(id: "s." + value, title: severity?.title ?? "No severity", mark: AnyView(severity.map { AnyView(SeverityChip(severity: $0)) } ?? AnyView(EmptyView())), remove: update { $0.severities.remove(value) }))
        }
        if let channel = filters.channel {
            out.append(Chip(id: "c", title: channel.title, mark: AnyView(Image(systemName: channel.symbol).font(.system(size: 10)).foregroundStyle(Theme.fg3)), remove: update { $0.channel = nil }))
        }
        if inbox.scope.teamID == nil {
            for value in filters.teams.sorted() {
                let title = value == InboxScope.myTeams ? "My teams" : value == InboxFilters.noTeam ? "No team" : env.directory.team(value)?.name ?? "Team"
                out.append(Chip(id: "t." + value, title: title, mark: AnyView(InboxTeamDot(id: value == InboxFilters.noTeam ? nil : value, name: title)), remove: update { $0.teams.remove(value) }))
            }
        }
        if let tag = filters.tag {
            out.append(Chip(id: "l", title: tag, mark: AnyView(Circle().fill(Tone(label: env.directory.tag(named: tag)?.color).solid).frame(width: 7, height: 7)), remove: update { $0.tag = nil }))
        }
        if let id = filters.mailboxID {
            let title = inbox.mailboxes.first { $0.id == id }?.email ?? "Address"
            out.append(Chip(id: "m", title: title, mark: AnyView(Image(systemName: "at").font(.system(size: 10)).foregroundStyle(Theme.fg3)), remove: update { $0.mailboxID = nil }))
        }
        if let address = filters.receivedAt {
            out.append(Chip(id: "r", title: address, mark: AnyView(Image(systemName: "at").font(.system(size: 10)).foregroundStyle(Theme.fg3)), remove: update { $0.receivedAt = nil }))
        }
        for filter in filters.fields {
            let field = env.directory.fields(for: "ticket").first { $0.key == filter.key }
            let values = filter.value.split(separator: ",").map(String.init).map { value in
                InboxFieldText.value(value, of: field, members: env.directory.members)
            }
            let title = (field?.name ?? filter.key) + ": " + values.joined(separator: ", ")
            let key = filter.key
            out.append(Chip(id: "f." + key, title: title, mark: AnyView(EmptyView()), remove: update { $0.fields.removeAll { $0.key == key } }))
        }
        return out
    }
}

/// How a custom field's value reads in a chip.
enum InboxFieldText {
    static func value(_ raw: String, of field: CustomField?, members: [Member]) -> String {
        guard let field else { return raw }
        switch field.type {
        case .select, .multiSelect: return field.options.first { $0.id == raw }?.label ?? raw
        case .user: return raw == "me" ? "Me" : members.first { $0.id == raw }?.user.name ?? raw
        case .checkbox: return raw == "true" ? "Yes" : "No"
        default: return raw
        }
    }
}

/// A team's small dot (a dashed ring for no team).
struct InboxTeamDot: View {
    let id: String?
    var name = ""
    var size: CGFloat = 8

    var body: some View {
        if let id {
            Circle().fill(Theme.avatarTone(for: id == InboxScope.myTeams ? "mine" : name.isEmpty ? id : name).solid).frame(width: size, height: size)
        } else {
            Circle().strokeBorder(Theme.fg4, style: StrokeStyle(lineWidth: 1, dash: [2, 1.5])).frame(width: size, height: size)
        }
    }
}

/// The round check a row shows while tickets are being selected.
struct InboxSelectMark: View {
    let selected: Bool

    var body: some View {
        ZStack {
            Circle().strokeBorder(selected ? Color.clear : Theme.lineStrong, lineWidth: 1.5)
            if selected {
                Circle().fill(Theme.inverse)
                Image(systemName: "checkmark").font(.system(size: 10.5, weight: .bold)).foregroundStyle(Theme.inverseFg)
                    .transition(.scale(scale: 0.5).combined(with: .opacity))
            }
        }
        .frame(width: 22, height: 22)
        .accessibilityHidden(true)
    }
}
