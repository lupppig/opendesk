import PhotosUI
import SwiftUI

/// One contact: who they are, how to reach them, their labels, fields and
/// notes, and their recent tickets. Everything edits in place and stays live.
struct ContactDetailView: View {
    let id: String

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var store: ContactDetailStore
    @State private var editing = false
    @State private var editingNotes = false
    @State private var labeling = false
    @State private var confirmDelete = false
    @State private var confirmBlock = false
    @State private var pickingPhoto = false
    @State private var photo: PhotosPickerItem?
    @State private var showTitle = false

    init(id: String) {
        self.id = id
        _store = State(initialValue: ContactDetailStore(id: id))
    }

    private var canWrite: Bool { env.session.canWrite }

    var body: some View {
        Group {
            if let contact = store.contact {
                content(contact)
            } else if let error = store.error {
                ErrorState(title: "Couldn't load this contact", message: error) { Task { await store.load() } }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ContactDetailSkeleton()
            }
        }
        .background(Theme.chrome.ignoresSafeArea())
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                Text(store.contact?.displayName ?? "")
                    .font(ODFont.sans(16, weight: .semibold))
                    .foregroundStyle(Theme.fg)
                    .opacity(showTitle ? 1 : 0)
                    .animation(Motion.fade, value: showTitle)
            }
            if let contact = store.contact, canWrite {
                ToolbarItem(placement: .topBarTrailing) { menu(contact) }
            }
        }
        .onAppear { store.start(api: env.session.api, realtime: env.realtime) }
        .onDisappear { store.stop() }
        .onChange(of: store.deleted) { _, deleted in if deleted { dismiss() } }
        .onChange(of: photo) { _, item in
            guard let item else { return }
            Task { await upload(item) }
        }
        .photosPicker(isPresented: $pickingPhoto, selection: $photo, matching: .images, preferredItemEncoding: .compatible)
        .sheet(isPresented: $editing) { editSheet(focusNotes: false) }
        .sheet(isPresented: $editingNotes) { editSheet(focusNotes: true) }
        .sheet(isPresented: $labeling) {
            if let contact = store.contact {
                DirectoryLabelPicker(entity: .contact, selected: contact.tags.map(\.name)) { names, known in
                    Task { await run { try await store.setTags(names, known: known) } }
                }
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
            }
        }
        .confirmationDialog("Delete \(store.contact?.displayName ?? "this contact")?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("Delete contact", role: .destructive) { Task { await delete() } }
        } message: {
            Text("Their tickets stay, but the contact record and its notes are removed.")
        }
        .confirmationDialog("Block \(store.contact?.displayName ?? "this contact")?", isPresented: $confirmBlock, titleVisibility: .visible) {
            Button("Block contact", role: .destructive) { Task { await setBlocked(true) } }
        } message: {
            Text("New emails and chats from them go to spam.")
        }
        #if DEBUG
        .onAppear { runDemoAction(env.router.demoAction) }
        .onChange(of: env.router.demoAction) { _, action in runDemoAction(action) }
        .onChange(of: store.contact != nil) { _, _ in runDemoAction(env.router.demoAction) }
        #endif
    }

    // MARK: Content

    private func content(_ contact: Contact) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                hero(contact)
                if contact.blocked { blockedBanner }
                quickActions(contact)
                DirectoryStats(items: stats(contact))
                details(contact)
                let fields = store.fields
                if !fields.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        DirectorySectionHeader(title: "Fields")
                        DirectoryFieldsPanel(fields: fields, values: contact.fields, canEdit: canWrite, members: env.directory.members) { key, value in
                            Task { await run { try await store.setField(key, value) } }
                        }
                    }
                }
                VStack(alignment: .leading, spacing: 8) {
                    DirectorySectionHeader(title: "Notes")
                    DirectoryNotes(notes: contact.notes, canEdit: canWrite) { editingNotes = true }
                }
                tickets(contact)
                if canWrite { dangerZone(contact) }
            }
            .padding(.horizontal, 16)
            .padding(.top, 6)
            .padding(.bottom, 48)
            .motion(Motion.snappy, value: contact)
        }
        .onScrollGeometryChange(for: Bool.self) { geometry in
            geometry.contentOffset.y + geometry.contentInsets.top > 150
        } action: { _, past in
            showTitle = past
        }
        .refreshable { await store.refresh() }
    }

    // MARK: Hero

    private func hero(_ contact: Contact) -> some View {
        VStack(spacing: 12) {
            ZStack(alignment: .bottomTrailing) {
                DirectoryAvatar(name: contact.displayName, url: contact.avatarURL, seed: contact.id, size: 88)
                    .overlay {
                        if store.avatarBusy {
                            Circle().fill(.black.opacity(0.35))
                            ProgressView().tint(.white)
                        }
                    }
                    .opacity(contact.blocked ? 0.6 : 1)
                if canWrite { photoMenu(contact) }
            }
            .padding(.bottom, 2)

            VStack(spacing: 4) {
                Text(contact.displayName)
                    .font(ODFont.sans(24, weight: .semibold))
                    .tracking(-0.6)
                    .foregroundStyle(Theme.fg)
                    .multilineTextAlignment(.center)
                    .textSelection(.enabled)
                subtitle(contact)
            }

            if !contact.tags.isEmpty || canWrite {
                DirectoryLabelsRow(tags: contact.tags, canEdit: canWrite, alignment: .center) { labeling = true }
                    .frame(maxWidth: 320)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 8)
    }

    @ViewBuilder
    private func subtitle(_ contact: Contact) -> some View {
        let title = contact.fields["job_title"]?.string ?? ""
        HStack(spacing: 4) {
            if !title.isEmpty {
                Text(title).foregroundStyle(Theme.fg2)
            }
            if let company = contact.company {
                if !title.isEmpty { Text("at").foregroundStyle(Theme.fg3) }
                Button { env.router.push(.company(company.id)) } label: {
                    HStack(spacing: 5) {
                        DirectoryAvatar(name: company.name, url: company.avatarURL, seed: company.id, size: 16, square: true)
                        Text(company.name).foregroundStyle(Theme.fg).underline(color: Theme.lineStrong)
                    }
                }
                .buttonStyle(TapScaleStyle())
            } else if title.isEmpty, let email = contact.email, contact.name.isEmpty == false {
                Text(email).foregroundStyle(Theme.fg3)
            }
        }
        .font(ODFont.sans(14.5))
        .lineLimit(1)
    }

    private func photoMenu(_ contact: Contact) -> some View {
        Menu {
            Button { pickingPhoto = true } label: {
                Label(contact.avatarSource == "upload" ? "Choose another photo" : "Choose photo", systemImage: "photo")
            }
            if contact.avatarSource == "upload" {
                Button(role: .destructive) { Task { await removeAvatar() } } label: { Label("Remove photo", systemImage: "trash") }
            }
        } label: {
            Image(systemName: "camera.fill")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(Theme.fg)
                .frame(width: 30, height: 30)
                .glassEffect(.regular.interactive(), in: Circle())
        }
        .offset(x: 4, y: 2)
        .accessibilityLabel("Change photo")
        .disabled(store.avatarBusy)
    }

    private var blockedBanner: some View {
        HStack(spacing: 10) {
            Image(systemName: "hand.raised.fill").font(.system(size: 14, weight: .semibold)).foregroundStyle(Tone.red.solid)
            VStack(alignment: .leading, spacing: 1) {
                Text("Blocked").font(ODFont.sans(14.5, weight: .semibold)).foregroundStyle(Tone.red.text)
                Text("New emails and chats from them go to spam.").font(ODFont.sans(13)).foregroundStyle(Tone.red.text.opacity(0.85))
            }
            Spacer(minLength: 8)
            if canWrite {
                Button("Unblock") { Task { await setBlocked(false) } }
                    .buttonStyle(ChipButtonStyle(tone: .red))
            }
        }
        .padding(12)
        .background(Tone.red.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Tone.red.line, lineWidth: 1))
        .transition(.opacity.combined(with: .scale(scale: 0.97)))
    }

    private func quickActions(_ contact: Contact) -> some View {
        HStack(spacing: 8) {
            DirectoryQuickAction(title: "Email", symbol: "envelope", enabled: contact.email != nil) {
                if let email = contact.email { DirectoryActions.open("mailto:" + email) }
            }
            DirectoryQuickAction(title: "Call", symbol: "phone", enabled: !contact.phone.isEmpty) {
                DirectoryActions.open(DirectoryActions.telURL(contact.phone))
            }
            DirectoryQuickAction(title: "Copy email", symbol: "doc.on.doc", enabled: contact.email != nil) {
                if let email = contact.email { DirectoryActions.copy(email, what: "Email", toasts: env.toasts) }
            }
            DirectoryQuickAction(title: "New ticket", symbol: "square.and.pencil", enabled: canWrite && contact.email != nil) {
                newTicket(contact)
            }
        }
    }

    private func stats(_ contact: Contact) -> [DirectoryStats.Item] {
        let open = store.tickets.filter { !$0.status.isDone }.count
        return [
            .init(label: "Tickets", value: "\(contact.ticketCount)"),
            .init(label: "Open", value: store.ticketsLoaded ? "\(open)" : "–", tone: open > 0 ? .amber : nil),
            .init(label: contact.blocked ? "First seen" : "Customer since", value: contact.createdAt.formatted(.dateTime.month(.abbreviated).year())),
        ]
    }

    // MARK: Details

    private func details(_ contact: Contact) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            DirectorySectionHeader(title: "Details") {
                if canWrite {
                    Button("Edit") { editing = true }
                        .font(ODFont.sans(13, weight: .medium))
                        .foregroundStyle(Theme.fg2)
                }
            }
            Panel {
                DirectoryInfoRow(label: "Email", symbol: "envelope", action: contact.email.map { email in { DirectoryActions.open("mailto:" + email) } }) {
                    if let email = contact.email {
                        Text(email).font(ODFont.sans(14.5)).foregroundStyle(Theme.fg).lineLimit(1).truncationMode(.middle)
                    } else {
                        DirectoryNone()
                    }
                }
                .contextMenu { if let email = contact.email { copyButton(email, what: "Email") } }
                Hairline(inset: 46)
                DirectoryInfoRow(label: "Phone", symbol: "phone", action: contact.phone.isEmpty ? nil : { DirectoryActions.open(DirectoryActions.telURL(contact.phone)) }) {
                    if contact.phone.isEmpty { DirectoryNone() } else {
                        Text(contact.phone).font(ODFont.sans(14.5)).monospacedDigit().foregroundStyle(Theme.fg).lineLimit(1)
                    }
                }
                .contextMenu { if !contact.phone.isEmpty { copyButton(contact.phone, what: "Phone number") } }
                Hairline(inset: 46)
                DirectoryInfoRow(
                    label: "Company",
                    symbol: "building.2",
                    action: contact.company.map { company in { env.router.push(.company(company.id)) } },
                    showsChevron: contact.company != nil
                ) {
                    if let company = contact.company {
                        HStack(spacing: 7) {
                            DirectoryAvatar(name: company.name, url: company.avatarURL, seed: company.id, size: 20, square: true)
                            Text(company.name).font(ODFont.sans(14.5, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                        }
                    } else {
                        DirectoryNone()
                    }
                }
                Hairline(inset: 46)
                DirectoryInfoRow(label: "External id", symbol: "number") {
                    if let external = contact.externalID, !external.isEmpty {
                        Text(external).font(ODFont.mono(13)).foregroundStyle(Theme.fg).lineLimit(1).textSelection(.enabled)
                    } else {
                        DirectoryNone()
                    }
                }
                .contextMenu { if let external = contact.externalID, !external.isEmpty { copyButton(external, what: "External id") } }
                Hairline(inset: 46)
                DirectoryInfoRow(label: "Created", symbol: "calendar") {
                    Text(contact.createdAt.formatted(date: .long, time: .omitted)).font(ODFont.sans(14.5)).foregroundStyle(Theme.fg2).lineLimit(1)
                }
            }
        }
    }

    private func copyButton(_ text: String, what: String) -> some View {
        Button { DirectoryActions.copy(text, what: what, toasts: env.toasts) } label: { Label("Copy", systemImage: "doc.on.doc") }
    }

    // MARK: Tickets

    private func tickets(_ contact: Contact) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            DirectorySectionHeader(title: "Recent tickets", count: contact.ticketCount > 0 ? contact.ticketCount : nil)
            if !store.ticketsLoaded {
                Panel { SkeletonRows(count: 2) }
            } else if store.tickets.isEmpty {
                Panel(padding: 14) {
                    HStack(spacing: 9) {
                        Image(systemName: "tray").font(.system(size: 14)).foregroundStyle(Theme.fg4)
                        Text("No tickets yet").font(ODFont.sans(14)).foregroundStyle(Theme.fg3)
                    }
                }
            } else {
                Panel {
                    ForEach(Array(store.tickets.prefix(8).enumerated()), id: \.element.id) { index, ticket in
                        if index > 0 { Hairline() }
                        Button { env.router.openTicket(ticket) } label: { TicketRow(ticket: ticket) }
                            .buttonStyle(RowPressStyle())
                    }
                }
                if store.tickets.count > 8 || store.moreTickets {
                    Text("Showing the latest \(min(store.tickets.count, 8)) of \(Format.plural(max(contact.ticketCount, store.tickets.count), "ticket"))")
                        .font(ODFont.footnote).foregroundStyle(Theme.fg3).padding(.horizontal, 4)
                }
            }
        }
    }

    // MARK: Danger zone

    private func dangerZone(_ contact: Contact) -> some View {
        Panel {
            Button {
                if contact.blocked { Task { await setBlocked(false) } } else { confirmBlock = true }
            } label: {
                HStack(spacing: 12) {
                    Image(systemName: contact.blocked ? "hand.raised.slash" : "hand.raised").font(.system(size: 14)).frame(width: 20)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(contact.blocked ? "Unblock contact" : "Block contact").font(ODFont.sans(15, weight: .medium))
                        Text(contact.blocked ? "Their emails and chats reach the inbox again." : "New emails and chats from them go to spam.")
                            .font(ODFont.footnote).foregroundStyle(Theme.fg3)
                    }
                    Spacer()
                }
                .foregroundStyle(Theme.fg)
                .padding(.horizontal, 14)
                .padding(.vertical, 11)
                .contentShape(Rectangle())
            }
            .buttonStyle(RowPressStyle())
            Hairline(inset: 46)
            Button { confirmDelete = true } label: {
                HStack(spacing: 12) {
                    Image(systemName: "trash").font(.system(size: 14)).frame(width: 20)
                    Text("Delete contact").font(ODFont.sans(15, weight: .medium))
                    Spacer()
                }
                .foregroundStyle(Tone.red.text)
                .padding(.horizontal, 14)
                .frame(height: 48)
                .contentShape(Rectangle())
            }
            .buttonStyle(RowPressStyle())
        }
    }

    // MARK: Menu and sheets

    private func menu(_ contact: Contact) -> some View {
        Menu {
            Button { editing = true } label: { Label("Edit contact", systemImage: "pencil") }
            Button { labeling = true } label: { Label("Labels", systemImage: "tag") }
            Button { pickingPhoto = true } label: { Label("Change photo", systemImage: "photo") }
            if let email = contact.email {
                Divider()
                Button { DirectoryActions.copy(email, what: "Email", toasts: env.toasts) } label: { Label("Copy email", systemImage: "doc.on.doc") }
            }
            Divider()
            Button {
                if contact.blocked { Task { await setBlocked(false) } } else { confirmBlock = true }
            } label: {
                Label(contact.blocked ? "Unblock" : "Block", systemImage: contact.blocked ? "hand.raised.slash" : "hand.raised")
            }
            Button(role: .destructive) { confirmDelete = true } label: { Label("Delete contact", systemImage: "trash") }
        } label: {
            Image(systemName: "ellipsis")
        }
        .accessibilityLabel("More")
    }

    @ViewBuilder
    private func editSheet(focusNotes: Bool) -> some View {
        if let contact = store.contact {
            ContactEditSheet(contact: contact, focusNotes: focusNotes, onSave: { body in
                try await store.save(body)
                env.toasts.show("Contact saved", kind: .success)
            }, onDelete: { Task { await delete() } })
            .presentationCornerRadius(Theme.Radius.sheet)
        }
    }

    // MARK: Actions

    private func run(_ work: () async throws -> Void) async {
        do {
            try await work()
        } catch {
            env.toasts.error(error)
        }
    }

    private func setBlocked(_ blocked: Bool) async {
        Haptics.tap()
        await run {
            try await store.setBlocked(blocked)
            env.toasts.show(blocked ? "Contact blocked" : "Contact unblocked", kind: .success)
        }
    }

    private func delete() async {
        await run {
            try await store.delete()
            Haptics.success()
            env.toasts.show("Contact deleted", kind: .success)
        }
    }

    private func upload(_ item: PhotosPickerItem) async {
        defer { photo = nil }
        do {
            guard let data = try await item.loadTransferable(type: Data.self) else { return }
            let (body, type) = Self.prepare(data)
            try await store.uploadAvatar(body, mimeType: type)
            Haptics.success()
            env.toasts.show("Photo updated", kind: .success)
        } catch {
            env.toasts.error(error)
        }
    }

    private func removeAvatar() async {
        await run {
            try await store.removeAvatar()
            env.toasts.show("Photo removed", kind: .success)
        }
    }

    /// Scales a picked photo down to 512 px and sends it as JPEG.
    static func prepare(_ data: Data) -> (Data, String) {
        guard let image = UIImage(data: data) else { return (data, "image/jpeg") }
        let side: CGFloat = 512
        let scale = min(1, side / max(image.size.width, image.size.height))
        let size = CGSize(width: image.size.width * scale, height: image.size.height * scale)
        let format = UIGraphicsImageRendererFormat.default()
        format.scale = 1
        let resized = UIGraphicsImageRenderer(size: size, format: format).image { _ in image.draw(in: CGRect(origin: .zero, size: size)) }
        return (resized.jpegData(compressionQuality: 0.85) ?? data, "image/jpeg")
    }

    private func newTicket(_ contact: Contact) {
        Haptics.tap()
        env.toasts.show(Toast(message: "New tickets start from the web app for now", kind: .info, actionTitle: contact.email == nil ? nil : "Copy email") {
            if let email = contact.email { DirectoryActions.copy(email, what: "Email", toasts: env.toasts) }
        })
    }

    #if DEBUG
    private func runDemoAction(_ action: String?) {
        guard store.contact != nil, let action else { return }
        switch action {
        case "contact-edit": editing = true
        case "contact-notes": editingNotes = true
        case "contact-labels": labeling = true
        case "contact-delete": confirmDelete = true
        case "contact-block": confirmBlock = true
        default: return
        }
        env.router.demoAction = nil
    }
    #endif
}

/// The contact screen's shape while it loads.
struct ContactDetailSkeleton: View {
    var body: some View {
        VStack(spacing: 14) {
            Circle().fill(Theme.active).frame(width: 88, height: 88)
            RoundedRectangle(cornerRadius: 6).fill(Theme.active).frame(width: 170, height: 20)
            RoundedRectangle(cornerRadius: 6).fill(Theme.hover).frame(width: 120, height: 14)
            HStack(spacing: 8) {
                ForEach(0 ..< 4, id: \.self) { _ in
                    RoundedRectangle(cornerRadius: Theme.Radius.control + 2).fill(Theme.surface).frame(height: 62)
                }
            }
            .padding(.top, 12)
            Panel { SkeletonRows(count: 4) }
        }
        .padding(.horizontal, 16)
        .padding(.top, 14)
        .frame(maxHeight: .infinity, alignment: .top)
    }
}
