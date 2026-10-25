import SwiftUI

/// Reports: how much came in and got solved, how fast, how customers rated it,
/// and where the open work sits, for a period and a team, against the period before.
struct ReportsView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var store = ReportsStore()
    @State private var showScopes = false
    @State private var showCustom = false
    @State private var shown = false
    @State private var query = ""
    @FocusState private var searchFocused: Bool
    @State private var scrollTarget: String?

    private var teamTitle: String {
        switch store.team {
        case .all: "All teams"
        case .none: "No team"
        case let .team(id): env.directory.team(id)?.name ?? "Team"
        }
    }

    /// The period as dates, under the title (the filter names the preset).
    private var span: String {
        let range = store.range
        let sameYear = Calendar.current.component(.year, from: range.from) == Calendar.current.component(.year, from: range.to)
        let from = sameYear ? range.from.formatted(.dateTime.month(.abbreviated).day()) : range.from.formatted(.dateTime.month(.abbreviated).day().year())
        return "\(from) to \(range.to.formatted(.dateTime.month(.abbreviated).day()))"
    }

    var body: some View {
        VStack(spacing: 0) {
            GlassHeaderBar(
                title: "Reports",
                subtitle: Text(span + (store.team == .all ? "" : " · " + teamTitle)),
                query: $query,
                searchPrompt: "",
                searchFocused: $searchFocused,
                menuLabel: "Folders and views",
                searchable: false,
                onMenu: { showScopes = true }
            ) {
                if store.loading && store.overview != nil {
                    ProgressView().controlSize(.small).padding(.trailing, 6)
                }
            }
            filters
            content
        }
        .background(Theme.chrome.ignoresSafeArea())
        .toolbarVisibility(.hidden, for: .navigationBar)
        .onAppear {
            store.attach(api: env.session.api, workspaceID: env.session.workspace?.id)
            #if DEBUG
            demo(env.router.demoAction)
            #endif
        }
        .onChange(of: store.overview) { _, value in
            if value != nil, !shown {
                Task {
                    try? await Task.sleep(for: .milliseconds(60))
                    shown = true
                }
            }
        }
        .sheet(isPresented: $showScopes) {
            ScopeSheet()
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(isPresented: $showCustom) {
            ReportRangeSheet(range: store.range) { store.range = $0 }
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        #if DEBUG
        .onChange(of: env.router.demoAction) { _, action in demo(action) }
        #endif
    }

    // MARK: Filters

    private var filters: some View {
        GlassEffectContainer(spacing: 8) {
            HStack(spacing: 8) {
                Menu {
                    Picker("Period", selection: Binding(get: { store.range.preset }, set: { preset in
                        guard let preset else { return }
                        Haptics.selection()
                        store.range = .preset(preset)
                    })) {
                        ForEach(ReportRange.Preset.allCases) { preset in
                            Text(preset.title).tag(Optional(preset))
                        }
                    }
                    Divider()
                    Button { showCustom = true } label: { Label("Custom range", systemImage: "calendar") }
                } label: {
                    filterLabel(symbol: "calendar", title: store.range.shortTitle)
                }
                Menu {
                    Picker("Team", selection: Binding(get: { store.team }, set: { team in
                        Haptics.selection()
                        store.team = team
                    })) {
                        Text("All teams").tag(ReportTeamFilter.all)
                        ForEach(env.directory.teams) { team in
                            Text(team.name).tag(ReportTeamFilter.team(team.id))
                        }
                        Text("No team").tag(ReportTeamFilter.none)
                    }
                } label: {
                    filterLabel(symbol: "person.2", title: teamTitle)
                }
                Spacer(minLength: 0)
            }
        }
        .padding(.horizontal, 16)
        .padding(.bottom, 10)
    }

    private func filterLabel(symbol: String, title: String) -> some View {
        HStack(spacing: 6) {
            Image(systemName: symbol).font(.system(size: 12, weight: .medium)).foregroundStyle(Theme.fg2)
            Text(title)
                .font(ODFont.sans(13.5, weight: .medium))
                .foregroundStyle(Theme.fg)
                .lineLimit(1)
                .contentTransition(.opacity)
            Image(systemName: "chevron.down").font(.system(size: 9.5, weight: .bold)).foregroundStyle(Theme.fg3)
        }
        .padding(.horizontal, 13)
        .frame(height: 36)
        .glassEffect(.regular.interactive(), in: Capsule())
        .contentShape(Capsule())
    }

    // MARK: Content

    @ViewBuilder
    private var content: some View {
        if let overview = store.overview {
            report(ReportFigures(overview: overview, previous: store.previous, open: store.openTickets, openPartial: store.openPartial, directory: env.directory))
        } else if let error = store.error {
            ErrorState(title: "Couldn't load reports", message: error) { store.reload() }
                .frame(maxHeight: .infinity)
        } else {
            ReportSkeleton()
        }
    }

    private func report(_ figures: ReportFigures) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 12) {
                    if figures.overview.isEmpty && !store.loading {
                        EmptyState(symbol: "chart.bar.xaxis", title: "No tickets in this period", message: "Pick a longer period or another team. Reports fill in as tickets come in and get solved.", actionTitle: store.range.preset == .quarter ? nil : "Show the last 90 days") {
                            store.range = .preset(.quarter)
                        }
                        .padding(.top, 40)
                    } else {
                        sections(figures)
                    }
                    Color.clear.frame(height: 90)
                }
                .padding(.horizontal, 16)
                .padding(.top, 2)
            }
            .scrollIndicators(.hidden)
            .refreshable { await store.refresh() }
            .opacity(store.loading ? 0.72 : 1)
            .animation(Motion.fade, value: store.loading)
            .onChange(of: scrollTarget) { _, target in
                guard let target else { return }
                Task {
                    try? await Task.sleep(for: .milliseconds(500))
                    proxy.scrollTo(target, anchor: .top)
                    scrollTarget = nil
                }
            }
        }
    }

    @ViewBuilder
    private func sections(_ figures: ReportFigures) -> some View {
        LazyVGrid(columns: [GridItem(.flexible(), spacing: 10), GridItem(.flexible(), spacing: 10)], spacing: 10) {
            ForEach(Array(figures.kpis.enumerated()), id: \.element.id) { index, kpi in
                ReportKPITile(kpi: kpi)
                    .reportReveal(shown, index / 2)
            }
        }
        .id("kpis")

        ReportCard(title: "Volume", caption: "Created and solved per day") {
            ReportVolumeChart(days: figures.overview.byDay, shown: shown)
        }
        .id("volume")
        .reportReveal(shown, 4)

        ReportCard(title: "Open work", caption: openCaption(figures)) {
            figures.open.isEmpty ? nil : figures.oldest.map { oldest in
                Text("Oldest " + Format.relShort(oldest.createdAt))
                    .font(ODFont.sans(12))
                    .foregroundStyle(Theme.fg3)
            }
        } content: {
            if figures.open.isEmpty && !store.loadingOpen {
                Text("Nothing open. Everything is answered.")
                    .font(ODFont.sans(14))
                    .foregroundStyle(Theme.fg3)
            } else {
                VStack(alignment: .leading, spacing: 14) {
                    ReportStackBar(slices: figures.backlog, shown: shown)
                    HStack(spacing: 8) {
                        backlogFact("person.crop.circle.badge.questionmark", "\(figures.unassigned) unassigned", tone: figures.unassigned > 0 ? .orange : .gray)
                        backlogFact("exclamationmark.triangle", "\(figures.breaching) past SLA", tone: figures.breaching > 0 ? .red : .gray)
                    }
                }
            }
        }
        .id("backlog")
        .reportReveal(shown, 5)

        if !figures.channels.isEmpty {
            ReportCard(title: "Channels", caption: "Where tickets came in") {
                ReportDonut(slices: figures.channels)
            }
            .id("channels")
            .reportReveal(shown, 6)
        }

        ReportCard(title: "Priority", caption: "Open tickets by priority") {
            ReportBars(slices: figures.priorities, shown: shown)
        }
        .id("priority")
        .reportReveal(shown, 7)

        if !figures.severities.isEmpty {
            ReportCard(title: "Severity", caption: "Tickets created in the period") {
                ReportBars(slices: figures.severities, shown: shown)
            }
            .id("severity")
            .reportReveal(shown, 8)
        }

        responseCard(figures)
            .id("response")
            .reportReveal(shown, 8)

        if store.team == .all, figures.teams.count > 1 {
            ReportCard(title: "By team", caption: "Tap a team to see its report") {
                ReportTeamTable(rows: figures.teams, name: figures.teamName, tone: teamTone) { id in
                    Haptics.selection()
                    store.team = id.map(ReportTeamFilter.team) ?? .none
                }
            }
            .id("teams")
            .reportReveal(shown, 8)
        }

        if !figures.overview.byAgent.isEmpty {
            ReportCard(title: "Agents", caption: "Solved and median first reply") {
                ReportAgentTable(rows: figures.overview.byAgent, shown: shown)
            }
            .id("agents")
            .reportReveal(shown, 8)
        }

        ReportCard(title: "Top labels", caption: "On open tickets") {
            if figures.labels.isEmpty {
                Text("No labels on open tickets.")
                    .font(ODFont.sans(14))
                    .foregroundStyle(Theme.fg3)
            } else {
                ReportBars(slices: figures.labels, shown: shown, labelWidth: 120)
            }
        }
        .id("labels")
        .reportReveal(shown, 8)
    }

    private func openCaption(_ figures: ReportFigures) -> String {
        let count = figures.open.count
        let text = Format.plural(count, "open ticket") + (figures.openPartial ? "+" : "")
        return store.team == .all ? text : text + " in " + teamTitle
    }

    private func backlogFact(_ symbol: String, _ text: String, tone: Tone) -> some View {
        HStack(spacing: 5) {
            Image(systemName: symbol).font(.system(size: 11, weight: .medium))
            Text(text).font(ODFont.sans(12.5, weight: .medium))
        }
        .foregroundStyle(tone == .gray ? Theme.fg3 : tone.text)
        .padding(.horizontal, 9)
        .frame(height: 26)
        .background(tone == .gray ? Theme.hover : tone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous))
    }

    private func teamTone(_ id: String?) -> Tone {
        guard let id, let index = env.directory.teams.firstIndex(where: { $0.id == id }) else { return .gray }
        return ReportsPalette.series[index % ReportsPalette.series.count]
    }

    private func responseCard(_ figures: ReportFigures) -> some View {
        let kpis = figures.kpis
        return ReportCard(title: "Response times", caption: "Medians for tickets in the period") {
            VStack(alignment: .leading, spacing: 16) {
                HStack(spacing: 10) {
                    ForEach(kpis.filter { $0.id == "first" || $0.id == "resolution" }) { kpi in
                        VStack(alignment: .leading, spacing: 4) {
                            Text(kpi.id == "first" ? "First reply" : "Resolution")
                                .font(ODFont.sans(12.5, weight: .medium))
                                .foregroundStyle(Theme.fg3)
                            Text(kpi.value)
                                .font(ODFont.mono(22, weight: .medium))
                                .foregroundStyle(Theme.fg)
                            if let text = kpi.changeText {
                                Text(kpi.changeTone == .gray ? text + " vs before" : text.trimmingCharacters(in: CharacterSet(charactersIn: "+\u{2212}")) + (kpi.changeTone == .green ? " faster" : " slower"))
                                    .font(ODFont.sans(12, weight: .medium))
                                    .foregroundStyle(kpi.changeTone == .gray ? Theme.fg3 : kpi.changeTone.text)
                            }
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(12)
                        .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.lineSoft, lineWidth: 1))
                    }
                }
                if store.team == .all, figures.teamResponse.count > 1 {
                    VStack(alignment: .leading, spacing: 10) {
                        Text("First reply by team")
                            .font(ODFont.sans(12.5, weight: .medium))
                            .foregroundStyle(Theme.fg3)
                        ReportBars(slices: figures.teamResponse, shown: shown)
                    }
                }
            }
        }
    }

    #if DEBUG
    /// QA states: `/demo/reports-range` (custom period), `/demo/reports-7`, `-14`, `-30`, `-90`,
    /// `/demo/reports-team` (first team), `/demo/reports-all`, `/demo/reports-scroll-<section>`
    /// (kpis, volume, backlog, channels, priority, severity, response, teams, agents, labels).
    private func demo(_ action: String?) {
        guard let action, action.hasPrefix("reports-") else { return }
        let rest = String(action.dropFirst("reports-".count))
        if rest == "range" {
            showCustom = true
        } else if let days = Int(rest), let preset = ReportRange.Preset(rawValue: days) {
            store.range = .preset(preset)
        } else if rest == "team", let team = env.directory.teams.first {
            store.team = .team(team.id)
        } else if rest == "all" {
            store.team = .all
        } else if rest.hasPrefix("scroll-") {
            scrollTarget = String(rest.dropFirst("scroll-".count))
        } else {
            return
        }
        env.router.demoAction = nil
    }
    #endif
}

