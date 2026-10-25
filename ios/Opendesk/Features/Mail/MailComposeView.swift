import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

// MARK: - Full composer

/// New mail, a forward, or a reply opened full size: From, To/Cc/Bcc with suggestions,
/// subject, the text, the signature, files, and the original when forwarding. Drafts
/// save at the provider while you write; closing keeps the draft.
struct MailComposeView: View {
    @State private var store: MailComposeStore
    let mail: MailStore
    var onDone: (Bool) -> Void = { _ in }

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var showCopies = false
    @State private var confirmClose = false
    @State private var importing = false
    @State private var photo: PhotosPickerItem?
    @State private var error: String?
    @FocusState private var focus: Field?

    enum Field: Hashable { case to, cc, bcc, subject, body }

    init(store: MailComposeStore, mail: MailStore, onDone: @escaping (Bool) -> Void = { _ in }) {
        _store = State(initialValue: store)
        self.mail = mail
        self.onDone = onDone
        _showCopies = State(initialValue: !store.cc.isEmpty || !store.bcc.isEmpty)
    }

    init(request: MailComposeRequest, mail: MailStore, api: APIClient) {
        let senders = request.senders ?? mail.senders
        let store: MailComposeStore
        if let draft = request.draft {
            store = MailComposeStore(api: api, draft: draft, threadID: request.threadID, original: request.original, senders: senders, ownEmails: mail.ownEmails)
        } else {
            store = MailComposeStore(api: api, mode: request.mode, threadID: request.threadID, original: request.original, senders: senders, ownEmails: mail.ownEmails, initialTo: request.to)
        }
        self.init(store: store, mail: mail)
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 0) {
                    if store.senders.count > 1 || store.sender == nil { fromRow; Hairline(inset: 16) }
                    MailRecipientField(label: "To", emails: $store.to, suggestions: suggestions, focused: $focus, field: .to) {
                        if !showCopies {
                            Button("Cc Bcc") { withAnimation(Motion.snappy) { showCopies = true } }
                                .font(ODFont.sans(13, weight: .medium))
                                .foregroundStyle(Theme.fg3)
                                .buttonStyle(.plain)
                        }
                    }
                    Hairline(inset: 16)
                    if showCopies {
                        MailRecipientField(label: "Cc", emails: $store.cc, suggestions: suggestions, focused: $focus, field: .cc) { EmptyView() }
                        Hairline(inset: 16)
                        MailRecipientField(label: "Bcc", emails: $store.bcc, suggestions: suggestions, focused: $focus, field: .bcc) { EmptyView() }
                        Hairline(inset: 16)
                    }
                    HStack(spacing: 8) {
                        Text("Subject").font(ODFont.sans(15)).foregroundStyle(Theme.fg3)
                        TextField("", text: $store.subject)
                            .font(ODFont.sans(15, weight: .medium))
                            .foregroundStyle(Theme.fg)
                            .focused($focus, equals: .subject)
                            .submitLabel(.next)
                            .onSubmit { focus = .body }
                    }
                    .padding(.horizontal, 16)
                    .frame(minHeight: 48)
                    Hairline()
                    editor
                }
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.surface.ignoresSafeArea())
            .safeAreaInset(edge: .bottom, spacing: 0) { bottomBar }
            .navigationTitle(store.mode == .new && store.draftID != nil ? "Draft" : store.mode.title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button { close() } label: { Image(systemName: "xmark") }
                        .accessibilityLabel("Close")
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button { send() } label: {
                        if store.sending { ProgressView() } else { Image(systemName: "arrow.up") }
                    }
                    .buttonStyle(.glassProminent)
                    .tint(Theme.inverse)
                    .disabled(store.sending || !store.hasRecipient || store.uploading)
                    .accessibilityLabel("Send")
                }
            }
        }
        .onAppear {
            if store.to.isEmpty { focus = .to } else if store.mode != .new || !store.subject.isEmpty { focus = .body } else { focus = .subject }
        }
        .confirmationDialog("Keep this draft?", isPresented: $confirmClose, titleVisibility: .visible) {
            Button("Save draft") { finish(save: true) }
            Button("Discard draft", role: .destructive) { discard() }
            Button("Keep writing", role: .cancel) {}
        } message: {
            Text("Drafts are saved in the mail account, so you can finish them on any device.")
        }
        .fileImporter(isPresented: $importing, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            guard case let .success(urls) = result else { return }
            for url in urls { attach(url) }
        }
        .onChange(of: photo) { _, item in
            guard let item else { return }
            photo = nil
            Task {
                guard let data = try? await item.loadTransferable(type: Data.self) else { return }
                let type = item.supportedContentTypes.first ?? .jpeg
                await store.attach(data: data, filename: "Photo." + (type.preferredFilenameExtension ?? "jpg"), mimeType: type.preferredMIMEType ?? "image/jpeg")
            }
        }
    }

    // MARK: Parts

    private var fromRow: some View {
        HStack(spacing: 8) {
            Text("From").font(ODFont.sans(15)).foregroundStyle(Theme.fg3)
            if let sender = store.sender {
                Menu {
                    ForEach(store.senders) { option in
                        Button {
                            store.sender = option
                        } label: {
                            if option == sender { Label(option.line, systemImage: "checkmark") } else { Text(option.line) }
                        }
                    }
                } label: {
                    HStack(spacing: 6) {
                        MailAccountDot(color: sender.color, size: 7)
                        Text(sender.line).font(ODFont.sans(15, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                        Image(systemName: "chevron.up.chevron.down").font(.system(size: 10, weight: .semibold)).foregroundStyle(Theme.fg3)
                    }
                }
            } else {
                Text("Connect a mail account to send mail").font(ODFont.sans(15)).foregroundStyle(Tone.red.text)
            }
            Spacer()
        }
        .padding(.horizontal, 16)
        .frame(minHeight: 48)
    }

    private var editor: some View {
        VStack(alignment: .leading, spacing: 14) {
            TextField(text: $store.body, axis: .vertical) {
                Text(store.mode == .new ? "Write your message" : "Write your reply").foregroundStyle(Theme.fg4)
            }
            .font(ODFont.sans(16))
            .foregroundStyle(Theme.fg)
            .lineLimit(8...)
            .focused($focus, equals: .body)
            if !store.signatureHTML.isEmpty, !store.signatureRemoved {
                MailSignaturePreview(html: store.signatureHTML) { withAnimation(Motion.snappy) { store.removeSignature() } }
            }
            if store.mode == .forward, let original = store.original {
                MailForwardPreview(message: original, include: $store.forwardAttachments)
            }
            if !store.files.isEmpty { MailComposeFiles(store: store) }
            if let error { FormError(text: error) }
        }
        .padding(16)
    }

    private var bottomBar: some View {
        HStack(spacing: 4) {
            PhotosPicker(selection: $photo, matching: .images) {
                Image(systemName: "photo").frame(width: 40, height: 40)
            }
            .accessibilityLabel("Attach a photo")
            Button { importing = true } label: { Image(systemName: "paperclip").frame(width: 40, height: 40) }
                .accessibilityLabel("Attach a file")
            Spacer()
            MailSaveState(state: store.saveState)
        }
        .font(.system(size: 16, weight: .medium))
        .foregroundStyle(Theme.fg2)
        .padding(.horizontal, 10)
        .padding(.vertical, 4)
        .background(Theme.surface)
        .overlay(alignment: .top) { Hairline() }
    }

    private var suggestions: [MailAddress] { MailSuggestions.addresses(mail: mail, members: env.directory.members) }

    // MARK: Actions

    private func attach(_ url: URL) {
        let access = url.startAccessingSecurityScopedResource()
        defer { if access { url.stopAccessingSecurityScopedResource() } }
        guard let data = try? Data(contentsOf: url) else { return }
        let type = UTType(filenameExtension: url.pathExtension)?.preferredMIMEType ?? "application/octet-stream"
        let name = url.lastPathComponent
        Task { await store.attach(data: data, filename: name, mimeType: type) }
    }

    private func send() {
        error = nil
        Task {
            do {
                try await store.send()
                Haptics.success()
                env.toasts.show(store.mode == .forward ? "Forwarded" : "Sent", kind: .success)
                onDone(true)
                dismiss()
            } catch {
                Haptics.error()
                self.error = error.userMessage
            }
        }
    }

    private func close() {
        if store.isEmpty {
            discard()
        } else {
            confirmClose = true
        }
    }

    private func finish(save: Bool) {
        Task {
            if save { await store.close() }
            onDone(false)
            dismiss()
        }
    }

    private func discard() {
        Task {
            do { try await store.discard() } catch { env.toasts.show("The draft was not discarded", kind: .error) }
            onDone(false)
            dismiss()
        }
    }
}

// MARK: - Inline reply

/// A reply written under the thread, above the comment bar: who it goes to, the text,
/// files, and send. It opens full size for Cc, Bcc and the rest.
struct MailReplyComposer: View {
    let store: MailComposeStore
    var onExpand: () -> Void
    var onClose: () -> Void
    var onSent: () -> Void

    @Environment(AppEnvironment.self) private var env
    @State private var importing = false
    @State private var error: String?
    @FocusState private var focused: Bool

    var body: some View {
        @Bindable var store = store
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: store.mode.symbol).font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.fg3)
                Text(recipientLine)
                    .font(ODFont.sans(13, weight: .medium))
                    .foregroundStyle(Theme.fg2)
                    .lineLimit(1)
                Spacer(minLength: 4)
                MailSaveState(state: store.saveState)
                Button(action: onExpand) {
                    Image(systemName: "arrow.up.left.and.arrow.down.right").font(.system(size: 12, weight: .semibold)).frame(width: 28, height: 28)
                }
                .accessibilityLabel("Open full size")
                Button(action: onClose) {
                    Image(systemName: "xmark").font(.system(size: 12, weight: .semibold)).frame(width: 28, height: 28)
                }
                .accessibilityLabel("Close reply, keep the draft")
            }
            .foregroundStyle(Theme.fg3)
            .buttonStyle(.plain)
            TextField(text: $store.body, axis: .vertical) {
                Text("Write your reply").foregroundStyle(Theme.fg4)
            }
            .font(ODFont.sans(15.5))
            .foregroundStyle(Theme.fg)
            .lineLimit(3...8)
            .focused($focused)
            if !store.files.isEmpty { MailComposeFiles(store: store) }
            if let error { FormError(text: error) }
            HStack(spacing: 6) {
                Button { importing = true } label: {
                    Image(systemName: "paperclip").font(.system(size: 15, weight: .medium)).frame(width: 34, height: 34)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Theme.fg2)
                .accessibilityLabel("Attach a file")
                if let sender = store.sender {
                    HStack(spacing: 5) {
                        MailAccountDot(color: sender.color, size: 6)
                        Text(sender.email).font(ODFont.sans(12)).foregroundStyle(Theme.fg3).lineLimit(1)
                    }
                }
                Spacer()
                Button { send() } label: {
                    ButtonLabel(title: "Send", busy: store.sending, symbol: "arrow.up")
                }
                .buttonStyle(InkButtonStyle(fullWidth: false, height: 36))
                .disabled(store.sending || store.body.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty || store.uploading)
            }
        }
        .padding(.horizontal, 14)
        .padding(.top, 8)
        .padding(.bottom, 8)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 20, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        .shadow(color: .black.opacity(0.10), radius: 18, y: 6)
        .onAppear { focused = true }
        .fileImporter(isPresented: $importing, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            guard case let .success(urls) = result else { return }
            for url in urls {
                let access = url.startAccessingSecurityScopedResource()
                defer { if access { url.stopAccessingSecurityScopedResource() } }
                guard let data = try? Data(contentsOf: url) else { continue }
                let type = UTType(filenameExtension: url.pathExtension)?.preferredMIMEType ?? "application/octet-stream"
                let name = url.lastPathComponent
                Task { await store.attach(data: data, filename: name, mimeType: type) }
            }
        }
    }

    private var recipientLine: String {
        let own = MailStore.shared.ownEmails
        let names = store.to.compactMap { email -> String? in
            guard !own.contains(email.lowercased()) else { return nil }
            let known = store.original.flatMap { message in ([message.from] + message.to + message.cc).first { $0.email.lowercased() == email.lowercased() } }
            return known?.short ?? email
        }
        let more = store.cc.count + store.bcc.count
        let line = names.isEmpty ? "No recipients" : names.joined(separator: ", ")
        return (store.mode == .replyAll ? "Reply all to " : "Reply to ") + line + (more > 0 ? " +\(more)" : "")
    }

    private func send() {
        error = nil
        Task {
            do {
                try await store.send()
                Haptics.success()
                env.toasts.show("Sent", kind: .success)
                onSent()
            } catch {
                Haptics.error()
                self.error = error.userMessage
            }
        }
    }
}

