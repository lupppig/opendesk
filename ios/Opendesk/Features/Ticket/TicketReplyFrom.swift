import SwiftUI

/// Who a ticket reply may go from (contracts/collab.md "Answering tickets"): the ticket's reply
/// address, or one of the member's own Collab addresses when Collab and the setting are on for
/// this address, on an email ticket with a customer address, for an agent or above. The server
/// checks the choice again when the reply goes out.
@MainActor
struct ReplySenders {
    let env: AppEnvironment
    let store: TicketStore
    let composer: TicketComposerStore

    private var settings: WorkspaceSettings? { env.session.workspace?.workspace.settings }

    var on: Bool {
        guard let ticket = store.ticket, env.session.canWrite, env.directory.collab.enabled,
              ticket.channel == .email, !(ticket.contact?.email ?? "").isEmpty
        else { return false }
        return store.replyAddress?.personalReplies?.enabled ?? (settings?.collabTicketReplies != false)
    }

    /// The addresses the member may answer from, under the reply address's rules.
    var all: [PersonalSender] { on ? store.personalSenders(under: store.replyAddress) : [] }

    /// Where answers go when nothing was chosen: the reply address's rule, else the workspace's.
    var defaultReplyTo: String {
        store.replyAddress?.personalReplies?.replyTo ?? settings?.collabTicketReplyTo ?? "address"
    }

    /// The address this reply goes from: the one chosen, else the one the member last answered
    /// the ticket from; nil is the ticket's reply address.
    var from: MessageFromAccount? {
        let senders = all
        guard !senders.isEmpty else { return nil }
        switch composer.from {
        case .team:
            return nil
        case let .personal(from):
            return senders.contains { $0.matches(from) } ? from : nil
        case .automatic:
            guard let last = store.lastPersonalFrom(me: env.session.user?.id), senders.contains(where: { $0.matches(last) }) else { return nil }
            return last
        }
    }

    var sender: PersonalSender? {
        guard let from else { return nil }
        return all.first { $0.matches(from) }
    }

    func choose(_ sender: PersonalSender?) {
        Haptics.selection()
        guard let sender else {
            composer.from = .team
            return
        }
        composer.from = .personal(MessageFromAccount(accountID: sender.account.id, email: sender.email, replyTo: from?.replyTo ?? ""))
    }

    /// "address", "mailbox" or an email address; the default is stored as "" so it follows the rule.
    func chooseReplyTo(_ value: String) {
        guard var from else { return }
        Haptics.selection()
        from.replyTo = value == defaultReplyTo ? "" : value
        composer.from = .personal(from)
    }

    /// The signature the reply carries: the address's own from a personal address, else the
    /// team's as the server fills it.
    var signatureHTML: String {
        if let sender { return sender.signatureHTML }
        return store.variables?.signatureHTML ?? ""
    }
}

/// The From line of an email reply: the ticket's address, or one of the member's own with a
/// menu to switch, then where the customer's answers go.
struct ReplyFromLine: View {
    let senders: ReplySenders
    let address: ReplyAddress?

    @State private var typing = false
    @State private var typed = ""

