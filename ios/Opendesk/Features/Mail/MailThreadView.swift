import QuickLook
import SwiftUI

/// One Collab conversation.
struct MailThreadView: View {
    let id: String

    var body: some View {
        // Keyed by the id, so moving to another conversation in place starts fresh.
        MailThreadHost(id: id).id(id)
    }
}

private struct MailThreadHost: View {
    let id: String
    @Environment(AppEnvironment.self) private var env
    @State private var store: MailThreadStore?

    var body: some View {
        Group {
            if let store {
                MailThreadScreen(store: store)
            } else {
                Theme.chrome.ignoresSafeArea()
            }
        }
        .onAppear {
            if store == nil { store = MailThreadStore(threadID: id, api: env.session.api) }
        }
    }
}

/// What the thread screen shows over itself.
enum MailThreadSheet: Identifiable {
    case share, assign, move, labels
    case details(MailMessage)
    case person(MailAddress)

    var id: String {
        switch self {
        case .share: "share"
        case .assign: "assign"
        case .move: "move"
        case .labels: "labels"
        case let .details(message): "details-" + message.id
        case let .person(address): "person-" + address.email
        }
    }
}

/// A composer in the full sheet.
struct MailComposeHandle: Identifiable {
    let id = UUID()
    let store: MailComposeStore
}

/// The conversation: subject, labels and who is in it; messages collapsed except the
/// latest and unread ones; team comments under them; and, pinned under the thread, the
/// comment bar with the reply buttons, which step aside while a reply is open.
struct MailThreadScreen: View {
    let store: MailThreadStore
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var expanded: Set<String> = []
    @State private var showAll = false
    @State private var allowImages: Set<String> = []
    @State private var sheet: MailThreadSheet?
    @State private var reply: MailComposeStore?
    @State private var composing: MailComposeHandle?
    @State private var editing: MailComment?
    @State private var deleting: MailComment?
    @State private var preview: URL?
    @State private var opening: String?
    @State private var scrollTarget: String?

