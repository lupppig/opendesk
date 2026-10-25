import SwiftUI

/// What a suggestion above the composer inserts.
enum ComposerPick {
    case savedReply(SavedReply)
    case member(Member)
    case variable(String)
}

/// Saved replies for `/`, teammates for `@`, and the ticket's values for `{{`, as the
/// token at the end of the text is typed.
struct ComposerSuggestionList: View {
    let token: ComposerToken
    let savedReplies: [SavedReply]
    let members: [Member]
    let me: String?
    let variables: TicketVariables?
    let pick: (ComposerPick) -> Void

    private struct Row: Identifiable {
        var id: String
        var title: String
        var detail: String?
        var leading: Leading
        var pick: ComposerPick
        enum Leading { case symbol(String), member(Member) }
    }

    private var rows: [Row] {
        switch token {
        case let .savedReply(query):
            let q = query.lowercased()
            return savedReplies.filter { q.isEmpty || ($0.shortcut ?? "").lowercased().hasPrefix(q) || $0.title.lowercased().contains(q) }
                .prefix(5)
                .map { Row(id: $0.id, title: $0.title, detail: $0.shortcut.map { "/" + $0 }, leading: .symbol("text.bubble"), pick: .savedReply($0)) }
        case let .mention(query):
            let q = query.lowercased()
            return members
                .filter { q.isEmpty || $0.user.name.lowercased().split(separator: " ").contains { $0.hasPrefix(q) } || $0.user.name.lowercased().hasPrefix(q) }
                .sorted { ($0.id == me ? 1 : 0, $0.user.name) < ($1.id == me ? 1 : 0, $1.user.name) }
                .prefix(5)
                .map { Row(id: $0.id, title: $0.user.name, detail: $0.user.email, leading: .member($0), pick: .member($0)) }
        case let .variable(query):
            let keys = (variables?.values.keys.map { $0 } ?? Self.fallbackKeys).sorted()
            return keys.filter { query.isEmpty || $0.hasPrefix(query) }
                .prefix(6)
                .map { key in Row(id: key, title: "{{\(key)}}", detail: variables?.values[key].flatMap { $0.isEmpty ? nil : $0 }, leading: .symbol("curlybraces"), pick: .variable(key)) }
        }
    }

    static let fallbackKeys = ["contact.first_name", "contact.name", "contact.email", "ticket.number", "ticket.subject", "agent.first_name", "agent.name", "workspace.name"]

