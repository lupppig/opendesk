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
            MailBoxesSheet(mail: mail) { openSettings() }
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
        if let account = mail.account(mail.scope.accountID) {
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
                .background(Theme.surface)
                .clipShape(UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous))
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
        let spans = mail.spansAccounts || mail.scope.box.spansAccounts
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
        .background(Theme.surface)
        .clipShape(UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous))
        .ignoresSafeArea(.container, edges: .bottom)
        .refreshable { await mail.refreshEverything() }
        .motion(Motion.snappy, value: mail.threads.map(\.id))
    }

    @ViewBuilder
    private var notices: some View {
        let reconnect = mail.accountsInView.filter { $0.reconnect || $0.status == "error" }
        ForEach(reconnect) { account in
            noticeRow {
                Button { openSettings() } label: {
                    Notice(text: "\(account.email) needs to be connected again to keep syncing.", tone: .red, symbol: "exclamationmark.arrow.triangle.2.circlepath")
                }
                .buttonStyle(TapScaleStyle())
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
        } else {
            switch mail.scope.box {
            case .inbox, .unread:
                EmptyState(symbol: "tray", title: "Inbox zero", message: "Nothing waits for you here. New mail shows up the moment it syncs.")
            case .shared:
                EmptyState(symbol: "person.2", title: "Nothing shared with you", message: "When a teammate shares a conversation or an account with you, it shows up here.")
            case .assigned:
                EmptyState(symbol: "person.crop.circle.badge.checkmark", title: "Nothing assigned to you", message: "Conversations a teammate asks you to answer show up here.")
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

    private func openSettings() {
        guard let url = env.session.instance.webURL(path: "/settings/mail-accounts") else { return }
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
