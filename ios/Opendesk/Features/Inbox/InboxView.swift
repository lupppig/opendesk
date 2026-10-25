import SwiftUI

struct InboxRootView: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.scenePhase) private var scenePhase
    @State private var alerts = InboxChatAlerts()

    var body: some View {
        @Bindable var router = env.router
        NavigationStack(path: $router.inboxPath) {
            AreaRootView()
                .navigationDestination(for: AppRoute.self) { route in
                    AppRouteDestination(route: route)
                }
        }
        .environment(alerts)
        .overlay(alignment: .top) {
            if let ticket = alerts.banner {
                InboxChatBanner(
                    ticket: ticket,
                    quiet: alerts.quiet,
                    onOpen: {
                        alerts.hideBanner()
                        env.router.show(.tickets, [.ticket(ticket)])
                    },
                    onQuiet: {
                        Haptics.selection()
                        alerts.quiet.toggle()
                    },
                    onClose: { alerts.hideBanner() }
                )
                .padding(.top, 4)
                .transition(.move(edge: .top).combined(with: .opacity))
            }
        }
        .motion(Motion.smooth, value: alerts.banner?.id)
        .task(id: env.session.workspace?.id) { alerts.start(env: env) }
        .onChange(of: scenePhase, initial: true) { _, phase in alerts.active = phase == .active }
        #if DEBUG
        .onChange(of: env.router.demoAction, initial: true) { _, action in
            guard let action, InboxListView.demoActions.contains(action) else { return }
            if env.router.area != .tickets || !env.router.inboxPath.isEmpty { env.router.show(.tickets, []) }
            if action == "chat-alert" {
                env.router.demoAction = nil
                Task {
                    try? await Task.sleep(for: .milliseconds(900))
                    alerts.preview()
                }
            }
        }
        #endif
    }
}

