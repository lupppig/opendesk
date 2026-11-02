import SwiftUI

/// What the compose sheet opens with: new mail, a reply or forward, or a saved draft.
struct MailComposeRequest: Identifiable {
    let id = UUID()
    var mode: MailComposeMode = .new
    var threadID: String?
    var original: MailMessage?
    var draft: MailMessage?
    var senders: [MailSender]?
    var to: [String] = []
}

/// The Mail area (Collab): the members' own mail accounts, private or shared. The box,
/// account or folder is the title; tabs switch the common boxes; rows swipe to archive,
/// trash, read and star; everything follows the mail live.
struct MailListView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var query = ""
    @FocusState private var searchFocused: Bool
    @State private var showScopes = false
    @State private var showBoxes = false
    @State private var composing: MailComposeRequest?
    @State private var webPage: MailWebPage?

    private var mail: MailStore { .shared }

    var body: some View {
        VStack(spacing: 0) {
            GlassHeaderBar(
                title: mail.title,
                subtitle: subtitle,
                query: $query,
                searchPrompt: "Search mail",
                searchFocused: $searchFocused,
                menuLabel: "Areas",
                searchable: hasAccounts,
                onMenu: { showScopes = true }
            ) {
                if hasAccounts {
                    GlassCircleButton(symbol: "tray.2", label: "Mailboxes", badge: 0) { showBoxes = true }
                    if env.session.canWrite {
                        HeaderPrimaryButton(symbol: "square.and.pencil", label: "Compose") { compose() }
                    }
                }
            }
            if hasAccounts, mail.search.isEmpty { MailTabs(mail: mail) }
            if hasAccounts, mail.isTabbed { MailCategoryBar(mail: mail) }
            content
        }
        .background(Theme.chrome.ignoresSafeArea())
        .toolbarVisibility(.hidden, for: .navigationBar)
        .onAppear(perform: start)
        .onChange(of: env.session.workspace?.id) { _, _ in start() }
        .onChange(of: query) { _, value in mail.setSearch(value) }
        .sheet(isPresented: $showScopes) {
            ScopeSheet()
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(isPresented: $showBoxes) {
            MailBoxesSheet(mail: mail) { openSettings(account: $0) }
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(item: $composing) { request in
            MailComposeView(request: request, mail: mail, api: env.session.api)
                .presentationDetents([.large])
                .presentationCornerRadius(Theme.Radius.sheet)
                .interactiveDismissDisabled()
        }
        .sheet(item: $webPage) { page in
            MailSafariView(url: page.url).ignoresSafeArea()
        }
        #if DEBUG
        .onAppear { runDemo(env.router.demoAction) }
        .onChange(of: env.router.demoAction) { _, value in runDemo(value) }
        #endif
    }

    private var hasAccounts: Bool { mail.status?.enabled != false && !mail.accounts.isEmpty }

    private var subtitle: Text? {
        guard hasAccounts else { return nil }
        if !mail.search.isEmpty {
            return Text(mail.loading ? "Searching" : Format.plural(mail.threads.count, "conversation") + (mail.nextCursor == nil ? "" : "+"))
        }
        var parts: [String] = []
        if let to = mail.scope.to, mail.scope.accountID != nil {
            parts.append("To \(to)")
        } else if let account = mail.account(mail.scope.accountID) {
            parts.append(account.email)
        } else if mail.accounts.count > 1, mail.scope.folderID == nil, !mail.scope.box.spansAccounts {
            parts.append(Format.plural(mail.accounts.count, "account"))
        }
        let unread = mail.threads.filter(\.unread).count
        if unread > 0 { parts.append("\(unread) unread") }
        return parts.isEmpty ? nil : Text(parts.joined(separator: " · "))
    }

    private func start() {
        guard let workspace = env.session.workspace?.id, let me = env.session.user?.id else { return }
        let directory = env.directory
        MailDirectoryNames.shared.directory = directory
        mail.onStatus = { status in directory.setCollab(status.availability) }
        mail.start(api: env.session.api, realtime: env.realtime, workspaceID: workspace, me: me)
    }

    // MARK: Content

    @ViewBuilder
    private var content: some View {
        if mail.status?.enabled == false {
            off.frame(maxHeight: .infinity)
        } else if !mail.accountsLoaded || (!mail.loadedOnce && mail.loading) {
            ScrollView { SkeletonRows() }
                .scrollDisabled(true)
                .panelSurface()
                .ignoresSafeArea(.container, edges: .bottom)
        } else if mail.accounts.isEmpty {
            EmptyState(
                symbol: "envelope.badge.person.crop",
                title: "Bring your mail into opendesk",
                message: "Connect your Google, Microsoft or IMAP account to read and answer it here, keep it private or share it with your team.",
                actionTitle: "Connect an account"
            ) { openSettings() }
                .frame(maxHeight: .infinity)
        } else if let error = mail.error, mail.threads.isEmpty {
            ErrorState(title: "Couldn't load mail", message: error) { mail.reload() }
                .frame(maxHeight: .infinity)
        } else {
            list
        }
    }

    @ViewBuilder
    private var off: some View {
        if mail.status?.reason == "disabled" {
            EmptyState(symbol: "envelope", title: "Collab is off", message: "Collab is turned off on this instance.")
        } else {
            EmptyState(symbol: "envelope", title: "Collab is not on this plan", message: "Connect your own mail accounts with the Collab plan.", actionTitle: env.session.isAdmin ? "See plans" : nil) {
                if let url = env.session.instance.webURL(path: "/settings/billing") { webPage = MailWebPage(url: url) }
            }
        }
    }

    private var sentBox: Bool {
        if let folder = mail.folder(mail.scope.folderID) { return folder.role == .sent }
        return mail.scope.box == .sent || mail.scope.box == .drafts
    }

    private var list: some View {
        let own = mail.ownEmails
        let spans = mail.listSpansAccounts
        return List {
            notices
            ForEach(mail.threads) { thread in
                Button {
                    open(thread)
                } label: {
                    MailThreadRow(
                        thread: thread,
                        own: own,
                        account: mail.account(thread.accountID),
                        showAccount: spans,
                        labels: mail.labels(on: thread),
                        category: mail.categoryPill(for: thread),
                        sentBox: sentBox,
                        pulse: mail.pulses[thread.id],
                        onStar: env.session.canWrite ? { act(thread, thread.starred ? .unstar : .star) } : nil
                    )
                }
                .buttonStyle(RowPressStyle())
                .listRowInsets(EdgeInsets())
                .listRowSeparator(.hidden)
                .listRowBackground(Theme.surface)
                .swipeActions(edge: .trailing, allowsFullSwipe: true) {
                    if env.session.canWrite { trailing(thread) }
                }
                .swipeActions(edge: .leading, allowsFullSwipe: true) {
                    if env.session.canWrite { leading(thread) }
                }
                .contextMenu { if env.session.canWrite { menu(thread) } }
                .onAppear {
                    if thread.id == mail.threads.last?.id { Task { await mail.loadMore() } }
                }
            }
            if mail.loadingMore {
                ProgressView().frame(maxWidth: .infinity).padding(.vertical, 18)
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)
            }
            if mail.threads.isEmpty, !mail.loading {
                emptyState
                    .listRowSeparator(.hidden)
                    .listRowBackground(Theme.surface)
            }
            Color.clear.frame(height: 124)
                .listRowSeparator(.hidden)
                .listRowBackground(Theme.surface)
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .scrollDismissesKeyboard(.immediately)
        .panelSurface()
        .ignoresSafeArea(.container, edges: .bottom)
        .refreshable { await mail.refreshEverything(syncing: true) }
        .motion(Motion.snappy, value: mail.threads.map(\.id))
    }

    @ViewBuilder
    private var notices: some View {
        let trouble = mail.accountsInView.filter { $0.reconnect || $0.status == "error" }
        ForEach(trouble) { account in
            noticeRow {
                Button { openSettings(account: account.canManage ? account.id : nil) } label: {
                    Notice(text: problem(account), tone: .red, symbol: account.reconnect ? "exclamationmark.arrow.triangle.2.circlepath" : "exclamationmark.triangle")
                }
                .buttonStyle(TapScaleStyle())
            }
        }
        if let to = mail.scope.to, let id = mail.scope.accountID {
            noticeRow {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Image(systemName: "at").font(.system(size: 12.5, weight: .semibold))
                    Text("Written to \(to)").font(ODFont.sans(13)).lineLimit(1)
                    Spacer(minLength: 6)
                    Button("Any address") {
                        Haptics.selection()
                        mail.setAddress(nil, account: id)
                    }
                    .font(ODFont.sans(13, weight: .semibold))
                    .foregroundStyle(Theme.fg)
                    .buttonStyle(.plain)
                }
                .foregroundStyle(Theme.fg3)
                .padding(.horizontal, 2)
            }
        }
        if mail.status?.storage?.full == true {
            noticeRow {
                Notice(text: "New mail is waiting at the provider: the workspace's Collab storage is full.", tone: .amber, symbol: "externaldrive.badge.exclamationmark")
            }
        }
        if !mail.search.isEmpty, let since = mail.searchCoveredFrom, !mail.accountsInView.allSatisfy({ $0.history?.importedAll == true }) {
            noticeRow {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Image(systemName: "clock.arrow.circlepath").font(.system(size: 12.5))
                    Text("Searching mail since \(MailFormat.day(since))").font(ODFont.sans(13))
                    Spacer(minLength: 6)
                    if mail.accountsInView.contains(where: \.canManage) {
                        Button("Import older mail") { openSettings() }
                            .font(ODFont.sans(13, weight: .semibold))
                            .foregroundStyle(Theme.fg)
                            .buttonStyle(.plain)
                    }
                }
                .foregroundStyle(Theme.fg3)
                .padding(.horizontal, 2)
            }
        }
    }

    /// What is wrong with an account, a sentence of its own: the provider's message ends its own.
    private func problem(_ account: MailAccount) -> String {
        if account.reconnect { return "\(account.email) needs to be connected again to keep syncing." }
        let reason = account.lastError.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !reason.isEmpty else { return "\(account.email) did not sync. See what went wrong in its settings." }
        let sentence = reason.last.map { ".!?".contains($0) } == true ? reason : reason + "."
        return "\(account.email): \(sentence)"
    }

    private func noticeRow(@ViewBuilder _ content: () -> some View) -> some View {
        content()
            .listRowInsets(EdgeInsets(top: 10, leading: 14, bottom: 4, trailing: 14))
            .listRowSeparator(.hidden)
            .listRowBackground(Theme.surface)
    }

    @ViewBuilder
    private var emptyState: some View {
        if !mail.search.isEmpty {
            EmptyState(symbol: "magnifyingglass", title: "No conversations match", message: "Check the spelling, or search for a name or an address.")
        } else if let folder = mail.folder(mail.scope.folderID) {
            EmptyState(symbol: folder.isLabel ? "tag" : "folder", title: "Nothing in \(folder.name)", message: "Conversations you file here show up in this list.")
        } else if let key = mail.activeCategory, key != MailCategory.primary, let tab = mail.categoryTabs.first(where: { $0.id == key }) {
            EmptyState(symbol: "tray", title: "Nothing in \(tab.category.name)", message: tab.category.description.isEmpty ? "Mail sorted into this tab shows up here." : tab.category.description)
        } else {
            switch mail.scope.box {
            case .inbox, .unread:
                EmptyState(symbol: "tray", title: "Inbox zero", message: "Nothing waits for you here. New mail shows up the moment it syncs.")
            case .shared:
                EmptyState(symbol: "person.2", title: "Nothing shared with you", message: "When a teammate shares a conversation or an account with you, it shows up here.")
            case .assigned:
                EmptyState(symbol: "person.crop.circle.badge.checkmark", title: "Nothing assigned to you", message: "Conversations a teammate asks you to answer show up here.")
            case .needsAction:
                EmptyState(symbol: "checkmark.circle", title: "Nothing needs action", message: "New mail in a shared inbox that tracks handling waits here until someone marks it done.")
            case .starred:
                EmptyState(symbol: "star", title: "No starred conversations", message: "Star a conversation to keep it close.")
            case .drafts:
                EmptyState(symbol: "doc.text", title: "No drafts", message: "Mail you start and don't send waits here, saved at the provider.")
            case .sent:
                EmptyState(symbol: "paperplane", title: "Nothing sent yet", message: "Mail you send from these accounts shows up here.")
            case .archive, .all:
                EmptyState(symbol: "archivebox", title: "Nothing here", message: "Archived conversations stay here, out of the inbox.")
            case .trash:
                EmptyState(symbol: "trash", title: "Trash is empty", message: "Conversations you delete wait here before the provider removes them.")
            case .spam:
                EmptyState(symbol: "xmark.shield", title: "No spam", message: "Mail the provider marks as spam is kept here, out of the way.")
            }
        }
    }

    // MARK: Swipes and menus

    private var box: MailBox { mail.scope.folderID == nil ? mail.scope.box : .inbox }

    @ViewBuilder
    private func trailing(_ thread: MailThread) -> some View {
        switch box {
        case .needsAction:
            Button { act(thread, .done) } label: { Label("Mark as done", systemImage: "checkmark.circle") }
                .tint(Tone.green.solid)
            Button { act(thread, .archive) } label: { Label("Archive", systemImage: "archivebox") }
                .tint(Tone.blue.solid)
        case .trash:
            Button { act(thread, .inbox) } label: { Label("Move to inbox", systemImage: "tray.and.arrow.down") }
                .tint(Tone.blue.solid)
        case .spam:
            Button { act(thread, .notSpam) } label: { Label("Not spam", systemImage: "checkmark.shield") }
                .tint(Tone.green.solid)
            Button(role: .destructive) { act(thread, .trash) } label: { Label("Move to trash", systemImage: "trash") }
        case .archive:
            Button { act(thread, .inbox) } label: { Label("Move to inbox", systemImage: "tray.and.arrow.down") }
                .tint(Tone.blue.solid)
            Button(role: .destructive) { act(thread, .trash) } label: { Label("Move to trash", systemImage: "trash") }
        default:
            Button { act(thread, .archive) } label: { Label("Archive", systemImage: "archivebox") }
                .tint(Tone.green.solid)
            Button(role: .destructive) { act(thread, .trash) } label: { Label("Move to trash", systemImage: "trash") }
        }
    }

    @ViewBuilder
    private func leading(_ thread: MailThread) -> some View {
        Button { act(thread, thread.unread ? .read : .unread) } label: {
            Label(thread.unread ? "Mark as read" : "Mark as unread", systemImage: thread.unread ? "envelope.open" : "envelope.badge")
        }
        .tint(Tone.blue.solid)
        Button { act(thread, thread.starred ? .unstar : .star) } label: {
            Label(thread.starred ? "Unstar" : "Star", systemImage: thread.starred ? "star.slash" : "star")
        }
        .tint(Tone.amber.solid)
    }

    @ViewBuilder
    private func menu(_ thread: MailThread) -> some View {
        Button { act(thread, thread.unread ? .read : .unread) } label: {
            Label(thread.unread ? "Mark as read" : "Mark as unread", systemImage: thread.unread ? "envelope.open" : "envelope.badge")
        }
        Button { act(thread, thread.starred ? .unstar : .star) } label: {
            Label(thread.starred ? "Unstar" : "Star", systemImage: thread.starred ? "star.slash" : "star")
        }
        if mail.account(thread.accountID)?.handles == true {
            Button { act(thread, thread.needsAction ? .done : .needsAction) } label: {
                Label(thread.needsAction ? "Mark as done" : "Mark as needing action", systemImage: thread.needsAction ? "checkmark.circle" : "circle.badge.exclamationmark")
            }
        }
        let tabs = mail.categories(of: thread.accountID).filter { $0.key != mail.categoryKey(of: thread) }
        if !tabs.isEmpty {
            Menu {
                ForEach(tabs) { category in
                    Button(category.name) { categorize(thread, category) }
                }
            } label: {
                Label("Move to category", systemImage: "square.stack.3d.up")
            }
        }
        Divider()
        if box == .archive || box == .trash || box == .spam {
            Button { act(thread, box == .spam ? .notSpam : .inbox) } label: { Label(box == .spam ? "Not spam" : "Move to inbox", systemImage: "tray.and.arrow.down") }
        } else {
            Button { act(thread, .archive) } label: { Label("Archive", systemImage: "archivebox") }
        }
        if box != .spam {
            Button { act(thread, .spam) } label: { Label("Report spam", systemImage: "xmark.shield") }
        }
        if box != .trash {
            Button(role: .destructive) { act(thread, .trash) } label: { Label("Move to trash", systemImage: "trash") }
        }
    }

    private func act(_ thread: MailThread, _ action: MailAction) {
        Haptics.tap()
        Task {
            do {
                _ = try await mail.act([thread.id], action, known: [thread])
                guard let done = action.done(1), let undo = action.undo else { return }
                env.toasts.show(Toast(message: done, kind: .success, actionTitle: "Undo") {
                    Task { _ = try? await mail.act([thread.id], undo, known: [thread]) }
                })
            } catch {
                env.toasts.error(error)
            }
        }
    }

    /// Moves a conversation to another inbox tab, with Undo back to where it was.
    private func categorize(_ thread: MailThread, _ category: MailCategory) {
        Haptics.tap()
        let back = mail.categoryKey(of: thread)
        Task {
            do {
                _ = try await mail.act([thread.id], .categorize, category: category.key, known: [thread])
                env.toasts.show(Toast(message: "Conversation moved to \(category.name)", kind: .success, actionTitle: "Undo") {
                    Task { _ = try? await mail.act([thread.id], .categorize, category: back, known: [thread]) }
                })
            } catch {
                env.toasts.error(error)
            }
        }
    }

    private func open(_ thread: MailThread) {
        if mail.scope.box == .drafts || mail.folder(mail.scope.folderID)?.role == .drafts, thread.messageCount <= 1 {
            Task { await openDraft(thread) }
            return
        }
        env.router.push(.mailThread(thread.id))
    }

    /// A draft that starts a conversation opens in the composer, as in a mail app.
    private func openDraft(_ thread: MailThread) async {
        do {
            let detail: MailThreadDetail = try await env.session.api.get("v1/collab/threads/\(thread.id)")
            guard let draft = detail.messages.last(where: \.draft), !detail.messages.contains(where: { !$0.draft }) else {
                env.router.push(.mailThread(thread.id))
                return
            }
            composing = MailComposeRequest(mode: .new, draft: draft, senders: MailStore.senders(of: detail.account))
        } catch {
            env.toasts.error(error)
        }
    }

    private func compose() {
        Haptics.tap()
        composing = MailComposeRequest()
    }

    /// Mail account settings on the web: one account's, else the list.
    private func openSettings(account id: String? = nil) {
        guard let url = env.session.instance.webURL(path: id.map { "/settings/mail-accounts/\($0)" } ?? "/settings/mail-accounts") else { return }
        webPage = MailWebPage(url: url)
    }

    #if DEBUG
    private func runDemo(_ action: String?) {
        guard let action, action.hasPrefix("mail-") else { return }
        if showBoxes || composing != nil || webPage != nil || showScopes {
            showBoxes = false
            composing = nil
            webPage = nil
            showScopes = false
            Task {
                try? await Task.sleep(for: .milliseconds(600))
                runDemo(action)
            }
            return
        }
        switch action {
        case "mail-close":
            env.router.demoAction = nil
        case "mail-compose":
            env.router.demoAction = nil
            composing = MailComposeRequest(to: ["hana@fieldnote.io"])
        case "mail-boxes":
            env.router.demoAction = nil
            showBoxes = true
        case "mail-search":
            env.router.demoAction = nil
            query = "reseller"
        case "mail-partners":
            env.router.demoAction = nil
            mail.setAccount(DemoMail.partners)
        case "mail-shared":
            env.router.demoAction = nil
            mail.setBox(.shared)
        case "mail-accounts":
            env.router.demoAction = nil
            openSettings()
        default:
            break
        }
    }
    #endif
}

