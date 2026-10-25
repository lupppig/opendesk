import QuickLook
import SwiftUI

/// The ticket screen: header, properties, the conversation with its activity, Ollie, and the
/// composer pinned under the thread. Moving to the next ticket swaps the route, which starts
/// a fresh screen.
struct TicketView: View {
    let route: TicketRoute

    var body: some View {
        TicketHost(route: route).id(route.id)
    }
}

private struct TicketHost: View {
    let route: TicketRoute
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var controller: TicketController?

    var body: some View {
        Group {
            if let controller {
                TicketScreen(c: controller)
            } else {
                Theme.chrome.ignoresSafeArea()
            }
        }
        .toolbarVisibility(.hidden, for: .navigationBar)
        .tabBarHidden(true)
        .onAppear {
            guard controller == nil else { return }
            let store = TicketStore(ticketID: route.id, api: env.session.api, seed: route.seed)
            let made = TicketController(env: env, store: store, composer: TicketComposerStore(ticketID: route.id))
            made.onClose = { dismiss() }
            controller = made
        }
    }
}

struct TicketScreen: View {
    @Bindable var c: TicketController
    @Environment(AppEnvironment.self) private var env
    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @FocusState private var composerFocused: Bool
    @State private var markedRead = false
    @State private var settled = false

    private var store: TicketStore { c.store }
    private var meID: String? { c.me?.id }

    private var viewers: [PresenceViewer] {
        (env.realtime.presence[store.resolvedID] ?? []).filter { $0.userID != meID }
    }

    var body: some View {
        content
            .background(Theme.chrome.ignoresSafeArea())
            .safeAreaBar(edge: .top, spacing: 0) {
                topBar.background(alignment: .top) { EdgeFade(edge: .top) }
            }
            .safeAreaBar(edge: .bottom, spacing: 0) {
                bottom.background(alignment: .bottom) { EdgeFade(edge: .bottom) }
            }
            .sheet(item: $c.sheet) { sheet in
                TicketSheetContent(c: c, sheet: sheet)
            }
            .confirmationDialog(deleteTitle, isPresented: $c.confirmDelete, titleVisibility: .visible) {
                Button("Delete ticket", role: .destructive) { c.deleteTicket() }
                Button("Cancel", role: .cancel) {}
            } message: {
                Text("The conversation, notes and attachments are removed for everyone. This can't be undone.")
            }
            .confirmationDialog(messageDeleteTitle, isPresented: pendingDeleteShown, titleVisibility: .visible, presenting: c.pendingDelete) { message in
                Button(message.isNote ? "Delete note" : "Delete message", role: .destructive) { c.deleteMessage(message) }
                Button("Cancel", role: .cancel) {}
            } message: { _ in
                Text("It's removed from the ticket for everyone.")
            }
            .confirmationDialog("Send this to the customer?", isPresented: mentionShown, titleVisibility: .visible, presenting: c.mentionCheck) { check in
                Button("Turn into internal note") {
                    c.composer.mode = .note
                    c.send(status: check.status, checked: true)
                }
                Button("Send anyway") { c.send(status: check.status, checked: true) }
                Button("Cancel", role: .cancel) {}
            } message: { check in
                Text("This reply mentions \(ListFormatter.localizedString(byJoining: check.names)). The customer will read it.")
            }
            .quickLookPreview($c.previewURL)
            .onAppear(perform: appeared)
            .onDisappear(perform: disappeared)
            .onChange(of: store.ticket?.id, initial: true) { _, id in joined(id) }
            .onChange(of: c.composerFocus) { _, wanted in
                guard wanted else { return }
                composerFocused = true
                c.composerFocus = false
            }
            .onChange(of: c.composer.trimmed.isEmpty) { _, empty in
                env.realtime.setPresenceState(empty ? .viewing : .replying)
            }
            .onChange(of: c.composer.text) { _, text in
                if !text.isEmpty, c.composer.mode == .reply { env.realtime.noteTyping() }
            }
        #if DEBUG
            .onChange(of: env.router.demoAction, initial: true) { _, action in
                guard let action, action.hasPrefix("ticket-") else { return }
                env.router.demoAction = nil
                Task {
                    try? await Task.sleep(for: .milliseconds(250))
                    runDemo(action)
                }
            }
        #endif
    }