// MARK: - Pieces

/// "Saving…", "Saved", "Not saved".
struct MailSaveState: View {
    let state: MailComposeStore.SaveState

    var body: some View {
        Group {
            switch state {
            case .idle: EmptyView()
            case .saving: Text("Saving…")
            case .saved: Label("Saved", systemImage: "checkmark").labelStyle(MailInlineLabelStyle())
            case .failed: Text("Not saved").foregroundStyle(Tone.red.text)
            }
        }
        .font(ODFont.sans(12))
        .foregroundStyle(Theme.fg4)
        .contentTransition(.opacity)
    }
}

/// The address's signature under the text, with a way to take it out of this message.
struct MailSignaturePreview: View {
    let html: String
    let remove: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 8) {
            Rectangle().fill(Theme.line).frame(width: 2)
            Text(HTMLText.plain(from: html))
                .font(ODFont.sans(13.5))
                .foregroundStyle(Theme.fg3)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 4)
            Button(action: remove) {
                Image(systemName: "xmark").font(.system(size: 10, weight: .bold)).foregroundStyle(Theme.fg4).frame(width: 24, height: 24)
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Remove the signature from this message")
        }
    }
}

/// The message being forwarded, and whether its files go along.
struct MailForwardPreview: View {
    let message: MailMessage
    @Binding var include: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 8) {
                Image(systemName: "arrowshape.turn.up.right").font(.system(size: 11, weight: .bold)).foregroundStyle(Theme.fg3)
                Text("Forwarded message").font(ODFont.sans(12.5, weight: .semibold)).foregroundStyle(Theme.fg2)
            }
            Text("From \(message.from.display), \(MailFormat.stamp(message.sentAt))").font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3)
            Text(message.snippet).font(ODFont.sans(13)).foregroundStyle(Theme.fg2).lineLimit(3)
            if !message.files.isEmpty {
                Toggle(isOn: $include) {
                    Text("Include the original attachments (\(message.files.count))").font(ODFont.sans(13.5)).foregroundStyle(Theme.fg)
                }
                .tint(Theme.inverse)
            }
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}