/// The boxes kept as tabs over the list (the member's own choice on the web), with the
/// folder in view in front when the list shows one.
struct MailTabs: View {
    let mail: MailStore

    private var boxes: [MailBox] {
        var out = mail.prefs.mailTabs.compactMap(MailBox.init(rawValue:)).filter(mail.offers)
        if out.isEmpty { out = [.inbox] }
        if mail.folder(mail.scope.folderID) == nil, !out.contains(mail.scope.box) { out.insert(mail.scope.box, at: 1 <= out.count ? 1 : 0) }
        return out
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 6) {
                    if let folder = mail.folder(mail.scope.folderID) {
                        tab(id: folder.id, title: folder.name, symbol: folder.isLabel ? "tag" : "folder", count: mail.unread(folder: folder), selected: true) {}
                    }
                    ForEach(boxes) { box in
                        let selected = mail.scope.folderID == nil && mail.scope.box == box
                        tab(id: box.rawValue, title: box == .inbox && mail.spansAccounts ? "All inboxes" : box.title, symbol: box.symbol, count: mail.unread(for: box), selected: selected) {
                            Haptics.selection()
                            mail.setBox(box)
                        }
                    }
                }
                .padding(.horizontal, 16)
            }
            .onChange(of: mail.scope) { _, scope in
                withAnimation(Motion.snappy) { proxy.scrollTo(scope.folderID ?? scope.box.rawValue, anchor: .center) }
            }
        }
        .padding(.bottom, 10)
    }

    private func tab(id: String, title: String, symbol: String, count: Int?, selected: Bool, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            HStack(spacing: 6) {
                Image(systemName: symbol).font(.system(size: 11.5, weight: .semibold))
                Text(title).font(ODFont.sans(13.5, weight: .semibold)).lineLimit(1)
                if let count, count > 0 {
                    Text("\(count)")
                        .font(ODFont.sans(11.5, weight: .semibold))
                        .monospacedDigit()
                        .padding(.horizontal, 5)
                        .frame(minWidth: 18, minHeight: 17)
                        .background(selected ? Theme.inverseFg.opacity(0.18) : Theme.active, in: Capsule())
                        .contentTransition(.numericText())
                }
            }
            .foregroundStyle(selected ? Theme.inverseFg : Theme.fg2)
            .padding(.horizontal, 11)
            .frame(height: 32)
            .background(selected ? Theme.inverse : Theme.surface, in: Capsule())
            .overlay(Capsule().strokeBorder(selected ? .clear : Theme.line, lineWidth: 1))
        }
        .buttonStyle(TapScaleStyle())
        .id(id)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// The tabs of an inbox that is split, under the box tabs: Primary first, each with its colour