    private var deleteTitle: String { "Delete #\(store.ticket?.number ?? 0)?" }
    private var messageDeleteTitle: String { c.pendingDelete?.isNote == true ? "Delete this note?" : "Delete this message?" }
    private var pendingDeleteShown: Binding<Bool> {
        Binding(get: { c.pendingDelete != nil }, set: { if !$0 { c.pendingDelete = nil } })
    }

    private var mentionShown: Binding<Bool> {
        Binding(get: { c.mentionCheck != nil }, set: { if !$0 { c.mentionCheck = nil } })
    }

    // MARK: Lifecycle

    private func appeared() {
        store.start(realtime: env.realtime)
        env.router.visibleTicketID = store.resolvedID
    }

    private func joined(_ id: String?) {
        guard let id, let ticket = store.ticket else { return }
        env.router.visibleTicketID = id
        env.realtime.setActiveTicket(id, contactID: ticket.contact?.id)
        if !markedRead {
            markedRead = true
            Task { await env.notifications.markRead(ticketID: id) }
        }
    }

    private func disappeared() {
        store.stop()
        let id = store.resolvedID
        if env.router.visibleTicketID == id || env.router.visibleTicketID == store.ticketID {
            env.router.visibleTicketID = nil
            env.realtime.setActiveTicket(nil, contactID: nil)
        }
    }

    // MARK: Header

    private var topBar: some View {
        TicketTopBar(ticket: store.ticket, viewers: viewers, canWrite: c.canWrite && !store.removed, mailbox: store.replyAddress,
                     onBack: c.onClose, onDetails: { c.open(.details) }, onSolve: c.solve, onReopen: c.reopen) {
            moreMenu
        }
    }

    @ViewBuilder private var moreMenu: some View {
        if let ticket = store.ticket {
            if c.canWrite {
                Section {
                    if ticket.assignee?.id != meID {
                        Button("Assign to me", systemImage: "person.crop.circle.badge.checkmark") { c.assignToMe() }
                    }
                    if ticket.isSnoozed {
                        Button("Unsnooze", systemImage: "sun.max") { c.unsnooze() }
                    } else if !ticket.status.isDone {
                        Button("Snooze", systemImage: "moon.zzz") { c.open(.snooze) }
                    }
                    Menu("Status", systemImage: "circle.dashed") {
                        ForEach(TicketStatus.allCases.filter { $0 != ticket.status }, id: \.self) { status in
                            Button(status.title) { status == .solved ? c.solve() : c.setStatus(status) }
                        }
                    }
                }
                Section {
                    Button("Side conversation", systemImage: "bubble.left.and.bubble.right") { c.open(store.threads.isEmpty ? .newSide : .sides) }
                    Button("Merge into another ticket", systemImage: "arrow.triangle.merge") { c.open(.merge) }
                }
                if env.directory.ai.assistantOn {
                    Section {
                        Button("Summarize with Ollie", systemImage: "text.alignleft") { c.summarize() }
                        Button("Triage with Ollie", systemImage: "wand.and.stars") { c.triage() }
                        Button("Ask Ollie", systemImage: "questionmark.bubble") { c.open(.ask) }
                    }
                }
            }
            Section {
                Button("Copy link", systemImage: "link") { c.copyLink() }
                Button("Details", systemImage: "info.circle") { c.open(.details) }
            }
            if c.canWrite {
                Section {
                    if ticket.spam {
                        Button("Not spam", systemImage: "tray.and.arrow.up") { c.setSpam(false) }
                    } else {
                        Button("Mark as spam", systemImage: "xmark.bin") { c.setSpam(true) }
                    }
                    Button("Delete ticket", systemImage: "trash", role: .destructive) { c.confirmDelete = true }
                }
            }
        }
    }

    // MARK: Content