/// The files in a composer, with their upload state.
struct MailComposeFiles: View {
    let store: MailComposeStore

    var body: some View {
        FlowLayout(spacing: 6) {
            ForEach(store.files) { file in
                HStack(spacing: 6) {
                    switch file.state {
                    case .uploading: ProgressView().controlSize(.mini)
                    case .ready: Image(systemName: "doc").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.fg3)
                    case .failed: Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 11)).foregroundStyle(Tone.red.solid)
                    }
                    Text(file.name).font(ODFont.sans(12.5, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                    Text(Format.bytes(file.size)).font(ODFont.sans(11.5)).foregroundStyle(Theme.fg3)
                    Button { store.remove(file) } label: {
                        Image(systemName: "xmark").font(.system(size: 9, weight: .bold)).foregroundStyle(Theme.fg3)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Remove \(file.name)")
                }
                .padding(.horizontal, 9)
                .frame(height: 30)
                .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous).strokeBorder(file.state == .failed ? Tone.red.line : Theme.line, lineWidth: 1))
            }
        }
    }
}

// MARK: - Recipients

enum MailSuggestions {
    /// People to suggest: everyone in the mail in view, then teammates.
    @MainActor
    static func addresses(mail: MailStore, members: [Member]) -> [MailAddress] {
        let own = mail.ownEmails
        var seen = Set<String>()
        var out: [MailAddress] = []
        for address in mail.threads.flatMap(\.participants) + members.compactMap({ m in m.user.email.map { MailAddress(name: m.user.name, email: $0) } }) {
            let key = address.email.lowercased()
            guard !key.isEmpty, !own.contains(key), !seen.contains(key) else { continue }
            seen.insert(key)
            out.append(address)
        }
        return out
    }
}

