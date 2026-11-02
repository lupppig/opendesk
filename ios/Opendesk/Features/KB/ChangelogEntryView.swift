import SwiftUI

extension ChangelogStore {
    func attach(_ env: AppEnvironment) {
        attach(api: env.session.api, workspaceID: env.session.workspace?.workspace.id)
    }
}

/// What the editor holds for one changelog entry.
struct ChangelogForm: KBBodyForm, Equatable {
    var title = ""
    var excerpt = ""
    var slug = ""
    var slugEdited = false
    var categories: [String] = []
    /// The date it carries; nil dates it when it is published.
    var publishedAt: Date?
    var markdown = ""
    var html = ""
    var raw = false

    static func from(_ entry: ChangelogEntry) -> ChangelogForm {
        var form = ChangelogForm(title: entry.title, excerpt: entry.excerpt, slug: entry.slug, slugEdited: true, categories: entry.categories, publishedAt: entry.publishedAt)
        form.load(html: entry.bodyHTML)
        return form
    }

    var isEmpty: Bool {
        title.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && excerpt.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty && bodyIsEmpty
    }

    func sameContent(as other: ChangelogForm) -> Bool {
        title == other.title && excerpt == other.excerpt && slug == other.slug && categories == other.categories
            && publishedAt == other.publishedAt && bodyHTML == other.bodyHTML
    }
}

/// One changelog entry (contracts/api.md "Changelog"): read it as the help center
/// shows it, or edit it (title, excerpt, categories, date, address and body), save
/// it as a draft, publish or schedule it, unpublish or delete it. `id == "new"`
/// starts a blank one.
struct ChangelogEntryView: View {
    let id: String

    var body: some View {
        ChangelogEntryScreen(id: id).id(id)
    }
}

struct ChangelogEntryScreen: View {
    let id: String

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    @State private var entry: ChangelogEntry?
    @State private var createdID: String?
    @State private var loadError: String?
    @State private var missing = false
    @State private var editing = false
    @State private var form = ChangelogForm()
    @State private var original = ChangelogForm()
    @State private var preview = false
    @State private var saving: KBStatus?
    @State private var bodyHeight: CGFloat = 240
    @State private var confirmDelete = false
    @State private var confirmDiscard = false
    @State private var addingCategory = false
    @State private var listener: UUID?
    @State private var started = false
    @FocusState private var bodyFocused: Bool
    @FocusState private var titleFocused: Bool

    private var store: ChangelogStore { ChangelogStore.shared }
    private var entryID: String { createdID ?? id }
    private var isNew: Bool { entryID == "new" }
    private var dirty: Bool { !form.sameContent(as: original) }
    /// Publishing an entry dated later schedules it.
    private var willSchedule: Bool { (form.publishedAt ?? .distantPast) > AppClock.now }

    var body: some View {
        screen
            .onAppear {
                // Back from a page pushed over this one: listen again, and catch up.
                if listener == nil {
                    listener = env.realtime.subscribe { update in ChangelogStore.shared.handle(update) }
                }
                if started, !editing { Task { await load() } }
            }
            .task { await start() }
            .onDisappear {
                env.realtime.unsubscribe(listener)
                listener = nil
            }
            .onChange(of: store.cached(entryID)) { _, fresh in
                guard let fresh, !editing, saving == nil else { return }
                entry = fresh
            }
            .onChange(of: form.title) { _, title in
                guard !form.slugEdited else { return }
                form.slug = KBSlug.make(title)
            }
            .sheet(isPresented: $addingCategory) {
                ChangelogCategorySheet(current: form.categories) { form.categories = $0 }
                    .presentationDetents([.medium, .large])
                    .presentationCornerRadius(Theme.Radius.sheet)
            }
            .confirmationDialog("Delete this update?", isPresented: $confirmDelete, titleVisibility: .visible) {
                Button("Delete update", role: .destructive) { Task { await delete() } }
            } message: {
                Text(entry?.status == .published ? "It disappears from your changelog and its feed right away." : "This can't be undone.")
            }
            .confirmationDialog("Discard your changes?", isPresented: $confirmDiscard, titleVisibility: .visible) {
                Button("Discard changes", role: .destructive) { discard() }
                Button("Keep editing", role: .cancel) {}
            }
        #if DEBUG
            .onChange(of: env.router.demoAction) { _, action in demo(action) }
        #endif
    }

