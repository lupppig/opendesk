import SwiftUI

/// The Companies area's root list: by name, search by name or domain, a label
/// filter, swipe to delete, pull to refresh, all live.
struct CompaniesListView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var store = CompaniesStore()
    @State private var query = ""
    @FocusState private var searchFocused: Bool
    @State private var showScopes = false
    @State private var showFilters = false
    @State private var creating = false
    @State private var deleting: Company?

    private var subtitle: Text? {
        guard store.loadedOnce, !store.companies.isEmpty else { return nil }
        let people = store.companies.reduce(0) { $0 + $1.contactCount }
        var parts = [Format.plural(store.companies.count, "company", "companies") + (store.nextCursor == nil ? "" : "+")]
        if people > 0 { parts.append(Format.plural(people, "person", "people")) }
        return Text(parts.joined(separator: " · "))
    }

    var body: some View {
        VStack(spacing: 0) {
            GlassHeaderBar(
                title: "Companies",
                subtitle: subtitle,
                query: $query,
                searchPrompt: "Search companies or domains",
                searchFocused: $searchFocused,
                menuLabel: "Folders and views",
                onMenu: { showScopes = true }
            ) {
                GlassCircleButton(symbol: "line.3.horizontal.decrease", label: "Filter", badge: store.tag == nil ? 0 : 1) {
                    showFilters = true
                }
                if env.session.canWrite {
                    HeaderPrimaryButton(symbol: "plus", label: "New company") { creating = true }
                }
            }
            if let tag = store.tag {
                HStack {
                    DirectoryFilterChip(title: tag, onClear: { store.tag = nil }) {
                        Image(systemName: "tag").font(.system(size: 10.5, weight: .semibold)).foregroundStyle(Theme.fg3)
                    }
                    Spacer()
                }
                .padding(.horizontal, 16)
                .padding(.bottom, 10)
                .transition(.opacity.combined(with: .move(edge: .top)))
            }
            list
        }
        .background(Theme.chrome.ignoresSafeArea())
        .toolbarVisibility(.hidden, for: .navigationBar)
        .onAppear { store.start(api: env.session.api, realtime: env.realtime) }
        .onDisappear { if env.router.area != .companies { store.stop() } }
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
            DirectoryFilterSheet(entity: .company, tag: $store.tag)
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(isPresented: $creating) {
            CompanyEditSheet(company: nil) { body in
                let company = try await store.create(body)
                env.toasts.show("Company created", kind: .success)
                Task {
                    try? await Task.sleep(for: .milliseconds(350))
                    env.router.push(.company(company.id))
                }
            }
            .presentationCornerRadius(Theme.Radius.sheet)
        }
        .confirmationDialog(
            "Delete \(deleting?.name ?? "company")?",
            isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }),
            titleVisibility: .visible,
            presenting: deleting
        ) { company in
            Button("Delete company", role: .destructive) { Task { await delete(company) } }
        } message: { _ in
            Text("People stay, but they are no longer linked to this company.")
        }
        .motion(Motion.snappy, value: store.tag)
        #if DEBUG
        .onAppear { runDemoAction(env.router.demoAction) }
        .onChange(of: env.router.demoAction) { _, action in runDemoAction(action) }
        #endif
    }

    @ViewBuilder
    private var list: some View {
        if !store.loadedOnce {
            ScrollView { SkeletonRows() }
                .scrollDisabled(true)
                .background(Theme.surface)
                .clipShape(UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous))
        } else if let error = store.error, store.companies.isEmpty {
            ErrorState(title: "Couldn't load companies", message: error) { store.reload() }
                .frame(maxHeight: .infinity)
        } else {
            List {
                ForEach(store.companies) { company in row(company) }
                if store.loadingMore {
                    ProgressView().frame(maxWidth: .infinity).padding(.vertical, 18)
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                }
                if store.companies.isEmpty {
                    emptyState
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                }
                Color.clear.frame(height: 90)
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .background(Theme.surface)
            .clipShape(UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous))
            .refreshable { await store.refresh() }
            .motion(Motion.snappy, value: store.companies.map(\.id))
        }
    }

    private func row(_ company: Company) -> some View {
        Button {
            env.router.push(.company(company.id))
        } label: {
            CompanyRow(company: company)
        }
        .buttonStyle(RowPressStyle())
        .listRowInsets(EdgeInsets())
        .listRowSeparator(.hidden)
        .listRowBackground(Theme.surface)
        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
            if env.session.canWrite {
                Button { deleting = company } label: { Label("Delete", systemImage: "trash") }
                    .tint(Tone.red.solid)
            }
        }
        .swipeActions(edge: .leading, allowsFullSwipe: true) {
            if !company.domain.isEmpty {
                Button { DirectoryActions.open(DirectoryActions.website(company.domain)) } label: { Label("Website", systemImage: "safari") }
                    .tint(Theme.inverse)
            }
        }
        .contextMenu {
            if !company.domain.isEmpty {
                Button { DirectoryActions.open(DirectoryActions.website(company.domain)) } label: { Label("Open website", systemImage: "safari") }
                Button { DirectoryActions.copy(company.domain, what: "Domain", toasts: env.toasts) } label: { Label("Copy domain", systemImage: "doc.on.doc") }
            }
            if env.session.canWrite {
                Divider()
                Button(role: .destructive) { deleting = company } label: { Label("Delete", systemImage: "trash") }
            }
        }
        .onAppear {
            if company.id == store.companies.last?.id { Task { await store.loadMore() } }
        }
    }

    @ViewBuilder
    private var emptyState: some View {
        if !store.search.isEmpty {
            EmptyState(symbol: "magnifyingglass", title: "No results for \u{201C}\(store.search)\u{201D}", message: "Check the spelling or search by domain.")
        } else if store.tag != nil {
            EmptyState(symbol: "line.3.horizontal.decrease", title: "No companies match", message: "Try removing the label filter.", actionTitle: "Clear filter") {
                store.tag = nil
            }
        } else {
            EmptyState(
                symbol: "building.2",
                title: "No companies yet",
                message: "Group the people you help by the company they work for. Contacts with a matching email domain can be linked to it.",
                actionTitle: env.session.canWrite ? "New company" : nil,
                action: env.session.canWrite ? { creating = true } : nil
            )
        }
    }

    private func delete(_ company: Company) async {
        do {
            try await store.delete(company)
            Haptics.success()
            env.toasts.show("Company deleted", kind: .success)
        } catch {
            env.toasts.error(error)
        }
    }

    #if DEBUG
    private func runDemoAction(_ action: String?) {
        switch action {
        case "company-new":
            env.router.demoAction = nil
            creating = true
        case "companies-filter":
            env.router.demoAction = nil
            showFilters = true
        default:
            break
        }
    }
    #endif
}

