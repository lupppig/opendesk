import SwiftUI

// MARK: - Comments under the thread

/// The team's comments on a conversation, under its messages. They never reach the sender.
struct MailCommentsSection: View {
    let comments: [MailComment]
    let fresh: Set<String>
    let me: String?
    var onEdit: (MailComment) -> Void
    var onDelete: (MailComment) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 6) {
                Image(systemName: "text.bubble.fill").font(.system(size: 11.5)).foregroundStyle(Tone.amber.solid)
                Text("Team comments").font(ODFont.sans(13, weight: .semibold)).foregroundStyle(Theme.fg2)
                Text("\(comments.count)").font(ODFont.sans(12.5, weight: .medium)).foregroundStyle(Theme.fg4).monospacedDigit()
            }
            .padding(.horizontal, 6)
            VStack(alignment: .leading, spacing: 0) {
                ForEach(Array(comments.enumerated()), id: \.element.id) { index, comment in
                    if index > 0 { Hairline(inset: 48) }
                    MailCommentRow(comment: comment, fresh: fresh.contains(comment.id), mine: comment.author?.id == me, onEdit: { onEdit(comment) }, onDelete: { onDelete(comment) })
                        .id("comment-" + comment.id)
                }
            }
            .background(Theme.note, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.noteLine.opacity(0.6), style: StrokeStyle(lineWidth: 1, dash: [4, 3])))
            Text("Only teammates who can see this conversation read comments. They never reach the sender.")
                .font(ODFont.sans(11.5))
                .foregroundStyle(Theme.fg4)
                .padding(.horizontal, 6)
        }
    }
}

struct MailCommentRow: View {
    let comment: MailComment
    let fresh: Bool
    let mine: Bool
    var onEdit: () -> Void
    var onDelete: () -> Void
    @State private var glow = false

    var body: some View {
        HStack(alignment: .top, spacing: 10) {
            Avatar(name: comment.author?.name ?? "A former teammate", url: comment.author?.avatarURL, seed: comment.author?.id ?? comment.id, size: 26)
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 6) {
                    Text(comment.author?.name ?? "A former teammate").font(ODFont.sans(13.5, weight: .semibold)).foregroundStyle(Theme.fg)
                    Text(Format.relShort(comment.createdAt)).font(ODFont.sans(12)).foregroundStyle(Theme.fg3)
                    if comment.edited { Text("edited").font(ODFont.sans(11.5)).foregroundStyle(Theme.fg4) }
                    Spacer(minLength: 0)
                }
                Text(MailCommentText.attributed(comment.bodyHTML))
                    .font(ODFont.sans(14))
                    .foregroundStyle(Theme.fg)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            }
            if mine {
                Menu {
                    Button(action: onEdit) { Label("Edit", systemImage: "pencil") }
                    Button(role: .destructive, action: onDelete) { Label("Delete", systemImage: "trash") }
                } label: {
                    Image(systemName: "ellipsis").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg4)
                        .frame(width: 26, height: 22).contentShape(Rectangle())
                }
                .accessibilityLabel("Comment actions")
            }
        }
        .padding(12)
        .background(Tone.amber.soft.opacity(glow ? 1 : 0))
        .onAppear {
            guard fresh else { return }
            glow = true
            withAnimation(.easeOut(duration: 1.4).delay(0.3)) { glow = false }
        }
    }
}

extension MailCommentText {
    /// A stored comment as text, its mentions in the ink with a soft wash.
    static func attributed(_ html: String) -> AttributedString {
        let mentions = mentions(in: html)
        let plain = HTMLText.plain(from: html)
        var out = AttributedString(plain)
        for mention in Set(mentions) {
            var search = out.startIndex..<out.endIndex
            while let range = out[search].range(of: "@" + mention.name) {
                out[range].font = ODFont.sans(14, weight: .semibold)
                out[range].foregroundColor = Tone.blue.text
                out[range].backgroundColor = Tone.blue.soft
                search = range.upperBound..<out.endIndex
            }
        }
        return out
    }
}

// MARK: - The pinned bar

/// Pinned under the thread: one line until it is used, with the comment count and the reply
/// buttons. Typing `@` lists teammates; mentioning someone who can't see the conversation
/// offers to share it with them. The reply buttons step aside while a comment is written or
/// a reply is open. An unsent comment waits for its conversation.
struct MailCommentBar: View {
    let store: MailThreadStore
    let members: [Member]
    let teams: [Team]
    let me: String?
    let replyOpen: Bool
    @Binding var editing: MailComment?
    var onComments: () -> Void
    var onReply: (MailComposeMode) -> Void

    @Environment(AppEnvironment.self) private var env
    @State private var text = ""
    @State private var picked: [MailMention] = []
    @State private var sending = false
    @State private var confirmShare: [Member] = []
    @FocusState private var focused: Bool

    #if DEBUG
    /// Text the QA harness puts in the bar (`/demo/mail-mention`).
    @MainActor static var demoText: String? {
        get { demoBox.value }
        set { demoBox.value = newValue }
    }
    @MainActor private static let demoBox = MailDemoTextBox()
    #endif

