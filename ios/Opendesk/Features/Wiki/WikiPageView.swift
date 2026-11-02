import SwiftUI

/// One wiki page (contracts/wiki.md). Read it rendered, with where it sits and its
/// subpages; agents and admins edit it in place (icon, title and the same body
/// editor as articles), saved as they type. When someone else saves first, what was
/// typed stays and the page asks whose version to keep. A version loaded from the
/// server (theirs, a restored one) is never an edit: only typing is ever sent.
struct WikiPageView: View {
    let id: String

    var body: some View {
        // A link to another page replaces this one; its state starts over.
        WikiPageScreen(id: id).id(id)
    }
}

struct WikiPageScreen: View {
    let id: String

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    @State private var saver: WikiSaver?
    @State private var loadError: String?
    @State private var missing = false
    @State private var editing = false
    @State private var text = KBBody()
    @State private var preview = false
    @State private var bodyHeight: CGFloat = 240
    /// A newer version someone else saved, while this page holds unsent typing.
    @State private var latest: WikiPage?
    @State private var showIcon = false
    @State private var showHistory = false
    @State private var moving: WikiNode?
    @State private var deleting: WikiNode?
    @State private var listener: UUID?
    @State private var started = false
    @FocusState private var titleFocused: Bool
    @FocusState private var bodyFocused: Bool

    private var store: WikiStore { WikiStore.shared }
    private var canEdit: Bool { env.session.canWrite }

    var body: some View {
        screen
            .onAppear {
                // Back from a page pushed over this one: listen again, and catch up.
                if listener == nil {
                    listener = env.realtime.subscribe { update in WikiStore.shared.handle(update) }
                }
                if started { Task { await load() } }
            }
            .task { await start() }
            .onDisappear {
                env.realtime.unsubscribe(listener)
                listener = nil
                if let saver { Task { await saver.flush() } }
            }
            .onChange(of: scenePhase) { _, phase in
                if phase != .active { store.flushAll() }
            }
            .onChange(of: store.node(id)?.version) { _, version in
                guard let version, let saver, saver.isNewer(version) else { return }
                Task { await takeNewer() }
            }
            .onChange(of: text) { _, value in
                guard editing else { return }
                saver?.edit(body: value.bodyHTML)
            }
            .sheet(isPresented: $showIcon) {
                WikiIconSheet(current: saver?.draft.icon ?? "") { saver?.edit(icon: $0) }
                    .presentationDetents([.medium, .large])
                    .presentationCornerRadius(Theme.Radius.sheet)
            }
            .sheet(isPresented: $showHistory) {
                if let saver {
                    WikiHistorySheet(page: saver.page, canRestore: canEdit) { restored in take(restored) }
                        .presentationDetents([.large])
                        .presentationCornerRadius(Theme.Radius.sheet)
                }
            }
            .sheet(item: $moving) { node in
                WikiMoveSheet(node: node)
                    .presentationDetents([.medium, .large])
                    .presentationCornerRadius(Theme.Radius.sheet)
            }
            .wikiDeleteConfirmation($deleting) { _ in dismiss() }
        #if DEBUG
            .onChange(of: env.router.demoAction) { _, action in demo(action) }
        #endif
    }

    private var screen: some View {
        Group {
            if let saver {
                page(saver)
            } else if missing {
                gone
            } else if let loadError {
                ErrorState(title: "Couldn't open this page", message: loadError) { Task { await load() } }
                    .frame(maxHeight: .infinity)
            } else {
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            }
        }
        .background(Theme.surface.ignoresSafeArea())
        .toolbarVisibility(.visible, for: .navigationBar)
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { toolbar }
        .tabBarHidden()
    }

    /// The page is gone; typing left on it can still be copied.
    private var gone: some View {
        VStack(spacing: 0) {
            if let orphan = store.openSaver(id), orphan.pending {
                unsent(orphan, reason: "This page was deleted.") {
                    store.forgetSaver(id)
                    dismiss()
                }
                .padding(16)
            }
            EmptyState(symbol: "doc.questionmark", title: "This page is gone", message: "It was deleted, or the link belongs to another workspace.", actionTitle: "Back to the wiki") { dismiss() }
                .frame(maxHeight: .infinity)
        }
    }

