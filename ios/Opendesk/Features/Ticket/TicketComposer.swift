import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

/// What Ollie can do from the composer.
enum ComposerOllieAction {
    case draft
    case guidedDraft
    case rewrite(RewriteMode)
    case translate(code: String, name: String)
    case ask
}

/// The composer's hooks into the screen.
struct ComposerActions {
    var send: (TicketStatus?) -> Void
    var open: (TicketSheet) -> Void
    var ollie: (ComposerOllieAction) -> Void
    var savedReply: (SavedReply) -> Void
    var photos: ([PhotosPickerItem]) -> Void
    var files: ([URL]) -> Void
    var useDraft: (SharedDraft) -> Void
    var discardDraft: () -> Void
    var undo: () -> Void
    var sendNow: () -> Void
}

/// The pinned composer: one line until it's used, then the mode, recipients, tools and
/// the send button with a status. Suggestions (saved replies, teammates, template values)
/// and banners (someone else replying, a staged draft) sit right above it.
struct TicketComposerView: View {
    @Bindable var composer: TicketComposerStore
    let store: TicketStore
    let ticket: Ticket
    let members: [Member]
    let me: String?
    let replying: [PresenceViewer]
    let savedReplies: [SavedReply]
    let assistantOn: Bool
    var focused: FocusState<Bool>.Binding
    let actions: ComposerActions

    @State private var showPhotos = false
    @State private var photoItems: [PhotosPickerItem] = []
    @State private var showFiles = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var expanded: Bool { focused.wrappedValue || composer.hasContent || composer.mode == .side || !composer.attachments.isEmpty }
    private var ollieBusy: Bool { store.drafting || store.rewriting }

    var body: some View {
        VStack(spacing: 8) {
            if let held = composer.held {
                HeldSendBar(held: held, undo: actions.undo, sendNow: actions.sendNow)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            } else {
                banners
                if let token = composer.token, focused.wrappedValue {
                    ComposerSuggestionList(token: token, savedReplies: savedReplies, members: members, me: me, variables: store.variables) { pick in
                        Haptics.selection()
                        switch pick {
                        case let .savedReply(reply): actions.savedReply(reply)
                        case let .member(member): composer.mention(member)
                        case let .variable(key): composer.complete(with: "{{\(key)}}")
                        }
                    }
                    .transition(.opacity.combined(with: .move(edge: .bottom)))
                }
                box
            }
        }
        .padding(.horizontal, 10)
        .padding(.bottom, 6)
        .animation(reduceMotion ? nil : Motion.snappy, value: expanded)
        .animation(reduceMotion ? nil : Motion.snappy, value: composer.held?.id)
        .animation(reduceMotion ? nil : Motion.fade, value: composer.token)
        .photosPicker(isPresented: $showPhotos, selection: $photoItems, maxSelectionCount: 6, matching: .any(of: [.images, .screenshots]))
        .onChange(of: photoItems) { _, items in
            guard !items.isEmpty else { return }
            actions.photos(items)
            photoItems = []
        }
        .fileImporter(isPresented: $showFiles, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            if case let .success(urls) = result { actions.files(urls) }
        }
    }

    // MARK: Banners

    @ViewBuilder private var banners: some View {
        let others = replying.filter { $0.userID != me }
        if !others.isEmpty, composer.mode == .reply {
            ComposerBanner(tone: .amber, symbol: "exclamationmark.bubble", text: "\(ListFormatter.localizedString(byJoining: others.map(\.name))) \(others.count == 1 ? "is" : "are") replying to this ticket right now.", actionTitle: "Leave a note instead") {
                composer.mode = .note
                focused.wrappedValue = true
            }
        }
        if let draft = store.sharedDraft, composer.text.isEmpty {
            ComposerBanner(tone: .blue, symbol: "doc.text", text: draftLine(draft), actionTitle: "Use") { actions.useDraft(draft) }
                .overlay(alignment: .topTrailing) {
                    Button(action: actions.discardDraft) {
                        Image(systemName: "xmark").font(.system(size: 9, weight: .bold)).foregroundStyle(Theme.fg3)
                            .frame(width: 22, height: 22).background(Theme.surface, in: Circle())
                            .overlay(Circle().strokeBorder(Theme.line, lineWidth: 1))
                    }
                    .buttonStyle(.plain)
                    .offset(x: 6, y: -8)
                    .accessibilityLabel("Discard the draft")
                }
        }
        if composer.fromNoteID != nil {
            ComposerBanner(tone: .gray, symbol: "arrowshape.turn.up.left", text: "Writing a reply from your note. Sending it removes the note.", actionTitle: "Keep the note") {
                composer.cancelFromNote()
            }
        }
    }