    private var active: Bool { focused || !text.isEmpty || editing != nil }
    private var count: Int { store.detail?.comments.count ?? 0 }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            if !suggestions.isEmpty {
                mentionList.transition(.move(edge: .bottom).combined(with: .opacity))
            }
            if editing != nil {
                HStack(spacing: 6) {
                    Image(systemName: "pencil").font(.system(size: 11, weight: .bold))
                    Text("Editing your comment").font(ODFont.sans(12.5, weight: .medium))
                    Spacer()
                    Button("Cancel") { cancelEdit() }.font(ODFont.sans(12.5, weight: .semibold)).buttonStyle(.plain)
                }
                .foregroundStyle(Theme.fg3)
                .padding(.horizontal, 6)
            }
            GlassEffectContainer(spacing: 8) {
                HStack(alignment: .bottom, spacing: 8) {
                    field
                    if !active, !replyOpen, store.canAct {
                        replyButtons.transition(.move(edge: .trailing).combined(with: .opacity))
                    }
                }
            }
        }
        .motion(Motion.snappy, value: active)
        .motion(Motion.snappy, value: replyOpen)
        .motion(Motion.snappy, value: suggestions.map(\.id))
        .onAppear { text = store.unsentComment }
        .onChange(of: text) { _, value in
            if editing == nil { store.unsentComment = value }
            picked.removeAll { !value.contains("@" + $0.name) }
        }
        .onChange(of: editing) { _, comment in
            guard let comment else { return }
            picked = MailCommentText.mentions(in: comment.bodyHTML)
            text = HTMLText.plain(from: comment.bodyHTML)
            focused = true
        }
        #if DEBUG
        .onChange(of: Self.demoBox.value) { _, value in
            guard let value else { return }
            text = value
            focused = true
            Self.demoText = nil
        }
        #endif
        .confirmationDialog(shareTitle, isPresented: Binding(get: { !confirmShare.isEmpty }, set: { if !$0 { confirmShare = [] } }), titleVisibility: .visible) {
            Button("Share and comment") {
                confirmShare = []
                Task { await post() }
            }
            Button("Cancel", role: .cancel) { confirmShare = [] }
        } message: {
            Text("\(names(confirmShare)) can't see this conversation yet. Mentioning them shares just this conversation with them.")
        }
    }

    private var field: some View {
        HStack(alignment: .bottom, spacing: 6) {
            Button(action: onComments) {
                HStack(spacing: 4) {
                    Image(systemName: count > 0 ? "text.bubble.fill" : "text.bubble")
                        .font(.system(size: 14, weight: .medium))
                    if count > 0 {
                        Text("\(count)").font(ODFont.sans(13, weight: .semibold)).monospacedDigit()
                    }
                }
                .foregroundStyle(count > 0 ? Tone.amber.text : Theme.fg3)
                .frame(minWidth: 30, minHeight: 36)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .disabled(count == 0)
            .accessibilityLabel(count == 0 ? "No comments yet" : Format.plural(count, "comment"))
            TextField(text: $text, axis: .vertical) {
                Text("Comment, @ to mention a teammate").foregroundStyle(Theme.fg4)
            }
            .font(ODFont.sans(15))
            .foregroundStyle(Theme.fg)
            .lineLimit(1...(active ? 6 : 1))
            .focused($focused)
            .padding(.vertical, 9)
            .disabled(!store.canAct)
            if active {
                Button { submit() } label: {
                    Group {
                        if sending {
                            ProgressView().controlSize(.small).tint(Theme.inverseFg)
                        } else {
                            Image(systemName: "arrow.up").font(.system(size: 14, weight: .bold))
                        }
                    }
                    .foregroundStyle(Theme.inverseFg)
                    .frame(width: 32, height: 32)
                    .background(canSend ? Theme.inverse : Theme.fg4, in: Circle())
                }
                .buttonStyle(TapScaleStyle(scale: 0.9))
                .disabled(!canSend)
                .padding(.bottom, 3)
                .accessibilityLabel(editing == nil ? "Comment" : "Save")
            }
        }
        .padding(.leading, 10)
        .padding(.trailing, active ? 4 : 14)
        .frame(minHeight: 44)
        .glassEffect(.regular.interactive(), in: RoundedRectangle(cornerRadius: 22, style: .continuous))
    }

    private var replyButtons: some View {
        HStack(spacing: 0) {
            replyButton(.reply, "arrowshape.turn.up.left", "Reply")
            if replyAllOffered { replyButton(.replyAll, "arrowshape.turn.up.left.2", "Reply all") }
            replyButton(.forward, "arrowshape.turn.up.right", "Forward")
        }
        .padding(.horizontal, 3)
        .frame(height: 44)
        .glassEffect(.regular.interactive(), in: Capsule())
    }

    private func replyButton(_ mode: MailComposeMode, _ symbol: String, _ label: String) -> some View {
        Button { onReply(mode) } label: {
            Image(systemName: symbol)
                .font(.system(size: 15, weight: .medium))
                .foregroundStyle(Theme.fg)
                .frame(width: 40, height: 40)
                .contentShape(Rectangle())
        }
        .buttonStyle(TapScaleStyle(scale: 0.88))
        .accessibilityLabel(label)
    }

    /// Reply all only when the last message reaches more than one other person.
    private var replyAllOffered: Bool {
        guard let last = store.messages.last else { return false }
        let own = MailStore.shared.ownEmails
        let people = Set(([last.from] + last.to + last.cc).map { $0.email.lowercased() }).subtracting(own)
        return people.count > 1
    }

    private var canSend: Bool {
        !sending && !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    // MARK: Mentions

    /// The word being typed after `@`, if any.
    private var mentionQuery: String? {
        guard focused, let at = text.lastIndex(of: "@") else { return nil }
        if at > text.startIndex, !text[text.index(before: at)].isWhitespace { return nil }
        let typed = text[text.index(after: at)...]
        guard !typed.contains("\n"), typed.count <= 24, typed.split(separator: " ", omittingEmptySubsequences: false).count <= 2 else { return nil }
        if typed.hasSuffix(" ") || picked.contains(where: { $0.name == typed }) { return nil }
        return String(typed)
    }

    private var suggestions: [Member] {
        guard let query = mentionQuery else { return [] }
        let needle = query.lowercased()
        return members
            .filter { $0.id != me }
            .filter { needle.isEmpty || $0.user.name.lowercased().contains(needle) || (($0.user.email ?? "").lowercased().hasPrefix(needle)) }
            .sorted { $0.user.name < $1.user.name }
            .prefix(5)
            .map { $0 }
    }

    private var mentionList: some View {
        VStack(spacing: 0) {
            ForEach(Array(suggestions.enumerated()), id: \.element.id) { index, member in
                if index > 0 { Hairline(inset: 46) }
                Button { pick(member) } label: {
                    HStack(spacing: 10) {
                        Avatar(name: member.user.name, url: member.user.avatarURL, seed: member.id, size: 26)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(member.user.name).font(ODFont.sans(14, weight: .medium)).foregroundStyle(Theme.fg)
                            if store.canSee(member, teams: teams) {
                                Text(member.user.email ?? "").font(ODFont.sans(12)).foregroundStyle(Theme.fg3)
                            } else {
                                Label("Can't see it yet, mentioning shares it", systemImage: "person.badge.plus")
                                    .font(ODFont.sans(12, weight: .medium))
                                    .foregroundStyle(Tone.teal.text)
                                    .labelStyle(MailInlineLabelStyle())
                            }
                        }
                        Spacer()
                    }
                    .padding(.horizontal, 12)
                    .frame(height: 50)
                    .contentShape(Rectangle())
                }
                .buttonStyle(RowPressStyle())
            }
        }
        .background(Theme.raised, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        .shadow(color: .black.opacity(0.08), radius: 16, y: 6)
    }

    private func pick(_ member: Member) {
        Haptics.selection()
        guard let at = text.lastIndex(of: "@") else { return }
        text = String(text[..<at]) + "@" + member.user.name + " "
        if !picked.contains(where: { $0.id == member.id }) { picked.append(MailMention(id: member.id, name: member.user.name)) }
    }

    private var shareTitle: String {
        confirmShare.count == 1 ? "Share with \(confirmShare[0].user.name)?" : "Share with \(confirmShare.count) teammates?"
    }

    private func names(_ list: [Member]) -> String {
        let names = list.map(\.user.name)
        guard names.count > 1 else { return names.first ?? "" }
        return names.dropLast().joined(separator: ", ") + " and " + names[names.count - 1]
    }

    // MARK: Sending

    private func submit() {
        guard canSend else { return }
        if editing == nil {
            let ids = MailCommentText.mentionIDs(in: text, mentions: picked)
            let blind = ids.compactMap { id in members.first { $0.id == id } }.filter { !store.canSee($0, teams: teams) }
            if !blind.isEmpty {
                confirmShare = blind
                return
            }
        }
        Task { await post() }
    }

    private func post() async {
        sending = true
        defer { sending = false }
        let body = text.trimmingCharacters(in: .whitespacesAndNewlines)
        do {
            if let editing {
                try await store.editComment(editing, text: body, mentions: picked)
                cancelEdit()
            } else {
                try await store.comment(text: body, mentions: picked)
                text = ""
                picked = []
                focused = false
            }
            Haptics.success()
        } catch {
            env.toasts.error(error)
        }
    }

    private func cancelEdit() {
        editing = nil
        text = store.unsentComment
        picked = []
        focused = false
    }
}

/// An icon and words sitting close together, for a line under a name.
struct MailInlineLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 4) {
            configuration.icon.font(.system(size: 10, weight: .bold))
            configuration.title
        }
    }
}

#if DEBUG
@MainActor
@Observable
final class MailDemoTextBox {
    var value: String?
}
#endif