    var body: some View {
        let list = rows
        if !list.isEmpty {
            VStack(spacing: 0) {
                ForEach(Array(list.enumerated()), id: \.element.id) { index, row in
                    if index > 0 { Hairline(inset: 46) }
                    Button { pick(row.pick) } label: {
                        HStack(spacing: 10) {
                            switch row.leading {
                            case let .symbol(symbol):
                                Image(systemName: symbol).font(.system(size: 12.5, weight: .medium)).foregroundStyle(Theme.fg3)
                                    .frame(width: 26, height: 26).background(Theme.active, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
                            case let .member(member):
                                Avatar(name: member.user.name, url: member.user.avatarURL, seed: member.id, size: 26)
                            }
                            Text(row.title).font(token.isVariable ? ODFont.mono(13) : ODFont.sans(14, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                            Spacer(minLength: 6)
                            if let detail = row.detail {
                                Text(detail).font(ODFont.sans(12)).foregroundStyle(Theme.fg3).lineLimit(1)
                            }
                        }
                        .padding(.horizontal, 10)
                        .frame(height: 42)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(RowPressStyle())
                }
            }
            .padding(.vertical, 4)
            .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 16, style: .continuous))
        }
    }
}

private extension ComposerToken {
    var isVariable: Bool {
        if case .variable = self { return true }
        return false
    }
}

/// All saved replies, searchable; picking one fills it in for this ticket.
struct SavedReplyPicker: View {
    let replies: [SavedReply]
    let onPick: (SavedReply) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""

    private var filtered: [SavedReply] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        guard !q.isEmpty else { return replies }
        return replies.filter { $0.title.lowercased().contains(q) || ($0.shortcut ?? "").lowercased().contains(q) || HTMLText.plain(from: $0.bodyHTML).lowercased().contains(q) }
    }

    var body: some View {
        PickerSheet(title: "Saved replies", query: $query) {
            if filtered.isEmpty {
                EmptyState(symbol: "text.bubble", title: replies.isEmpty ? "No saved replies yet" : "Nothing matches", message: replies.isEmpty ? "Admins add saved replies in Settings on the web." : "Try another word or shortcut.")
                    .padding(.top, 30)
            } else {
                Panel {
                    ForEach(Array(filtered.enumerated()), id: \.element.id) { index, reply in
                        if index > 0 { Hairline(inset: 14) }
                        Button {
                            Haptics.selection()
                            onPick(reply)
                            dismiss()
                        } label: {
                            VStack(alignment: .leading, spacing: 4) {
                                HStack(spacing: 8) {
                                    Text(reply.title).font(ODFont.sans(15, weight: .medium)).foregroundStyle(Theme.fg)
                                    Spacer()
                                    if let shortcut = reply.shortcut, !shortcut.isEmpty {
                                        Text("/" + shortcut).font(ODFont.mono(11.5)).foregroundStyle(Theme.fg3)
                                            .padding(.horizontal, 6).frame(height: 20)
                                            .background(Theme.active, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
                                    }
                                }
                                Text(HTMLText.plain(from: reply.bodyHTML))
                                    .font(ODFont.sans(13))
                                    .foregroundStyle(Theme.fg3)
                                    .lineLimit(2)
                                    .multilineTextAlignment(.leading)
                            }
                            .padding(.horizontal, 14)
                            .padding(.vertical, 12)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(RowPressStyle())
                    }
                }
            }
        }
    }
}

/// Tell Ollie what the reply should say.
struct GuidedDraftSheet: View {
    let onDraft: (String) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var instructions = ""
    @FocusState private var focused: Bool

    private let examples = ["Apologize for the wait and confirm the refund", "Ask for a screenshot and their browser", "Explain the steps in a short list"]

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    HStack(spacing: 10) {
                        OllieMark(size: 26)
                        Text("Say what the reply should cover. Ollie writes it from the conversation.")
                            .font(ODFont.sans(14))
                            .foregroundStyle(Theme.fg2)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    TextField("For example: confirm the fix and thank them for their patience", text: $instructions, axis: .vertical)
                        .font(ODFont.sans(15.5))
                        .lineLimit(3 ... 8)
                        .focused($focused)
                        .padding(12)
                        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Tone.violet.line, lineWidth: 1))
                    FlowLayout(spacing: 7) {
                        ForEach(examples, id: \.self) { example in
                            Button(example) { instructions = example }.buttonStyle(ChipButtonStyle())
                        }
                    }
                }
                .padding(16)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Draft with Ollie")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Draft") {
                        onDraft(instructions.trimmingCharacters(in: .whitespacesAndNewlines))
                        dismiss()
                    }
                    .fontWeight(.semibold)
                    .disabled(instructions.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
            .onAppear { focused = true }
        }
    }
}

/// Edit one of your own internal notes.
struct EditNoteSheet: View {
    let message: Message
    let onSave: (String) async -> Bool
    @Environment(\.dismiss) private var dismiss
    @State private var text = ""
    @State private var saving = false
    @FocusState private var focused: Bool

    var body: some View {
        NavigationStack {
            ScrollView {
                TextField("Note", text: $text, axis: .vertical)
                    .font(ODFont.sans(15.5))
                    .lineLimit(4 ... 20)
                    .focused($focused)
                    .padding(14)
                    .background(Theme.note, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.noteLine, style: StrokeStyle(lineWidth: 1, dash: [4, 3])))
                    .padding(16)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Edit note")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        saving = true
                        Task {
                            let ok = await onSave(text.trimmingCharacters(in: .whitespacesAndNewlines))
                            saving = false
                            if ok { dismiss() }
                        }
                    } label: {
                        if saving { ProgressView() } else { Text("Save").fontWeight(.semibold) }
                    }
                    .disabled(saving || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
            .onAppear {
                text = HTMLText.plain(from: message.bodyHTML.isEmpty ? HTMLText.html(fromPlain: message.bodyText) : message.bodyHTML)
                focused = true
            }
        }
    }
}
