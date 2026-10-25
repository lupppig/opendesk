import SwiftUI

/// Ollie, the assistant, as a sheet over the screen it was opened from. The
/// conversation lives in `AssistantHolder`, so closing and reopening the
/// sheet picks it back up.
struct AssistantView: View {
    let context: AssistantPageContext

    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let suite = AssistantHolder.shared.suite(api: env.session.api, workspaceID: env.session.workspace?.id, userID: env.session.user?.id)
        AssistantScreen(chat: suite.chat, history: suite.history, tips: suite.tips, context: context)
    }
}

private enum AssistantPage: Hashable { case history }

private struct AssistantScreen: View {
    let chat: AssistantStore
    let history: AssistantHistoryStore
    let tips: AssistantTipsStore
    let context: AssistantPageContext

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    @State private var path: [AssistantPage] = []
    @State private var input = ""
    @State private var celebrating = false
    @State private var celebration: Task<Void, Never>?
    @FocusState private var focused: Bool

    private var name: String { env.directory.ai.assistantName ?? "Ollie" }
    private var links: AssistantLinks { AssistantLinks(env: env, openURL: openURL) }
    private var prompts: [AssistantPrompt] { AssistantPrompt.suggestions(for: context.page) }

    private var markState: OllieMark.State {
        if chat.approval != nil { return .attention }
        if chat.running { return .thinking }
        if celebrating { return .done }
        return .idle
    }

    var body: some View {
        NavigationStack(path: $path) {
            main
                .navigationDestination(for: AssistantPage.self) { _ in
                    AssistantHistoryView(history: history, chat: chat, onOpen: open, onNewChat: newChat)
                        .environment(\.openURL, OpenURLAction { links.handle($0) })
                }
        }
        .tint(Theme.fg)
        .environment(\.openURL, OpenURLAction { links.handle($0) })
        .task { await tips.refresh() }
        .onAppear { runDemoAction() }
        .onChange(of: env.router.demoAction) { runDemoAction() }
        .onChange(of: chat.finishedRuns) { finished() }
        .onChange(of: chat.approval?.actionID) { _, id in
            if id != nil { Haptics.warning() }
        }
        .onChange(of: chat.sessionsChanged) {
            guard history.loaded else { return }
            Task { await history.load() }
        }
    }