/// One company in a list: its logo, name and domain, labels, and how many people.
struct CompanyRow: View {
    let company: Company

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            DirectoryAvatar(name: company.name, url: company.avatarURL, seed: company.id, size: 38, square: true)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(company.name)
                        .font(ODFont.sans(15.5, weight: .semibold))
                        .tracking(-0.2)
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    Spacer(minLength: 6)
                    if company.contactCount > 0 {
                        HStack(spacing: 3) {
                            Image(systemName: "person.2").font(.system(size: 10, weight: .medium))
                            Text("\(company.contactCount)").font(ODFont.mono(11.5)).monospacedDigit()
                        }
                        .foregroundStyle(Theme.fg3)
                        .accessibilityLabel(Format.plural(company.contactCount, "person", "people"))
                    }
                }
                Text(company.domain.isEmpty ? "No domain" : company.domain)
                    .font(ODFont.mono(12.5))
                    .foregroundStyle(company.domain.isEmpty ? Theme.fg4 : Theme.fg3)
                    .lineLimit(1)
                if !company.tags.isEmpty {
                    HStack(spacing: 4) {
                        ForEach(company.tags.prefix(3)) { tag in LabelChip(name: tag.name, color: tag.color) }
                        if company.tags.count > 3 {
                            Text("+\(company.tags.count - 3)").font(ODFont.mono(11)).foregroundStyle(Theme.fg4)
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
}
