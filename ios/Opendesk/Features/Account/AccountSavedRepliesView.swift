import SwiftUI

/// The workspace's saved replies: search, add, edit and remove (agents and up).
struct AccountSavedRepliesView: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @State private var query = ""
    @State private var editing: AccountReplyEdit?
    @State private var confirm: AccountConfirm?

    private var canWrite: Bool { env.session.canWrite }

    private var shown: [AccountSavedReply] {
        let text = query.trimmingCharacters(in: .whitespaces).lowercased()
        guard !text.isEmpty else { return store.replies }
        let bare = text.hasPrefix("/") ? String(text.dropFirst()) : text
        return store.replies.filter {
            $0.title.lowercased().contains(text) || $0.shortcut.lowercased().hasPrefix(bare) || $0.plainBody.lowercased().contains(text)
        }
    }

    var body: some View {
        content
            .background(Theme.chrome.ignoresSafeArea())
            .accountTitle("Saved replies")
            .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Search titles, shortcuts and text")
            .toolbar {
                if canWrite {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button { editing = AccountReplyEdit(reply: nil) } label: { Image(systemName: "plus") }
                            .accessibilityLabel("New saved reply")
                    }
                }
            }
            .sheet(item: $editing) { edit in
                AccountReplyEditor(reply: edit.reply)
                    .presentationDetents([.large])
                    .presentationCornerRadius(Theme.Radius.sheet)
            }
            .accountConfirm($confirm)
            .task { await store.loadReplies() }
            .onChange(of: env.directory.savedReplies) { _, _ in Task { await store.loadReplies() } }
            .onAppear(perform: handleDemo)
            .onChange(of: env.router.demoAction) { _, _ in handleDemo() }
            .onChange(of: store.repliesLoaded) { _, _ in handleDemo() }
    }

    @ViewBuilder
    private var content: some View {
        if !store.repliesLoaded {
            ScrollView { Panel { SkeletonRows(count: 5) }.padding(16) }
        } else if let error = store.repliesError, store.replies.isEmpty {
            ErrorState(message: error) { Task { await store.loadReplies() } }.frame(maxHeight: .infinity)
        } else if store.replies.isEmpty {
            EmptyState(
                symbol: "text.bubble",
                title: "No saved replies yet",
                message: "Answers you send often, ready to insert with a /shortcut while you reply.",
                actionTitle: canWrite ? "Add a saved reply" : nil,
                action: canWrite ? { editing = AccountReplyEdit(reply: nil) } : nil
            )
            .frame(maxHeight: .infinity)
        } else {
            List {
                Section {
                    ForEach(shown) { reply in
                        Button { if canWrite { editing = AccountReplyEdit(reply: reply) } } label: {
                            AccountReplyRow(reply: reply, team: env.directory.team(reply.teamID)?.name)
                        }
                        .buttonStyle(RowPressStyle())
                        .listRowInsets(EdgeInsets())
                        .listRowBackground(Theme.surface)
                        .swipeActions(edge: .trailing) {
                            if canWrite {
                                Button(role: .destructive) { ask(reply) } label: { Label("Delete", systemImage: "trash") }
                            }
                        }
                    }
                } footer: {
                    if shown.isEmpty {
                        Text("No saved replies match \u{201C}\(query)\u{201D}.").font(ODFont.footnote).foregroundStyle(Theme.fg3)
                    } else {
                        Text("Type / and the shortcut in a reply to insert one. \(canWrite ? "Swipe left to delete." : "Viewers can use them but not change them.")")
                            .font(ODFont.footnote)
                            .foregroundStyle(Theme.fg3)
                    }
                }
            }
            .listStyle(.insetGrouped)
            .scrollContentBackground(.hidden)
            .contentMargins(.bottom, 100, for: .scrollContent)
            .motion(Motion.snappy, value: shown.map(\.id))
        }
    }

    private func ask(_ reply: AccountSavedReply) {
        confirm = AccountConfirm(title: "Delete \u{201C}\(reply.title)\u{201D}?", message: "It's removed for everyone in the workspace.", confirmTitle: "Delete") {
            do {
                try await store.deleteReply(reply)
                Haptics.success()
            } catch {
                env.toasts.error(error)
            }
        }
    }

    private func handleDemo() {
        if AccountDemo.take("new-reply", from: env.router) { editing = AccountReplyEdit(reply: nil) }
        guard store.repliesLoaded else { return }
        if AccountDemo.take("edit-reply", from: env.router), let first = store.replies.first { editing = AccountReplyEdit(reply: first) }
    }
}