    private var mail: MailStore { .shared }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                content
                    .padding(.horizontal, 12)
                    .padding(.top, 4)
                    .padding(.bottom, 24)
            }
            .scrollDismissesKeyboard(.interactively)
            .refreshable { await store.load() }
            .onChange(of: scrollTarget) { _, target in
                guard let target else { return }
                withAnimation(Motion.smooth) { proxy.scrollTo(target, anchor: .top) }
                scrollTarget = nil
            }
            .onChange(of: store.postedComment) { _, id in
                guard let id else { return }
                withAnimation(Motion.smooth) { proxy.scrollTo("comment-" + id, anchor: .bottom) }
            }
        }
        .background(Theme.chrome.ignoresSafeArea())
        .safeAreaInset(edge: .bottom, spacing: 0) { dock }
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { toolbar }
        .tabBarHidden()
        .task {
            MailDirectoryNames.shared.directory = env.directory
            store.start(realtime: env.realtime)
            await store.load()
            openInitial()
            await store.markReadOnOpen()
        }
        .onDisappear { store.stop() }
        .sheet(item: $sheet) { sheet in sheetView(sheet) }
        .sheet(item: $composing) { handle in
            MailComposeView(store: handle.store, mail: mail) { sent in
                if sent { Task { await store.load() } }
            }
            .presentationDetents([.large])
            .presentationCornerRadius(Theme.Radius.sheet)
            .interactiveDismissDisabled()
        }
        .quickLookPreview($preview)
        .confirmationDialog("Delete comment", isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }), titleVisibility: .visible, presenting: deleting) { comment in
            Button("Delete", role: .destructive) {
                Task {
                    do { try await store.deleteComment(comment) } catch { env.toasts.error(error) }
                }
            }
        } message: { _ in
            Text("Delete this comment? Teammates will no longer see it.")
        }
        #if DEBUG
        .onChange(of: env.router.demoAction) { _, value in runDemo(value) }
        .onChange(of: store.detail != nil) { _, loaded in if loaded { runDemo(env.router.demoAction) } }
        #endif
    }

    // MARK: Content

    @ViewBuilder
    private var content: some View {
        if store.notFound {
            EmptyState(symbol: "envelope.badge.shield.half.filled", title: "This conversation isn't here", message: "It was deleted, or it is no longer shared with you.", actionTitle: "Back to mail") { dismiss() }
                .padding(.top, 80)
        } else if let error = store.error, store.detail == nil {
            ErrorState(title: "Couldn't load this conversation", message: error) { Task { await store.load() } }
                .padding(.top, 80)
        } else if let detail = store.detail {
            VStack(alignment: .leading, spacing: 10) {
                MailThreadHeader(
                    detail: detail,
                    labels: labels(detail),
                    canAct: store.canAct,
                    onLabels: { sheet = .labels },
                    onShare: { sheet = .share },
                    onAssign: { sheet = .assign }
                )
                .padding(.horizontal, 6)
                .padding(.bottom, 4)
                messageList(detail)
                ForEach(store.drafts) { draft in
                    MailDraftCard(draft: draft) { continueDraft(draft, detail: detail) }
                }
                if !detail.comments.isEmpty {
                    MailCommentsSection(
                        comments: detail.comments,
                        fresh: store.freshComments,
                        me: env.session.user?.id,
                        onEdit: { editing = $0 },
                        onDelete: { deleting = $0 }
                    )
                    .id("comments")
                    .padding(.top, 8)
                }
            }
        } else {
            VStack(spacing: 10) {
                ForEach(0..<3, id: \.self) { _ in
                    RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).fill(Theme.surface).frame(height: 96)
                }
            }
            .redacted(reason: .placeholder)
            .padding(.top, 60)
        }
    }

    private func labels(_ detail: MailThreadDetail) -> [MailFolder] {
        let list = mail.folders(of: detail.account.id)
        return detail.thread.folderIDs.compactMap { id in list.first { $0.id == id } }.filter(\.isCustom)
    }

    /// The first message, the older ones folded into one line when there are several, then the rest.
    @ViewBuilder
    private func messageList(_ detail: MailThreadDetail) -> some View {
        let messages = store.messages
        let folded = foldedRange(messages)
        ForEach(Array(messages.enumerated()), id: \.element.id) { index, message in
            if let folded, index == folded.lowerBound {
                Button {
                    withAnimation(Motion.smooth) { showAll = true }
                } label: {
                    HStack(spacing: 8) {
                        Rectangle().fill(Theme.line).frame(height: 1)
                        Text("\(folded.count) older messages")
                            .font(ODFont.sans(12.5, weight: .semibold))
                            .foregroundStyle(Theme.fg2)
                            .padding(.horizontal, 12)
                            .frame(height: 28)
                            .background(Theme.surface, in: Capsule())
                            .overlay(Capsule().strokeBorder(Theme.line, lineWidth: 1))
                        Rectangle().fill(Theme.line).frame(height: 1)
                    }
                    .padding(.vertical, 2)
                }
                .buttonStyle(TapScaleStyle())
            }
            if folded?.contains(index) != true {
                MailMessageCard(
                    message: message,
                    account: detail.account,
                    own: mail.ownEmails,
                    expanded: expanded.contains(message.id),
                    imagesAllowed: allowImages.contains(message.id),
                    apiHost: env.session.instance.apiURL.host(),
                    opening: opening,
                    canAct: store.canAct,
                    onToggle: { toggle(message) },
                    onLoadImages: { allowImages.insert(message.id) },
                    onPerson: { sheet = .person($0) },
                    onDetails: { sheet = .details(message) },
                    onReply: { startReply($0, to: message) },
                    onOpen: { open($0, in: message) }
                )
                .id(message.id)
            }
        }
    }

    private func foldedRange(_ messages: [MailMessage]) -> Range<Int>? {
        guard !showAll, messages.count >= 4 else { return nil }
        var end = 1
        while end < messages.count - 1, !expanded.contains(messages[end].id) { end += 1 }
        return end - 1 >= 2 ? 1..<end : nil
    }

    private func toggle(_ message: MailMessage) {
        withAnimation(Motion.snappy) {
            if expanded.contains(message.id) { expanded.remove(message.id) } else { expanded.insert(message.id) }
        }
    }

    /// The latest message and the unread ones open; the rest fold.
    private func openInitial() {
        guard expanded.isEmpty else { return }
        let messages = store.messages
        var open = Set(messages.filter(\.unread).map(\.id))
        if let last = messages.last { open.insert(last.id) }
        expanded = open
    }

    // MARK: Dock

    private var dock: some View {
        VStack(spacing: 8) {
            if let reply {
                MailReplyComposer(
                    store: reply,
                    onExpand: {
                        composing = MailComposeHandle(store: reply)
                        withAnimation(Motion.snappy) { self.reply = nil }
                    },
                    onClose: {
                        Task { await reply.close() }
                        withAnimation(Motion.snappy) { self.reply = nil }
                    },
                    onSent: {
                        withAnimation(Motion.snappy) { self.reply = nil }
                        Task { await store.load() }
                    }
                )
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
            if store.detail != nil {
                MailCommentBar(
                    store: store,
                    members: env.directory.members,
                    teams: env.directory.teams,
                    me: env.session.user?.id,
                    replyOpen: reply != nil,
                    editing: $editing,
                    onComments: { scrollTarget = "comments" },
                    onReply: { mode in
                        if let last = store.messages.last { startReply(mode, to: last) }
                    }
                )
            }
        }
        .padding(.horizontal, 12)
        .padding(.top, 6)
        .padding(.bottom, 6)
        .background {
            LinearGradient(stops: [.init(color: Theme.chrome.opacity(0), location: 0), .init(color: Theme.chrome.opacity(0.96), location: 0.32), .init(color: Theme.chrome, location: 1)], startPoint: .top, endPoint: .bottom)
                .padding(.top, -18)
                .ignoresSafeArea()
                .allowsHitTesting(false)
        }
        .motion(Motion.snappy, value: reply != nil)
    }

    private func startReply(_ mode: MailComposeMode, to message: MailMessage) {
        guard let detail = store.detail, store.canAct else { return }
        Haptics.tap()
        let composer = MailComposeStore(
            api: env.session.api, mode: mode, threadID: mode == .forward ? nil : detail.thread.id, original: message,
            senders: MailStore.senders(of: detail.account), ownEmails: mail.ownEmails
        )
        if mode == .forward {
            composing = MailComposeHandle(store: composer)
        } else {
            if let open = reply { Task { await open.close() } }
            withAnimation(Motion.snappy) { reply = composer }
        }
    }

    private func continueDraft(_ draft: MailMessage, detail: MailThreadDetail) {
        let original = store.messages.last { $0.id == draft.inReplyToID } ?? store.messages.last
        let composer = MailComposeStore(api: env.session.api, draft: draft, threadID: detail.thread.id, original: original, senders: MailStore.senders(of: detail.account), ownEmails: mail.ownEmails)
        composing = MailComposeHandle(store: composer)
    }

    private func open(_ attachment: MailAttachment, in message: MailMessage) {
        let key = message.id + "/" + attachment.id
        opening = key
        Task {
            defer { if opening == key { opening = nil } }
            do {
                preview = try await MailFiles.local(attachment, apiURL: env.session.instance.apiURL)
            } catch {
                env.toasts.show("Couldn't open \(attachment.filename)", kind: .error)
            }
        }
    }

    // MARK: Toolbar

    private var inInbox: Bool {
        guard let thread = store.thread else { return true }
        return thread.folderIDs.contains { mail.folder($0)?.role == .inbox }
    }

    private var inRole: (MailFolderRole) -> Bool {
        { role in store.thread?.folderIDs.contains { mail.folder($0)?.role == role } ?? false }
    }

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        let neighbours = mail.neighbours(of: store.threadID)
        ToolbarItemGroup(placement: .topBarTrailing) {
            Button { go(neighbours.previous) } label: { Image(systemName: "chevron.up") }
                .disabled(neighbours.previous == nil)
                .accessibilityLabel("Previous conversation")
            Button { go(neighbours.next) } label: { Image(systemName: "chevron.down") }
                .disabled(neighbours.next == nil)
                .accessibilityLabel("Next conversation")
        }
        if store.canAct, let thread = store.thread {
            ToolbarSpacer(.fixed, placement: .topBarTrailing)
            ToolbarItemGroup(placement: .topBarTrailing) {
                if inInbox {
                    Button { perform(.archive) } label: { Image(systemName: "archivebox") }
                        .accessibilityLabel("Archive")
                } else {
                    Button { perform(inRole(.spam) ? .notSpam : .inbox) } label: { Image(systemName: "tray.and.arrow.down") }
                        .accessibilityLabel(inRole(.spam) ? "Not spam" : "Move to inbox")
                }
                Menu {
                    menu(thread)
                } label: {
                    Image(systemName: "ellipsis")
                }
                .accessibilityLabel("More actions")
            }
        }
    }

    @ViewBuilder
    private func menu(_ thread: MailThread) -> some View {
        Section {
            Button { perform(thread.unread ? .read : .unread) } label: {
                Label(thread.unread ? "Mark as read" : "Mark as unread", systemImage: thread.unread ? "envelope.open" : "envelope.badge")
            }
            Button { perform(thread.starred ? .unstar : .star) } label: {
                Label(thread.starred ? "Unstar" : "Star", systemImage: thread.starred ? "star.slash" : "star")
            }
        }
        Section {
            Button { sheet = .move } label: { Label("Move to", systemImage: "folder") }
            if !mail.customFolders(of: thread.accountID).filter(\.isLabel).isEmpty {
                Button { sheet = .labels } label: { Label("Labels", systemImage: "tag") }
            }
            Button { sheet = .assign } label: { Label("Assign", systemImage: "person.crop.circle.badge.plus") }
            Button { sheet = .share } label: { Label("Share", systemImage: "person.2") }
            if let person = MailNames.lead(thread, own: mail.ownEmails) {
                Button { sheet = .person(person) } label: { Label("Mail with \(person.short)", systemImage: "person.text.rectangle") }
            }
        }
        Section {
            if inRole(.spam) {
                Button { perform(.notSpam) } label: { Label("Not spam", systemImage: "checkmark.shield") }
            } else {
                Button { perform(.spam) } label: { Label("Report spam", systemImage: "xmark.shield") }
            }
            if !inRole(.trash) {
                Button(role: .destructive) { perform(.trash) } label: { Label("Move to trash", systemImage: "trash") }
            }
        }
    }

    private func go(_ thread: MailThread?) {
        guard let thread else { return }
        Haptics.selection()
        var path = env.router.inboxPath
        guard !path.isEmpty else { return env.router.push(.mailThread(thread.id)) }
        path[path.count - 1] = .mailThread(thread.id)
        env.router.inboxPath = path
    }

    private func perform(_ action: MailAction, folderID: String? = nil) {
        Haptics.tap()
        let id = store.threadID
        let known = store.thread.map { [$0] } ?? []
        let next = mail.neighbours(of: id).next
        Task {
            do {
                try await store.act(action, folderID: folderID)
                guard action.removes else { return }
                if let done = action.done(1, folder: mail.folder(folderID)?.name) {
                    let undo = action.undo
                    env.toasts.show(Toast(message: done, kind: .success, actionTitle: undo == nil ? nil : "Undo") {
                        guard let undo else { return }
                        Task { _ = try? await mail.act([id], undo, known: known) }
                    })
                }
                if action == .move || action == .inbox || action == .notSpam { return }
                if let next, mail.thread(next.id) != nil { go(next) } else { dismiss() }
            } catch {
                env.toasts.error(error)
            }
        }
    }

    // MARK: Sheets

    @ViewBuilder
    private func sheetView(_ sheet: MailThreadSheet) -> some View {
        switch sheet {
        case .share:
            MailShareSheet(store: store, members: env.directory.members, teams: env.directory.teams, me: env.session.user?.id)
                .presentationDetents([.large])
                .presentationCornerRadius(Theme.Radius.sheet)
        case .assign:
            MailAssignSheet(store: store, members: env.directory.members, me: env.session.user?.id)
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        case .move:
            MailFolderSheet(store: store, mode: .move) { action, folder in perform(action, folderID: folder) }
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        case .labels:
            MailFolderSheet(store: store, mode: .labels) { action, folder in perform(action, folderID: folder) }
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        case let .details(message):
            MailDetailsSheet(message: message, store: store)
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        case let .person(address):
            MailPersonSheet(address: address, store: store) { action in
                self.sheet = nil
                switch action {
                case let .open(thread): env.router.push(.mailThread(thread.id))
                case let .write(email):
                    let senders = store.detail.map { MailStore.senders(of: $0.account) } ?? mail.senders
                    let composer = MailComposeStore(api: env.session.api, mode: .new, senders: senders, ownEmails: mail.ownEmails, initialTo: [email])
                    Task {
                        try? await Task.sleep(for: .milliseconds(350))
                        composing = MailComposeHandle(store: composer)
                    }
                case let .file(file):
                    open(file.attachment, in: MailMessage(id: file.messageID, threadID: file.threadID, accountID: "", from: address, sentAt: file.sentAt))
                }
            }
            .presentationDetents([.medium, .large])
            .presentationCornerRadius(Theme.Radius.sheet)
        }
    }

    #if DEBUG
    private func runDemo(_ action: String?) {
        guard let action, action.hasPrefix("mail-"), store.detail != nil else { return }
        if sheet != nil || composing != nil {
            sheet = nil
            composing = nil
            Task {
                try? await Task.sleep(for: .milliseconds(600))
                runDemo(action)
            }
            return
        }
        let handled: Bool
        switch action {
        case "mail-close":
            withAnimation { reply = nil }
            handled = true
        case "mail-share": sheet = .share; handled = true
        case "mail-assign": sheet = .assign; handled = true
        case "mail-move": sheet = .move; handled = true
        case "mail-labels": sheet = .labels; handled = true
        case "mail-details":
            if let last = store.messages.last { sheet = .details(last) }
            handled = true
        case "mail-people":
            if let person = store.thread.flatMap({ MailNames.lead($0, own: mail.ownEmails) }) { sheet = .person(person) }
            handled = true
        case "mail-reply": onReplyDemo(.reply); handled = true
        case "mail-replyall": onReplyDemo(.replyAll); handled = true
        case "mail-forward": onReplyDemo(.forward); handled = true
        case "mail-comments": scrollTarget = "comments"; handled = true
        case "mail-images":
            allowImages.formUnion(store.messages.map(\.id))
            handled = true
        case "mail-expand":
            withAnimation { showAll = true; expanded = Set(store.messages.map(\.id)) }
            handled = true
        case "mail-mention":
            editing = nil
            MailCommentBar.demoText = "@Pri"
            handled = true
        default: handled = false
        }
        if handled { env.router.demoAction = nil }
    }

    private func onReplyDemo(_ mode: MailComposeMode) {
        guard let last = store.messages.last else { return }
        startReply(mode, to: last)
        if mode != .forward {
            reply?.body = "Hi Erik,\n\nFinland is fine by us, and 30 days works too. I'll send the updated agreement tomorrow."
        }
    }
    #endif
}