    private var screen: some View {
        Group {
            if editing {
                editor
            } else if let entry {
                reader(entry)
            } else if missing {
                EmptyState(symbol: "doc.questionmark", title: "Update not found", message: "It may have been deleted. Updates you can open are in the changelog.", actionTitle: "Back to the changelog") { dismiss() }
                    .frame(maxHeight: .infinity)
            } else if let loadError {
                ErrorState(title: "Couldn't load the update", message: loadError) { Task { await load() } }
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

    // MARK: Reading

    private func reader(_ entry: ChangelogEntry) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                HStack(spacing: 6) {
                    ForEach(entry.categories, id: \.self) { ChangelogCategoryPill(name: $0) }
                    Spacer(minLength: 4)
                    ChangelogStatePill(entry: entry)
                }
                .padding(.bottom, 12)

                Text(entry.displayTitle)
                    .font(ODFont.sans(27, weight: .semibold, relativeTo: .largeTitle))
                    .tracking(-0.7)
                    .foregroundStyle(Theme.fg)
                    .fixedSize(horizontal: false, vertical: true)
                if !entry.excerpt.isEmpty {
                    Text(entry.excerpt)
                        .font(ODFont.sans(16.5, relativeTo: .title3))
                        .foregroundStyle(Theme.fg3)
                        .lineSpacing(2)
                        .padding(.top, 8)
                        .fixedSize(horizontal: false, vertical: true)
                }

                meta(entry)
                    .padding(.top, 16)

                switch entry.state() {
                case .draft:
                    Notice(text: "Draft. Only your team can see it until it's published.", tone: .amber, symbol: "pencil.line")
                        .padding(.top, 16)
                case .scheduled:
                    Notice(text: "Scheduled for \(Format.fullDate(entry.publishedAt)). It stays hidden until then.", tone: .blue, symbol: "calendar.badge.clock")
                        .padding(.top, 16)
                case .published:
                    EmptyView()
                }

                Hairline().padding(.vertical, 20)

                if HTMLText.plain(from: entry.bodyHTML).trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                    Text("This update has no text yet.")
                        .font(ODFont.sans(15))
                        .foregroundStyle(Theme.fg4)
                } else {
                    KBArticleWebView(html: entry.bodyHTML, height: $bodyHeight)
                        .frame(height: bodyHeight)
                }

                if let url = store.publicURL(for: entry, helpCenter: KBStore.shared.helpCenterURL) {
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

    private func meta(_ entry: ChangelogEntry) -> some View {
        HStack(spacing: 10) {
            if let author = entry.author {
                Avatar(name: author.name, url: author.avatarURL, seed: author.id, size: 26)
            }
            VStack(alignment: .leading, spacing: 1) {
                Text(entry.author?.name ?? "Unknown author")
                    .font(ODFont.sans(13.5, weight: .medium))
                    .foregroundStyle(Theme.fg)
                Text(dateLine(entry))
                    .font(ODFont.sans(12.5))
                    .foregroundStyle(Theme.fg3)
            }
            Spacer(minLength: 4)
        }
    }

    private func dateLine(_ entry: ChangelogEntry) -> String {
        guard let date = entry.publishedAt else { return "Not dated · updated \(Format.relLong(entry.updatedAt))" }
        let day = date.formatted(.dateTime.month(.wide).day().year())
        switch entry.state() {
        case .published: return "Published \(day)"
        case .scheduled: return "Goes live \(day)"
        case .draft: return "Dated \(day) · updated \(Format.relLong(entry.updatedAt))"
        }
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
                    TextField(text: Binding(get: { form.title }, set: { form.title = String($0.replacingOccurrences(of: "\n", with: " ").prefix(200)) }), axis: .vertical) {
                        Text("What changed?").foregroundStyle(Theme.fg4)
                    }
                    .font(ODFont.sans(25, weight: .semibold, relativeTo: .title))
                    .tracking(-0.6)
                    .foregroundStyle(Theme.fg)
                    .focused($titleFocused)
                    .submitLabel(.next)
                    TextField(text: Binding(get: { form.excerpt }, set: { form.excerpt = String($0.prefix(300)) }), axis: .vertical) {
                        Text("A one line summary for the changelog and its feed").foregroundStyle(Theme.fg4)
                    }
                    .font(ODFont.sans(15.5))
                    .foregroundStyle(Theme.fg2)
                    .lineLimit(1 ... 4)
                }
                .padding(.horizontal, 4)
                .padding(.top, 4)

                details

                KBBodyEditor(form: $form, preview: $preview, focused: $bodyFocused, noun: "update", prompt: "Describe the update. Short lists help readers see what's new.") {
                    env.toasts.show("This HTML can't be shown as text without losing something.", kind: .info)
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 8)
            .padding(.bottom, 24)
        }
        .scrollDismissesKeyboard(.interactively)
        .safeAreaInset(edge: .bottom) { actionBar }
    }

    private var details: some View {
        Panel {
            // Categories
            HStack(alignment: .center, spacing: 10) {
                Image(systemName: "tag").font(.system(size: 13, weight: .medium)).foregroundStyle(Theme.fg3).frame(width: 18)
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 6) {
                        if form.categories.isEmpty {
                            Text("Categories").font(ODFont.sans(14.5)).foregroundStyle(Theme.fg2)
                        }
                        ForEach(form.categories, id: \.self) { name in
                            ChangelogCategoryPill(name: name) {
                                withAnimation(Motion.snappy) { form.categories.removeAll { $0 == name } }
                            }
                        }
                    }
                }
                if form.categories.count < ChangelogCategories.max {
                    Button { addingCategory = true } label: {
                        Text("Add")
                            .font(ODFont.sans(13.5, weight: .medium))
                            .foregroundStyle(Theme.fg2)
                            .padding(.horizontal, 10)
                            .frame(height: 28)
                            .background(Theme.hover, in: Capsule())
                    }
                    .buttonStyle(TapScaleStyle())
                    .accessibilityLabel("Add a category")
                }
            }
            .padding(.horizontal, 14)
            .frame(minHeight: 46)
            Hairline(inset: 42)

            // Date
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 10) {
                    Image(systemName: "calendar").font(.system(size: 13, weight: .medium)).foregroundStyle(Theme.fg3).frame(width: 18)
                    Text("Date").font(ODFont.sans(14.5)).foregroundStyle(Theme.fg2)
                    Spacer(minLength: 6)
                    if let date = form.publishedAt {
                        DatePicker("Date", selection: Binding(get: { date }, set: { form.publishedAt = $0 }), displayedComponents: [.date, .hourAndMinute])
                            .labelsHidden()
                            .datePickerStyle(.compact)
                        Button {
                            withAnimation(Motion.fade) { form.publishedAt = nil }
                        } label: {
                            Image(systemName: "xmark.circle.fill").font(.system(size: 15)).foregroundStyle(Theme.fg4)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Date it when published")
                    } else {
                        Button("When published") {
                            withAnimation(Motion.fade) { form.publishedAt = Calendar.current.date(bySetting: .second, value: 0, of: AppClock.now) ?? AppClock.now }
                        }
                        .font(ODFont.sans(14.5, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .accessibilityHint("Pick a date")
                    }
                }
                Text(dateHint)
                    .font(ODFont.sans(12))
                    .foregroundStyle(Theme.fg3)
                    .padding(.leading, 28)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            Hairline(inset: 42)

            // Address
            HStack(spacing: 10) {
                Image(systemName: "link").font(.system(size: 13, weight: .medium)).foregroundStyle(Theme.fg3).frame(width: 18)
                Text("/changelog/").font(ODFont.mono(13.5)).foregroundStyle(Theme.fg3)
                TextField(text: Binding(get: { form.slug }, set: { value in
                    form.slug = value
                    form.slugEdited = true
                })) {
                    Text(KBSlug.make(form.title).isEmpty ? "update-address" : KBSlug.make(form.title)).foregroundStyle(Theme.fg4)
                }
                .font(ODFont.mono(13.5))
                .foregroundStyle(Theme.fg)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .keyboardType(.URL)
            }
            .padding(.horizontal, 14)
            .frame(height: 46)
        }
    }

    private var dateHint: String {
        guard let date = form.publishedAt else { return "Set to the moment you publish." }
        if entry?.state() == .scheduled, date == entry?.publishedAt {
            return "Scheduled for \(Format.fullDate(date)). It stays hidden until then."
        }
        if date > AppClock.now, entry?.status != .published { return "Publishing schedules it for \(Format.fullDate(date))." }
        return "Readers see this date on the update."
    }

    private var actionBar: some View {
        let published = entry?.status == .published
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
                    ButtonLabel(title: published ? "Update" : willSchedule ? "Schedule" : "Publish", busy: saving == .published, symbol: published ? nil : willSchedule ? "calendar.badge.clock" : "paperplane.fill")
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
                Text(isNew ? "New update" : "Edit update")
                    .font(ODFont.sans(15.5, weight: .semibold))
                    .foregroundStyle(Theme.fg)
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
                        Button(role: .destructive) { confirmDelete = true } label: { Label("Delete update", systemImage: "trash") }
                    }
                } label: {
                    Image(systemName: "ellipsis")
                }
                .accessibilityLabel("More")
            }
        } else if let entry {
            if env.session.canWrite {
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Edit") { startEditing() }
                        .fontWeight(.semibold)
                }
            }
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    if env.session.canWrite {
                        Button { Task { await setStatus(entry.status == .published ? .draft : .published) } } label: {
                            Label(entry.status == .published ? "Unpublish" : (entry.publishedAt ?? .distantPast) > AppClock.now ? "Schedule" : "Publish",
                                  systemImage: entry.status == .published ? "eye.slash" : "paperplane")
                        }
                    }
                    if let url = store.publicURL(for: entry, helpCenter: KBStore.shared.helpCenterURL) {
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
                        Button(role: .destructive) { confirmDelete = true } label: { Label("Delete update", systemImage: "trash") }
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
        KBStore.shared.attach(env)
        if isNew {
            form = ChangelogForm()
            original = form
            editing = true
            try? await Task.sleep(for: .milliseconds(450))
            titleFocused = true
        } else {
            if let cached = store.cached(entryID) { entry = cached }
            await load()
        }
        #if DEBUG
        demo(env.router.demoAction)
        #endif
    }

    private func load() async {
        guard !isNew else { return }
        do {
            entry = try await store.entry(entryID)
            loadError = nil
        } catch {
            guard !error.isCancellation else { return }
            if (error as? APIError)?.status == 404 {
                missing = true
            } else if entry == nil {
                loadError = error.userMessage
            }
        }
    }

    // MARK: Actions

    private func startEditing() {
        guard let entry else { return }
        form = ChangelogForm.from(entry)
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
            env.toasts.show("Give the update a title first.", kind: .error)
            titleFocused = true
            return
        }
        saving = status
        defer { saving = nil }
        var input = ChangelogEntryInput(
            title: title,
            slug: form.slug.isEmpty ? KBSlug.make(title) : KBSlug.make(form.slug),
            bodyHTML: form.bodyHTML,
            categories: form.categories,
            status: status
        )
        // The excerpt is derived from the body unless written, and the date is set when
        // first published unless given: each is sent only once it was changed here.
        let excerpt = form.excerpt.trimmingCharacters(in: .whitespacesAndNewlines)
        if isNew ? !excerpt.isEmpty : form.excerpt != original.excerpt { input.excerpt = excerpt }
        if isNew ? form.publishedAt != nil : form.publishedAt != original.publishedAt {
            input.publishedAt = form.publishedAt
            input.datePatched = true
        }
        do {
            let was = entry?.status ?? .draft
            let saved = try await store.save(input, id: isNew ? nil : entryID)
            if isNew { createdID = saved.id }
            entry = saved
            form = ChangelogForm.from(saved)
            original = form
            bodyFocused = false
            titleFocused = false
            Haptics.success()
            withAnimation(reduceMotion ? nil : Motion.fade) { editing = false }
            env.toasts.show(message(from: was, to: saved), kind: .success)
        } catch {
            Haptics.error()
            env.toasts.error(error)
        }
    }

    private func message(from was: KBStatus, to saved: ChangelogEntry) -> String {
        switch (was, saved.status) {
        case (.draft, .published):
            if saved.state() == .scheduled, let date = saved.publishedAt { return "Update scheduled for \(Format.shortDate(date))" }
            return "Update published"
        case (.published, .draft): return "Update unpublished"
        case (.published, .published): return "Update saved"
        default: return "Draft saved"
        }
    }

    private func setStatus(_ status: KBStatus) async {
        guard let entry else { return }
        let input = ChangelogEntryInput(title: entry.title, slug: entry.slug, bodyHTML: entry.bodyHTML, categories: entry.categories, status: status)
        do {
            let was = entry.status
            let saved = try await store.save(input, id: entry.id)
            self.entry = saved
            Haptics.success()
            env.toasts.show(message(from: was, to: saved), kind: .success)
        } catch {
            env.toasts.error(error)
        }
    }

    private func delete() async {
        guard let entry else {
            dismiss()
            return
        }
        do {
            try await store.delete(entry)
            Haptics.success()
            env.toasts.show("Update deleted", kind: .success)
            dismiss()
        } catch {
            env.toasts.error(error)
        }
    }

    #if DEBUG
    /// QA states: `/demo/changelog-edit`, `/demo/changelog-category`, `/demo/changelog-delete`.
    private func demo(_ action: String?) {
        guard let action, action.hasPrefix("changelog-") else { return }
        switch action {
        case "changelog-edit": startEditing()
        case "changelog-category":
            if !editing { startEditing() }
            addingCategory = true
        case "changelog-delete": confirmDelete = true
        default: return
        }
        env.router.demoAction = nil
    }
    #endif
}