/// and unread count. Primary is the plain inbox.
struct MailCategoryBar: View {
    let mail: MailStore

    var body: some View {
        let active = mail.activeCategory
        ScrollViewReader { proxy in
            ScrollView(.horizontal, showsIndicators: false) {
                HStack(spacing: 2) {
                    ForEach(mail.categoryTabs) { tab in
                        let selected = tab.id == active
                        Button {
                            Haptics.selection()
                            mail.setCategory(tab.id)
                        } label: {
                            HStack(spacing: 6) {
                                Circle().fill(tab.category.tone.solid).frame(width: 7, height: 7)
                                Text(tab.category.name)
                                    .font(ODFont.sans(13, weight: selected ? .semibold : .medium))
                                    .foregroundStyle(selected ? Theme.fg : Theme.fg3)
                                    .lineLimit(1)
                                if tab.unread > 0 {
                                    Text(tab.unread > 999 ? "999+" : "\(tab.unread)")
                                        .font(ODFont.mono(11))
                                        .monospacedDigit()
                                        .foregroundStyle(selected ? Theme.fg3 : tab.category.tone.text)
                                        .contentTransition(.numericText())
                                }
                            }
                            .padding(.horizontal, 10)
                            .frame(height: 28)
                            .background(selected ? Theme.active : .clear, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(TapScaleStyle())
                        .id(tab.id)
                        .accessibilityLabel(tab.unread > 0 ? "\(tab.category.name), \(tab.unread) unread" : tab.category.name)
                        .accessibilityAddTraits(selected ? .isSelected : [])
                    }
                }
                .padding(.horizontal, 12)
            }
            .onChange(of: active) { _, key in
                guard let key else { return }
                withAnimation(Motion.snappy) { proxy.scrollTo(key, anchor: .center) }
            }
        }
        .padding(.bottom, 8)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Inbox categories")
    }
}

enum MailFormat {
    /// "16 June 2026".
    static func day(_ date: Date) -> String {
        date.formatted(.dateTime.day().month(.wide).year())
    }

    /// "Tue 16 Jun, 09:30", the year added outside this one.
    static func stamp(_ date: Date, now: Date = AppClock.now) -> String {
        let sameYear = Calendar.current.isDate(date, equalTo: now, toGranularity: .year)
        let day = sameYear ? date.formatted(.dateTime.weekday(.abbreviated).day().month(.abbreviated)) : date.formatted(.dateTime.day().month(.abbreviated).year())
        return day + ", " + Format.clock(date)
    }
}