// MARK: - Header

/// Subject, the account it is in, its labels, who it is shared with and assigned to.
struct MailThreadHeader: View {
    let detail: MailThreadDetail
    let labels: [MailFolder]
    let canAct: Bool
    var onLabels: () -> Void
    var onShare: () -> Void
    var onAssign: () -> Void

    private var mail: MailStore { .shared }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text(detail.thread.displaySubject)
                .font(ODFont.sans(22, weight: .semibold))
                .tracking(-0.45)
                .foregroundStyle(Theme.fg)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
            FlowLayout(spacing: 6) {
                accountChip
                ForEach(labels) { label in
                    Button(action: onLabels) { MailLabelChip(name: label.name, color: label.color, height: 24) }
                        .buttonStyle(TapScaleStyle())
                        .disabled(!canAct)
                }
                if !detail.shares.isEmpty {
                    Button(action: onShare) { MarkPill(symbol: "person.2.fill", text: shareText, tone: .teal).frame(height: 24) }
                        .buttonStyle(TapScaleStyle())
                        .disabled(!canAct)
                }
                if !detail.thread.assignees.isEmpty {
                    Button(action: onAssign) { assignees }
                        .buttonStyle(TapScaleStyle())
                        .disabled(!canAct)
                }
            }
            if !canAct {
                Notice(text: "You can read this conversation. Agents and above can act on it.", symbol: "eye")
            }
        }
    }

    private var accountChip: some View {
        let account = mail.account(detail.account.id)
        let shared = account.map { !$0.isPrivate } ?? false
        return HStack(spacing: 6) {
            MailAccountDot(color: detail.account.color, size: 7)
            Text(detail.account.label).font(ODFont.sans(12, weight: .semibold))
            Text(detail.account.email).font(ODFont.sans(12)).foregroundStyle(Theme.fg3).lineLimit(1)
            Image(systemName: account == nil ? "person.2.fill" : (shared ? "person.2.fill" : "lock.fill"))
                .font(.system(size: 8.5, weight: .bold))
                .foregroundStyle(Theme.fg4)
        }
        .foregroundStyle(Theme.fg2)
        .padding(.horizontal, 8)
        .frame(height: 24)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(detail.account.label), \(detail.account.email), \(account == nil ? "shared with you" : (shared ? "shared account" : "private account"))")
    }

    private var shareText: String {
        let shares = detail.shares
        if shares.everyone { return "Shared with everyone" }
        let directory = MailDirectoryNames.shared
        let names = shares.userIDs.compactMap { directory.member($0)?.user.name.split(separator: " ").first.map(String.init) }
            + shares.teamIDs.compactMap { directory.team($0)?.name }
        if names.isEmpty { return "Shared" }
        return "Shared with " + (names.count > 2 ? "\(names[0]) +\(names.count - 1)" : names.joined(separator: ", "))
    }

    private var assignees: some View {
        let people = detail.thread.assignees
        return HStack(spacing: 6) {
            HStack(spacing: -5) {
                ForEach(people.prefix(3)) { person in
                    Avatar(name: person.name, url: person.avatarURL, seed: person.id, size: 18, ring: Theme.surface)
                }
            }
            Text(people.count == 1 ? "Assigned to \(people[0].name)" : "\(people.count) assignees")
                .font(ODFont.sans(12, weight: .medium))
        }
        .foregroundStyle(Tone.violet.text)
        .padding(.leading, 4)
        .padding(.trailing, 8)
        .frame(height: 24)
        .background(Tone.violet.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous).strokeBorder(Tone.violet.line, lineWidth: 1))
    }
}

