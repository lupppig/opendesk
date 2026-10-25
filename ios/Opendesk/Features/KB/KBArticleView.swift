import SwiftUI

extension KBStore {
    /// Binds the shared store to the signed-in workspace (help center fallback: the instance's `/help/<slug>`).
    func attach(_ env: AppEnvironment) {
        let workspace = env.session.workspace?.workspace
        let fallback = workspace.flatMap { env.session.instance.webURL(path: "/help/\($0.slug ?? $0.id)") }
        attach(api: env.session.api, workspaceID: workspace?.id, helpFallback: fallback)
    }
}

/// One article: read as customers will see it, or edit it (title, excerpt,
/// address, collection and body), save it as a draft, publish or unpublish
/// it, delete it, and draft it from tickets with Ollie. `id == "new"` starts a
/// blank article.
struct ArticleView: View {
    let id: String

    var body: some View {
        // A link to another article replaces this one; its state starts over.
        KBArticleScreen(id: id).id(id)
    }
}

struct KBArticleScreen: View {
    let id: String

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    @State private var article: KBArticle?
    @State private var createdID: String?
    @State private var loadError: String?
    @State private var missing = false
    @State private var editing = false
    @State private var form = KBArticleForm()
    @State private var original = KBArticleForm()
    @State private var preview = false
    @State private var saving: KBStatus?
    @State private var bodyHeight: CGFloat = 240
    @State private var confirmDelete = false
    @State private var confirmDiscard = false
    @State private var showDraft = false
    @State private var pendingDraft: KBArticleDraft?
    @State private var listener: UUID?
    @State private var started = false
    @FocusState private var bodyFocused: Bool
    @FocusState private var titleFocused: Bool

    private var store: KBStore { KBStore.shared }
    private var articleID: String { createdID ?? id }
    private var isNew: Bool { articleID == "new" }
    private var dirty: Bool { !form.sameContent(as: original) }
    private var canDraft: Bool { env.session.canWrite && env.directory.ai.enabled }

    var body: some View {
        withDialogs
        #if DEBUG
            .onChange(of: env.router.demoAction) { _, action in demo(action) }
        #endif
    }

