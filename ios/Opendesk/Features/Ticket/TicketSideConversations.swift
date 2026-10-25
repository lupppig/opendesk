import SwiftUI

/// One side conversation in a list: subject, who it's with, and whether it's still open.
struct SideThreadRow: View {
    let thread: SideThread
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                Image(systemName: thread.status == .open ? "bubble.left.and.bubble.right" : "checkmark.bubble")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(thread.status == .open ? Tone.blue.solid : Theme.fg3)
                    .frame(width: 26, height: 26)
                    .background(thread.status == .open ? Tone.blue.soft : Theme.active, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
                VStack(alignment: .leading, spacing: 2) {
                    Text(thread.subject.isEmpty ? "No subject" : thread.subject)
                        .font(ODFont.sans(14.5, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    Text("\(thread.to.joined(separator: ", ")) · \(Format.plural(thread.messageCount, "message")) · \(Format.relShort(thread.lastMessageAt))")
                        .font(ODFont.sans(12))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(1)
                }
                Spacer(minLength: 6)
                if thread.status == .closed {
                    TonePill(text: "Closed", tone: .gray) { EmptyView() }
                }
                Chevron()
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
    }
}

/// The ticket's side conversations.
struct SideConversationsSheet: View {
    let c: TicketController
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    Text("Email people outside the ticket, like a supplier or another team. The customer doesn't see these.")
                        .font(ODFont.sans(13))
                        .foregroundStyle(Theme.fg3)
                        .padding(.horizontal, 2)
                    if c.store.threads.isEmpty {
                        EmptyState(symbol: "bubble.left.and.bubble.right", title: "No side conversations", message: "Start one to ask someone outside the ticket.", actionTitle: c.canWrite ? "New side conversation" : nil) {
                            c.replace(with: .newSide)
                        }
                        .padding(.top, 10)
                    } else {
                        Panel {
                            ForEach(Array(c.store.threads.enumerated()), id: \.element.id) { index, thread in
                                if index > 0 { Hairline(inset: 50) }
                                SideThreadRow(thread: thread) { c.replace(with: .thread(thread.id)) }
                            }
                        }
                    }
                }
                .padding(16)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Side conversations")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } }
                if c.canWrite {
                    ToolbarItem(placement: .primaryAction) {
                        Button("New", systemImage: "square.and.pencil") { c.replace(with: .newSide) }
                    }
                }
            }
        }
    }
}

/// Start a side conversation: who it goes to, a subject, the message, and whether the
/// ticket's history goes along.
struct NewSideConversationSheet: View {
    let c: TicketController
    @Environment(\.dismiss) private var dismiss
    @State private var to = ""
    @State private var cc = ""
    @State private var subject = ""
    @State private var text = ""
    @State private var includeHistory = false
    @State private var sending = false
    @State private var error: String?
    @FocusState private var focus: Field?

    private enum Field { case to, cc, subject, body }

    private var recipients: [String] { TicketComposerStore.addresses(to) }
    private var canSend: Bool {
        !sending && !recipients.isEmpty && recipients.allSatisfy { $0.contains("@") } && !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    Panel {
                        field("To", text: $to, prompt: "name@company.com", field: .to, keyboard: .emailAddress)
                        Hairline(inset: 14)
                        field("Cc", text: $cc, prompt: "Optional", field: .cc, keyboard: .emailAddress)
                        Hairline(inset: 14)
                        field("Subject", text: $subject, prompt: c.store.ticket?.displaySubject ?? "Subject", field: .subject, keyboard: .default)
                    }
                    TextField("Write your message", text: $text, axis: .vertical)
                        .font(ODFont.sans(15.5))
                        .lineLimit(6 ... 16)
                        .focused($focus, equals: .body)
                        .padding(14)
                        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                    Panel {
                        Toggle(isOn: $includeHistory) {
                            VStack(alignment: .leading, spacing: 2) {
                                Text("Include the conversation").font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                                Text("Quotes the customer's messages below yours.").font(ODFont.footnote).foregroundStyle(Theme.fg3)
                            }
                        }
                        .tint(Theme.fg)
                        .padding(.horizontal, 14)
                        .padding(.vertical, 10)
                    }
                    if let error { FormError(text: error) }
                }
                .padding(16)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("New side conversation")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button(action: send) {
                        if sending { ProgressView() } else { Text("Send").fontWeight(.semibold) }
                    }
                    .disabled(!canSend)
                }
            }
            .onAppear { focus = .to }
        }
    }

    private func field(_ title: String, text: Binding<String>, prompt: String, field: Field, keyboard: UIKeyboardType) -> some View {
        HStack(spacing: 10) {
            Text(title).font(ODFont.sans(14.5)).foregroundStyle(Theme.fg3).frame(width: 58, alignment: .leading)
            TextField(text: text) { Text(prompt).foregroundStyle(Theme.fg4) }
                .font(ODFont.sans(15))
                .keyboardType(keyboard)
                .textInputAutocapitalization(keyboard == .emailAddress ? .never : .sentences)
                .autocorrectionDisabled(keyboard == .emailAddress)
                .focused($focus, equals: field)
        }
        .padding(.horizontal, 14)
        .frame(height: 46)
    }

    private func send() {
        sending = true
        error = nil
        let body = HTMLText.html(fromPlain: text.trimmingCharacters(in: .whitespacesAndNewlines))
        let title = subject.trimmingCharacters(in: .whitespaces).isEmpty ? (c.store.ticket?.subject ?? "") : subject
        Task {
            defer { sending = false }
            do {
                let thread = try await c.store.startThread(to: recipients, cc: TicketComposerStore.addresses(cc), subject: title, html: body, includeHistory: includeHistory, attachmentIDs: [])
                Haptics.success()
                c.replace(with: .thread(thread.id))
            } catch {
                self.error = error.userMessage
                Haptics.error()
            }
        }
    }
}

