import SwiftUI

/// The Notifications tab, with its own stack bound to `router.notificationsPath`.
struct NotificationsView: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        @Bindable var router = env.router
        NavigationStack(path: $router.notificationsPath) {
            NotificationsList()
                .navigationDestination(for: AppRoute.self) { route in
                    AppRouteDestination(route: route)
                }
        }
    }
}

/// Everything that asked for you, newest first and grouped by day. Tap one
/// to open what it's about; it's read from then on.
private struct NotificationsList: View {
    @Environment(AppEnvironment.self) private var env
    @State private var web: AccountWebPage?
    @FocusState private var searchFocused: Bool

    private var store: NotificationsStore { env.notifications }

    private struct Day: Identifiable {
        let id: String
        var items: [AppNotification]
    }

    private var days: [Day] {
        var result: [Day] = []
        for item in store.items {
            let label = Format.dayLabel(item.createdAt)
            if result.last?.id == label { result[result.count - 1].items.append(item) } else { result.append(Day(id: label, items: [item])) }
        }
        return result
    }

    private var subtitle: Text {
        if store.unread > 0 { return Text("\(store.unread) unread") }
        return Text(store.loadedOnce ? "All caught up" : " ")
    }

    var body: some View {
        @Bindable var store = env.notifications
        VStack(spacing: 0) {
            GlassHeaderBar(
                title: "Notifications",
                subtitle: subtitle,
                query: .constant(""),
                searchPrompt: "",
                searchFocused: $searchFocused,
                searchable: false,
                onMenu: nil
            ) {
                if store.unread > 0 {
                    GlassCircleButton(symbol: "checkmark.circle", label: "Mark all as read") {
                        Haptics.success()
                        Task { await store.markAllRead() }
                    }
                    .transition(.scale.combined(with: .opacity))
                }
                GlassCircleButton(symbol: "slider.horizontal.3", label: "Notification settings", action: openSettings)
            }
            VStack(spacing: 10) {
                AccountSegmented(
                    options: NotificationsStore.Filter.allCases,
                    selection: $store.filter,
                    label: { $0 == .unread && store.unread > 0 ? "Unread \(store.unread)" : $0.title },
                    dot: { $0 == .unread && store.unread > 0 ? Tone.blue.solid : nil }
                )
                NotificationsPushNotice()
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 10)
            list
        }
        .background(Theme.chrome.ignoresSafeArea())
        .toolbarVisibility(.hidden, for: .navigationBar)
        .motion(Motion.snappy, value: store.unread > 0)
        .accountWebSheet($web)
        .task { await env.push.refreshAuthorization() }
    }

    // MARK: List

    @ViewBuilder
    private var list: some View {
        if !store.loadedOnce {
            ScrollView { SkeletonRows(count: 8) }
                .scrollDisabled(true)
                .background(Theme.surface)
                .clipShape(UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous))
        } else if let error = store.error, store.items.isEmpty {
            ErrorState(title: "Couldn't load notifications", message: error) { Task { await store.load() } }
                .frame(maxHeight: .infinity)
        } else {
            List {
                ForEach(days) { day in
                    Text(day.id)
                        .font(ODFont.sans(12.5, weight: .medium))
                        .foregroundStyle(Theme.fg3)
                        .padding(.horizontal, 22)
                        .padding(.top, day.id == days.first?.id ? 14 : 20)
                        .padding(.bottom, 2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .listRowInsets(EdgeInsets())
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                        .accessibilityAddTraits(.isHeader)
                    ForEach(day.items) { item in
                        row(item)
                    }
                }
                if store.loadingMore {
                    ProgressView().frame(maxWidth: .infinity).padding(.vertical, 18)
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                }
                if store.items.isEmpty {
                    empty
                        .padding(.top, 40)
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                }
                Color.clear.frame(height: 100)
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .background(Theme.surface)
            .clipShape(UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous))
            .refreshable {
                await store.load()
                await store.refreshBadge()
            }
            .motion(Motion.snappy, value: store.items.map(\.id))
        }
    }

    private func row(_ item: AppNotification) -> some View {
        Button {
            open(item)
        } label: {
            NotificationRow(notification: item)
        }
        .buttonStyle(RowPressStyle())
        .listRowInsets(EdgeInsets())
        .listRowSeparator(.hidden)
        .listRowBackground(Theme.surface)
        .swipeActions(edge: .leading, allowsFullSwipe: true) {
            if item.isUnread {
                Button {
                    Haptics.tap()
                    Task { await store.markRead(item) }
                } label: {
                    Label("Mark read", systemImage: "envelope.open")
                }
                .tint(Tone.blue.solid)
            }
        }
        .swipeActions(edge: .trailing, allowsFullSwipe: true) {
            if item.isUnread {
                Button {
                    Haptics.tap()
                    Task { await store.markRead(item) }
                } label: {
                    Label("Mark read", systemImage: "checkmark")
                }
                .tint(Tone.blue.solid)
            }
        }
        .contextMenu {
            Button { open(item) } label: { Label("Open", systemImage: "arrow.up.forward") }
            if item.isUnread {
                Button { Task { await store.markRead(item) } } label: { Label("Mark as read", systemImage: "envelope.open") }
            }
        }
        .onAppear {
            if item.id == store.items.last?.id { Task { await store.loadMore() } }
        }
    }

    @ViewBuilder
    private var empty: some View {
        if store.filter == .unread {
            EmptyState(
                symbol: "checkmark.circle",
                title: "You're all caught up",
                message: "Nothing unread. New assignments, mentions and replies show up here.",
                actionTitle: "Show all",
                action: { store.filter = .all }
            )
        } else {
            EmptyState(
                symbol: "bell",
                title: "No notifications yet",
                message: "When a ticket is assigned to you, someone mentions you or a customer replies, it shows up here.",
                actionTitle: "Choose what you hear about",
                action: openSettings
            )
        }
    }

    // MARK: Actions

    private func open(_ item: AppNotification) {
        Haptics.tap()
        Task { await store.markRead(item) }
        switch NotificationStyle.target(of: item) {
        case let .ticket(id): env.router.push(.ticket(TicketRoute(id: id)))
        case let .mailThread(id): env.router.push(.mailThread(id))
        case let .web(path): web = env.session.instance.webURL(path: path).map(AccountWebPage.init)
        case .none: break
        }
    }

    private func openSettings() {
        Haptics.tap()
        env.router.tab = .account
        env.router.accountPath = [.notifications]
    }
}