/// The ticket list for a folder, view or team inbox: dense rows, swipe to solve,
/// snooze or take, select many to change together, pull to refresh, all live.
struct InboxListView: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(InboxChatAlerts.self) private var alerts: InboxChatAlerts?
    @State private var query = ""
    @FocusState private var searchFocused: Bool
    @State private var showScopes = false
    @State private var showFilters = false
    @State private var showNewTicket = false
    @State private var snoozing: Ticket?
    @State private var assigning: Ticket?
    @State private var selecting = false
    @State private var selected: Set<String> = []

    static let demoActions: Set<String> = ["new-ticket", "bulk", "filters", "filter-chips", "chat-alert", "search-words", "search-ollie", "scopes"]

    private var inbox: InboxStore { env.inbox }
    private var directory: DirectoryStore { env.directory }

    private var title: String {
        switch inbox.scope {
        case let .folder(folder): folder.title
        case let .view(id): directory.view(id)?.name ?? "View"
        case let .team(id, _): teamName(id)
        }
    }

    private func teamName(_ id: String) -> String {
        id == InboxScope.myTeams ? "My teams" : directory.team(id)?.name ?? "Team"
    }

    private var subtitle: Text? {
        let tickets = inbox.tickets
        guard inbox.loadedOnce else { return nil }
        let count = tickets.isEmpty ? nil : Format.plural(tickets.count, "ticket") + (inbox.nextCursor == nil ? "" : "+")
        if inbox.scope.teamID != nil {
            return Text(["Team inbox", count].compactMap { $0 }.joined(separator: " · "))
        }
        return count.map { Text($0) }
    }

    private var waitingChats: [Ticket] {
        inbox.tickets.filter { ChatWait.of($0, agentOn: directory.ai.widgetAgentOn)?.state == .urgent && $0.assignee == nil }
    }

    /// The rows in order: in Unassigned's default order, chats waiting for a person come first.
    private var rows: [Ticket] {
        guard inbox.scope == .folder(.unassigned), inbox.sort.isDefault, inbox.search.isEmpty else { return inbox.tickets }
        let waiting = Set(waitingChats.map(\.id))
        guard !waiting.isEmpty else { return inbox.tickets }
        return inbox.tickets.filter { waiting.contains($0.id) } + inbox.tickets.filter { !waiting.contains($0.id) }
    }

    private var urgentOnly: Bool { inbox.filters.priorities == [.urgent] }

    var body: some View {
        VStack(spacing: 0) {
            if selecting {
                InboxSelectHeader(
                    count: selected.count,
                    allSelected: !inbox.tickets.isEmpty && selected.count == inbox.tickets.count,
                    onClose: { endSelecting() },
                    onAll: {
                        withAnimation(Motion.snappy) {
                            selected = selected.count == inbox.tickets.count ? [] : Set(inbox.tickets.map(\.id))
                        }
                    }
                )
                .transition(.opacity)
            } else {
                GlassHeaderBar(
                    title: title,
                    subtitle: subtitle,
                    query: $query,
                    searchPrompt: "Search \(title.lowercased())",
                    searchFocused: $searchFocused,
                    menuLabel: "Folders, teams and views",
                    onMenu: { showScopes = true }
                ) {
                    GlassCircleButton(symbol: "line.3.horizontal.decrease", label: "Filter and sort", badge: inbox.filters.count + (inbox.sort.isDefault ? 0 : 1)) {
                        showFilters = true
                    }
                    if env.session.canWrite {
                        HeaderPrimaryButton(symbol: "plus", label: "New ticket") {
                            showNewTicket = true
                        }
                    }
                }
                .transition(.opacity)
            }
            panel
        }
        .background(Theme.chrome.ignoresSafeArea())
        .overlay(alignment: .bottom) {
            if selecting {
                InboxBulkBar(ids: inbox.tickets.map(\.id).filter(selected.contains)) { endSelecting() }
                    .padding(.bottom, 8)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .motion(Motion.smooth, value: selecting)
        .tabBarHidden(selecting)
        .toolbarVisibility(.hidden, for: .navigationBar)
        .onChange(of: query) { _, value in inbox.setSearch(value) }
        .onChange(of: inbox.tickets.map(\.id)) { _, ids in
            env.realtime.setWatched(ids)
            selected.formIntersection(ids)
        }
        .onChange(of: inbox.scope) { _, _ in endSelecting() }
        .onChange(of: directory.teams, initial: true) { _, teams in
            guard let me = env.session.user?.id else { return }
            inbox.myTeamIDs = Set(teams.filter { $0.memberIDs?.contains(me) == true }.map(\.id))
        }
        .onAppear {
            env.router.visibleTicketID = nil
            env.realtime.setWatched(inbox.tickets.map(\.id))
        }
        #if DEBUG
        .onChange(of: env.router.demoAction, initial: true) { _, action in runDemo(action) }
        #endif
        .sheet(isPresented: $showScopes) {
            ScopeSheet()
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(isPresented: $showFilters) {
            FilterSheet()
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(isPresented: $showNewTicket) {
            NewTicketSheet { ticket in
                Task {
                    try? await Task.sleep(for: .milliseconds(350))
                    env.router.openTicket(ticket)
                }
            }
            .presentationDetents([.large])
            .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(item: $snoozing) { ticket in
            SnoozeSheet { date in
                Task { await apply(ticket, TicketPatch(snoozedUntil: .set(date)), done: "Snoozed #\(ticket.number)") }
            }
            .presentationDetents([.medium])
            .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(item: $assigning) { ticket in
            AssigneePicker(current: ticket.assignee?.id) { id in
                let change = TicketPatch(assigneeID: id.map { TicketPatch.Reference.set($0) } ?? .clear)
                Task { await apply(ticket, change, done: nil) }
            }
            .presentationDetents([.medium, .large])
            .presentationCornerRadius(Theme.Radius.sheet)
        }
    }

    // MARK: Panel

    private var panel: some View {
        VStack(spacing: 0) {
            if case let .team(id, tab) = inbox.scope {
                InboxTeamTabs(tab: tab, counts: id == InboxScope.myTeams ? directory.counts?.myTeams : directory.counts?.teams[id]) { next in
                    inbox.setTeamTab(next)
                }
                .overlay(alignment: .bottom) { Hairline() }
                .disabled(selecting)
            }
            if inbox.loadedOnce, !inbox.tickets.isEmpty || urgentOnly || inbox.sort.key == .due {
                InboxSummaryStrip(
                    tickets: inbox.tickets,
                    total: "",
                    urgentOnly: urgentOnly,
                    byDue: inbox.sort.key == .due,
                    canSelect: env.session.canWrite && !selecting && !inbox.tickets.isEmpty,
                    onUrgent: {
                        if urgentOnly { inbox.filters.priorities = [] } else { inbox.filters.priorities = [.urgent] }
                    },
                    onDue: {
                        inbox.sort = inbox.sort.key == .due ? .latest : .due
                    },
                    onSelect: { startSelecting(nil) }
                )
            }
            InboxActiveFilters()
            list
        }
        .background(Theme.surface)
        .clipShape(UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous))
    }

    @ViewBuilder
    private var list: some View {
        if !inbox.loadedOnce && inbox.loading {
            ScrollView { SkeletonRows() }
                .scrollDisabled(true)
        } else if let error = inbox.error, inbox.tickets.isEmpty {
            ErrorState(title: "Couldn't load tickets", message: error) { inbox.reload() }
                .frame(maxHeight: .infinity)
        } else {
            List {
                if !inbox.search.isEmpty, !selecting {
                    searchEverywhereRow
                        .listRowInsets(EdgeInsets())
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                }
                if !waitingChats.isEmpty, inbox.search.isEmpty, !selecting {
                    WaitingBanner(tickets: waitingChats, quiet: alerts?.quiet, onQuiet: alerts.map { alerts in { alerts.quiet.toggle() } }) {
                        env.router.openTicket($0)
                    }
                    .listRowInsets(EdgeInsets(top: 10, leading: 12, bottom: 4, trailing: 12))
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)
                }
                ForEach(rows) { ticket in
                    row(ticket)
                        .listRowInsets(EdgeInsets())
                        .listRowSeparator(.hidden)
                        .listRowBackground(selected.contains(ticket.id) ? Theme.hover : Theme.surface)
                        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                            if env.session.canWrite, !selecting { trailingActions(ticket) }
                        }
                        .swipeActions(edge: .leading, allowsFullSwipe: true) {
                            if env.session.canWrite, !selecting { leadingActions(ticket) }
                        }
                        .contextMenu { if env.session.canWrite, !selecting { menu(ticket) } }
                        .onAppear {
                            if ticket.id == inbox.tickets.last?.id { Task { await inbox.loadMore() } }
                        }
                }
                if inbox.loadingMore {
                    ProgressView().frame(maxWidth: .infinity).padding(.vertical, 18)
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                }
                if inbox.tickets.isEmpty {
                    emptyState
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                }
                Color.clear.frame(height: selecting ? 120 : 90)
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .background(Theme.surface)
            .refreshable {
                await inbox.refresh()
                await directory.refreshCounts()
            }
            .motion(Motion.snappy, value: rows.map(\.id))
        }
    }

    private func row(_ ticket: Ticket) -> some View {
        let isSelected = selected.contains(ticket.id)
        return Button {
            if selecting {
                Haptics.selection()
                withAnimation(Motion.snappy) {
                    if isSelected { selected.remove(ticket.id) } else { selected.insert(ticket.id) }
                }
            } else {
                env.router.openTicket(ticket)
            }
        } label: {
            HStack(spacing: 0) {
                if selecting {
                    InboxSelectMark(selected: isSelected)
                        .padding(.leading, 14)
                        .padding(.trailing, 2)
                        .transition(.move(edge: .leading).combined(with: .opacity))
                }
                TicketRow(
                    ticket: ticket,
                    viewers: env.realtime.presence[ticket.id] ?? [],
                    agentOn: directory.ai.widgetAgentOn,
                    hideAssignee: inbox.scope == .folder(.mine),
                    replyingBot: env.realtime.live[ticket.id]?.bot
                )
            }
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(selecting && isSelected ? .isSelected : [])
    }

    /// "Search everywhere for …": the in-list search stays on this list; this opens the global one.
    private var searchEverywhereRow: some View {
        Button {
            openSearch(inbox.search)
        } label: {
            HStack(spacing: 10) {
                Image(systemName: "magnifyingglass").font(.system(size: 13, weight: .medium)).foregroundStyle(Theme.fg3)
                Text("Search everywhere for \u{201C}\(inbox.search)\u{201D}")
                    .font(ODFont.sans(13.5))
                    .foregroundStyle(Theme.fg2)
                    .lineLimit(1)
                Spacer()
                Image(systemName: "arrow.up.right").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.fg4)
            }
            .padding(.horizontal, 16)
            .frame(height: 40)
            .overlay(alignment: .bottom) { Hairline() }
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
    }

    private func openSearch(_ text: String) {
        InboxSearchHandoff.query = text
        searchFocused = false
        env.router.push(.search)
    }

    @ViewBuilder
    private var emptyState: some View {
        if !inbox.search.isEmpty {
            EmptyState(symbol: "magnifyingglass", title: "No results for \u{201C}\(inbox.search)\u{201D}", message: "Check the spelling or try fewer words. Search everywhere looks through contacts and articles too.", actionTitle: "Search everywhere") {
                openSearch(inbox.search)
            }
        } else if !inbox.filters.isEmpty {
            EmptyState(symbol: "line.3.horizontal.decrease", title: "No tickets match", message: "Try removing a filter.", actionTitle: "Clear filters") {
                inbox.filters = InboxFilters()
            }
        } else {
            switch inbox.scope {
            case .folder(.unassigned):
                EmptyState(symbol: "tray", title: "Inbox zero", message: "Every conversation has an owner. New ones land here until someone picks them up.")
            case .folder(.mine):
                EmptyState(symbol: "checkmark.circle", title: "You're all caught up", message: "Nothing assigned to you needs an answer. Tickets you own show up here.", actionTitle: "Pick up from Unassigned") {
                    inbox.setScope(.folder(.unassigned))
                }
            case .folder(.open):
                EmptyState(symbol: "tray", title: "Inbox zero", message: "No open conversations. When customers write in, they show up here.")
            case .folder(.pending):
                EmptyState(symbol: "clock", title: "Nothing pending", message: "Pending means you replied and are waiting on the customer. Their next message moves the ticket back to open.")
            case .folder(.solved):
                EmptyState(symbol: "checkmark.circle", title: "No solved tickets yet", message: "Solved conversations stay here for reference. A reply from the customer reopens them.")
            case .folder(.spam):
                EmptyState(symbol: "xmark.octagon", title: "No spam", message: "Messages marked as spam are kept here, out of the way.")
            case .view:
                EmptyState(symbol: "square.stack", title: "Nothing matches this view", message: "No tickets match this view's filters right now.")
            case let .team(id, tab):
                teamEmpty(teamName(id), tab: tab)
            }
        }
    }

    private func teamEmpty(_ team: String, tab: TeamTab) -> some View {
        let title = switch tab {
        case .unassigned: "Nothing unassigned in \(team)"
        case .open: "Nothing open in \(team)"
        case .pending: "Nothing pending in \(team)"
        case .all: "No tickets in \(team) yet"
        }
        return EmptyState(symbol: "person.3", title: title, message: "New conversations land here when an address, a widget or a routing rule sends them to this team.")
    }

    // MARK: Selecting

    private func startSelecting(_ ticket: Ticket?) {
        Haptics.tap()
        searchFocused = false
        selected = ticket.map { [$0.id] } ?? []
        selecting = true
    }

    private func endSelecting() {
        selecting = false
        selected = []
    }

    // MARK: Swipes

    @ViewBuilder
    private func trailingActions(_ ticket: Ticket) -> some View {
        if ticket.status.isDone {
            Button {
                Task { await apply(ticket, TicketPatch(status: .open), done: "Reopened #\(ticket.number)") }
            } label: { Label("Reopen", systemImage: "arrow.uturn.backward") }
                .tint(Tone.blue.solid)
        } else {
            Button {
                Task { await solve(ticket) }
            } label: { Label("Solve", systemImage: "checkmark") }
                .tint(Tone.green.solid)
        }
        Button {
            snoozing = ticket
        } label: { Label("Snooze", systemImage: "alarm") }
            .tint(Tone.violet.solid)
    }

    @ViewBuilder
    private func leadingActions(_ ticket: Ticket) -> some View {
        if ticket.assignee?.id != env.session.user?.id, let me = env.session.user {
            Button {
                Task { await apply(ticket, TicketPatch(assigneeID: .set(me.id)), done: "Assigned #\(ticket.number) to you") }
            } label: { Label("Assign to me", systemImage: "person.crop.circle.badge.plus") }
                .tint(Theme.inverse)
        } else {
            Button {
                assigning = ticket
            } label: { Label("Assign", systemImage: "person.2") }
                .tint(Theme.fg3)
        }
    }

    @ViewBuilder
    private func menu(_ ticket: Ticket) -> some View {
        Button { startSelecting(ticket) } label: { Label("Select", systemImage: "checkmark.circle") }
        Divider()
        if let me = env.session.user, ticket.assignee?.id != me.id {
            Button { Task { await apply(ticket, TicketPatch(assigneeID: .set(me.id)), done: nil) } } label: { Label("Assign to me", systemImage: "person.crop.circle.badge.plus") }
        }
        Button { assigning = ticket } label: { Label("Assign to\u{2026}", systemImage: "person.2") }
        Menu {
            ForEach(TicketStatus.allCases) { status in
                Button(status.title) { Task { await apply(ticket, TicketPatch(status: status), done: nil) } }
            }
        } label: { Label("Status", systemImage: "circle.dashed") }
        Menu {
            ForEach(Priority.allCases) { priority in
                Button(priority.title) { Task { await apply(ticket, TicketPatch(priority: priority), done: nil) } }
            }
        } label: { Label("Priority", systemImage: "chart.bar") }
        Button { snoozing = ticket } label: { Label("Snooze", systemImage: "alarm") }
        Divider()
        Button(role: ticket.spam ? nil : .destructive) {
            Task { await apply(ticket, TicketPatch(spam: !ticket.spam), done: ticket.spam ? "Moved out of spam" : "Marked as spam") }
        } label: { Label(ticket.spam ? "Not spam" : "Mark as spam", systemImage: "xmark.octagon") }
    }

    private func solve(_ ticket: Ticket) async {
        let previous = ticket.status
        do {
            _ = try await inbox.patch(ticket, TicketPatch(status: .solved), members: directory.members, meRef: env.session.userRef)
            Haptics.success()
            env.toasts.show(Toast(message: "Solved #\(ticket.number)", kind: .success, actionTitle: "Undo") {
                Task { await apply(ticket, TicketPatch(status: previous), done: nil) }
            })
        } catch {
            env.toasts.error(error)
        }
    }

    private func apply(_ ticket: Ticket, _ change: TicketPatch, done: String?) async {
        do {
            _ = try await inbox.patch(ticket, change, members: directory.members, meRef: env.session.userRef)
            Haptics.tap()
            if let done { env.toasts.show(done, kind: .success) }
        } catch {
            env.toasts.error(error)
        }
    }

    #if DEBUG
    /// QA states: `/demo/new-ticket`, `/demo/bulk`, `/demo/filters`, `/demo/filter-chips`,
    /// `/demo/search-words`, `/demo/search-ollie`, `/demo/scopes` (`/demo/chat-alert` is the root's).
    private func runDemo(_ action: String?) {
        guard let action, Self.demoActions.contains(action), action != "chat-alert" else { return }
        env.router.demoAction = nil
        Task {
            try? await Task.sleep(for: .milliseconds(500))
            switch action {
            case "new-ticket":
                showNewTicket = true
            case "bulk":
                if !inbox.loadedOnce { try? await Task.sleep(for: .milliseconds(800)) }
                startSelecting(nil)
                selected = Set(inbox.tickets.prefix(3).map(\.id))
            case "filters":
                showFilters = true
            case "scopes":
                showScopes = true
            case "search-words":
                openSearch("refund")
            case "search-ollie":
                InboxSearchHandoff.ollie = true
                openSearch("Customers who were charged twice")
            case "filter-chips":
                inbox.filters = InboxFilters(priorities: [.urgent, .high], channel: .email, teams: [InboxScope.myTeams])
            default:
                break
            }
        }
    }
    #endif
}

/// "2 customers waiting in live chat": a red line above the list while someone waits for a person.
struct WaitingBanner: View {
    let tickets: [Ticket]
    /// Whether chat alert sounds are muted, and the switch; nil hides it.
    var quiet: Bool?
    var onQuiet: (() -> Void)?
    let open: (Ticket) -> Void

    var body: some View {
        let longest = tickets.min { ($0.waitingSince ?? AppClock.now) < ($1.waitingSince ?? AppClock.now) }
        HStack(spacing: 0) {
            Button {
                if let longest { open(longest) }
            } label: {
                HStack(spacing: 10) {
                    PingDot(color: Tone.red.solid, size: 7)
                    Text(tickets.count == 1 ? "1 customer waiting in live chat" : "\(tickets.count) customers waiting in live chat")
                        .font(ODFont.sans(13.5, weight: .medium))
                        .foregroundStyle(Tone.red.text)
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    Text("Reply now").font(ODFont.sans(13, weight: .semibold)).foregroundStyle(Tone.red.text)
                    Image(systemName: "arrow.right").font(.system(size: 11, weight: .bold)).foregroundStyle(Tone.red.text)
                }
                .padding(.leading, 12)
                .padding(.trailing, quiet == nil ? 12 : 6)
                .frame(height: 40)
                .contentShape(Rectangle())
            }
            .buttonStyle(TapScaleStyle())
            if let quiet, let onQuiet {
                Rectangle().fill(Tone.red.line).frame(width: 1, height: 20)
                Button {
                    Haptics.selection()
                    onQuiet()
                } label: {
                    Image(systemName: quiet ? "speaker.slash" : "speaker.wave.2")
                        .font(.system(size: 12.5, weight: .medium))
                        .foregroundStyle(Tone.red.text)
                        .frame(width: 40, height: 40)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(quiet ? "Play a sound when a visitor is waiting" : "Mute chat alert sounds")
            }
        }
        .background(Tone.red.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Tone.red.line, lineWidth: 1))
    }
}