    private func draftLine(_ draft: SharedDraft) -> String {
        let who = draft.by.user?.id == me ? "You" : (draft.by.name.isEmpty ? "Someone" : draft.by.name)
        let flat = draft.text.split(whereSeparator: \.isWhitespace).joined(separator: " ")
        return "\(who) staged a reply: \u{201C}\(flat.prefix(90))\u{201D}"
    }

    // MARK: The box

    private var box: some View {
        VStack(alignment: .leading, spacing: 0) {
            if expanded {
                modeRow
                    .padding(.horizontal, 10)
                    .padding(.top, 9)
                recipients
                    .padding(.horizontal, 12)
                    .padding(.top, 8)
            }
            if !composer.attachments.isEmpty {
                ComposerAttachmentStrip(attachments: $composer.attachments)
                    .padding(.top, 8)
            }
            HStack(alignment: .bottom, spacing: 6) {
                if !expanded {
                    Button {
                        Haptics.tap()
                        composer.mode = composer.mode == .note ? .reply : .note
                    } label: {
                        Image(systemName: composer.mode.symbol)
                            .font(.system(size: 14, weight: .medium))
                            .foregroundStyle(Theme.fg2)
                            .frame(width: 36, height: 36)
                            .contentShape(Circle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(composer.mode == .note ? "Writing a note. Switch to reply" : "Writing a reply. Switch to note")
                }
                TextField(text: $composer.text, axis: .vertical) {
                    Text(placeholder).foregroundStyle(Theme.fg4)
                }
                .font(ODFont.sans(15.5))
                .foregroundStyle(Theme.fg)
                .lineLimit(expanded ? 2 ... 9 : 1 ... 1)
                .focused(focused)
                .padding(.vertical, 9)
                .padding(.horizontal, expanded ? 12 : 0)
                .accessibilityLabel(placeholder)
                if !expanded {
                    sendButton(compact: true)
                }
            }
            .padding(.horizontal, expanded ? 0 : 4)
            .padding(.vertical, expanded ? 2 : 2)
            if expanded {
                toolbar
                    .padding(.horizontal, 6)
                    .padding(.bottom, 6)
            }
        }
        .background {
            if composer.mode == .note {
                RoundedRectangle(cornerRadius: 22, style: .continuous).fill(Theme.note.opacity(0.85))
            }
        }
        .glassEffect(.regular.tint(composer.mode == .note ? Theme.note.opacity(0.9) : Theme.glassTint), in: RoundedRectangle(cornerRadius: 22, style: .continuous))
        .overlay {
            if composer.mode == .note {
                RoundedRectangle(cornerRadius: 22, style: .continuous).strokeBorder(Theme.noteLine, style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
            }
        }
    }

    private var placeholder: String {
        switch composer.mode {
        case .reply: ticket.channel == .chat ? "Reply in the chat" : "Reply to \(ticket.contact?.name.split(separator: " ").first.map(String.init) ?? ticket.contactName)"
        case .note: "Write an internal note, @ to mention"
        case .side: composer.sideThreadID == nil ? "Write the first message" : "Write to the side conversation"
        }
    }

    private var modeRow: some View {
        HStack(spacing: 4) {
            ForEach(ComposerMode.allCases) { mode in
                let on = composer.mode == mode
                Button {
                    Haptics.selection()
                    composer.mode = mode
                    if mode == .side, composer.sideThreadID == nil, let open = store.threads.last(where: { $0.status == .open }) {
                        composer.sideThreadID = open.id
                    }
                } label: {
                    HStack(spacing: 5) {
                        Image(systemName: mode.symbol).font(.system(size: 11, weight: .semibold))
                        Text(mode.short).font(ODFont.sans(12.5, weight: .medium))
                    }
                    .foregroundStyle(on ? Theme.inverseFg : Theme.fg2)
                    .padding(.horizontal, 10)
                    .frame(height: 28)
                    .background(on ? Theme.inverse : Color.clear, in: Capsule())
                    .contentShape(Capsule())
                }
                .buttonStyle(TapScaleStyle(scale: 0.95))
                .accessibilityAddTraits(on ? .isSelected : [])
            }
            Spacer(minLength: 4)
            if composer.mode == .reply, ticket.channel == .email {
                Button {
                    Haptics.tap()
                    composer.showCopies.toggle()
                } label: {
                    Text("Cc").font(ODFont.sans(12.5, weight: .medium))
                        .foregroundStyle(composer.showCopies ? Theme.fg : Theme.fg3)
                        .padding(.horizontal, 9)
                        .frame(height: 28)
                        .background(composer.showCopies ? Theme.active : Color.clear, in: Capsule())
                }
                .buttonStyle(.plain)
                .accessibilityLabel(composer.showCopies ? "Hide copies" : "Add Cc or Bcc")
            }
            Button {
                focused.wrappedValue = false
            } label: {
                Image(systemName: "chevron.down").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.fg3)
                    .frame(width: 28, height: 28).contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel("Hide the keyboard")
        }
    }

    @ViewBuilder private var recipients: some View {
        switch composer.mode {
        case .reply:
            VStack(alignment: .leading, spacing: 6) {
                if ticket.channel == .email {
                    RecipientLine(label: "To") {
                        Text(ticket.contact?.email ?? ticket.contactName).font(ODFont.sans(13)).foregroundStyle(Theme.fg).lineLimit(1)
                    }
                    if composer.showCopies {
                        RecipientLine(label: "Cc") { AddressField(text: $composer.cc, prompt: "Add people") }
                        RecipientLine(label: "Bcc") { AddressField(text: $composer.bcc, prompt: "Add people") }
                    }
                    if let from = store.replyAddress {
                        RecipientLine(label: "From") {
                            Text(from.displayName.isEmpty ? from.email : "\(from.displayName) <\(from.email)>")
                                .font(ODFont.sans(13)).foregroundStyle(Theme.fg2).lineLimit(1)
                        }
                    }
                } else {
                    HStack(spacing: 6) {
                        Image(systemName: ticket.channel.symbol).font(.system(size: 10.5, weight: .semibold))
                        Text(ticket.channel == .chat ? "Replying in the live chat" : "Replying on the \(ticket.channel.title.lowercased()) ticket")
                    }
                    .font(ODFont.sans(12.5))
                    .foregroundStyle(Theme.fg3)
                }
            }
        case .note:
            HStack(spacing: 6) {
                Image(systemName: "lock.fill").font(.system(size: 9.5))
                Text("Only the team sees notes")
            }
            .font(ODFont.sans(12.5))
            .foregroundStyle(Theme.fg3)
        case .side:
            sideRecipients
        }
    }

    @ViewBuilder private var sideRecipients: some View {
        let open = store.threads.filter { $0.status == .open }
        VStack(alignment: .leading, spacing: 6) {
            Menu {
                ForEach(open) { thread in
                    Button {
                        composer.sideThreadID = thread.id
                    } label: {
                        Label(thread.displaySubject, systemImage: composer.sideThreadID == thread.id ? "checkmark" : "arrow.triangle.branch")
                    }
                }
                if !open.isEmpty { Divider() }
                Button {
                    composer.sideThreadID = nil
                } label: { Label("New side conversation", systemImage: "plus") }
            } label: {
                HStack(spacing: 5) {
                    Text(open.first { $0.id == composer.sideThreadID }.map { $0.displaySubject } ?? "New side conversation")
                        .font(ODFont.sans(13, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    Image(systemName: "chevron.up.chevron.down").font(.system(size: 9, weight: .semibold)).foregroundStyle(Theme.fg3)
                }
            }
            if let thread = open.first(where: { $0.id == composer.sideThreadID }) {
                Text("With " + (thread.to + thread.cc).joined(separator: ", "))
                    .font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).lineLimit(1)
            } else {
                RecipientLine(label: "To") { AddressField(text: $composer.sideTo, prompt: "name@company.com") }
                RecipientLine(label: "Subject") {
                    TextField(text: $composer.sideSubject) { Text(ticket.subject).foregroundStyle(Theme.fg4) }
                        .font(ODFont.sans(13))
                }
                Button {
                    Haptics.selection()
                    composer.includeHistory.toggle()
                } label: {
                    HStack(spacing: 6) {
                        Image(systemName: composer.includeHistory ? "checkmark.square.fill" : "square")
                            .font(.system(size: 13))
                            .foregroundStyle(composer.includeHistory ? Theme.fg : Theme.fg3)
                        Text("Include the conversation so far").font(ODFont.sans(12.5)).foregroundStyle(Theme.fg2)
                    }
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(composer.includeHistory ? .isSelected : [])
            }
        }
    }

    // MARK: Toolbar

    private var toolbar: some View {
        HStack(spacing: 2) {
            Menu {
                Button { showPhotos = true } label: { Label("Photos", systemImage: "photo.on.rectangle") }
                Button { showFiles = true } label: { Label("Files", systemImage: "folder") }
            } label: { toolIcon("paperclip") }
                .accessibilityLabel("Attach")
            if composer.mode != .side {
                Button { actions.open(.savedReplies) } label: { toolIcon("text.bubble") }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Saved replies")
            }
            if composer.mode == .note {
                Button {
                    composer.text += composer.text.isEmpty || composer.text.hasSuffix(" ") || composer.text.hasSuffix("\n") ? "@" : " @"
                    focused.wrappedValue = true
                } label: { toolIcon("at") }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Mention a teammate")
            }
            if assistantOn { ollieMenu }
            Spacer(minLength: 6)
            sendButton(compact: false)
        }
    }

    private func toolIcon(_ symbol: String) -> some View {
        Image(systemName: symbol)
            .font(.system(size: 15, weight: .medium))
            .foregroundStyle(Theme.fg2)
            .frame(width: 36, height: 36)
            .contentShape(Rectangle())
    }

    private var ollieMenu: some View {
        Menu {
            if composer.mode == .reply {
                Button { actions.ollie(.draft) } label: { Label(composer.trimmed.isEmpty ? "Draft a reply" : "Draft a new reply", systemImage: "sparkles") }
                Button { actions.ollie(.guidedDraft) } label: { Label("Draft with instructions", systemImage: "text.and.command.macwindow") }
            }
            if !composer.trimmed.isEmpty {
                Section("Rewrite") {
                    ForEach(RewriteMode.allCases) { mode in
                        Button { actions.ollie(.rewrite(mode)) } label: { Label(mode.title, systemImage: mode.symbol) }
                    }
                }
                Menu {
                    ForEach(Self.languages, id: \.0) { code, name in
                        Button(name) { actions.ollie(.translate(code: code, name: name)) }
                    }
                } label: { Label("Translate to", systemImage: "translate") }
            }
            Divider()
            Button { actions.ollie(.ask) } label: { Label("Ask about this ticket", systemImage: "questionmark.bubble") }
        } label: {
            OllieMark(size: 22, state: ollieBusy ? .thinking : .idle, eyes: true)
                .frame(width: 36, height: 36)
                .contentShape(Rectangle())
        }
        .disabled(ollieBusy)
        .accessibilityLabel("Ollie")
    }

    static let languages: [(String, String)] = [
        ("en", "English"), ("es", "Spanish"), ("fr", "French"), ("de", "German"), ("pt", "Portuguese"),
        ("it", "Italian"), ("nl", "Dutch"), ("pl", "Polish"), ("ja", "Japanese"), ("zh", "Chinese"),
    ]

    @ViewBuilder private func sendButton(compact: Bool) -> some View {
        let enabled = composer.canSend
        let title = composer.mode == .note ? "Add note" : "Send"
        if compact {
            Button { actions.send(nil) } label: {
                Image(systemName: "arrow.up")
                    .font(.system(size: 14, weight: .bold))
                    .foregroundStyle(Theme.inverseFg)
                    .frame(width: 32, height: 32)
                    .background(Theme.inverse.opacity(enabled ? 1 : 0.22), in: Circle())
            }
            .buttonStyle(TapScaleStyle(scale: 0.9))
            .disabled(!enabled)
            .padding(.bottom, 2)
            .padding(.trailing, 2)
            .accessibilityLabel(title)
        } else if composer.mode == .side {
            Button { actions.send(nil) } label: {
                Text(composer.sideThreadID == nil ? "Start" : "Send")
                    .font(ODFont.sans(14, weight: .semibold))
                    .foregroundStyle(Theme.inverseFg)
                    .padding(.horizontal, 16)
                    .frame(height: 34)
                    .background(Theme.inverse.opacity(enabled ? 1 : 0.3), in: Capsule())
            }
            .buttonStyle(TapScaleStyle())
            .disabled(!enabled)
        } else {
            Menu {
                Section(composer.mode == .note ? "Add the note and" : "Send and") {
                    ForEach([TicketStatus.pending, .onHold, .solved]) { status in
                        Button { actions.send(status) } label: {
                            Label("Mark \(status.title.lowercased())", systemImage: symbol(for: status))
                        }
                        .disabled(status == ticket.status)
                    }
                }
            } label: {
                HStack(spacing: 0) {
                    Text(title)
                        .font(ODFont.sans(14, weight: .semibold))
                        .padding(.leading, 14)
                        .padding(.trailing, 10)
                    Rectangle().fill(Theme.inverseFg.opacity(0.25)).frame(width: 1, height: 18)
                    Image(systemName: "chevron.down")
                        .font(.system(size: 10, weight: .bold))
                        .frame(width: 28)
                }
                .foregroundStyle(Theme.inverseFg)
                .frame(height: 34)
                .background(Theme.inverse.opacity(enabled ? 1 : 0.3), in: Capsule())
            } primaryAction: {
                actions.send(nil)
            }
            .disabled(!enabled)
            .accessibilityLabel(title)
            .accessibilityHint("Touch and hold to send and change the status")
        }
    }

    private func symbol(for status: TicketStatus) -> String {
        switch status {
        case .pending: "clock"
        case .onHold: "pause.circle"
        case .solved: "checkmark.circle"
        default: "circle"
        }
    }
}

/// "To", "Cc": a small grey label and its value.
private struct RecipientLine<Content: View>: View {
    let label: String
    @ViewBuilder var content: () -> Content

    var body: some View {
        HStack(spacing: 8) {
            Text(label).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).frame(width: 50, alignment: .leading)
            content()
            Spacer(minLength: 0)
        }
        .frame(minHeight: 22)
    }
}

private struct AddressField: View {
    @Binding var text: String
    let prompt: String

    var body: some View {
        TextField(text: $text) { Text(prompt).foregroundStyle(Theme.fg4) }
            .font(ODFont.sans(13))
            .keyboardType(.emailAddress)
            .textInputAutocapitalization(.never)
            .autocorrectionDisabled()
            .textContentType(.emailAddress)
    }
}

/// A one-line note above the composer with one action.
struct ComposerBanner: View {
    var tone: Tone = .gray
    let symbol: String
    let text: String
    var actionTitle: String?
    var action: (() -> Void)?

    var body: some View {
        HStack(spacing: 9) {
            Image(systemName: symbol).font(.system(size: 12, weight: .semibold)).foregroundStyle(tone == .gray ? Theme.fg3 : tone.text)
            Text(text).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg2).lineLimit(2)
            Spacer(minLength: 4)
            if let actionTitle, let action {
                Button(actionTitle) {
                    Haptics.tap()
                    action()
                }
                .font(ODFont.sans(12.5, weight: .semibold))
                .foregroundStyle(Theme.fg)
                .fixedSize()
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .background(Theme.surface.opacity(0.82), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .glassEffect(.regular.tint(tone == .gray ? Theme.glassTint : tone.soft), in: RoundedRectangle(cornerRadius: 14, style: .continuous))
        .transition(.opacity.combined(with: .move(edge: .bottom)))
    }
}

/// A reply in its undo window: it goes out when the ring runs out.
struct HeldSendBar: View {
    let held: HeldSend
    let undo: () -> Void
    let sendNow: () -> Void
    @State private var total: TimeInterval = 5

    var body: some View {
        HStack(spacing: 10) {
            TimelineView(.animation(minimumInterval: 1 / 15)) { context in
                let left = max(0, held.until.timeIntervalSince(AppClock.now(at: context.date)))
                ZStack {
                    Circle().stroke(Theme.line, lineWidth: 2.5)
                    Circle().trim(from: 0, to: total > 0 ? left / total : 0)
                        .stroke(Theme.fg, style: StrokeStyle(lineWidth: 2.5, lineCap: .round))
                        .rotationEffect(.degrees(-90))
                    Text("\(Int(left.rounded(.up)))").font(ODFont.mono(10, weight: .semibold)).foregroundStyle(Theme.fg2)
                }
                .frame(width: 24, height: 24)
            }
            Text("Sending your reply").font(ODFont.sans(14, weight: .medium)).foregroundStyle(Theme.fg)
            Spacer(minLength: 6)
            Button("Undo") {
                Haptics.warning()
                undo()
            }
            .font(ODFont.sans(14, weight: .semibold))
            .foregroundStyle(Theme.fg)
            Button("Send now") {
                Haptics.tap()
                sendNow()
            }
            .font(ODFont.sans(14, weight: .medium))
            .foregroundStyle(Theme.fg3)
        }
        .padding(.horizontal, 14)
        .frame(height: 52)
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 22, style: .continuous))
        .onAppear { total = max(1, held.until.timeIntervalSince(AppClock.now)) }
    }
}

/// Files picked for the message, uploading or ready, each removable.
struct ComposerAttachmentStrip: View {
    @Binding var attachments: [PendingAttachment]

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                ForEach(attachments) { file in
                    HStack(spacing: 7) {
                        Group {
                            if let data = file.preview, let image = UIImage(data: data) {
                                Image(uiImage: image).resizable().scaledToFill()
                            } else {
                                Image(systemName: file.isImage ? "photo" : "doc").font(.system(size: 12)).foregroundStyle(Theme.fg2)
                            }
                        }
                        .frame(width: 28, height: 28)
                        .background(Theme.active)
                        .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
                        VStack(alignment: .leading, spacing: 0) {
                            Text(file.filename).font(ODFont.sans(12, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1).frame(maxWidth: 120, alignment: .leading)
                            switch file.state {
                            case .uploading: Text("Uploading\u{2026}").font(ODFont.sans(10.5)).foregroundStyle(Theme.fg3)
                            case .ready: Text(Format.bytes(file.size)).font(ODFont.mono(10)).foregroundStyle(Theme.fg3)
                            case let .failed(reason): Text(reason).font(ODFont.sans(10.5)).foregroundStyle(Tone.red.text).lineLimit(1)
                            }
                        }
                        if file.state == .uploading {
                            ProgressView().controlSize(.mini)
                        }
                        Button {
                            attachments.removeAll { $0.id == file.id }
                        } label: {
                            Image(systemName: "xmark").font(.system(size: 9, weight: .bold)).foregroundStyle(Theme.fg3)
                                .frame(width: 20, height: 20).contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Remove \(file.filename)")
                    }
                    .padding(.leading, 5)
                    .padding(.trailing, 3)
                    .frame(height: 38)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                }
            }
            .padding(.horizontal, 10)
        }
    }
}