struct AccountReplyEdit: Identifiable {
    let id = UUID()
    var reply: AccountSavedReply?
}

private struct AccountReplyRow: View {
    let reply: AccountSavedReply
    var team: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            HStack(spacing: 8) {
                Text(reply.title)
                    .font(ODFont.sans(15.5, weight: .medium))
                    .foregroundStyle(Theme.fg)
                    .lineLimit(1)
                Spacer(minLength: 4)
                if !reply.shortcut.isEmpty {
                    Text("/" + reply.shortcut)
                        .font(ODFont.mono(12, weight: .medium))
                        .foregroundStyle(Tone.teal.text)
                        .padding(.horizontal, 6)
                        .frame(height: 20)
                        .background(Tone.teal.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
                }
            }
            Text(reply.plainBody.split(whereSeparator: \.isNewline).map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }.joined(separator: " "))
                .font(ODFont.sans(13.5))
                .foregroundStyle(Theme.fg3)
                .lineLimit(2)
            if let team {
                TonePill(text: team, tone: .gray, height: 19) {
                    Image(systemName: "person.3").font(.system(size: 9, weight: .semibold))
                }
                .padding(.top, 2)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// A new saved reply, or changes to one.
struct AccountReplyEditor: View {
    let reply: AccountSavedReply?

    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var draft = AccountStore.ReplyDraft()
    @State private var saving = false
    @State private var error: String?

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                AccountSheetHeader(title: reply == nil ? "New saved reply" : "Edit saved reply") { dismiss() }
                ODTextField(label: "Title", text: $draft.title, prompt: "Refund on the way")
                VStack(alignment: .leading, spacing: 6) {
                    ODTextField(label: "Shortcut", text: $draft.shortcut, prompt: "/refund", keyboard: .asciiCapable)
                    if let problem = draft.shortcutProblem { FormError(text: problem) }
                }
                AccountTextEditor(label: "Reply", text: $draft.body, prompt: "Hi {{contact.first_name}}, ...", minHeight: 170)
                Text("Variables like {{contact.first_name}} are filled in when you insert the reply.")
                    .font(ODFont.footnote)
                    .foregroundStyle(Theme.fg3)
                teamPicker
                FormError(text: error)
                Button(action: save) { ButtonLabel(title: reply == nil ? "Add saved reply" : "Save changes", busy: saving) }
                    .buttonStyle(InkButtonStyle())
                    .disabled(!draft.isValid || saving)
            }
            .padding(22)
            .motion(Motion.snappy, value: error)
        }
        .scrollDismissesKeyboard(.interactively)
        .background(Theme.surface)
        .onAppear { if let reply { draft = AccountStore.ReplyDraft(reply) } }
    }

    private var teamPicker: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Team").font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg2)
            Menu {
                Button("Everyone") { draft.teamID = nil }
                ForEach(env.directory.teams) { team in
                    Button(team.name) { draft.teamID = team.id }
                }
            } label: {
                HStack {
                    Text(env.directory.team(draft.teamID)?.name ?? "Everyone")
                        .font(ODFont.sans(16))
                        .foregroundStyle(Theme.fg)
                    Spacer()
                    Image(systemName: "chevron.up.chevron.down").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.fg3)
                }
                .padding(.horizontal, 13)
                .frame(height: 46)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            }
            Text("A team's replies show first for that team's tickets.")
                .font(ODFont.footnote)
                .foregroundStyle(Theme.fg3)
        }
    }

    private func save() {
        guard draft.isValid else { return }
        saving = true
        error = nil
        Task {
            do {
                try await store.saveReply(draft, existing: reply)
                Haptics.success()
                env.toasts.show(reply == nil ? "Saved reply added" : "Saved reply updated", kind: .success)
                dismiss()
            } catch let failure as APIError where failure.status == 409 {
                Haptics.error()
                error = "Another saved reply uses /\(draft.cleanShortcut). Choose a different shortcut."
            } catch {
                Haptics.error()
                self.error = error.userMessage
            }
            saving = false
        }
    }
}