    private var screen: some View {
        Group {
            if editing {
                editor
            } else if let article {
                reader(article)
            } else if missing {
                EmptyState(symbol: "doc.questionmark", title: "This article isn't here any more", message: "It may have been deleted. Articles you can open are in the knowledge base.", actionTitle: "Back to the knowledge base") { dismiss() }
                    .frame(maxHeight: .infinity)
            } else if let loadError {
                ErrorState(title: "Couldn't load the article", message: loadError) { Task { await load() } }
                    .frame(maxHeight: .infinity)
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .background((editing ? Theme.chrome : Theme.surface).ignoresSafeArea())
        .toolbarVisibility(.visible, for: .navigationBar)
        .navigationBarTitleDisplayMode(.inline)
        .navigationBarBackButtonHidden(editing)
        .toolbar { toolbar }
        .tabBarHidden()
    }

    private var live: some View {
        screen
        .task { await start() }
        .onDisappear {
            env.realtime.unsubscribe(listener)
            listener = nil
        }
        .onChange(of: store.cached(articleID)) { _, fresh in
            guard let fresh, !editing, saving == nil else { return }
            article = fresh
        }
        .onChange(of: form.title) { _, title in
            guard isNew || article?.status == .draft, !form.slugEdited else { return }
            form.slug = KBSlug.make(title)
        }
    }

    private var withDialogs: some View {
        live
        .sheet(isPresented: $showDraft) {
            KBDraftSheet { draft in receive(draft) }
                .presentationDetents([.large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .confirmationDialog("Delete this article?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("Delete article", role: .destructive) { Task { await delete() } }
        } message: {
            Text(article?.status == .published ? "It comes off the help center right away. This can't be undone." : "This can't be undone.")
        }
        .confirmationDialog("Discard your changes?", isPresented: $confirmDiscard, titleVisibility: .visible) {
            Button("Discard changes", role: .destructive) { discard() }
            Button("Keep editing", role: .cancel) {}
        }
        .confirmationDialog("Replace what you wrote?", isPresented: Binding(get: { pendingDraft != nil }, set: { if !$0 { pendingDraft = nil } }), titleVisibility: .visible, presenting: pendingDraft) { draft in
            Button("Use Ollie's draft") { apply(draft) }
            Button("Keep mine", role: .cancel) {}
        } message: { _ in
            Text("Ollie's draft replaces the title, excerpt and body.")
        }
    }

    // MARK: Reading

    private func reader(_ article: KBArticle) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 8) {
                    if let collection = store.collection(article.collectionID) {
                        HStack(spacing: 6) {
                            Circle().fill(store.tone(for: collection.id).solid).frame(width: 7, height: 7)
                            Text(collection.name)
                        }
                        .font(ODFont.sans(13, weight: .medium))
                        .foregroundStyle(Theme.fg2)
                    } else {
                        Text("No collection").font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg3)
                    }
                    Spacer(minLength: 4)
                    TonePill(text: article.status.title, tone: article.status.tone)
                }
                .padding(.bottom, 12)

                Text(article.displayTitle)
                    .font(ODFont.sans(27, weight: .semibold, relativeTo: .largeTitle))
                    .tracking(-0.7)
                    .foregroundStyle(Theme.fg)
                    .fixedSize(horizontal: false, vertical: true)
                if !article.excerpt.isEmpty {
                    Text(article.excerpt)
                        .font(ODFont.sans(16.5, relativeTo: .title3))
                        .foregroundStyle(Theme.fg3)
                        .lineSpacing(2)
                        .padding(.top, 8)
                        .fixedSize(horizontal: false, vertical: true)
                }

                meta(article)
                    .padding(.top, 16)

                if article.status == .draft {
                    Notice(text: "Draft. Only your team can see it until it's published.", tone: .amber, symbol: "pencil.line")
                        .padding(.top, 16)
                }

                Hairline().padding(.vertical, 20)

                if HTMLText.plain(from: article.bodyHTML).trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    Text("This article has no text yet.")
                        .font(ODFont.sans(15))
                        .foregroundStyle(Theme.fg4)
                } else {
                    KBArticleWebView(html: article.bodyHTML, height: $bodyHeight)
                        .frame(height: bodyHeight)
                }

                if let url = store.publicURL(for: article) {
                    helpCenterCard(url)
                        .padding(.top, 28)
                }
            }
            .padding(.horizontal, 20)
            .padding(.top, 8)
            .padding(.bottom, 48)
        }
        .scrollIndicators(.hidden)
        .refreshable { await load() }
    }

    private func meta(_ article: KBArticle) -> some View {
        HStack(spacing: 10) {
            if let author = article.author {
                Avatar(name: author.name, url: author.avatarURL, seed: author.id, size: 26)
            }
            VStack(alignment: .leading, spacing: 1) {
                Text(article.author?.name ?? "Unknown author")
                    .font(ODFont.sans(13.5, weight: .medium))
                    .foregroundStyle(Theme.fg)
                Text(updatedLine(article))
                    .font(ODFont.sans(12.5))
                    .foregroundStyle(Theme.fg3)
            }
            Spacer(minLength: 4)
            if article.status == .published {
                VStack(alignment: .trailing, spacing: 1) {
                    Text(article.views.formatted())
                        .font(ODFont.mono(15, weight: .medium))
                        .foregroundStyle(Theme.fg)
                    Text(article.views == 1 ? "view" : "views")
                        .font(ODFont.sans(11.5))
                        .foregroundStyle(Theme.fg3)
                }
                .accessibilityElement(children: .combine)
            }
        }
    }

    private func updatedLine(_ article: KBArticle) -> String {
        if article.status == .published, let published = article.publishedAt, abs(published.timeIntervalSince(article.updatedAt)) > 3600 * 6 {
            return "Published \(Format.shortDate(published)) · updated \(Format.relLong(article.updatedAt))"
        }
        return (article.status == .published ? "Published " : "Updated ") + Format.relLong(article.status == .published ? (article.publishedAt ?? article.updatedAt) : article.updatedAt)
    }

    private func helpCenterCard(_ url: URL) -> some View {
        Link(destination: url) {
            HStack(spacing: 12) {
                Image(systemName: "globe")
                    .font(.system(size: 14, weight: .medium))
                    .foregroundStyle(Tone.blue.text)
                    .frame(width: 34, height: 34)
                    .background(Tone.blue.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.control - 2, style: .continuous))
                VStack(alignment: .leading, spacing: 2) {
                    Text("Open in help center")
                        .font(ODFont.sans(14.5, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                    Text(url.absoluteString.replacingOccurrences(of: "https://", with: ""))
                        .font(ODFont.mono(11.5))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
                Spacer(minLength: 4)
                Image(systemName: "arrow.up.right")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundStyle(Theme.fg3)
            }
            .padding(12)
            .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        }
        .buttonStyle(TapScaleStyle())
    }

    // MARK: Editing

    private var editor: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                VStack(alignment: .leading, spacing: 6) {
                    TextField(text: $form.title, axis: .vertical) {
                        Text("Article title").foregroundStyle(Theme.fg4)
                    }
                    .font(ODFont.sans(25, weight: .semibold, relativeTo: .title))
                    .tracking(-0.6)
                    .foregroundStyle(Theme.fg)
                    .focused($titleFocused)
                    .submitLabel(.next)
                    TextField(text: $form.excerpt, axis: .vertical) {
                        Text("A one line summary for search and the help center").foregroundStyle(Theme.fg4)
                    }
                    .font(ODFont.sans(15.5))
                    .foregroundStyle(Theme.fg2)
                    .lineLimit(1 ... 4)
                }
                .padding(.horizontal, 4)
                .padding(.top, 4)

                details

                if canDraft, form.isEmpty {
                    KBOllieCard { showDraft = true }
                        .transition(.opacity.combined(with: .move(edge: .top)))
                }

                KBBodyEditor(form: $form, preview: $preview, focused: $bodyFocused) {
                    env.toasts.show("This HTML can't be shown as text without losing something.", kind: .info)
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 8)
            .padding(.bottom, 24)
            .animation(reduceMotion ? nil : Motion.snappy, value: form.isEmpty)
        }
        .scrollDismissesKeyboard(.interactively)
        .safeAreaInset(edge: .bottom) { actionBar }
    }

    private var details: some View {
        Panel {
            Menu {
                Picker("Collection", selection: $form.collectionID) {
                    Text("No collection").tag(String?.none)
                    ForEach(store.collections) { collection in
                        Text(collection.name).tag(Optional(collection.id))
                    }
                }
            } label: {
                HStack(spacing: 10) {
                    Image(systemName: "folder").font(.system(size: 13, weight: .medium)).foregroundStyle(Theme.fg3).frame(width: 18)
                    Text("Collection").font(ODFont.sans(14.5)).foregroundStyle(Theme.fg2)
                    Spacer(minLength: 6)
                    if let collection = store.collection(form.collectionID) {
                        Circle().fill(store.tone(for: collection.id).solid).frame(width: 7, height: 7)
                        Text(collection.name).font(ODFont.sans(14.5, weight: .medium)).foregroundStyle(Theme.fg)
                    } else {
                        Text("None").font(ODFont.sans(14.5)).foregroundStyle(Theme.fg3)
                    }
                    Image(systemName: "chevron.up.chevron.down").font(.system(size: 10.5, weight: .semibold)).foregroundStyle(Theme.fg4)
                }
                .padding(.horizontal, 14)
                .frame(height: 46)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            Hairline(inset: 42)
            HStack(spacing: 10) {
                Image(systemName: "link").font(.system(size: 13, weight: .medium)).foregroundStyle(Theme.fg3).frame(width: 18)
                Text("/a/").font(ODFont.mono(13.5)).foregroundStyle(Theme.fg3)
                TextField(text: Binding(get: { form.slug }, set: { value in
                    form.slug = value
                    form.slugEdited = true
                })) {
                    Text("article-address").foregroundStyle(Theme.fg4)
                }
                .font(ODFont.mono(13.5))
                .foregroundStyle(Theme.fg)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .keyboardType(.URL)
                if form.slugEdited, !form.title.isEmpty, form.slug != KBSlug.make(form.title) {
                    Button {
                        form.slug = KBSlug.make(form.title)
                        form.slugEdited = false
                    } label: {
                        Image(systemName: "arrow.triangle.2.circlepath").font(.system(size: 12, weight: .medium)).foregroundStyle(Theme.fg3)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Make the address from the title")
                }
            }
            .padding(.horizontal, 14)
            .frame(height: 46)
            Hairline(inset: 42)
            HStack(spacing: 10) {
                Image(systemName: "circle.dashed").font(.system(size: 13, weight: .medium)).foregroundStyle(Theme.fg3).frame(width: 18)
                Text("Status").font(ODFont.sans(14.5)).foregroundStyle(Theme.fg2)
                Spacer(minLength: 6)
                TonePill(text: (article?.status ?? .draft).title, tone: (article?.status ?? .draft).tone)
            }
            .padding(.horizontal, 14)
            .frame(height: 46)
        }
    }

    private var actionBar: some View {
        let published = article?.status == .published
        return GlassEffectContainer(spacing: 10) {
            HStack(spacing: 10) {
                Button {
                    Task { await save(.draft) }
                } label: {
                    ButtonLabel(title: published ? "Unpublish" : "Save draft", busy: saving == .draft)
                        .font(ODFont.sans(15.5, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .frame(maxWidth: .infinity)
                        .frame(height: 48)
                        .contentShape(Capsule())
                }
                .buttonStyle(.plain)
                .glassEffect(.regular.interactive(), in: Capsule())
                .disabled(saving != nil)

                Button {
                    Task { await save(.published) }
                } label: {
                    ButtonLabel(title: published ? "Update" : "Publish", busy: saving == .published, symbol: published ? nil : "paperplane.fill")
                        .font(ODFont.sans(15.5, weight: .semibold))
                        .foregroundStyle(Theme.inverseFg)
                        .frame(maxWidth: .infinity)
                        .frame(height: 48)
                        .background(Theme.inverse.opacity(saving == nil ? 1 : 0.6), in: Capsule())
                        .contentShape(Capsule())
                }
                .buttonStyle(TapScaleStyle())
                .disabled(saving != nil)
            }
        }
        .padding(.horizontal, 16)
        .padding(.top, 8)
        .padding(.bottom, 6)
    }

    // MARK: Toolbar

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        if editing {
            ToolbarItem(placement: .topBarLeading) {
                Button("Cancel") { cancel() }
            }
            ToolbarItem(placement: .principal) {
                Text(isNew ? "New article" : "Edit article")
                    .font(ODFont.sans(15.5, weight: .semibold))
                    .foregroundStyle(Theme.fg)
            }
            if canDraft {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { showDraft = true } label: {
                        OllieMark(size: 22)
                    }
                    .accessibilityLabel("Draft from tickets with Ollie")
                }
            }
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    if form.raw {
                        Button { switchToText() } label: { Label("Edit as text", systemImage: "textformat") }
                    } else {
                        Button { form.useHTML() } label: { Label("Edit as HTML", systemImage: "chevron.left.forwardslash.chevron.right") }
                    }
                    if !isNew {
                        Divider()
                        Button(role: .destructive) { confirmDelete = true } label: { Label("Delete article", systemImage: "trash") }
                    }
                } label: {
                    Image(systemName: "ellipsis")
                }
                .accessibilityLabel("More")
            }
        } else if let article {
            if env.session.canWrite {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Edit") { startEditing() }
                        .fontWeight(.semibold)
                }
            }
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    if env.session.canWrite {
                        Button { Task { await setStatus(article.status == .published ? .draft : .published) } } label: {
                            Label(article.status == .published ? "Unpublish" : "Publish", systemImage: article.status == .published ? "eye.slash" : "paperplane")
                        }
                        if canDraft {
                            Button {
                                startEditing()
                                showDraft = true
                            } label: { Label("Redraft with Ollie", systemImage: "sparkles") }
                        }
                    }
                    if let url = store.publicURL(for: article) {
                        Divider()
                        Link(destination: url) { Label("Open in help center", systemImage: "arrow.up.right.square") }
                        Button {
                            UIPasteboard.general.url = url
                            env.toasts.show("Link copied", kind: .success)
                        } label: { Label("Copy link", systemImage: "link") }
                        ShareLink(item: url) { Label("Share", systemImage: "square.and.arrow.up") }
                    }
                    if env.session.canWrite {
                        Divider()
                        Button(role: .destructive) { confirmDelete = true } label: { Label("Delete article", systemImage: "trash") }
                    }
                } label: {
                    Image(systemName: "ellipsis")
                }
                .accessibilityLabel("More")
            }
        }
    }

    // MARK: Loading

    private func start() async {
        guard !started else { return }
        started = true
        store.attach(env)
        if listener == nil {
            listener = env.realtime.subscribe { update in KBStore.shared.handle(update) }
        }
        if isNew {
            form = KBArticleForm(collectionID: store.collectionID)
            original = form
            editing = true
            if KBDraftIntent.take(), canDraft {
                try? await Task.sleep(for: .milliseconds(350))
                showDraft = true
            } else {
                try? await Task.sleep(for: .milliseconds(450))
                titleFocused = true
            }
        } else {
            if let cached = store.cached(articleID) { article = cached }
            await load()
        }
        #if DEBUG
        demo(env.router.demoAction)
        #endif
    }

    private func load() async {
        guard !isNew else { return }
        do {
            let fresh = try await store.article(articleID)
            article = fresh
            loadError = nil
        } catch {
            guard !error.isCancellation else { return }
            if (error as? APIError)?.status == 404 {
                missing = true
            } else if article == nil {
                loadError = error.userMessage
            }
        }
    }

    // MARK: Actions

    private func startEditing() {
        guard let article else { return }
        form = KBArticleForm.from(article)
        original = form
        preview = false
        withAnimation(reduceMotion ? nil : Motion.fade) { editing = true }
    }

    private func cancel() {
        if dirty { confirmDiscard = true } else { discard() }
    }

    private func discard() {
        bodyFocused = false
        titleFocused = false
        if isNew {
            dismiss()
        } else {
            withAnimation(reduceMotion ? nil : Motion.fade) { editing = false }
        }
    }

    private func switchToText() {
        if !form.useText() {
            Haptics.warning()
            env.toasts.show("This HTML can't be shown as text without losing something.", kind: .info)
        }
    }

    private func save(_ status: KBStatus) async {
        let title = form.title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !title.isEmpty else {
            Haptics.warning()
            env.toasts.show("Give the article a title first.", kind: .error)
            titleFocused = true
            return
        }
        saving = status
        defer { saving = nil }
        let input = KBArticleInput(
            title: title,
            slug: form.slug.isEmpty ? nil : KBSlug.make(form.slug),
            excerpt: form.excerpt.trimmingCharacters(in: .whitespacesAndNewlines),
            collectionID: form.collectionID,
            bodyHTML: form.bodyHTML,
            status: status,
            isUpdate: !isNew
        )
        do {
            let wasPublished = article?.status == .published
            let saved = try await store.save(input, id: isNew ? nil : articleID)
            if isNew { createdID = saved.id }
            article = saved
            form = KBArticleForm.from(saved)
            original = form
            bodyFocused = false
            titleFocused = false
            Haptics.success()
            withAnimation(reduceMotion ? nil : Motion.fade) { editing = false }
            let message = switch (wasPublished, status) {
            case (false, .published): "Published"
            case (true, .draft): "Moved to drafts"
            case (true, .published): "Article updated"
            default: "Draft saved"
            }
            env.toasts.show(message, kind: .success)
        } catch {
            Haptics.error()
            env.toasts.error(error)
        }
    }

    private func setStatus(_ status: KBStatus) async {
        guard let article else { return }
        let input = KBArticleInput(title: article.title, slug: article.slug, excerpt: article.excerpt, collectionID: article.collectionID, bodyHTML: article.bodyHTML, status: status, isUpdate: true)
        do {
            self.article = try await store.save(input, id: article.id)
            Haptics.success()
            env.toasts.show(status == .published ? "Published" : "Moved to drafts", kind: .success)
        } catch {
            env.toasts.error(error)
        }
    }

    private func delete() async {
        guard let article else {
            dismiss()
            return
        }
        do {
            try await store.delete(article)
            Haptics.success()
            env.toasts.show("Article deleted", kind: .success)
            dismiss()
        } catch {
            env.toasts.error(error)
        }
    }

    private func receive(_ draft: KBArticleDraft) {
        if form.isEmpty { apply(draft) } else { pendingDraft = draft }
    }

    private func apply(_ draft: KBArticleDraft) {
        titleFocused = false
        bodyFocused = false
        withAnimation(reduceMotion ? nil : Motion.snappy) {
            form.title = draft.title
            form.excerpt = draft.excerpt
            form.load(html: draft.bodyHTML)
            preview = false
        }
        Haptics.success()
        env.toasts.show("Ollie drafted the article. Read it through before publishing.", kind: .success)
    }

    #if DEBUG
    /// QA states: `/demo/kb-edit`, `/demo/kb-preview`, `/demo/kb-html`, `/demo/kb-ollie`, `/demo/kb-delete`.
    private func demo(_ action: String?) {
        guard let action else { return }
        switch action {
        case "kb-edit":
            startEditing()
        case "kb-preview":
            startEditing()
            preview = true
        case "kb-html":
            startEditing()
            form.useHTML()
        case "kb-ollie", "kb-ollie-pick", "kb-ollie-run":
            if !editing { startEditing() }
            showDraft = true
            if action != "kb-ollie" { return }
        case "kb-delete":
            confirmDelete = true
        case "kb-unsaved":
            if !editing { startEditing() }
            form.title += " (edited)"
            confirmDiscard = true
        default:
            return
        }
        env.router.demoAction = nil
    }
    #endif
}