    var body: some View {
        let choices = senders.all
        let chosen = senders.sender
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 8) {
                Text("From").font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).frame(width: 50, alignment: .leading)
                if choices.isEmpty {
                    fromLabel(chosen)
                } else {
                    Menu {
                        if let address {
                            Section("Team addresses") {
                                Button { senders.choose(nil) } label: {
                                    Label(line(address), systemImage: chosen == nil ? "checkmark" : "tray")
                                }
                            }
                        }
                        Section("Your addresses") {
                            ForEach(choices) { sender in
                                Button { senders.choose(sender) } label: {
                                    Label(sender.account.isPrivate ? sender.line : "\(sender.line), shared", systemImage: chosen?.id == sender.id ? "checkmark" : "person.crop.circle")
                                }
                            }
                        }
                    } label: {
                        HStack(spacing: 5) {
                            fromLabel(chosen)
                            Image(systemName: "chevron.up.chevron.down").font(.system(size: 9, weight: .semibold)).foregroundStyle(Theme.fg4)
                        }
                        .contentShape(Rectangle())
                    }
                    .accessibilityLabel("Change the address this reply goes out from")
                }
                Spacer(minLength: 0)
            }
            .frame(minHeight: 22)
            if let chosen {
                replyTo(chosen)
                Text("Sent from your own address. The customer's answers still come back to this ticket, and the ticket keeps its reply address.")
                    .font(ODFont.sans(11.5))
                    .foregroundStyle(Theme.fg3)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .alert("Answers go to", isPresented: $typing) {
            TextField("Type an email address", text: $typed)
                .textInputAutocapitalization(.never)
                .keyboardType(.emailAddress)
                .autocorrectionDisabled()
            Button("Use") {
                let value = typed.trimmingCharacters(in: .whitespaces).lowercased()
                if Self.isEmail(value) { senders.chooseReplyTo(value) }
            }
            Button("Cancel", role: .cancel) {}
        }
    }

    @ViewBuilder private func fromLabel(_ chosen: PersonalSender?) -> some View {
        HStack(spacing: 6) {
            if let chosen {
                Circle().fill(chosen.account.tone.solid).frame(width: 7, height: 7)
                Text(chosen.line)
            } else if let address {
                Text(line(address))
            }
        }
        .font(ODFont.sans(13))
        .foregroundStyle(Theme.fg2)
        .lineLimit(1)
    }

    private func line(_ address: ReplyAddress) -> String {
        address.displayName.isEmpty ? address.email : "\(address.displayName) <\(address.email)>"
    }

    /// Where answers go: the address's own reply-to (else the address), the ticket's address, or one typed.
    private func replyTo(_ chosen: PersonalSender) -> some View {
        let mode = (senders.from?.replyTo).flatMap { $0.isEmpty ? nil : $0 } ?? senders.defaultReplyTo
        let team = address?.email
        let shown = mode == "address" ? chosen.ownReplyTo : mode == "mailbox" ? (team ?? chosen.ownReplyTo) : mode
        return HStack(spacing: 8) {
            Text("Answers").font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).frame(width: 50, alignment: .leading)
            Menu {
                Button { senders.chooseReplyTo("address") } label: {
                    Label("This address (\(chosen.ownReplyTo))", systemImage: mode == "address" ? "checkmark" : "person.crop.circle")
                }
                if let team {
                    Button { senders.chooseReplyTo("mailbox") } label: {
                        Label("The ticket's address (\(team))", systemImage: mode == "mailbox" ? "checkmark" : "tray")
                    }
                }
                if mode != "address", mode != "mailbox" {
                    Button {} label: { Label(mode, systemImage: "checkmark") }
                }
                Button {
                    typed = ""
                    typing = true
                } label: { Label("Type an address", systemImage: "keyboard") }
            } label: {
                HStack(spacing: 5) {
                    Image(systemName: "arrow.turn.down.left").font(.system(size: 10, weight: .semibold)).foregroundStyle(Theme.fg4)
                    Text(shown).font(ODFont.mono(12)).foregroundStyle(Theme.fg2).lineLimit(1)
                    Image(systemName: "chevron.up.chevron.down").font(.system(size: 9, weight: .semibold)).foregroundStyle(Theme.fg4)
                }
                .contentShape(Rectangle())
            }
            .accessibilityLabel("Answers go to \(shown). Change where answers go")
            Spacer(minLength: 0)
        }
        .frame(minHeight: 22)
    }

    static func isEmail(_ value: String) -> Bool {
        value.range(of: #"^[^\s@,;]+@[^\s@,;]+\.[^\s@,;]+$"#, options: .regularExpression) != nil
    }
}