/// One side conversation: its messages, a reply field, and close or reopen.
struct SideThreadSheet: View {
    let c: TicketController
    let threadID: String
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var text = ""
    @State private var sending = false
    @FocusState private var focused: Bool

    private var thread: SideThread? { c.store.threads.first { $0.id == threadID } }
    private var messages: [Message] { c.store.threadMessages(threadID) }

    var body: some View {
        NavigationStack {
            ScrollView {
                if let thread {
                    VStack(alignment: .leading, spacing: 12) {
                        header(thread)
                        if messages.isEmpty {
                            Text("Messages in this conversation show up here.")
                                .font(ODFont.sans(13)).foregroundStyle(Theme.fg3).padding(.top, 8)
                        }
                        ForEach(messages) { message in
                            TicketMessageCell(
                                message: Self.asEmail(message),
                                permissions: MessagePermissions(canWrite: false, isAdmin: false, me: c.me?.id, channel: .email),
                                baseURL: env.session.instance.apiURL,
                                remoteImages: Binding(
                                    get: { c.remoteImages.contains(message.id) },
                                    set: { if $0 { c.remoteImages.insert(message.id) } else { c.remoteImages.remove(message.id) } }
                                ),
                                onAction: { c.handle($0, on: message) }
                            )
                        }
                    }
                    .padding(16)
                }
            }
            .defaultScrollAnchor(.bottom, for: .initialOffset)
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .safeAreaBar(edge: .bottom, spacing: 0) {
                if c.canWrite, thread?.status == .open { replyBar }
            }
            .navigationTitle("Side conversation")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Close") { dismiss() } }
                if c.canWrite, let thread {
                    ToolbarItem(placement: .primaryAction) {
                        Menu {
                            if thread.status == .open {
                                Button("Mark as done", systemImage: "checkmark.circle") { set(thread, .closed) }
                            } else {
                                Button("Reopen", systemImage: "arrow.uturn.left") { set(thread, .open) }
                            }
                            Button("All side conversations", systemImage: "list.bullet") { c.replace(with: .sides) }
                        } label: {
                            Image(systemName: "ellipsis")
                        }
                    }
                }
            }
        }
    }

    /// Side conversation messages are stored as team-only, but read as the emails they are.
    private static func asEmail(_ message: Message) -> Message {
        var shown = message
        shown.kind = .reply
        return shown
    }

    private func header(_ thread: SideThread) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                TonePill(text: thread.status == .open ? "Open" : "Done", tone: thread.status == .open ? .blue : .gray) { EmptyView() }
                Text("Only your team sees this").font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3)
            }
            Text(thread.subject.isEmpty ? "No subject" : thread.subject)
                .font(ODFont.sans(18, weight: .semibold))
                .headingTracking(18)
                .foregroundStyle(Theme.fg)
            Text("With " + ([thread.to.joined(separator: ", ")] + (thread.cc.isEmpty ? [] : ["cc " + thread.cc.joined(separator: ", ")])).joined(separator: ", "))
                .font(ODFont.sans(13))
                .foregroundStyle(Theme.fg3)
        }
        .padding(.bottom, 4)
    }

    private var replyBar: some View {
        HStack(alignment: .bottom, spacing: 8) {
            TextField("Reply in this conversation", text: $text, axis: .vertical)
                .font(ODFont.sans(15.5))
                .lineLimit(1 ... 6)
                .focused($focused)
                .padding(.vertical, 10)
                .padding(.leading, 14)
            Button(action: send) {
                Group {
                    if sending { ProgressView().tint(Theme.inverseFg) } else { Image(systemName: "arrow.up").font(.system(size: 14, weight: .bold)) }
                }
                .foregroundStyle(Theme.inverseFg)
                .frame(width: 34, height: 34)
                .background(Theme.inverse, in: Circle())
            }
            .buttonStyle(TapScaleStyle())
            .disabled(sending || text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            .opacity(text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? 0.4 : 1)
            .padding(5)
        }
        .glassEffect(.regular.tint(Theme.glassTint), in: RoundedRectangle(cornerRadius: 22, style: .continuous))
        .padding(.horizontal, 10)
        .padding(.bottom, 6)
    }

    private func send() {
        guard let me = c.me else { return }
        let html = HTMLText.html(fromPlain: text.trimmingCharacters(in: .whitespacesAndNewlines))
        sending = true
        Task {
            defer { sending = false }
            do {
                try await c.store.replyInThread(threadID, html: html, author: me)
                text = ""
                Haptics.success()
            } catch {
                env.toasts.error(error)
            }
        }
    }

    private func set(_ thread: SideThread, _ status: SideThread.Status) {
        Task {
            do {
                try await c.store.setThread(thread, status: status)
                Haptics.success()
            } catch {
                env.toasts.error(error)
            }
        }
    }
}
