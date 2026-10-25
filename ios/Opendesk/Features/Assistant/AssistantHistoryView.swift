import SwiftUI

/// Past conversations, newest first: open one, rename it, delete it, or
/// start a new chat (web `SessionList.tsx`).
struct AssistantHistoryView: View {
    let history: AssistantHistoryStore
    let chat: AssistantStore
    let onOpen: (AssistantSession) -> Void
    let onNewChat: () -> Void

    @Environment(AppEnvironment.self) private var env
    @State private var query = ""
    @State private var renaming: AssistantSession?
    @State private var renameText = ""
    @State private var deleting: AssistantSession?

    var body: some View {
        content
            .background(Theme.surface)
            .navigationTitle("Conversations")
            .navigationBarTitleDisplayMode(.inline)
            .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .automatic), prompt: "Search conversations")
            .toolbar {
                ToolbarItem(placement: .topBarTrailing) {
                    Button {
                        onNewChat()
                    } label: {
                        Image(systemName: "square.and.pencil")
                    }
                    .accessibilityLabel("New chat")
                }
            }
            .task { await history.load() }
            .refreshable { await history.load() }
            .alert("Rename conversation", isPresented: Binding(get: { renaming != nil }, set: { if !$0 { renaming = nil } })) {
                TextField("Name", text: $renameText)
                Button("Cancel", role: .cancel) { renaming = nil }
                Button("Rename") { commitRename() }
            }
            .confirmationDialog("Delete this conversation? Its messages are removed.", isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }), titleVisibility: .visible, presenting: deleting) { session in
                Button("Delete conversation", role: .destructive) { delete(session) }
            }
    }

    @ViewBuilder
    private var content: some View {
        let groups = history.grouped(matching: query)
        if !history.loaded, history.loading {
            VStack(spacing: 0) { SkeletonRows(count: 5) }
                .frame(maxHeight: .infinity, alignment: .top)
        } else if let error = history.error, history.sessions.isEmpty {
            ErrorState(title: "Couldn't load conversations", message: error) { Task { await history.load() } }
                .frame(maxHeight: .infinity)
        } else if groups.isEmpty {
            Group {
                if query.isEmpty {
                    EmptyState(title: "No conversations yet", message: "Ask \(env.directory.ai.assistantName ?? "Ollie") anything and it shows up here.", actionTitle: "New chat", action: onNewChat) {
                        OllieMark(size: 30, state: .muted)
                    }
                } else {
                    EmptyState(symbol: "magnifyingglass", title: "No matches", message: "No conversation is called \u{201C}\(query)\u{201D}.")
                }
            }
            .frame(maxHeight: .infinity)
        } else {
            List {
                ForEach(groups, id: \.title) { group in
                    Section {
                        ForEach(group.sessions) { session in
                            row(session)
                                .listRowInsets(EdgeInsets(top: 0, leading: 20, bottom: 0, trailing: 16))
                                .listRowBackground(Theme.surface)
                                .listRowSeparatorTint(Theme.lineSoft)
                        }
                    } header: {
                        Text(group.title)
                            .font(ODFont.sans(12.5, weight: .medium))
                            .foregroundStyle(Theme.fg3)
                            .textCase(nil)
                    }
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
        }
    }

    private func row(_ session: AssistantSession) -> some View {
        let current = session.id == chat.session?.id
        return Button {
            Haptics.selection()
            onOpen(session)
        } label: {
            HStack(spacing: 12) {
                Image(systemName: current ? "bubble.left.and.text.bubble.right.fill" : "bubble.left.and.text.bubble.right")
                    .font(.system(size: 13.5, weight: .regular))
                    .foregroundStyle(current ? Tone.violet.text : Theme.fg4)
                    .frame(width: 22)
                VStack(alignment: .leading, spacing: 3) {
                    Text(Self.title(session))
                        .font(ODFont.sans(15, weight: current ? .semibold : .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    if current {
                        Text("Open now")
                            .font(ODFont.sans(12))
                            .foregroundStyle(Tone.violet.text)
                    }
                }
                Spacer(minLength: 8)
                Text(Format.relShort(session.updatedAt ?? session.createdAt))
                    .font(ODFont.sans(12.5).monospacedDigit())
                    .foregroundStyle(Theme.fg4)
            }
            .frame(minHeight: 52)
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .swipeActions(edge: .trailing, allowsFullSwipe: false) {
            Button(role: .destructive) {
                deleting = session
            } label: {
                Label("Delete", systemImage: "trash")
            }
            Button {
                startRename(session)
            } label: {
                Label("Rename", systemImage: "pencil")
            }
            .tint(Theme.fg3)
        }
        .contextMenu {
            Button {
                startRename(session)
            } label: {
                Label("Rename", systemImage: "pencil")
            }
            Button(role: .destructive) {
                deleting = session
            } label: {
                Label("Delete", systemImage: "trash")
            }
        }
    }

    static func title(_ session: AssistantSession) -> String {
        let title = session.title?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        return title.isEmpty ? "New conversation" : title
    }

    private func startRename(_ session: AssistantSession) {
        renameText = session.title ?? ""
        renaming = session
    }

    private func commitRename() {
        guard let session = renaming else { return }
        renaming = nil
        let title = renameText
        Task {
            switch await history.rename(session.id, to: title) {
            case let .success(updated): chat.renamed(updated)
            case let .failure(error): env.toasts.show(error.message, kind: .error)
            }
        }
    }

    private func delete(_ session: AssistantSession) {
        deleting = nil
        Haptics.warning()
        Task {
            if let error = await history.delete(session.id) {
                env.toasts.show(error.message, kind: .error)
            } else {
                chat.deleted(session.id)
            }
        }
    }
}
