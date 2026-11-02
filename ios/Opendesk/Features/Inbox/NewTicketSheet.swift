import PhotosUI
import SwiftUI
import UniformTypeIdentifiers

/// Starts a ticket: email the customer from one of the team's addresses, or log what
/// they said elsewhere as an internal note (the web's `NewTicketDialog` and its full page):
/// the customer's card and earlier tickets, saved replies and files, and the ticket's
/// details (assignee, team, priority, severity, labels and fields) before it exists.
struct NewTicketSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var store = NewTicketStore()
    @State private var picking: Picker?
    @State private var editingField: CustomField?
    @State private var confirmDiscard = false
    @State private var showPhotos = false
    @State private var showFiles = false
    @State private var photoItems: [PhotosPickerItem] = []
    @FocusState private var focus: Field?

    /// Opens the ticket once it exists.
    let onCreated: (Ticket) -> Void

    enum Field: Hashable { case to, subject, body }

    enum Picker: String, Identifiable {
        case assignee, team, labels, severity, savedReply
        var id: String { rawValue }
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    InboxSegmented(
                        options: NewTicketStore.Kind.allCases.map { (value: $0, title: $0.title) },
                        selection: store.kind
                    ) { store.kind = $0 }

                    Panel {
                        toRow
                        if store.recipient == nil, !store.contactQuery.trimmingCharacters(in: .whitespaces).isEmpty {
                            Hairline()
                            suggestions
                        }
                        if !store.mailboxes.isEmpty {
                            Hairline(inset: 14)
                            fromRow
                        }
                        Hairline(inset: 14)
                        HStack(spacing: 10) {
                            fieldLabel("Subject")
                            TextField(text: $store.subject) { Text("What is this about?").foregroundStyle(Theme.fg4) }
                                .font(ODFont.sans(15.5))
                                .foregroundStyle(Theme.fg)
                                .focused($focus, equals: .subject)
                                .submitLabel(.next)
                                .onSubmit { focus = .body }
                        }
                        .padding(.horizontal, 14)
                        .frame(height: 48)
                    }

                    if case let .contact(contact) = store.recipient {
                        NewTicketCustomerCard(contact: contact, tickets: store.pastTickets, loading: store.pastLoading)
                            .transition(.opacity.combined(with: .move(edge: .top)))
                    }

                    VStack(spacing: 0) {
                        ZStack(alignment: .topLeading) {
                            if store.body.isEmpty {
                                Text(store.kind == .reply ? "Write your message" : "Describe the issue")
                                    .font(ODFont.sans(15.5))
                                    .foregroundStyle(Theme.fg4)
                                    .padding(.horizontal, 14)
                                    .padding(.vertical, 12)
                                    .allowsHitTesting(false)
                            }
                            TextEditor(text: $store.body)
                                .font(ODFont.sans(15.5))
                                .foregroundStyle(Theme.fg)
                                .scrollContentBackground(.hidden)
                                .padding(.horizontal, 9)
                                .padding(.vertical, 4)
                                .frame(minHeight: 180)
                                .focused($focus, equals: .body)
                        }
                        if !store.attachments.isEmpty {
                            ComposerAttachmentStrip(attachments: $store.attachments)
                                .padding(.horizontal, 10)
                                .padding(.bottom, 6)
                        }
                        bodyTools
                    }
                    .background(store.kind == .note ? Tone.amber.soft : Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(store.kind == .note ? Tone.amber.line : Theme.line, lineWidth: 1))
                    .motion(Motion.fade, value: store.kind)

                    options
                    details

                    if store.kind == .note {
                        Text("Only your team sees this. The customer gets nothing until someone replies.")
                            .font(ODFont.footnote)
                            .foregroundStyle(Theme.fg3)
                    }
                    if store.cannotSend {
                        Notice(text: "This address can't send email yet. Pick another, or ask an admin to connect a way to send.", tone: .amber, symbol: "exclamationmark.triangle")
                    }
                    FormError(text: store.error)
                }
                .padding(16)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("New ticket")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        if store.isDirty { confirmDiscard = true } else { dismiss() }
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        Task { await create() }
                    } label: {
                        if store.creating {
                            ProgressView().controlSize(.small)
                        } else {
                            Text(store.kind == .reply ? "Create and send" : "Create ticket").fontWeight(.semibold)
                        }
                    }
                    .disabled(store.creating)
                }
            }
            .confirmationDialog("Discard this ticket?", isPresented: $confirmDiscard, titleVisibility: .visible) {
                Button("Discard", role: .destructive) { dismiss() }
                Button("Keep writing", role: .cancel) {}
            } message: {
                Text("Your draft will be lost.")
            }
        }
        .interactiveDismissDisabled(store.isDirty)
        .sheet(item: $picking) { picker in
            Group {
                switch picker {
                case .assignee:
                    AssigneePicker(current: store.assigneeID) { store.assigneeID = $0 }
                case .team:
                    TeamPicker(current: store.teamID) { store.teamChoice = .some($0) }
                case .labels:
                    LabelPicker(current: store.tags.map { name in env.directory.tag(named: name)?.ref ?? TagRef(id: "new-" + name, name: name, color: nil, groupID: nil) }) { add, remove in
                        let removed = Set(remove.map { $0.lowercased() })
                        var next = store.tags.filter { !removed.contains($0.lowercased()) }
                        for name in add where !next.contains(where: { $0.caseInsensitiveCompare(name) == .orderedSame }) { next.append(name) }
                        store.tags = next
                    }
                case .severity:
                    SeverityPicker(current: store.severity) { store.severity = $0 }
                case .savedReply:
                    SavedReplyPicker(replies: env.directory.savedReplies) { reply in
                        store.insert(reply)
                        focus = .body
                    }
                }
            }
            .presentationDetents([.medium, .large])
            .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(item: $editingField) { field in
            FieldEditorSheet(field: field, initial: store.fields[field.key], members: env.directory.members) { value in
                if let value, !TicketFieldValue.isEmpty(value, type: field.type) { store.fields[field.key] = value } else { store.fields[field.key] = nil }
            }
            .presentationDetents([.medium, .large])
            .presentationCornerRadius(Theme.Radius.sheet)
        }
        .photosPicker(isPresented: $showPhotos, selection: $photoItems, maxSelectionCount: 6, matching: .any(of: [.images, .screenshots]))
        .onChange(of: photoItems) { _, items in
            guard !items.isEmpty else { return }
            addPhotos(items)
            photoItems = []
        }
        .fileImporter(isPresented: $showFiles, allowedContentTypes: [.item], allowsMultipleSelection: true) { result in
            if case let .success(urls) = result { addFiles(urls) }
        }
        .onChange(of: store.recipient) { _, _ in
            withAnimation(Motion.snappy) { store.loadPast() }
        }
        .onAppear {
            store.start(api: env.session.api, me: env.session.user?.id)
            Task {
                try? await Task.sleep(for: .milliseconds(350))
                focus = .to
            }
        }
    }

    // MARK: To

    private var contactText: Binding<String> {
        Binding(get: { store.contactQuery }, set: { store.searchContacts($0) })
    }

    @ViewBuilder
    private var toRow: some View {
        HStack(spacing: 10) {
            fieldLabel("To")
            if let recipient = store.recipient {
                HStack(spacing: 7) {
                    Avatar(name: recipient.display, seed: recipient.email, size: 20)
                    VStack(alignment: .leading, spacing: 0) {
                        Text(recipient.display).font(ODFont.sans(14.5, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                        if !recipient.name.isEmpty {
                            Text(recipient.email).font(ODFont.sans(12)).foregroundStyle(Theme.fg3).lineLimit(1)
                        }
                    }
                    Spacer(minLength: 4)
                    Button {
                        Haptics.selection()
                        store.recipient = nil
                        store.searchContacts("")
                        focus = .to
                    } label: {
                        Image(systemName: "xmark.circle.fill").font(.system(size: 16)).foregroundStyle(Theme.fg4)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Remove \(recipient.display)")
                }
            } else {
                TextField(text: contactText) { Text("Search contacts or type an email").foregroundStyle(Theme.fg4) }
                    .font(ODFont.sans(15.5))
                    .foregroundStyle(Theme.fg)
                    .keyboardType(.emailAddress)
                    .textContentType(.emailAddress)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .focused($focus, equals: .to)
                    .submitLabel(.next)
                    .onSubmit { useTyped() }
                if store.searchingContacts { ProgressView().controlSize(.mini) }
            }
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 48)
    }

    @ViewBuilder
    private var suggestions: some View {
        let typed = store.contactQuery.trimmingCharacters(in: .whitespaces)
        let known = store.contacts.contains { $0.email?.caseInsensitiveCompare(typed) == .orderedSame }
        VStack(spacing: 0) {
            ForEach(store.contacts.prefix(5)) { contact in
                Button {
                    pick(.contact(contact))
                } label: {
                    HStack(spacing: 10) {
                        Avatar(name: contact.displayName, url: contact.avatarURL, seed: contact.id, size: 26)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(contact.displayName).font(ODFont.sans(14.5, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                            if let email = contact.email, email != contact.displayName {
                                Text(email).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).lineLimit(1)
                            }
                        }
                        Spacer()
                    }
                    .padding(.horizontal, 14)
                    .frame(height: 50)
                    .contentShape(Rectangle())
                }
                .buttonStyle(RowPressStyle())
            }
            if NewTicketStore.isEmail(typed), !known {
                Button {
                    useTyped()
                } label: {
                    HStack(spacing: 10) {
                        Image(systemName: "plus.circle").font(.system(size: 18)).foregroundStyle(Theme.fg2).frame(width: 26)
                        Text("Use \(typed)").font(ODFont.sans(14.5, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                        Spacer()
                        Text("New contact").font(ODFont.sans(12)).foregroundStyle(Theme.fg4)
                    }
                    .padding(.horizontal, 14)
                    .frame(height: 48)
                    .contentShape(Rectangle())
                }
                .buttonStyle(RowPressStyle())
            } else if store.contacts.isEmpty, !store.searchingContacts {
                Text("No contacts. Type a full email to use it.")
                    .font(ODFont.sans(13.5))
                    .foregroundStyle(Theme.fg3)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 14)
                    .frame(height: 44)
            }
        }
    }

    private func useTyped() {
        let typed = store.contactQuery.trimmingCharacters(in: .whitespaces)
        if let contact = store.contacts.first(where: { $0.email?.caseInsensitiveCompare(typed) == .orderedSame }) {
            pick(.contact(contact))
        } else if NewTicketStore.isEmail(typed) {
            pick(.email(typed))
        }
    }

    private func pick(_ recipient: NewTicketRecipient) {
        Haptics.selection()
        store.recipient = recipient
        focus = .subject
    }

    // MARK: From and options

    private var fromRow: some View {
        HStack(spacing: 10) {
            fieldLabel("From")
            Menu {
                ForEach(store.mailboxes) { mailbox in
                    Button {
                        store.mailboxID = mailbox.id
                    } label: {
                        if mailbox.id == store.mailboxID {
                            Label(mailbox.label, systemImage: "checkmark")
                        } else {
                            Text(mailbox.label)
                        }
                    }
                }
            } label: {
                HStack(spacing: 6) {
                    Text(store.mailbox?.label ?? "Pick an address")
                        .font(ODFont.sans(15))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    Image(systemName: "chevron.up.chevron.down").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.fg4)
                }
                .contentShape(Rectangle())
            }
        }
        .padding(.horizontal, 14)
        .frame(height: 48)
    }

    private var options: some View {
        HStack(spacing: 8) {
            optionButton {
                picking = .assignee
            } label: {
                if let member = env.directory.member(store.assigneeID) {
                    Avatar(name: member.user.name, url: member.user.avatarURL, seed: member.id, size: 18)
                    Text(member.id == env.session.user?.id ? "Me" : member.user.name).lineLimit(1)
                } else {
                    Image(systemName: "person.crop.circle.dashed").font(.system(size: 14))
                    Text("Unassigned")
                }
            }
            optionButton {
                picking = .team
            } label: {
                if let team = env.directory.team(store.teamID) {
                    InboxTeamDot(id: team.id, name: team.name)
                    Text(team.name).lineLimit(1)
                } else {
                    InboxTeamDot(id: nil)
                    Text("No team")
                }
            }
            Menu {
                ForEach(Priority.allCases) { priority in
                    Button {
                        store.priority = priority
                    } label: {
                        if priority == store.priority {
                            Label(priority.title, systemImage: "checkmark")
                        } else {
                            Text(priority.title)
                        }
                    }
                }
            } label: {
                chipLabel {
                    PriorityIcon(priority: store.priority, size: 13)
                    Text(store.priority.title)
                }
            }
            Spacer(minLength: 0)
        }
    }

    private func optionButton<Label: View>(action: @escaping () -> Void, @ViewBuilder label: () -> Label) -> some View {
        Button {
            Haptics.tap()
            action()
        } label: {
            chipLabel(label)
        }
        .buttonStyle(TapScaleStyle())
    }

    private func chipLabel<Label: View>(@ViewBuilder _ label: () -> Label) -> some View {
        HStack(spacing: 6) { label() }
            .font(ODFont.sans(13.5, weight: .medium))
            .foregroundStyle(Theme.fg)
            .padding(.horizontal, 10)
            .frame(height: 32)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }

    private func fieldLabel(_ text: String) -> some View {
        Text(text)
            .font(ODFont.sans(14))
            .foregroundStyle(Theme.fg3)
            .frame(width: 58, alignment: .leading)
    }

    // MARK: Saved replies and files

    private var bodyTools: some View {
        HStack(spacing: 4) {
            Menu {
                Button { showPhotos = true } label: { Label("Photos", systemImage: "photo.on.rectangle") }
                Button { showFiles = true } label: { Label("Files", systemImage: "folder") }
            } label: {
                toolIcon("paperclip")
            }
            .accessibilityLabel("Attach files")
            if !env.directory.savedReplies.isEmpty {
                Button {
                    Haptics.tap()
                    picking = .savedReply
                } label: {
                    toolIcon("text.bubble")
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Saved replies")
            }
            Spacer(minLength: 0)
            if store.uploading {
                ProgressView().controlSize(.mini)
                    .padding(.trailing, 8)
            }
        }
        .padding(.horizontal, 6)
        .padding(.bottom, 6)
    }

    private func toolIcon(_ symbol: String) -> some View {
        Image(systemName: symbol)
            .font(.system(size: 15, weight: .medium))
            .foregroundStyle(Theme.fg3)
            .frame(width: 36, height: 34)
            .contentShape(Rectangle())
    }

    private func addPhotos(_ items: [PhotosPickerItem]) {
        for (offset, item) in items.enumerated() {
            let type = item.supportedContentTypes.first { $0.conforms(to: .image) } ?? .jpeg
            let name = "Photo \(store.attachments.count + offset + 1).\(type.preferredFilenameExtension ?? "jpg")"
            store.attachLater(filename: name, mimeType: type.preferredMIMEType ?? "image/jpeg") {
                try? await item.loadTransferable(type: Data.self)
            }
        }
    }

    private func addFiles(_ urls: [URL]) {
        for url in urls {
            let access = url.startAccessingSecurityScopedResource()
            defer { if access { url.stopAccessingSecurityScopedResource() } }
            let data = try? Data(contentsOf: url)
            let type = UTType(filenameExtension: url.pathExtension) ?? .data
            let mime = type.preferredMIMEType ?? "application/octet-stream"
            store.attach(data: data, filename: url.lastPathComponent, mimeType: mime, preview: mime.hasPrefix("image/") ? data.flatMap(NewTicketStore.thumbnail) : nil)
        }
    }

    // MARK: Details

    /// Severity, labels and the ticket's custom fields, set before the ticket exists.
    private var details: some View {
        let fields = env.directory.fields(for: "ticket")
        return VStack(alignment: .leading, spacing: 8) {
            SectionLabel("Details")
            Panel {
                detailRow("Severity", symbol: "exclamationmark.triangle") {
                    picking = .severity
                } value: {
                    if let severity = store.severity {
                        SeverityChip(severity: severity)
                    } else {
                        Text("None").foregroundStyle(Theme.fg4)
                    }
                }
                Hairline(inset: 44)
                detailRow("Labels", symbol: "tag") {
                    picking = .labels
                } value: {
                    if store.tags.isEmpty {
                        Text("None").foregroundStyle(Theme.fg4)
                    } else {
                        HStack(spacing: 4) {
                            ForEach(store.tags.prefix(2), id: \.self) { name in
                                LabelChip(name: name, color: env.directory.tag(named: name)?.color)
                            }
                            if store.tags.count > 2 {
                                Text("+\(store.tags.count - 2)").font(ODFont.mono(11)).foregroundStyle(Theme.fg3)
                            }
                        }
                    }
                }
                ForEach(fields) { field in
                    Hairline(inset: 44)
                    detailRow(field.name, symbol: TicketFieldValue.symbol(field.type)) {
                        editingField = field
                    } value: {
                        if let text = TicketFieldValue.display(store.fields[field.key], field: field, members: env.directory.members) {
                            Text(text).foregroundStyle(Theme.fg).lineLimit(1)
                        } else {
                            Text("Empty").foregroundStyle(Theme.fg4)
                        }
                    }
                }
            }
        }
    }

    private func detailRow<Value: View>(_ title: String, symbol: String, action: @escaping () -> Void, @ViewBuilder value: () -> Value) -> some View {
        Button {
            Haptics.tap()
            action()
        } label: {
            HStack(spacing: 12) {
                Image(systemName: symbol).font(.system(size: 14)).foregroundStyle(Theme.fg3).frame(width: 20)
                Text(title).font(ODFont.sans(15)).foregroundStyle(Theme.fg2).lineLimit(1)
                Spacer(minLength: 8)
                value().font(ODFont.sans(14.5))
                Image(systemName: "chevron.right").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.fg4)
            }
            .padding(.horizontal, 14)
            .frame(minHeight: 46)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
    }

    private func create() async {
        guard let ticket = await store.create() else {
            Haptics.error()
            return
        }
        Haptics.success()
        env.inbox.place(ticket)
        env.directory.scheduleCounts()
        env.toasts.show("Ticket #\(ticket.number) created", kind: .success)
        dismiss()
        onCreated(ticket)
    }
}

/// Who the new ticket goes to: their card, with their company and their earlier tickets.
struct NewTicketCustomerCard: View {
    let contact: Contact
    let tickets: [Ticket]
    let loading: Bool

    var body: some View {
        Panel {
            HStack(spacing: 12) {
                Avatar(name: contact.displayName, url: contact.avatarURL, seed: contact.id, size: 34)
                VStack(alignment: .leading, spacing: 1) {
                    Text(contact.displayName).font(ODFont.sans(15, weight: .semibold)).foregroundStyle(Theme.fg).lineLimit(1)
                    let line = [contact.email, contact.company?.name].compactMap { $0 }.filter { !$0.isEmpty && $0 != contact.displayName }
                    if !line.isEmpty {
                        Text(line.joined(separator: " · ")).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).lineLimit(1)
                    }
                }
                Spacer(minLength: 4)
                if contact.ticketCount > 0 {
                    Text(Format.plural(contact.ticketCount, "ticket")).font(ODFont.sans(12)).foregroundStyle(Theme.fg4)
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 11)
            if loading && tickets.isEmpty {
                Hairline(inset: 14)
                HStack(spacing: 8) {
                    ProgressView().controlSize(.mini)
                    Text("Loading earlier tickets").font(ODFont.sans(13)).foregroundStyle(Theme.fg3)
                }
                .padding(.horizontal, 14)
                .frame(height: 40)
            } else if !tickets.isEmpty {
                Hairline(inset: 14)
                Text("Earlier tickets")
                    .font(ODFont.sans(12, weight: .medium))
                    .foregroundStyle(Theme.fg3)
                    .padding(.horizontal, 14)
                    .padding(.top, 9)
                    .padding(.bottom, 2)
                ForEach(tickets.prefix(5)) { ticket in
                    HStack(spacing: 9) {
                        StatusIcon(status: ticket.status, size: 13)
                        Text(ticket.displaySubject).font(ODFont.sans(14)).foregroundStyle(Theme.fg).lineLimit(1)
                        Spacer(minLength: 4)
                        TicketNumber(number: ticket.number)
                        Text(Format.relShort(ticket.lastMessageAt))
                            .font(ODFont.mono(11))
                            .monospacedDigit()
                            .foregroundStyle(Theme.fg4)
                    }
                    .padding(.horizontal, 14)
                    .frame(height: 34)
                    .accessibilityElement(children: .combine)
                }
                Spacer().frame(height: 6)
            }
        }
        .accessibilityElement(children: .contain)
    }
}