    // MARK: Page

    private func page(_ saver: WikiSaver) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                banners(saver)
                breadcrumbs
                    .padding(.bottom, 14)
                head(saver)
                if editing {
                    KBBodyEditor(form: $text, preview: $preview, focused: $bodyFocused, noun: "page", prompt: "Write something, or paste it in. Start a line with ## for a heading, - for a list.") {
                        env.toasts.show("This HTML can't be shown as text without losing something.", kind: .info)
                    }
                    .padding(.top, 20)
                } else {
                    reader(saver)
                        .padding(.top, 20)
                }
                subpages
                    .padding(.top, 36)
            }
            .padding(.horizontal, editing ? 16 : 20)
            .padding(.top, 8)
            .padding(.bottom, 48)
            .animation(reduceMotion ? nil : Motion.snappy, value: saver.status)
        }
        .scrollIndicators(.hidden)
        .scrollDismissesKeyboard(.interactively)
        .refreshable { await load() }
    }

    /// Someone else saved first and this page holds what was typed.
    private func theirs(_ saver: WikiSaver) -> Bool {
        if saver.status == .conflict { return true }
        guard let latest else { return false }
        return saver.isNewer(latest.version) && saver.pending
    }

    @ViewBuilder
    private func banners(_ saver: WikiSaver) -> some View {
        if saver.final, saver.pending {
            unsent(saver, reason: saver.status == .forbidden ? "You can't edit this page." : "This page was deleted.") {
                if saver.status == .forbidden {
                    Task { if let fresh = try? await store.page(id) { take(fresh) } }
                } else {
                    store.forgetSaver(id)
                    dismiss()
                }
            }
            .padding(.bottom, 14)
            .transition(.opacity.combined(with: .move(edge: .top)))
        } else if theirs(saver) {
            conflict(saver)
                .padding(.bottom, 14)
                .transition(.opacity.combined(with: .move(edge: .top)))
        } else if saver.status == .error {
            Notice(text: "Couldn't save, retrying.", tone: .amber, symbol: "icloud.slash")
                .padding(.bottom, 14)
        }
    }

    private func conflict(_ saver: WikiSaver) -> some View {
        let who = (latest ?? saver.page).updatedBy
        return VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 13))
                    .foregroundStyle(Tone.amber.solid)
                VStack(alignment: .leading, spacing: 3) {
                    Text(who.map { "\($0.displayName) edited this page." } ?? "Someone else edited this page.")
                        .font(ODFont.sans(14, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                    Text("What you typed is still here. Load their version, or keep yours and save it over theirs.")
                        .font(ODFont.footnote)
                        .foregroundStyle(Theme.fg2)
                        .fixedSize(horizontal: false, vertical: true)
                }
            }
            HStack(spacing: 8) {
                Button("Load their version") { Task { await loadTheirs() } }
                    .buttonStyle(OutlineButtonStyle(fullWidth: false, height: 34))
                Button("Keep mine") { Task { await keepMine() } }
                    .buttonStyle(InkButtonStyle(fullWidth: false, height: 34))
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Tone.amber.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Tone.amber.line, lineWidth: 1))
    }

    /// Saving stopped for good: the typing can be copied, or let go.
    private func unsent(_ saver: WikiSaver, reason: String, onDiscard: @escaping () -> Void) -> some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Image(systemName: "icloud.slash").font(.system(size: 13)).foregroundStyle(Tone.amber.solid)
                VStack(alignment: .leading, spacing: 3) {
                    Text(reason).font(ODFont.sans(14, weight: .semibold)).foregroundStyle(Theme.fg)
                    Text("What you typed wasn't saved. Copy it to keep it.")
                        .font(ODFont.footnote)
                        .foregroundStyle(Theme.fg2)
                }
            }
            HStack(spacing: 8) {
                Button("Copy my text") {
                    let draft = saver.draft
                    let body = KBMarkdown.markdown(from: draft.body) ?? HTMLText.plain(from: draft.body)
                    UIPasteboard.general.string = [draft.title, body].filter { !$0.isEmpty }.joined(separator: "\n\n")
                    Haptics.success()
                    env.toasts.show("Your text is copied", kind: .success)
                }
                .buttonStyle(InkButtonStyle(fullWidth: false, height: 34))
                Button("Discard", action: onDiscard)
                    .buttonStyle(OutlineButtonStyle(fullWidth: false, height: 34))
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Tone.amber.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Tone.amber.line, lineWidth: 1))
    }

    @ViewBuilder
    private var breadcrumbs: some View {
        let crumbs: [WikiRef] = store.node(id) != nil
            ? store.ancestors(of: id).map { WikiRef(id: $0.id, title: $0.title, icon: $0.icon) }
            : (saver?.page.breadcrumbs ?? [])
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 4) {
                Button { env.router.show(.wiki, []) } label: { Text("Wiki") }
                    .buttonStyle(.plain)
                    .foregroundStyle(Theme.fg3)
                ForEach(crumbs) { crumb in
                    Text("/").foregroundStyle(Theme.fg4)
                    Button { env.router.push(.wikiPage(crumb.id)) } label: {
                        HStack(spacing: 3) {
                            if !crumb.icon.isEmpty { Text(crumb.icon) }
                            Text(crumb.displayTitle).lineLimit(1)
                        }
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(Theme.fg3)
                }
            }
            .font(ODFont.sans(13, weight: .medium))
        }
        .accessibilityLabel("Where this page sits")
    }

    private func head(_ saver: WikiSaver) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            if canEdit, editing || !saver.draft.icon.isEmpty {
                Button { showIcon = true } label: {
                    if saver.draft.icon.isEmpty {
                        Label("Add icon", systemImage: "face.smiling")
                            .font(ODFont.sans(13, weight: .medium))
                            .foregroundStyle(Theme.fg3)
                            .padding(.horizontal, 10)
                            .frame(height: 30)
                            .background(Theme.hover, in: Capsule())
                    } else {
                        Text(saver.draft.icon).font(.system(size: 44))
                    }
                }
                .buttonStyle(TapScaleStyle())
                .accessibilityLabel(saver.draft.icon.isEmpty ? "Add icon" : "Change icon")
            } else if !saver.draft.icon.isEmpty {
                Text(saver.draft.icon).font(.system(size: 44)).accessibilityHidden(true)
            }
            if editing {
                TextField(text: Binding(get: { saver.draft.title }, set: { saver.edit(title: $0.replacingOccurrences(of: "\n", with: " ")) }), axis: .vertical) {
                    Text("Untitled").foregroundStyle(Theme.fg4)
                }
                .font(ODFont.sans(27, weight: .semibold, relativeTo: .largeTitle))
                .tracking(-0.7)
                .foregroundStyle(Theme.fg)
                .focused($titleFocused)
                .submitLabel(.next)
                .onSubmit { bodyFocused = true }
            } else {
                Text(WikiText.title(saver.draft.title))
                    .font(ODFont.sans(27, weight: .semibold, relativeTo: .largeTitle))
                    .tracking(-0.7)
                    .foregroundStyle(saver.draft.title.trimmingCharacters(in: .whitespaces).isEmpty ? Theme.fg3 : Theme.fg)
                    .fixedSize(horizontal: false, vertical: true)
            }
            WikiEditedLine(page: saver.page)
        }
        .padding(.horizontal, editing ? 4 : 0)
    }

    @ViewBuilder
    private func reader(_ saver: WikiSaver) -> some View {
        if HTMLText.plain(from: saver.draft.body).trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
            if canEdit, !saver.final {
                Button { startEditing(focusBody: true) } label: {
                    Text("Write something, or paste it in.")
                        .font(ODFont.sans(15))
                        .foregroundStyle(Theme.fg4)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            } else {
                Text("This page has no text yet.")
                    .font(ODFont.sans(15))
                    .foregroundStyle(Theme.fg4)
            }
        } else {
            KBArticleWebView(html: saver.draft.body, height: $bodyHeight)
                .frame(height: bodyHeight)
        }
    }

    @ViewBuilder
    private var subpages: some View {
        let kids: [WikiRef] = store.node(id) != nil
            ? store.children(of: id).map { WikiRef(id: $0.id, title: $0.title, icon: $0.icon) }
            : (saver?.page.children ?? [])
        if !kids.isEmpty || canEdit {
            VStack(alignment: .leading, spacing: 6) {
                SectionLabel("Subpages")
                Panel {
                    ForEach(Array(kids.enumerated()), id: \.element.id) { index, kid in
                        if index > 0 { Hairline(inset: 44) }
                        Button { env.router.push(.wikiPage(kid.id)) } label: {
                            HStack(spacing: 10) {
                                WikiPageIcon(icon: kid.icon, size: 16)
                                Text(kid.displayTitle)
                                    .font(ODFont.sans(15))
                                    .foregroundStyle(kid.title.trimmingCharacters(in: .whitespaces).isEmpty ? Theme.fg3 : Theme.fg)
                                    .lineLimit(1)
                                Spacer(minLength: 6)
                                Chevron()
                            }
                            .padding(.horizontal, 12)
                            .frame(minHeight: 44)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(RowPressStyle())
                    }
                    if canEdit {
                        if !kids.isEmpty { Hairline(inset: 44) }
                        Button { addSubpage() } label: {
                            HStack(spacing: 10) {
                                Image(systemName: "plus").font(.system(size: 13, weight: .semibold)).frame(width: 22)
                                Text("Add a subpage").font(ODFont.sans(15))
                                Spacer()
                            }
                            .foregroundStyle(Theme.fg3)
                            .padding(.horizontal, 12)
                            .frame(minHeight: 44)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(RowPressStyle())
                    }
                }
            }
        }
    }

    // MARK: Toolbar

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        if let saver {
            ToolbarItem(placement: .principal) { saveState(saver) }
            if editing {
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        if text.raw {
                            Button { switchToText() } label: { Label("Edit as text", systemImage: "textformat") }
                        } else {
                            Button { text.useHTML() } label: { Label("Edit as HTML", systemImage: "chevron.left.forwardslash.chevron.right") }
                        }
                    } label: {
                        Image(systemName: "ellipsis")
                    }
                    .accessibilityLabel("More")
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button("Done") { stopEditing() }
                        .fontWeight(.semibold)
                }
            } else {
                if canEdit, !saver.final {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button("Edit") { startEditing() }
                            .fontWeight(.semibold)
                    }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Menu {
                        WikiPageMenu(
                            node: store.node(id) ?? saver.page.node,
                            onAddSubpage: addSubpage,
                            onMove: { moving = store.node(id) ?? saver.page.node },
                            onDelete: { deleting = store.node(id) ?? saver.page.node },
                            onHistory: { showHistory = true }
                        )
                    } label: {
                        Image(systemName: "ellipsis")
                    }
                    .accessibilityLabel("More actions")
                }
            }
        }
    }

    /// Saving, Saved, or what went wrong, where the title would be.
    @ViewBuilder
    private func saveState(_ saver: WikiSaver) -> some View {
        if canEdit {
            HStack(spacing: 5) {
                switch saver.status {
                case .saving, .dirty:
                    ProgressView().controlSize(.mini)
                    Text("Saving")
                case .error:
                    Image(systemName: "icloud.slash").foregroundStyle(Tone.amber.solid)
                    Text("Couldn't save, retrying")
                case .conflict, .forbidden, .deleted:
                    Image(systemName: "exclamationmark.triangle.fill").foregroundStyle(Tone.amber.solid)
                    Text("Not saved")
                case .saved:
                    if saver.lastSavedAt != nil {
                        Image(systemName: "checkmark").foregroundStyle(Tone.green.solid)
                        Text("Saved")
                    }
                }
            }
            .font(ODFont.sans(12.5, weight: .medium))
            .foregroundStyle(Theme.fg3)
            .animation(Motion.fade, value: saver.status)
            .accessibilityElement(children: .combine)
        }
    }

    // MARK: Loading

    private func start() async {
        guard !started else { return }
        started = true
        store.attach(env)
        await load()
        // A page made a moment ago opens with its title ready to type.
        if canEdit, saver != nil, store.fresh.remove(id) != nil {
            startEditing()
            try? await Task.sleep(for: .milliseconds(450))
            titleFocused = true
        }
        #if DEBUG
        demo(env.router.demoAction)
        #endif
    }

    private func load() async {
        do {
            let fresh = try await store.page(id)
            loadError = nil
            missing = false
            if let saver {
                if !saver.busy, saver.status == .saved, saver.isNewer(fresh.version) { take(fresh) }
            } else {
                saver = store.saver(for: fresh)
            }
        } catch {
            guard !error.isCancellation else { return }
            if (error as? APIError)?.status == 404 {
                missing = true
                saver = nil
            } else if saver == nil {
                loadError = error.userMessage
            }
        }
    }

    /// Someone else saved (realtime said so): taken in place when nothing here is
    /// unsent; with unsent typing, the banner asks whose version stays.
    private func takeNewer() async {
        guard let saver, let fresh = try? await store.page(id), saver.isNewer(fresh.version) else { return }
        if saver.busy || saver.status == .conflict {
            withAnimation(Motion.snappy) { latest = fresh }
        } else {
            take(fresh)
        }
    }

    /// Shows a version from the server: never an edit.
    private func take(_ page: WikiPage) {
        guard let saver else { return }
        saver.rebase(page)
        latest = nil
        if editing { text = KBBody(html: saver.draft.body) }
    }

    private func loadTheirs() async {
        do {
            take(try await store.page(id))
        } catch {
            env.toasts.error(error)
        }
    }

    private func keepMine() async {
        do {
            let fresh = try await store.page(id)
            latest = nil
            saver?.overwrite(latest: fresh)
        } catch {
            env.toasts.error(error)
        }
    }

    // MARK: Actions

    private func startEditing(focusBody: Bool = false) {
        guard let saver, canEdit else { return }
        text = KBBody(html: saver.draft.body)
        preview = false
        withAnimation(reduceMotion ? nil : Motion.fade) { editing = true }
        if focusBody {
            Task {
                try? await Task.sleep(for: .milliseconds(350))
                bodyFocused = true
            }
        }
    }

    private func stopEditing() {
        titleFocused = false
        bodyFocused = false
        withAnimation(reduceMotion ? nil : Motion.fade) { editing = false }
        if let saver { Task { await saver.flush() } }
    }

    private func switchToText() {
        if !text.useText() {
            Haptics.warning()
            env.toasts.show("This HTML can't be shown as text without losing something.", kind: .info)
        }
    }

    private func addSubpage() {
        Task {
            if let saver { await saver.flush() }
            await WikiCreate.page(WikiPageCreate(parentID: id), env: env)
        }
    }

    #if DEBUG
    /// QA states: `/demo/wiki-edit`, `/demo/wiki-icon`, `/demo/wiki-history`, `/demo/wiki-move-page`.
    private func demo(_ action: String?) {
        guard let action, action.hasPrefix("wiki-"), saver != nil else { return }
        switch action {
        case "wiki-edit": startEditing()
        case "wiki-icon":
            startEditing()
            showIcon = true
        case "wiki-history": showHistory = true
        case "wiki-move-page": moving = store.node(id) ?? saver?.page.node
        default:
            return
        }
        env.router.demoAction = nil
    }
    #endif
}