/// The signature an email reply carries, under what is written: faded, a few lines until
/// opened, as the web's composer shows it.
struct ReplySignature: View {
    let html: String
    @State private var open = false

    var body: some View {
        let text = HTMLText.plain(from: html).trimmingCharacters(in: .whitespacesAndNewlines)
        if !text.isEmpty {
            let long = text.split(separator: "\n").count > 3 || text.count > 160
            VStack(alignment: .leading, spacing: 3) {
                Text("Signature, added when it's sent")
                    .font(ODFont.sans(11, weight: .medium))
                    .foregroundStyle(Theme.fg4)
                Text(text)
                    .font(ODFont.sans(13))
                    .foregroundStyle(Theme.fg3)
                    .lineLimit(open ? nil : 3)
                    .fixedSize(horizontal: false, vertical: true)
                if long {
                    Button(open ? "Show less" : "Show all") {
                        Haptics.tap()
                        open.toggle()
                    }
                    .font(ODFont.sans(12, weight: .medium))
                    .foregroundStyle(Theme.fg2)
                    .buttonStyle(.plain)
                }
            }
            .frame(maxWidth: .infinity, alignment: .leading)
            .padding(.horizontal, 12)
            .padding(.top, 2)
            .padding(.bottom, 4)
            .accessibilityElement(children: .combine)
        }
    }
}

/// The email a reply sends, before it goes. From a team address the server renders it as
/// sending does (`POST /v1/templates/preview`, kind reply): the brand's layout, the reply
/// filled, the signature and the quoted conversation, with what is wrong with its variables.
/// From one of the member's own addresses it is personal mail: the reply filled, then the
/// signature, no layout.
struct ReplyPreviewSheet: View {
    let store: TicketStore
    let ticket: Ticket
    let html: String
    let from: String
    let to: String
    let cc: [String]
    let bcc: [String]
    let files: [String]
    /// Set when it goes from one of the member's own addresses: that address's signature.
    let personalSignature: String?
    let canSend: Bool
    let onSend: () -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var preview: TemplatePreview?
    @State private var error: String?

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    Text("The email as \(to) gets it.")
                        .font(ODFont.sans(13))
                        .foregroundStyle(Theme.fg3)
                    header
                    if let problems = preview?.problems, !problems.isEmpty {
                        VStack(alignment: .leading, spacing: 4) {
                            ForEach(problems, id: \.self) { problem in
                                Label(problem, systemImage: "exclamationmark.triangle")
                                    .font(ODFont.sans(12.5))
                                    .foregroundStyle(Tone.amber.text)
                            }
                        }
                        .padding(10)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .background(Tone.amber.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                    }
                    if let error {
                        Text(error).font(ODFont.sans(13)).foregroundStyle(Tone.red.text)
                    } else if let preview {
                        EmailDocumentFrame(html: preview.html)
                    } else {
                        ProgressView().frame(maxWidth: .infinity, minHeight: 160)
                    }
                }
                .padding(16)
            }
            .background(Theme.canvas)
            .navigationTitle("Preview")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Keep editing") { dismiss() }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Send") {
                        dismiss()
                        onSend()
                    }
                    .disabled(!canSend)
                }
            }
        }
        .task { await load() }
    }

    private var header: some View {
        EmailHeaderRows(
            from: from,
            to: to,
            cc: cc,
            bcc: bcc,
            subject: preview?.subject.isEmpty == false ? preview!.subject : "Re: \(ticket.displaySubject)",
            files: files
        )
    }

    private func load() async {
        if let personalSignature {
            // Filled as the server fills it when the reply is created.
            let filled = await store.render(html)
            let signature = personalSignature.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "" : "<p></p>" + personalSignature
            preview = TemplatePreview(subject: "Re: \(ticket.subject)", html: filled + signature)
            return
        }
        do {
            preview = try await store.previewReply(html: html, mailboxID: ticket.mailboxID)
        } catch {
            if !error.isCancellation { self.error = error.userMessage }
        }
    }
}