/// The directory as the mail views read it, set by the screens that have the environment.
@MainActor
final class MailDirectoryNames {
    static let shared = MailDirectoryNames()
    weak var directory: DirectoryStore?

    func member(_ id: String) -> Member? { directory?.member(id) }
    func team(_ id: String) -> Team? { directory?.teams.first { $0.id == id } }
}

// MARK: - Messages

/// One message: a line when folded (who and its first words), the whole message when open.
struct MailMessageCard: View {
    let message: MailMessage
    let account: MailThreadAccount
    let own: Set<String>
    let expanded: Bool
    let imagesAllowed: Bool
    var apiHost: String?
    var opening: String?
    var canAct: Bool
    var onToggle: () -> Void
    var onLoadImages: () -> Void
    var onPerson: (MailAddress) -> Void
    var onDetails: () -> Void
    var onReply: (MailComposeMode) -> Void
    var onOpen: (MailAttachment) -> Void

    private var fromSelf: Bool { own.contains(message.from.email.lowercased()) }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            if expanded {
                bodyView
                    .transition(.opacity)
            }
        }
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.lineSoft, lineWidth: 1))
    }

    private var header: some View {
        HStack(alignment: expanded ? .top : .center, spacing: 10) {
            Button { onPerson(message.from) } label: {
                Avatar(name: message.from.display, seed: message.from.email, size: expanded ? 36 : 30)
            }
            .buttonStyle(TapScaleStyle(scale: 0.92))
            .accessibilityLabel("Mail with \(message.from.display)")
            VStack(alignment: .leading, spacing: 2) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(fromSelf ? (message.from.name.isEmpty ? "me" : message.from.name) : message.from.display)
                        .font(ODFont.sans(14.5, weight: message.unread || expanded ? .semibold : .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    if message.unread {
                        Circle().fill(Tone.blue.solid).frame(width: 6, height: 6).accessibilityLabel("Unread")
                    }
                    Spacer(minLength: 4)
                    if !message.files.isEmpty, !expanded {
                        Image(systemName: "paperclip").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.fg4)
                    }
                    Text(expanded ? MailFormat.stamp(message.sentAt) : Format.relShort(message.sentAt))
                        .font(ODFont.sans(12))
                        .foregroundStyle(Theme.fg3)
                        .monospacedDigit()
                        .lineLimit(1)
                }
                if expanded {
                    Button(action: onDetails) {
                        HStack(spacing: 3) {
                            Text(MailNames.recipients(message, own: own))
                                .lineLimit(1)
                            Image(systemName: "chevron.down").font(.system(size: 8.5, weight: .bold))
                        }
                        .font(ODFont.sans(12.5))
                        .foregroundStyle(Theme.fg3)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Details, \(MailNames.recipients(message, own: own))")
                } else {
                    Text(message.snippet.isEmpty ? " " : message.snippet)
                        .font(ODFont.sans(13))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(1)
                }
            }
            if expanded, canAct {
                Menu {
                    Button { onReply(.reply) } label: { Label("Reply", systemImage: "arrowshape.turn.up.left") }
                    Button { onReply(.replyAll) } label: { Label("Reply all", systemImage: "arrowshape.turn.up.left.2") }
                    Button { onReply(.forward) } label: { Label("Forward", systemImage: "arrowshape.turn.up.right") }
                    Divider()
                    Button(action: onDetails) { Label("Details", systemImage: "info.circle") }
                } label: {
                    Image(systemName: "ellipsis")
                        .font(.system(size: 14, weight: .semibold))
                        .foregroundStyle(Theme.fg3)
                        .frame(width: 30, height: 30)
                        .contentShape(Rectangle())
                }
                .accessibilityLabel("Message actions")
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, expanded ? 12 : 10)
        .contentShape(Rectangle())
        .onTapGesture(perform: onToggle)
        .accessibilityAddTraits(.isButton)
        .accessibilityAction(named: expanded ? "Fold" : "Open", onToggle)
    }

    @ViewBuilder
    private var bodyView: some View {
        let source = MailHTML.body(of: message)
        let held = imagesAllowed ? (html: source, held: 0) : MailHTML.holdingImages(source, attachmentHost: apiHost)
        VStack(alignment: .leading, spacing: 12) {
            if held.held > 0 {
                HStack(alignment: .center, spacing: 10) {
                    Image(systemName: "photo.badge.exclamationmark").font(.system(size: 14)).foregroundStyle(Theme.fg3)
                    Text("Images from other sites are held back, so opening this tells the sender nothing.")
                        .font(ODFont.sans(12.5))
                        .foregroundStyle(Theme.fg2)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 4)
                    Button("Load images", action: onLoadImages)
                        .buttonStyle(ChipButtonStyle())
                }
                .padding(10)
                .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.lineSoft, lineWidth: 1))
            }
            MailBodyView(html: held.html)
                .padding(.horizontal, HTMLText.isDesignedEmail(source) ? -6 : 0)
            if !message.files.isEmpty {
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        ForEach(message.files) { file in
                            MailFileTile(file: file, busy: opening == message.id + "/" + file.id) { onOpen(file) }
                        }
                    }
                }
                .scrollClipDisabled()
            }
            if let by = message.sentBy, fromSelf {
                HStack(spacing: 5) {
                    Avatar(name: by.name, url: by.avatarURL, seed: by.id, size: 15)
                    Text("Sent by \(by.name) from opendesk").font(ODFont.sans(11.5)).foregroundStyle(Theme.fg4)
                }
            }
        }
        .padding(.horizontal, 14)
        .padding(.bottom, 14)
    }
}