/// A recipient line: chips for the addresses so far, a field for the next one, and the
/// people it could be under it while typing.
struct MailRecipientField<Accessory: View>: View {
    let label: String
    @Binding var emails: [String]
    let suggestions: [MailAddress]
    var focused: FocusState<MailComposeView.Field?>.Binding
    let field: MailComposeView.Field
    @ViewBuilder var accessory: () -> Accessory

    @State private var draft = ""

    private var matches: [MailAddress] {
        let needle = draft.trimmingCharacters(in: .whitespaces).lowercased()
        guard !needle.isEmpty else { return [] }
        let taken = Set(emails.map { $0.lowercased() })
        return suggestions.filter { !taken.contains($0.email.lowercased()) && ($0.name.lowercased().contains(needle) || $0.email.lowercased().contains(needle)) }.prefix(4).map { $0 }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                Text(label).font(ODFont.sans(15)).foregroundStyle(Theme.fg3).frame(minWidth: 30, alignment: .leading)
                FlowLayout(spacing: 6) {
                    ForEach(emails, id: \.self) { email in chip(email) }
                    TextField("", text: $draft)
                        .font(ODFont.sans(15))
                        .foregroundStyle(Theme.fg)
                        .keyboardType(.emailAddress)
                        .textContentType(.emailAddress)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .submitLabel(.next)
                        .focused(focused, equals: field)
                        .frame(minWidth: 120)
                        .onSubmit { commit() }
                        .onChange(of: draft) { _, value in
                            if value.hasSuffix(",") || value.hasSuffix(" ") || value.hasSuffix(";") { commit() }
                        }
                        .onChange(of: focused.wrappedValue) { _, value in if value != field { commit() } }
                }
                accessory()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            if !matches.isEmpty {
                VStack(spacing: 0) {
                    ForEach(matches, id: \.email) { address in
                        Button {
                            add(address.email)
                        } label: {
                            HStack(spacing: 10) {
                                Avatar(name: address.display, seed: address.email, size: 28)
                                VStack(alignment: .leading, spacing: 1) {
                                    Text(address.display).font(ODFont.sans(14, weight: .medium)).foregroundStyle(Theme.fg)
                                    if !address.name.isEmpty { Text(address.email).font(ODFont.sans(12)).foregroundStyle(Theme.fg3) }
                                }
                                Spacer()
                            }
                            .padding(.horizontal, 16)
                            .frame(height: 48)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(RowPressStyle())
                    }
                }
                .background(Theme.sunken)
            }
        }
    }

