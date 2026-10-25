import SwiftUI

/// The Contacts area's root list: search, newest first or by name in letter
/// sections, a label filter, swipe to block or delete, pull to refresh, all live.
struct ContactsListView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var store = ContactsStore()
    @State private var query = ""
    @FocusState private var searchFocused: Bool
    @State private var showScopes = false
    @State private var showFilters = false
    @State private var creating = false
    @State private var deleting: Contact?

    private var subtitle: Text? {
        guard store.loadedOnce, !store.contacts.isEmpty else { return nil }
        var parts = [Format.plural(store.contacts.count, "contact") + (store.nextCursor == nil ? "" : "+")]
        let blocked = store.contacts.filter(\.blocked).count
        if blocked > 0 { parts.append("\(blocked) blocked") }
        return Text(parts.joined(separator: " · "))
    }

    var body: some View {
        VStack(spacing: 0) {
            GlassHeaderBar(
                title: "Contacts",
                subtitle: subtitle,
                query: $query,
                searchPrompt: "Search name, email or company",
                searchFocused: $searchFocused,
                menuLabel: "Folders and views",
                onMenu: { showScopes = true }
            ) {
                GlassCircleButton(symbol: "line.3.horizontal.decrease", label: "Order and filter", badge: store.tag == nil ? 0 : 1) {
                    showFilters = true
                }
                if env.session.canWrite {
                    HeaderPrimaryButton(symbol: "plus", label: "New contact") { creating = true }
                }
            }
            chips
            list
        }
        .background(Theme.chrome.ignoresSafeArea())
        .toolbarVisibility(.hidden, for: .navigationBar)
        .onAppear { store.start(api: env.session.api, realtime: env.realtime) }
        .onDisappear { if env.router.area != .contacts { store.stop() } }
        .onChange(of: query) { _, value in store.setSearch(value) }
        .onChange(of: env.router.inboxPath.count) {
            // A link that opens another screen closes this list's sheets.
            showScopes = false
            showFilters = false
            creating = false
        }
        .sheet(isPresented: $showScopes) {
            ScopeSheet()
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(isPresented: $showFilters) {
            DirectoryFilterSheet(entity: .contact, sort: $store.sort, tag: $store.tag)
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(isPresented: $creating) {
            ContactEditSheet(contact: nil, defaultCompany: store.company) { body in
                let contact = try await store.create(body)
                env.toasts.show("Contact created", kind: .success)
                Task {
                    try? await Task.sleep(for: .milliseconds(350))
                    env.router.push(.contact(contact.id))
                }
            }
            .presentationCornerRadius(Theme.Radius.sheet)
        }
        .confirmationDialog(
            "Delete \(deleting?.displayName ?? "contact")?",
            isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }),
            titleVisibility: .visible,
            presenting: deleting
        ) { contact in
            Button("Delete contact", role: .destructive) { Task { await delete(contact) } }
        } message: { _ in
            Text("Their tickets stay, but the contact record and its notes are removed.")
        }
        #if DEBUG
        .onAppear { runDemoAction(env.router.demoAction) }
        .onChange(of: env.router.demoAction) { _, action in runDemoAction(action) }
        #endif
    }

    // MARK: Chips

    @ViewBuilder
    private var chips: some View {
        if store.tag != nil || store.company != nil {
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 8) {
                    if let company = store.company {
                        DirectoryFilterChip(title: company.name, onClear: { store.company = nil }) {
                            DirectoryAvatar(name: company.name, url: company.avatarURL, seed: company.id, size: 18, square: true)
                        }
                    }
                    if let tag = store.tag {
                        DirectoryFilterChip(title: tag, onClear: { store.tag = nil }) {
                            Image(systemName: "tag").font(.system(size: 10.5, weight: .semibold)).foregroundStyle(Theme.fg3)
                        }
                    }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 2)
            }
            .scrollClipDisabled()
            .padding(.bottom, 10)
            .transition(.opacity.combined(with: .move(edge: .top)))
        }
    }

    // MARK: List

    @ViewBuilder
    private var list: some View {
        if !store.loadedOnce {
            ScrollView { SkeletonRows() }
                .scrollDisabled(true)
                .background(Theme.surface)
                .clipShape(UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous))
        } else if let error = store.error, store.contacts.isEmpty {
            ErrorState(title: "Couldn't load contacts", message: error) { store.reload() }
                .frame(maxHeight: .infinity)
        } else {
            List {
                if store.sort == .name {
                    ForEach(store.sections, id: \.letter) { section in
                        Section {
                            ForEach(section.contacts) { contact in row(contact) }
                        } header: {
                            Text(section.letter)
                                .font(ODFont.mono(12, weight: .medium))
                                .foregroundStyle(Theme.fg3)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(.horizontal, 18)
                                .padding(.top, 10)
                                .padding(.bottom, 4)
                                .background(Theme.surface)
                                .listRowInsets(EdgeInsets())
                        }
                        .listSectionSeparator(.hidden)
                        .sectionIndexLabel(section.letter)
                    }
                } else {
                    ForEach(store.contacts) { contact in row(contact) }
                }
                if store.loadingMore {
                    ProgressView().frame(maxWidth: .infinity).padding(.vertical, 18)
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                }
                if store.contacts.isEmpty {
                    emptyState
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                }
                Color.clear.frame(height: 90)
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)
            }
            .listStyle(.plain)
            .listSectionIndexVisibility(store.sort == .name && store.contacts.count > 12 ? .visible : .hidden)
            .scrollContentBackground(.hidden)
            .background(Theme.surface)
            .clipShape(UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous))
            .refreshable { await store.refresh() }
            .motion(Motion.snappy, value: store.contacts.map(\.id))
            .motion(Motion.snappy, value: store.sort)
        }
    }

    private func row(_ contact: Contact) -> some View {
        Button {
            env.router.push(.contact(contact.id))
        } label: {
            ContactRow(contact: contact, showsDate: store.sort == .recent)
        }
        .buttonStyle(RowPressStyle())
        .listRowInsets(EdgeInsets())
        .listRowSeparator(.hidden)
        .listRowBackground(Theme.surface)
        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
            if env.session.canWrite {
                Button { deleting = contact } label: { Label("Delete", systemImage: "trash") }
                    .tint(Tone.red.solid)
                Button { Task { await setBlocked(contact, !contact.blocked) } } label: {
                    Label(contact.blocked ? "Unblock" : "Block", systemImage: contact.blocked ? "hand.raised.slash" : "hand.raised")
                }
                .tint(contact.blocked ? Theme.fg3 : Tone.orange.solid)
            }
        }
        .swipeActions(edge: .leading, allowsFullSwipe: true) {
            if let email = contact.email {
                Button { DirectoryActions.copy(email, what: "Email", toasts: env.toasts) } label: { Label("Copy email", systemImage: "doc.on.doc") }
                    .tint(Theme.inverse)
            }
        }
        .contextMenu {
            if let email = contact.email {
                Button { DirectoryActions.open("mailto:" + email) } label: { Label("Send email", systemImage: "envelope") }
                Button { DirectoryActions.copy(email, what: "Email", toasts: env.toasts) } label: { Label("Copy email", systemImage: "doc.on.doc") }
            }
            if !contact.phone.isEmpty {
                Button { DirectoryActions.open(DirectoryActions.telURL(contact.phone)) } label: { Label("Call", systemImage: "phone") }
            }
            if let company = contact.company {
                Button { env.router.push(.company(company.id)) } label: { Label("Open \(company.name)", systemImage: "building.2") }
            }
            if env.session.canWrite {
                Divider()
                Button { Task { await setBlocked(contact, !contact.blocked) } } label: {
                    Label(contact.blocked ? "Unblock" : "Block", systemImage: contact.blocked ? "hand.raised.slash" : "hand.raised")
                }
                Button(role: .destructive) { deleting = contact } label: { Label("Delete", systemImage: "trash") }
            }
        }
        .onAppear {
            if contact.id == store.contacts.last?.id { Task { await store.loadMore() } }
        }
    }

    @ViewBuilder
    private var emptyState: some View {
        if !store.search.isEmpty {
            EmptyState(symbol: "magnifyingglass", title: "No results for \u{201C}\(store.search)\u{201D}", message: "Check the spelling or search by email or company.")
        } else if store.tag != nil || store.company != nil {
            EmptyState(symbol: "line.3.horizontal.decrease", title: "No contacts match", message: "Try removing a filter.", actionTitle: "Clear filters") {
                store.tag = nil
                store.company = nil
            }
        } else {
            EmptyState(
                symbol: "person.2",
                title: "No contacts yet",
                message: "Contacts are created when customers email or chat with you. You can also add one yourself.",
                actionTitle: env.session.canWrite ? "New contact" : nil,
                action: env.session.canWrite ? { creating = true } : nil
            )
        }
    }

    // MARK: Actions

    private func setBlocked(_ contact: Contact, _ blocked: Bool) async {
        Haptics.tap()
        do {
            try await store.setBlocked(contact, blocked)
            env.toasts.show(Toast(message: blocked ? "\(contact.displayName) blocked" : "\(contact.displayName) unblocked", kind: .success, actionTitle: "Undo") {
                Task { await setBlocked(contact, !blocked) }
            })
        } catch {
            env.toasts.error(error)
        }
    }

    private func delete(_ contact: Contact) async {
        do {
            try await store.delete(contact)
            Haptics.success()
            env.toasts.show("Contact deleted", kind: .success)
        } catch {
            env.toasts.error(error)
        }
    }

    #if DEBUG
    private func runDemoAction(_ action: String?) {
        switch action {
        case "contact-new":
            env.router.demoAction = nil
            creating = true
        case "contacts-filter":
            env.router.demoAction = nil
            showFilters = true
        case "contacts-name":
            env.router.demoAction = nil
            store.sort = .name
        case "contact-delete-confirm":
            env.router.demoAction = nil
            deleting = store.contacts.first
        default:
            break
        }
    }
    #endif
}