/// An attachment: its kind, name and size; tapping opens it in Quick Look.
struct MailFileTile: View {
    let file: MailAttachment
    var busy = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 9) {
                ZStack {
                    RoundedRectangle(cornerRadius: 7, style: .continuous).fill(tone.soft)
                    if busy {
                        ProgressView().controlSize(.small)
                    } else {
                        Image(systemName: file.symbol).font(.system(size: 14, weight: .medium)).foregroundStyle(tone.text)
                    }
                }
                .frame(width: 32, height: 32)
                VStack(alignment: .leading, spacing: 1) {
                    Text(file.filename).font(ODFont.sans(12.5, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                    Text(Format.bytes(file.size)).font(ODFont.sans(11)).foregroundStyle(Theme.fg3)
                }
                .frame(maxWidth: 150, alignment: .leading)
            }
            .padding(.leading, 6)
            .padding(.trailing, 12)
            .frame(height: 46)
            .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        }
        .buttonStyle(TapScaleStyle())
        .accessibilityLabel("\(file.filename), \(Format.bytes(file.size))")
        .accessibilityHint("Opens a preview")
    }

    private var tone: Tone {
        let name = file.filename.lowercased()
        if name.hasSuffix(".pdf") { return .red }
        if file.isImage { return .violet }
        if name.hasSuffix(".docx") || name.hasSuffix(".doc") { return .blue }
        if name.hasSuffix(".csv") || name.hasSuffix(".xlsx") { return .green }
        if name.hasSuffix(".key") || name.hasSuffix(".pptx") { return .orange }
        if name.hasSuffix(".ics") { return .teal }
        return .gray
    }
}

/// A draft saved at the provider, waiting in the conversation.
struct MailDraftCard: View {
    let draft: MailMessage
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Image(systemName: "doc.text")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Tone.red.text)
                    .frame(width: 30, height: 30)
                    .background(Tone.red.soft, in: Circle())
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text("Draft").font(ODFont.sans(14, weight: .semibold)).foregroundStyle(Tone.red.text)
                        Text(Format.relShort(draft.sentAt)).font(ODFont.sans(12)).foregroundStyle(Theme.fg3)
                    }
                    Text(draft.snippet.isEmpty ? "Empty draft" : draft.snippet).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).lineLimit(1)
                }
                Spacer(minLength: 6)
                Text("Continue").font(ODFont.sans(13, weight: .semibold)).foregroundStyle(Theme.fg)
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 10)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Tone.red.line, style: StrokeStyle(lineWidth: 1, dash: [4, 3])))
        }
        .buttonStyle(TapScaleStyle())
    }
}