    private func chip(_ email: String) -> some View {
        let known = suggestions.first { $0.email.lowercased() == email.lowercased() }
        let valid = email.contains("@") && email.contains(".")
        return HStack(spacing: 5) {
            Text(known?.display ?? email).font(ODFont.sans(14, weight: .medium)).lineLimit(1)
            Button {
                emails.removeAll { $0 == email }
            } label: {
                Image(systemName: "xmark").font(.system(size: 8.5, weight: .bold))
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Remove \(email)")
        }
        .foregroundStyle(valid ? Theme.fg : Tone.red.text)
        .padding(.horizontal, 9)
        .frame(height: 28)
        .background(valid ? Theme.hover : Tone.red.soft, in: Capsule())
        .overlay(Capsule().strokeBorder(valid ? Theme.line : Tone.red.line, lineWidth: 1))
        .accessibilityLabel(known.map { "\($0.display), \(email)" } ?? email)
    }

    private func commit() {
        let value = draft.trimmingCharacters(in: CharacterSet(charactersIn: " ,;")).trimmingCharacters(in: .whitespaces)
        let candidates = matches
        draft = ""
        guard !value.isEmpty else { return }
        if !value.contains("@"), let match = candidates.first ?? suggestions.first(where: { $0.name.lowercased().hasPrefix(value.lowercased()) }) {
            add(match.email)
        } else {
            add(value)
        }
    }

    private func add(_ email: String) {
        draft = ""
        guard !emails.contains(where: { $0.lowercased() == email.lowercased() }) else { return }
        emails.append(email)
    }
}