    @ViewBuilder private var content: some View {
        if store.removed {
            EmptyState(symbol: store.mergedInto == nil ? "trash" : "arrow.triangle.merge",
                       title: store.mergedInto.map { "Merged into #\($0)" } ?? "This ticket was deleted",
                       message: store.mergedInto == nil ? "Someone removed it while you had it open." : "Its conversation continues on the other ticket.",
                       actionTitle: "Go back", action: c.onClose)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let ticket = store.ticket {
            thread(ticket)
        } else if store.notFound {
            EmptyState(symbol: "questionmark.folder", title: "Ticket not found", message: "It may have been deleted, merged, or moved to a team you can't see.", actionTitle: "Go back", action: c.onClose)
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else if let error = store.error {
            ErrorState(title: "Couldn't open this ticket", message: error) { Task { await store.load() } }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
        } else {
            ScrollView {
                SkeletonRows().padding(16)
            }
            .scrollDisabled(true)
        }
    }

    private func thread(_ ticket: Ticket) -> some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    Color.clear.frame(height: 0).id("top")
                    TicketSubjectBlock(ticket: ticket, canWrite: c.canWrite, teamName: ticket.teamID.flatMap { env.directory.team($0)?.name }, me: meID,
                                       editing: $c.editingSubject, onRename: c.rename, onOpen: { c.open($0) })
                    banners(ticket)
                    if env.directory.ai.assistantOn, store.summary != nil || !store.suggestions.isEmpty || c.canWrite {
                        TicketOllieCard(summary: store.summary, suggestions: c.canWrite ? store.suggestions : [], summarizing: store.summarizing, triaging: store.triaging,
                                        canWrite: c.canWrite, teamName: { env.directory.team($0)?.name }, expanded: $c.ollieExpanded,
                                        onSummarize: c.summarize, onApply: c.accept, onSkip: c.skip, onApplyAll: c.acceptAll)
                    }
                    TicketThreadList(items: store.thread, phrasing: ActivityPhrasing(directory: env.directory, me: meID, contactName: ticket.contactName)) { message in
                        cell(message, ticket: ticket)
                    }
                    live(ticket)
                    if let suggestion = store.suggestedReply, suggestion.isPending {
                        TicketSuggestedReplyCard(suggestion: suggestion, canWrite: c.canWrite, sending: c.sendingSuggestion,
                                                 onSend: c.sendSuggestedReply, onEdit: c.editSuggestedReply, onDiscard: c.discardSuggestedReply)
                    }
                    if let csat = ticket.csat {
                        TicketCSATCard(csat: csat, contactName: ticket.contactName)
                    }
                    Color.clear.frame(height: 1).id("bottom")
                }
                .padding(.horizontal, 14)
                .padding(.top, 6)
                .padding(.bottom, 14)
            }
            .scrollDismissesKeyboard(.interactively)
            .defaultScrollAnchor(.bottom, for: .initialOffset)
            .defaultScrollAnchor(.bottom, for: .sizeChanges)
            .refreshable { await store.load() }
            .task(id: store.messages.isEmpty) {
                // Designed emails size themselves after the first layout; keep the newest message in view while they do.
                guard !store.messages.isEmpty, !settled else { return }
                for delay in [120, 350, 800] {
                    try? await Task.sleep(for: .milliseconds(delay))
                    guard !Task.isCancelled else { return }
                    proxy.scrollTo("bottom", anchor: .bottom)
                }
                settled = true
            }
            .onChange(of: c.scroll) { _, request in
                guard let request else { return }
                withAnimation(reduceMotion ? nil : Motion.smooth) {
                    proxy.scrollTo(request.target == .top ? "top" : "bottom", anchor: request.target == .top ? .top : .bottom)
                }
            }
            .onChange(of: store.messages.last?.id) { _, _ in
                guard let last = store.messages.last, last.isLocal || last.isInbound else { return }
                withAnimation(reduceMotion ? nil : Motion.smooth) { proxy.scrollTo("bottom", anchor: .bottom) }
            }
        }
    }

    @ViewBuilder private func banners(_ ticket: Ticket) -> some View {
        if let until = ticket.snoozedUntil, ticket.isSnoozed {
            TicketBanner(symbol: "moon.zzz", text: "Snoozed until \(Format.shortDate(until)), \(Format.clock(until)). It comes back on its own, or when the customer writes.",
                         tone: .indigo, actionTitle: c.canWrite ? "Unsnooze" : nil, action: c.unsnooze)
        }
        if ticket.spam {
            TicketBanner(symbol: "xmark.bin", text: "Marked as spam. It stays out of your views and no replies go out.", tone: .red,
                         actionTitle: c.canWrite ? "Not spam" : nil, action: { c.setSpam(false) })
        }
        if ticket.aiHandedOff, !ticket.status.isDone {
            TicketBanner(symbol: "arrow.triangle.branch", text: "Ollie handed this conversation to the team. The customer is waiting for a person.", tone: .violet)
        }
    }

    private func cell(_ message: Message, ticket: Ticket) -> some View {
        let permissions = MessagePermissions(canWrite: c.canWrite, isAdmin: env.session.isAdmin, me: meID, channel: ticket.channel)
        return TicketMessageCell(
            message: message,
            permissions: permissions,
            baseURL: env.session.instance.apiURL,
            translation: store.translations[message.id],
            translating: store.translating.contains(message.id),
            original: store.originals[message.id],
            retrying: c.retrying.contains(message.id),
            remoteImages: Binding(
                get: { c.remoteImages.contains(message.id) },
                set: { if $0 { c.remoteImages.insert(message.id) } else { c.remoteImages.remove(message.id) } }
            ),
            onAction: { c.handle($0, on: message) }
        )
    }

    @ViewBuilder private func live(_ ticket: Ticket) -> some View {
        let agents = env.realtime.typingAgents(ticket.id).filter { $0.userID != meID }
        if let reply = env.realtime.live[ticket.id] {
            TicketLiveReplyCard(live: reply, bot: store.messages.last(where: { $0.bot != nil })?.bot)
                .transition(.opacity)
        }
        if env.realtime.isCustomerTyping(ticket.id) {
            TicketTypingRow(text: "\(ticket.contactName) is typing", customer: true)
        }
        if !agents.isEmpty {
            TicketTypingRow(text: "\(ListFormatter.localizedString(byJoining: agents.map(\.name))) \(agents.count == 1 ? "is" : "are") typing")
        }
    }

    // MARK: Composer

    @ViewBuilder private var bottom: some View {
        if let ticket = store.ticket, !store.removed {
            if c.canWrite {
                TicketComposerView(
                    composer: c.composer,
                    store: store,
                    ticket: ticket,
                    members: env.directory.members,
                    me: meID,
                    replying: viewers.filter { $0.state == .replying },
                    savedReplies: env.directory.savedReplies,
                    assistantOn: env.directory.ai.assistantOn,
                    focused: $composerFocused,
                    actions: ComposerActions(
                        send: { c.send(status: $0) },
                        open: { c.open($0) },
                        ollie: c.ollie,
                        savedReply: c.insertSavedReply,
                        photos: c.addPhotos,
                        files: c.addFiles,
                        useDraft: c.useSharedDraft,
                        discardDraft: c.discardSharedDraft,
                        undo: c.undoSend,
                        sendNow: c.sendHeldNow
                    )
                )
            } else {
                HStack(spacing: 8) {
                    Image(systemName: "eye").font(.system(size: 12.5, weight: .medium))
                    Text("You can read this ticket. Replying needs an agent role.")
                        .font(ODFont.sans(13))
                }
                .foregroundStyle(Theme.fg3)
                .padding(.horizontal, 14)
                .frame(height: 40)
                .glassEffect(.regular, in: Capsule())
                .padding(.bottom, 6)
            }
        }
    }

    // MARK: Demo

    #if DEBUG
    private func runDemo(_ action: String) {
        let ticket = store.ticket
        switch action {
        case "ticket-details": c.sheet = .details
        case "ticket-merge": c.sheet = .merge
        case "ticket-saved-replies": c.sheet = .savedReplies
        case "ticket-ask": c.sheet = .ask
        case "ticket-side": c.sheet = .sides
        case "ticket-new-side": c.sheet = .newSide
        case "ticket-side-thread": if let thread = store.threads.first { c.sheet = .thread(thread.id) }
        case "ticket-snooze": c.sheet = .snooze
        case "ticket-labels": c.sheet = .labels
        case "ticket-assignee": c.sheet = .assignee
        case "ticket-team": c.sheet = .team
        case "ticket-status": c.sheet = .status
        case "ticket-priority": c.sheet = .priority
        case "ticket-severity": c.sheet = .severity
        case "ticket-guided-draft": c.sheet = .guidedDraft
        case "ticket-delete": c.confirmDelete = true
        case "ticket-edit-subject": c.editingSubject = true
        case "ticket-top": c.scroll = ScrollRequest(target: .top)
        case "ticket-field":
            if let field = env.directory.fields(for: "ticket").first { c.sheet = .field(field) }
        case "ticket-required":
            let fields = ticket.map(c.missingRequired) ?? []
            c.sheet = .required(fields.isEmpty ? env.directory.fields(for: "ticket").filter(\.requiredOnSolve) : fields)
        case "ticket-edit-note":
            if let note = store.messages.last(where: { $0.isNote && $0.author.user?.id == meID }) { c.sheet = .editNote(note) }
        case "ticket-compose":
            c.composer.mode = .reply
            c.composer.text = "Hi \(ticket?.contactName.split(separator: " ").first.map(String.init) ?? "there"), thanks for the details. I've checked your account and "
            composerFocused = true
        case "ticket-note":
            c.composer.mode = .note
            c.composer.text = "Checked the logs, the retry job failed twice. @"
            composerFocused = true
        case "ticket-cc":
            c.composer.mode = .reply
            c.composer.showCopies = true
            c.composer.cc = "finance@northwind.io"
            c.composer.text = "Looping in your finance team so they have the receipt."
        case "ticket-undo-send":
            c.composer.mode = .reply
            c.composer.text = "Thanks for your patience. The fix is live, could you try again?"
            c.send(status: nil)
        case "ticket-mention-check":
            c.composer.mode = .reply
            c.composer.text = "Thanks! I asked @Priya to look at the export for you."
            c.send(status: nil)
        default: break
        }
    }
    #endif
}

