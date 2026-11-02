import SwiftUI

/// The wiki (contracts/wiki.md): the team's runbooks, policies and how-tos, as a
/// tree of pages. The pages edited lately, then every page with its subpages
/// folded under it; search finds words in titles and text. Agents and admins add
/// pages here and under any page; viewers read.
struct WikiListView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var query = ""
    @FocusState private var searchFocused: Bool
    @State private var showScopes = false
    @State private var hits: [WikiSearchHit] = []
    @State private var searching = false
    @State private var searchError: String?
    @State private var searchTask: Task<Void, Never>?
    @State private var moving: WikiNode?
    @State private var deleting: WikiNode?
    @State private var creating = false
    @State private var listener: UUID?

    private var store: WikiStore { WikiStore.shared }
    private var term: String { query.trimmingCharacters(in: .whitespacesAndNewlines) }

    private var subtitle: Text? {
        guard store.loadedOnce, !store.nodes.isEmpty else { return nil }
        return Text(Format.plural(store.nodes.count, "page"))
    }

    var body: some View {
        VStack(spacing: 0) {
            GlassHeaderBar(
                title: "Wiki",
                subtitle: subtitle,
                query: $query,
                searchPrompt: "Search pages",
                searchFocused: $searchFocused,
                menuLabel: "Folders and views",
                onMenu: { showScopes = true }
            ) {
                if env.session.canWrite, !store.nodes.isEmpty {
                    HeaderPrimaryButton(symbol: "plus", label: "New page") { create(WikiPageCreate()) }
                        .disabled(creating)
                }
            }
            content
        }
        .background(Theme.chrome.ignoresSafeArea())
        .toolbarVisibility(.hidden, for: .navigationBar)
        .onChange(of: query) { _, _ in search() }
        .onAppear(perform: appear)
        .onDisappear {
            env.realtime.unsubscribe(listener)
            listener = nil
        }
        .sheet(isPresented: $showScopes) {
            ScopeSheet()
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(item: $moving) { node in
            WikiMoveSheet(node: node)
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .wikiDeleteConfirmation($deleting)
        #if DEBUG
        .onChange(of: env.router.demoAction) { _, action in demo(action) }
        #endif
    }

    // MARK: Content

    @ViewBuilder
    private var content: some View {
        if !store.loadedOnce && store.loading {
            ScrollView { SkeletonRows() }
                .scrollDisabled(true)
                .panelSurface()
        } else if let error = store.error, !store.loadedOnce {
            ErrorState(title: "Couldn't load the wiki", message: error) { store.reload() }
                .frame(maxHeight: .infinity)
        } else if store.nodes.isEmpty && term.isEmpty {
            ScrollView { firstRun }
                .panelSurface()
                .refreshable { await store.refresh() }
        } else {
            List {
                if term.isEmpty { tree } else { results }
                Color.clear.frame(height: 90)
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .panelSurface()
            .refreshable { await store.refresh() }
        }
    }

    @ViewBuilder
    private var tree: some View {
        let recent = Array(store.recent.prefix(5))
        if store.nodes.count > 3 {
            header("Recently edited")
            ForEach(recent) { node in
                Button { open(node.id) } label: {
                    WikiRecentRow(node: node, path: store.ancestors(of: node.id))
                }
                .buttonStyle(RowPressStyle())
                .listRowInsets(EdgeInsets())
                .listRowSeparator(.hidden)
                .listRowBackground(Theme.surface)
                .contextMenu { menu(node) }
            }
        }
        header("All pages")
        ForEach(store.outline()) { item in
            let subpages = store.children(of: item.node.id).count
            WikiTreeRow(node: item.node, level: item.level, subpages: subpages, expanded: store.expanded.contains(item.node.id), onToggle: {
                Haptics.selection()
                withAnimation(Motion.snappy) { store.toggle(item.node.id) }
            }, onOpen: { open(item.node.id) })
            .listRowInsets(EdgeInsets())
            .listRowSeparator(.hidden)
            .listRowBackground(Theme.surface)
            .contextMenu { menu(item.node) }
            .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                if env.session.canWrite {
                    Button { create(WikiPageCreate(parentID: item.node.id)) } label: { Label("Add a subpage", systemImage: "plus") }
                        .tint(Tone.blue.solid)
                    Button { moving = item.node } label: { Label("Move", systemImage: "folder") }
                        .tint(Tone.violet.solid)
                }
            }
        }
        .motion(Motion.snappy, value: store.expanded)
        if env.session.canWrite {
            Button { create(WikiPageCreate()) } label: {
                HStack(spacing: 10) {
                    Image(systemName: "plus").font(.system(size: 13, weight: .semibold)).frame(width: 28)
                    Text("New page").font(ODFont.sans(15))
                    Spacer()
                }
                .foregroundStyle(Theme.fg3)
                .padding(.leading, 8)
                .frame(height: 44)
                .contentShape(Rectangle())
            }
            .buttonStyle(RowPressStyle())
            .disabled(creating)
            .listRowInsets(EdgeInsets())
            .listRowSeparator(.hidden)
            .listRowBackground(Theme.surface)
        }
    }

    @ViewBuilder
    private var results: some View {
        if hits.isEmpty {
            Group {
                if searching {
                    ProgressView().frame(maxWidth: .infinity).padding(.vertical, 40)
                } else if let searchError {
                    EmptyState(symbol: "exclamationmark.triangle", title: "Couldn't search", message: searchError)
                } else {
                    EmptyState(symbol: "magnifyingglass", title: "No pages match \u{201C}\(term)\u{201D}", message: "Search looks at page titles and text.")
                }
            }
            .listRowSeparator(.hidden)
            .listRowBackground(Theme.surface)
        } else {
            ForEach(hits) { hit in
                Button { open(hit.id) } label: { WikiSearchRow(hit: hit) }
                    .buttonStyle(RowPressStyle())
                    .listRowInsets(EdgeInsets())
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)
            }
        }
    }

    private func header(_ text: String) -> some View {
        SectionLabel(text)
            .padding(.horizontal, 16)
            .padding(.top, 16)
            .padding(.bottom, 4)
            .listRowInsets(EdgeInsets())
            .listRowSeparator(.hidden)
            .listRowBackground(Theme.surface)
    }

    /// A new workspace: what the wiki is for, the first page, and starter outlines.
    private var firstRun: some View {
        VStack(spacing: 8) {
            EmptyState(
                symbol: "book.pages",
                title: "Write down how your team works",
                message: env.session.canWrite
                    ? "The wiki holds your team's runbooks, policies and how-tos. Only members see it: customers, the help center and the chat widget never do."
                    : "Your team hasn't written any pages yet. Agents and admins can add the first one.",
                actionTitle: env.session.canWrite ? "Create the first page" : nil
            ) { create(WikiPageCreate()) }
            if env.session.canWrite {
                VStack(alignment: .leading, spacing: 8) {
                    SectionLabel("Start from a template")
                    Panel {
                        ForEach(Array(WikiTemplate.all.enumerated()), id: \.element.id) { index, template in
                            if index > 0 { Hairline(inset: 52) }
                            Button {
                                create(WikiPageCreate(title: template.title, icon: template.icon, bodyHTML: template.bodyHTML))
                            } label: {
                                HStack(spacing: 12) {
                                    Text(template.icon).font(.system(size: 20)).frame(width: 28)
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(template.title).font(ODFont.sans(15, weight: .medium)).foregroundStyle(Theme.fg)
                                        Text(template.hint).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3)
                                    }
                                    Spacer(minLength: 4)
                                    Chevron()
                                }
                                .padding(.horizontal, 14)
                                .padding(.vertical, 11)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(RowPressStyle())
                            .disabled(creating)
                        }
                    }
                }
                .padding(.horizontal, 20)
            }
        }
        .padding(.bottom, 90)
    }

    @ViewBuilder
    private func menu(_ node: WikiNode) -> some View {
        Button { open(node.id) } label: { Label("Open", systemImage: "doc.text") }
        WikiPageMenu(
            node: node,
            onAddSubpage: { create(WikiPageCreate(parentID: node.id)) },
            onMove: { moving = node },
            onDelete: { deleting = node }
        )
    }

    // MARK: Actions

    private func appear() {
        store.attach(env)
        if listener == nil {
            listener = env.realtime.subscribe { update in WikiStore.shared.handle(update) }
        }
        if store.loadedOnce { store.reload() }
        #if DEBUG
        demo(env.router.demoAction)
        #endif
    }

    private func open(_ id: String) {
        env.router.push(.wikiPage(id))
    }

    private func create(_ body: WikiPageCreate) {
        guard !creating else { return }
        creating = true
        Task {
            await WikiCreate.page(body, env: env)
            creating = false
        }
    }

    private func search() {
        searchTask?.cancel()
        let text = term
        guard !text.isEmpty else {
            hits = []
            searching = false
            searchError = nil
            return
        }
        searching = true
        searchTask = Task {
            try? await Task.sleep(for: .milliseconds(250))
            guard !Task.isCancelled else { return }
            do {
                let found = try await store.search(text)
                guard !Task.isCancelled else { return }
                hits = found
                searchError = nil
            } catch {
                guard !error.isCancellation, !Task.isCancelled else { return }
                hits = []
                searchError = error.userMessage
            }
            searching = false
        }
    }

    #if DEBUG
    /// QA states: `/demo/wiki-expand`, `/demo/wiki-move`, `/demo/wiki-delete`.
    private func demo(_ action: String?) {
        guard let action, action.hasPrefix("wiki-") else { return }
        switch action {
        case "wiki-expand":
            store.expand(store.nodes.map(\.id))
        case "wiki-move":
            moving = store.children(of: nil).last
        case "wiki-delete":
            deleting = store.children(of: nil).first
        default:
            return
        }
        env.router.demoAction = nil
    }
    #endif
}