    private var main: some View {
        Group {
            if chat.loading {
                loadingSkeleton
            } else if let error = chat.loadError {
                ErrorState(title: "Couldn't open this conversation", message: error) {
                    if let session = chat.session { Task { await chat.open(session) } }
                }
                .frame(maxHeight: .infinity)
            } else if chat.turns.isEmpty {
                AssistantGreeting(assistantName: name, prompts: prompts) { send($0.prompt) }
            } else {
                AssistantConversationView(chat: chat, assistantName: name, onRef: links.open) {
                    chat.retry(context: context)
                }
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.surface)
        .safeAreaInset(edge: .top, spacing: 0) { tipBar }
        .safeAreaInset(edge: .bottom, spacing: 0) { bottomBar }
        .navigationBarTitleDisplayMode(.inline)
        .toolbar { toolbar }
        .motion(Motion.smooth, value: chat.turns.isEmpty)
        .motion(Motion.snappy, value: focused)
    }

    // MARK: Header

    @ToolbarContentBuilder
    private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .topBarLeading) {
            Button {
                dismiss()
            } label: {
                Image(systemName: "xmark")
            }
            .accessibilityLabel("Close \(name)")
        }
        ToolbarItem(placement: .principal) {
            HStack(spacing: 8) {
                OllieMark(size: 24, state: markState)
                VStack(alignment: .leading, spacing: 0) {
                    Text(name)
                        .font(ODFont.sans(15, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                    Text(subtitle)
                        .font(ODFont.sans(11.5))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(1)
                        .contentTransition(.opacity)
                }
            }
            .frame(width: 150)
            .motion(Motion.fade, value: subtitle)
            .accessibilityElement(children: .combine)
        }
        ToolbarItemGroup(placement: .topBarTrailing) {
            Button {
                path = [.history]
            } label: {
                Image(systemName: "clock.arrow.circlepath")
            }
            .accessibilityLabel("Conversations")
            Button {
                newChat()
            } label: {
                Image(systemName: "square.and.pencil")
            }
            .disabled(chat.isEmpty)
            .accessibilityLabel("New chat")
        }
    }

    private var subtitle: String {
        if chat.approval != nil { return "Waiting for your approval" }
        if chat.running { return chat.deciding ? "Making the change" : "Working on it" }
        return chat.title ?? (chat.turns.isEmpty ? "Your AI teammate" : "New conversation")
    }

    // MARK: Tip

    @ViewBuilder
    private var tipBar: some View {
        if let tip = tips.tip, chat.turns.isEmpty, !chat.loading, path.isEmpty, input.isEmpty, !focused {
            AssistantTipCard(tip: tip, assistantName: name) {
                tips.dismiss()
                ask(tip.prompt)
            } onLater: {
                withAnimation(reduceMotion ? nil : Motion.snappy) { tips.dismiss() }
            } onPause: {
                withAnimation(reduceMotion ? nil : Motion.snappy) { tips.pauseForAWeek() }
            }
            .padding(.horizontal, 16)
            .padding(.top, 8)
            .padding(.bottom, 4)
            .transition(reduceMotion ? .opacity : .scale(scale: 0.96, anchor: .top).combined(with: .opacity))
        }
    }

    // MARK: Composer

    private var bottomBar: some View {
        VStack(spacing: 10) {
            if !chat.turns.isEmpty, !chat.running, chat.approval == nil, input.isEmpty, !chat.loading {
                AssistantSuggestionStrip(prompts: Array(prompts.prefix(3))) { send($0.prompt) }
                    .transition(.opacity.combined(with: .offset(y: 6)))
            }
            AssistantComposer(
                text: $input,
                focused: $focused,
                running: chat.running,
                waitingForApproval: chat.approval != nil,
                assistantName: name,
                contextLabel: contextLabel,
                onSend: send,
                onStop: { chat.stop() }
            )
            .padding(.horizontal, 12)
        }
        .padding(.top, 8)
        .padding(.bottom, 6)
        .motion(Motion.snappy, value: chat.running)
        .motion(Motion.snappy, value: input.isEmpty)
    }

    /// What Ollie is told about the screen behind the sheet, in a word.
    private var contextLabel: AssistantContextLabel? {
        switch context.page {
        case "ticket":
            if let id = context.ticketID, let ticket = env.inbox.tickets.first(where: { $0.id == id || String($0.number) == id }) {
                return AssistantContextLabel(title: "Ticket #\(ticket.number)", symbol: "eye")
            }
            return AssistantContextLabel(title: "This ticket", symbol: "eye")
        case "inbox":
            let folder = context.folder.flatMap(Folder.init(rawValue:))
            return AssistantContextLabel(title: folder.map { "Inbox, \($0.title.lowercased())" } ?? "Inbox", symbol: "tray")
        case "view":
            let view = env.directory.views.first { $0.id == context.viewID }
            return AssistantContextLabel(title: view?.name ?? "This view", symbol: "line.3.horizontal.decrease")
        case "reports": return AssistantContextLabel(title: "Reports", symbol: "chart.bar.xaxis")
        case "kb": return AssistantContextLabel(title: "Knowledge base", symbol: "book.closed")
        case "contacts": return AssistantContextLabel(title: "Contacts", symbol: "person.2")
        case "mail": return AssistantContextLabel(title: "Mail", symbol: "envelope")
        default: return nil
        }
    }

    private var loadingSkeleton: some View {
        VStack(alignment: .leading, spacing: 22) {
            HStack {
                Spacer()
                RoundedRectangle(cornerRadius: 20, style: .continuous).fill(Theme.hover).frame(width: 190, height: 42)
            }
            VStack(alignment: .leading, spacing: 10) {
                ForEach([0.94, 0.82, 0.58], id: \.self) { width in
                    RoundedRectangle(cornerRadius: 5).fill(Theme.hover)
                        .frame(height: 13)
                        .containerRelativeFrame(.horizontal) { length, _ in (length - 40) * width }
                }
            }
            Spacer()
        }
        .padding(20)
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .topLeading)
        .accessibilityLabel("Loading")
    }

    // MARK: Actions

    private func send(_ text: String) {
        guard !chat.running else { return }
        Haptics.tap()
        chat.send(text, context: context)
    }

    /// A tip's question goes to a fresh conversation, as on the web.
    private func ask(_ prompt: String) {
        if !chat.isEmpty { chat.newChat() }
        send(prompt)
    }

    private func newChat() {
        Haptics.selection()
        withAnimation(reduceMotion ? nil : Motion.snappy) { chat.newChat() }
        input = ""
        path = []
        focused = true
    }

    private func open(_ session: AssistantSession) {
        path = []
        Task { await chat.open(session) }
    }

    private func finished() {
        switch chat.outcome {
        case .done:
            celebration?.cancel()
            celebrating = true
            celebration = Task {
                try? await Task.sleep(for: .seconds(2.4))
                guard !Task.isCancelled else { return }
                celebrating = false
            }
        case .failed:
            Haptics.error()
        case .none:
            break
        }
    }

    // MARK: Demo

    /// QA states no tap reaches (`/demo/ollie-…`).
    private func runDemoAction() {
        #if DEBUG
        guard let action = env.router.demoAction, action.hasPrefix("ollie") else { return }
        env.router.demoAction = nil
        let parts = action.split(separator: "/").map(String.init)
        switch parts.first {
        case "ollie-ask":
            let key = parts.dropFirst().first ?? "triage"
            chat.newChat()
            send(AssistantPrompt(rawValue: key)?.prompt ?? key.replacingOccurrences(of: "-", with: " "))
        case "ollie-history":
            path = [.history]
        case "ollie-open":
            let index = Int(parts.dropFirst().first ?? "") ?? 0
            Task {
                await history.load()
                if index < history.sessions.count { open(history.sessions[index]) }
            }
        case "ollie-new":
            newChat()
        case "ollie-slash":
            input = "/" + (parts.dropFirst().first ?? "")
            focused = true
        case "ollie-approve":
            chat.decide(approve: true)
        case "ollie-deny":
            chat.decide(approve: false)
        case "ollie-error":
            Task {
                await chat.demoFailNext()
                chat.newChat()
                send(AssistantPrompt.breach.prompt)
            }
        default:
            break
        }
        #endif
    }
}