/// One contact in a list: avatar, name and a blocked mark, then company and
/// email, labels, and their ticket count.
struct ContactRow: View {
    let contact: Contact
    var showsDate = false

    private var subtitle: String {
        [contact.company?.name, contact.email].compactMap { $0 }.filter { !$0.isEmpty }.joined(separator: " · ")
    }

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            DirectoryAvatar(name: contact.displayName, url: contact.avatarURL, seed: contact.id, size: 38)
                .opacity(contact.blocked ? 0.55 : 1)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(contact.displayName)
                        .font(ODFont.sans(15.5, weight: .semibold))
                        .tracking(-0.2)
                        .foregroundStyle(contact.blocked ? Theme.fg3 : Theme.fg)
                        .lineLimit(1)
                    if contact.blocked {
                        TonePill(text: "Blocked", tone: .red, height: 18) {
                            Image(systemName: "hand.raised.fill").font(.system(size: 8, weight: .semibold))
                        }
                    }
                    Spacer(minLength: 6)
                    trailing
                }
                if !subtitle.isEmpty {
                    Text(subtitle)
                        .font(ODFont.sans(13.5))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(1)
                }
                if !contact.tags.isEmpty {
                    HStack(spacing: 4) {
                        ForEach(contact.tags.prefix(3)) { tag in LabelChip(name: tag.name, color: tag.color) }
                        if contact.tags.count > 3 {
                            Text("+\(contact.tags.count - 3)").font(ODFont.mono(11)).foregroundStyle(Theme.fg4)
                        }
                    }
                    .padding(.top, 3)
                }
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 11)
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(Rectangle())
        .overlay(alignment: .bottom) { Hairline(inset: 66) }
        .accessibilityElement(children: .combine)
    }

    @ViewBuilder
    private var trailing: some View {
        HStack(spacing: 8) {
            if contact.ticketCount > 0 {
                HStack(spacing: 3) {
                    Image(systemName: "tray").font(.system(size: 10, weight: .medium))
                    Text("\(contact.ticketCount)").font(ODFont.mono(11.5)).monospacedDigit()
                }
                .foregroundStyle(Theme.fg3)
                .accessibilityLabel(Format.plural(contact.ticketCount, "ticket"))
            }
            if showsDate {
                Text(Format.relShort(contact.createdAt))
                    .font(ODFont.mono(11.5))
                    .foregroundStyle(Theme.fg4)
            }
        }
    }
}
