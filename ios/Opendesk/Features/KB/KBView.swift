import SwiftUI

/// The knowledge base: what is published and how much it is read, the
/// collections as a strip of cards, a status filter and the articles. New
/// articles start blank or from Ollie's draft of a few tickets.
struct KBListView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var query = ""
    @FocusState private var searchFocused: Bool
    @State private var showScopes = false
    @State private var editingCollection: KBCollectionDraft?
    @State private var deletingCollection: KBCollection?
    @State private var deletingArticle: KBArticle?
    @State private var listener: UUID?

    private var store: KBStore { KBStore.shared }

    private var subtitle: Text? {
        guard store.loadedOnce, !store.everything.isEmpty else { return nil }
        var parts = ["\(store.publishedCount) published"]
        if store.draftCount > 0 { parts.append(Format.plural(store.draftCount, "draft")) }
        return Text(parts.joined(separator: " · "))
    }

    private var canDraft: Bool { env.session.canWrite && env.directory.ai.enabled }

    var body: some View {
        VStack(spacing: 0) {
            GlassHeaderBar(
                title: "Knowledge base",
                subtitle: subtitle,
                query: $query,
                searchPrompt: "Search articles",
                searchFocused: $searchFocused,
                menuLabel: "Folders and views",
                onMenu: { showScopes = true }
            ) {
                if env.session.canWrite {
                    HeaderPrimaryButton(symbol: "plus", label: "New article") {
                        Haptics.tap()
                        env.router.push(.article("new"))
                    }
                }
            }
            content
        }
        .background(Theme.chrome.ignoresSafeArea())
        .toolbarVisibility(.hidden, for: .navigationBar)
        .onChange(of: query) { _, value in store.setSearch(value) }
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
        .sheet(item: $editingCollection) { draft in
            KBCollectionSheet(draft: draft)
                .presentationDetents([.medium])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .confirmationDialog(
            "Delete \u{201C}\(deletingCollection?.name ?? "")\u{201D}?",
            isPresented: Binding(get: { deletingCollection != nil }, set: { if !$0 { deletingCollection = nil } }),
            titleVisibility: .visible,
            presenting: deletingCollection
        ) { collection in
            Button("Delete collection", role: .destructive) { delete(collection) }
        } message: { collection in
            Text(collection.articleCount > 0
                ? "Its \(Format.plural(collection.articleCount, "article")) stay in the knowledge base, without a collection."
                : "The collection is empty.")
        }
        .confirmationDialog(
            "Delete \u{201C}\(deletingArticle?.displayTitle ?? "")\u{201D}?",
            isPresented: Binding(get: { deletingArticle != nil }, set: { if !$0 { deletingArticle = nil } }),
            titleVisibility: .visible,
            presenting: deletingArticle
        ) { article in
            Button("Delete article", role: .destructive) { delete(article) }
        } message: { article in
            Text(article.status == .published ? "It comes off the help center right away. This can't be undone." : "This can't be undone.")
        }
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
                .background(Theme.surface)
                .clipShape(UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous))
        } else if let error = store.error, !store.loadedOnce {
            ErrorState(title: "Couldn't load the knowledge base", message: error) { store.reload() }
                .frame(maxHeight: .infinity)
        } else {
            List {
                if store.search.isEmpty {
                    KBOverview(
                        published: store.publishedCount,
                        drafts: store.draftCount,
                        views: store.totalViews,
                        top: store.topArticle,
                        onTop: { env.router.push(.article($0.id)) },
                        onDrafts: { withAnimation(Motion.snappy) { store.status = .draft } }
                    )
                    .listRowInsets(EdgeInsets(top: 14, leading: 16, bottom: 6, trailing: 16))
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)

                    if canDraft {
                        KBOllieCard {
                            KBDraftIntent.request()
                            env.router.push(.article("new"))
                        }
                        .listRowInsets(EdgeInsets(top: 6, leading: 16, bottom: 6, trailing: 16))
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                    }
                }

                KBCollectionStrip(
                    selected: store.collectionID,
                    canWrite: env.session.canWrite,
                    onSelect: { id in
                        Haptics.selection()
                        withAnimation(Motion.snappy) { store.collectionID = id }
                    },
                    onNew: { editingCollection = KBCollectionDraft(collection: nil) },
                    onRename: { editingCollection = KBCollectionDraft(collection: $0) },
                    onDelete: { deletingCollection = $0 }
                )
                .listRowInsets(EdgeInsets(top: 10, leading: 0, bottom: 4, trailing: 0))
                .listRowSeparator(.hidden)
                .listRowBackground(Theme.surface)

                KBStatusPicker(selection: Binding(get: { store.status }, set: { value in
                    Haptics.selection()
                    withAnimation(Motion.snappy) { store.status = value }
                }), counts: counts)
                    .listRowInsets(EdgeInsets(top: 8, leading: 16, bottom: 6, trailing: 16))
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)

                ForEach(store.articles) { article in
                    Button {
                        env.router.push(.article(article.id))
                    } label: {
                        KBArticleRow(
                            article: article,
                            collection: store.collection(article.collectionID),
                            tone: store.tone(for: article.collectionID),
                            showCollection: store.collectionID == nil
                        )
                    }
                    .buttonStyle(RowPressStyle())
                    .listRowInsets(EdgeInsets())
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)
                    .swipeActions(edge: .trailing, allowsFullSwipe: false) {
                        if env.session.canWrite {
                            Button { deletingArticle = article } label: { Label("Delete", systemImage: "trash") }
                                .tint(Theme.danger)
                            Button { toggle(article) } label: {
                                Label(article.status == .published ? "Unpublish" : "Publish", systemImage: article.status == .published ? "eye.slash" : "paperplane")
                            }
                            .tint(article.status == .published ? Tone.amber.solid : Tone.green.solid)
                        }
                    }
                    .contextMenu { menu(article) }
                }

                if store.articles.isEmpty {
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
            .motion(Motion.snappy, value: store.articles.map(\.id))
        }
    }

    private var counts: [KBStatusFilter: Int] {
        let scoped = store.everything.filter { store.collectionID == nil || $0.collectionID == store.collectionID }
        let published = scoped.filter { $0.status == .published }.count
        return [.all: scoped.count, .published: published, .draft: scoped.count - published]
    }

    @ViewBuilder
    private var emptyState: some View {
        if !store.search.isEmpty {
            EmptyState(symbol: "magnifyingglass", title: "No articles match \u{201C}\(store.search)\u{201D}", message: "Search looks at titles, excerpts and the article text.")
        } else if store.everything.isEmpty {
            EmptyState(
                symbol: "book.closed",
                title: "Write your first article",
                message: "Articles answer the questions customers ask most. Published ones show on your help center and help Ollie answer.",
                actionTitle: env.session.canWrite ? "New article" : nil
            ) { env.router.push(.article("new")) }
        } else if store.status == .draft {
            EmptyState(symbol: "doc.badge.ellipsis", title: "No drafts", message: "Articles you save without publishing wait here.")
        } else if store.status == .published {
            EmptyState(symbol: "doc.text", title: "Nothing published here yet", message: "Publish a draft to put it on the help center.")
        } else {
            EmptyState(
                symbol: "folder",
                title: "This collection is empty",
                message: "Move an article here from its details, or write a new one.",
                actionTitle: env.session.canWrite ? "New article" : nil
            ) { env.router.push(.article("new")) }
        }
    }

    @ViewBuilder
    private func menu(_ article: KBArticle) -> some View {
        Button { env.router.push(.article(article.id)) } label: { Label("Open", systemImage: "doc.text") }
        if let url = store.publicURL(for: article) {
            Link(destination: url) { Label("Open in help center", systemImage: "arrow.up.right.square") }
            Button {
                UIPasteboard.general.url = url
                env.toasts.show("Link copied", kind: .success)
            } label: { Label("Copy link", systemImage: "link") }
        }
        if env.session.canWrite {
            Divider()
            Button { toggle(article) } label: {
                Label(article.status == .published ? "Unpublish" : "Publish", systemImage: article.status == .published ? "eye.slash" : "paperplane")
            }
            Button(role: .destructive) { deletingArticle = article } label: { Label("Delete", systemImage: "trash") }
        }
    }

    // MARK: Actions

    private func appear() {
        store.attach(env)
        if listener == nil {
            listener = env.realtime.subscribe { update in KBStore.shared.handle(update) }
        }
        if store.loadedOnce { store.reload() }
        #if DEBUG
        demo(env.router.demoAction)
        #endif
    }

    private func toggle(_ article: KBArticle) {
        let status: KBStatus = article.status == .published ? .draft : .published
        let input = KBArticleInput(title: article.title, slug: article.slug, excerpt: article.excerpt, collectionID: article.collectionID, bodyHTML: article.bodyHTML, status: status, isUpdate: true)
        Task {
            do {
                _ = try await store.save(input, id: article.id)
                Haptics.success()
                env.toasts.show(status == .published ? "Published" : "Moved to drafts", kind: .success)
            } catch {
                env.toasts.error(error)
            }
        }
    }

    private func delete(_ article: KBArticle) {
        Task {
            do {
                try await store.delete(article)
                Haptics.success()
                env.toasts.show("Article deleted", kind: .success)
            } catch {
                env.toasts.error(error)
            }
        }
    }

    private func delete(_ collection: KBCollection) {
        Task {
            do {
                try await store.deleteCollection(collection)
                Haptics.success()
                env.toasts.show("Collection deleted", kind: .success)
            } catch {
                env.toasts.error(error)
            }
        }
    }

    #if DEBUG
    /// QA states: `/demo/kb-new-collection`, `/demo/kb-rename-collection`, `/demo/kb-drafts`,
    /// `/demo/kb-published`, `/demo/kb-all`, `/demo/kb-collection-<n>`, `/demo/kb-new-draft`.
    private func demo(_ action: String?) {
        guard let action, action.hasPrefix("kb-") else { return }
        switch action {
        case "kb-new-collection": editingCollection = KBCollectionDraft(collection: nil)
        case "kb-rename-collection": editingCollection = KBCollectionDraft(collection: store.collections.first)
        case "kb-delete-collection": deletingCollection = store.collections.last
        case "kb-drafts": store.status = .draft
        case "kb-published": store.status = .published
        case "kb-all":
            store.status = .all
            store.collectionID = nil
        case "kb-new-draft":
            KBDraftIntent.request()
            env.router.push(.article("new"))
        default:
            if action.hasPrefix("kb-collection-"), let n = Int(action.dropFirst("kb-collection-".count)), store.collections.indices.contains(n - 1) {
                store.collectionID = store.collections[n - 1].id
            } else {
                return
            }
        }
        env.router.demoAction = nil
    }
    #endif
}