// MARK: - Custom range

/// Pick the first and last day of a custom period.
struct ReportRangeSheet: View {
    let range: ReportRange
    let onApply: (ReportRange) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var from: Date
    @State private var to: Date

    init(range: ReportRange, onApply: @escaping (ReportRange) -> Void) {
        self.range = range
        self.onApply = onApply
        let now = AppClock.now
        _from = State(initialValue: min(range.from, now))
        _to = State(initialValue: min(max(range.to, range.from), now))
    }

    /// The earliest day "To" may take; never past today, so the picker's range stays valid.
    private var toFloor: Date { min(from, AppClock.now) }

    private var picked: ReportRange { .custom(from: from, to: to) }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    Panel {
                        DatePicker("From", selection: $from, in: ...AppClock.now, displayedComponents: .date)
                            .font(ODFont.sans(15))
                            .padding(.horizontal, 14)
                            .frame(height: 52)
                        Hairline(inset: 14)
                        DatePicker("To", selection: $to, in: toFloor ... AppClock.now, displayedComponents: .date)
                            .font(ODFont.sans(15))
                            .padding(.horizontal, 14)
                            .frame(height: 52)
                    }
                    HStack(spacing: 6) {
                        Image(systemName: "calendar").font(.system(size: 12))
                        Text("\(Format.plural(picked.days, "day")), compared with the \(picked.days) before")
                            .font(ODFont.sans(13))
                            .contentTransition(.numericText())
                    }
                    .foregroundStyle(Theme.fg3)
                    Button {
                        Haptics.tap()
                        onApply(picked)
                        dismiss()
                    } label: {
                        Text("Show this period")
                    }
                    .buttonStyle(InkButtonStyle())
                    .disabled(picked.days > 366)
                    if picked.days > 366 {
                        FormError(text: "Pick a period of a year or less.")
                    }
                }
                .padding(20)
            }
            .background(Theme.chrome)
            .navigationTitle("Custom range")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
        }
        .onChange(of: from) { _, value in
            if to < value { to = value }
        }
    }
}

// MARK: - Loading

struct ReportSkeleton: View {
    @State private var pulse = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        ScrollView {
            VStack(spacing: 12) {
                LazyVGrid(columns: [GridItem(.flexible(), spacing: 10), GridItem(.flexible(), spacing: 10)], spacing: 10) {
                    ForEach(0 ..< 6, id: \.self) { _ in block(height: 112) }
                }
                block(height: 280)
                block(height: 150)
            }
            .padding(.horizontal, 16)
        }
        .scrollDisabled(true)
        .opacity(pulse ? 0.55 : 1)
        .onAppear {
            guard !reduceMotion else { return }
            withAnimation(.easeInOut(duration: 0.9).repeatForever(autoreverses: true)) { pulse = true }
        }
        .accessibilityLabel("Loading")
    }

    private func block(height: CGFloat) -> some View {
        RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous)
            .fill(Theme.surface)
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            .frame(height: height)
    }
}