/// One sheet's content; the same views open from the screen and from the details sheet.
struct TicketSheetContent: View {
    @Bindable var c: TicketController
    let sheet: TicketSheet
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        if let ticket = c.store.ticket {
            switch sheet {
            case .details:
                TicketDetailsSheet(c: c)
            case .status:
                StatusPicker(current: ticket.status) { status in
                    status == .solved ? c.solve() : c.setStatus(status)
                }
                .presentationDetents([.medium])
            case .priority:
                PriorityPicker(current: ticket.priority, onPick: c.setPriority)
                    .presentationDetents([.medium])
            case .severity:
                SeverityPicker(current: ticket.severity, onPick: c.setSeverity)
                    .presentationDetents([.medium, .large])
            case .assignee:
                AssigneePicker(current: ticket.assignee?.id, onPick: c.assign)
                    .presentationDetents([.medium, .large])
            case .team:
                TeamPicker(current: ticket.teamID, onPick: c.setTeam)
                    .presentationDetents([.medium, .large])
            case .labels:
                LabelPicker(current: ticket.tags, onSave: c.setLabels)
                    .presentationDetents([.medium, .large])
            case .snooze:
                SnoozeSheet { c.snooze(until: $0) }
                    .presentationDetents([.medium])
            case .merge:
                TicketMergeSheet(c: c)
            case .savedReplies:
                SavedReplyPicker(replies: env.directory.savedReplies, onPick: c.insertSavedReply)
                    .presentationDetents([.medium, .large])
            case .ask:
                TicketAskSheet(store: c.store, contactName: ticket.contactName, onInsert: c.insertNote)
                    .presentationDetents([.medium, .large])
            case .sides:
                SideConversationsSheet(c: c)
                    .presentationDetents([.medium, .large])
            case .newSide:
                NewSideConversationSheet(c: c)
            case let .thread(id):
                SideThreadSheet(c: c, threadID: id)
            case .guidedDraft:
                GuidedDraftSheet { c.draft(instructions: $0) }
                    .presentationDetents([.medium])
            case let .field(field):
                FieldEditorSheet(field: field, initial: ticket.fields[field.key], members: env.directory.members) { c.setField(field.key, $0) }
                    .presentationDetents([.medium, .large])
            case let .required(fields):
                RequiredFieldsSheet(fields: fields, initial: ticket.fields, members: env.directory.members, onSolve: c.solve(with:))
                    .presentationDetents([.medium, .large])
            case let .editNote(message):
                EditNoteSheet(message: message) { await c.saveNote(message, text: $0) }
            }
        }
    }
}

/// The chrome fading out behind the floating header or composer, so the thread passing
/// under the glass never fights with it.
private struct EdgeFade: View {
    let edge: VerticalEdge

    var body: some View {
        LinearGradient(
            stops: [.init(color: Theme.chrome, location: 0), .init(color: Theme.chrome.opacity(0.85), location: 0.55), .init(color: Theme.chrome.opacity(0), location: 1)],
            startPoint: edge == .top ? .top : .bottom,
            endPoint: edge == .top ? .bottom : .top
        )
        .padding(edge == .top ? .bottom : .top, -18)
        .ignoresSafeArea(edges: edge == .top ? .top : .bottom)
        .allowsHitTesting(false)
    }
}
