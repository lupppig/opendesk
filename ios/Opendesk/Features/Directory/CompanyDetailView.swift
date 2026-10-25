import PhotosUI
import SwiftUI

/// One company: its logo and domain, labels, fields and notes, its people,
/// and the latest tickets from any of them. Edits in place and stays live.
struct CompanyDetailView: View {
    let id: String

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var store: CompanyDetailStore
    @State private var editing = false
    @State private var editingNotes = false
    @State private var labeling = false
    @State private var addingPerson = false
    @State private var confirmDelete = false
    @State private var pickingLogo = false
    @State private var logo: PhotosPickerItem?
    @State private var showTitle = false
    @State private var allPeople = false

    init(id: String) {
        self.id = id
        _store = State(initialValue: CompanyDetailStore(id: id))
    }

    private var canWrite: Bool { env.session.canWrite }

    var body: some View {
        Group {
            if let company = store.company {
                content(company)
            } else if let error = store.error {
                ErrorState(title: "Couldn't load this company", message: error) { Task { await store.load() } }
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ContactDetailSkeleton()
            }
        }
        .background(Theme.chrome.ignoresSafeArea())
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .principal) {
                Text(store.company?.name ?? "")
                    .font(ODFont.sans(16, weight: .semibold))
                    .foregroundStyle(Theme.fg)
                    .opacity(showTitle ? 1 : 0)
                    .animation(Motion.fade, value: showTitle)
            }
            if let company = store.company, canWrite {
                ToolbarItem(placement: .topBarTrailing) { menu(company) }
            }
        }
        .onAppear { store.start(api: env.session.api, realtime: env.realtime) }
        .onDisappear { store.stop() }
        .onChange(of: store.deleted) { _, deleted in if deleted { dismiss() } }
        .onChange(of: logo) { _, item in
            guard let item else { return }
            Task { await upload(item) }
        }
        .photosPicker(isPresented: $pickingLogo, selection: $logo, matching: .images, preferredItemEncoding: .compatible)
        .sheet(isPresented: $editing) { editSheet(focusNotes: false) }
        .sheet(isPresented: $editingNotes) { editSheet(focusNotes: true) }
        .sheet(isPresented: $labeling) {
            if let company = store.company {
                DirectoryLabelPicker(entity: .company, selected: company.tags.map(\.name)) { names, known in
                    Task { await run { try await store.setTags(names, known: known) } }
                }
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
            }
        }
        .sheet(isPresented: $addingPerson) {
            if let company = store.company {
                ContactEditSheet(contact: nil, defaultCompany: company.ref) { body in
                    let contact: Contact = try await DirectoryRequests.create(.contact, body, api: env.session.api)
                    env.toasts.show(Toast(message: "\(contact.displayName) added", kind: .success, actionTitle: "Open") {
                        env.router.push(.contact(contact.id))
                    })
                    await store.loadPeople()
                }
                .presentationCornerRadius(Theme.Radius.sheet)
            }
        }
        .confirmationDialog("Delete \(store.company?.name ?? "this company")?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("Delete company", role: .destructive) { Task { await delete() } }
        } message: {
            Text("People stay, but they are no longer linked to this company.")
        }
        #if DEBUG
        .onAppear { runDemoAction(env.router.demoAction) }
        .onChange(of: env.router.demoAction) { _, action in runDemoAction(action) }
        .onChange(of: store.company != nil) { _, _ in runDemoAction(env.router.demoAction) }
        #endif
    }

    // MARK: Content

    private func content(_ company: Company) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 22) {
                hero(company)
                quickActions(company)
                DirectoryStats(items: stats(company))
                details(company)
                let fields = store.fields
                if !fields.isEmpty {
                    VStack(alignment: .leading, spacing: 8) {
                        DirectorySectionHeader(title: "Fields")
                        DirectoryFieldsPanel(fields: fields, values: company.fields, canEdit: canWrite, members: env.directory.members) { key, value in
                            Task { await run { try await store.setField(key, value) } }
                        }
                    }
                }
                VStack(alignment: .leading, spacing: 8) {
                    DirectorySectionHeader(title: "Notes")
                    DirectoryNotes(notes: company.notes, canEdit: canWrite, placeholder: "Add a note your team will see on the company and its people") { editingNotes = true }
                }
                people(company)
                tickets
                if canWrite { deleteZone }
            }
            .padding(.horizontal, 16)
            .padding(.top, 6)
            .padding(.bottom, 48)
            .motion(Motion.snappy, value: company)
            .motion(Motion.snappy, value: store.people.map(\.id))
        }
        .onScrollGeometryChange(for: Bool.self) { geometry in
            geometry.contentOffset.y + geometry.contentInsets.top > 150
        } action: { _, past in
            showTitle = past
        }
        .refreshable { await store.refresh() }
    }

    private func hero(_ company: Company) -> some View {
        VStack(spacing: 12) {
            ZStack(alignment: .bottomTrailing) {
                DirectoryAvatar(name: company.name, url: company.avatarURL, seed: company.id, size: 84, square: true)
                    .overlay {
                        if store.avatarBusy {
                            RoundedRectangle(cornerRadius: 84 * 0.26, style: .continuous).fill(.black.opacity(0.35))
                            ProgressView().tint(.white)
                        }
                    }
                    .shadow(color: .black.opacity(0.06), radius: 10, y: 4)
                if canWrite { logoMenu(company) }
            }
            .padding(.bottom, 2)

            VStack(spacing: 4) {
                Text(company.name)
                    .font(ODFont.sans(24, weight: .semibold))
                    .tracking(-0.6)
                    .foregroundStyle(Theme.fg)
                    .multilineTextAlignment(.center)
                    .textSelection(.enabled)
                if !company.domain.isEmpty {
                    Button { DirectoryActions.open(DirectoryActions.website(company.domain)) } label: {
                        HStack(spacing: 4) {
                            Text(company.domain).font(ODFont.mono(13.5))
                            Image(systemName: "arrow.up.right").font(.system(size: 10, weight: .semibold))
                        }
                        .foregroundStyle(Theme.fg3)
                    }
                    .buttonStyle(TapScaleStyle())
                    .contextMenu {
                        Button { DirectoryActions.copy(company.domain, what: "Domain", toasts: env.toasts) } label: { Label("Copy domain", systemImage: "doc.on.doc") }
                    }
                }
            }

            if !company.tags.isEmpty || canWrite {
                DirectoryLabelsRow(tags: company.tags, canEdit: canWrite, alignment: .center) { labeling = true }
                    .frame(maxWidth: 320)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 8)
    }

    private func logoMenu(_ company: Company) -> some View {
        Menu {
            Button { pickingLogo = true } label: {
                Label(company.avatarSource == "upload" ? "Choose another logo" : "Choose logo", systemImage: "photo")
            }
            if company.avatarSource == "upload" {
                Button(role: .destructive) { Task { await removeLogo() } } label: { Label("Remove logo", systemImage: "trash") }
            }
        } label: {
            Image(systemName: "camera.fill")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(Theme.fg)
                .frame(width: 30, height: 30)
                .glassEffect(.regular.interactive(), in: Circle())
        }
        .offset(x: 6, y: 4)
        .accessibilityLabel("Change logo")
        .disabled(store.avatarBusy)
    }

    private func quickActions(_ company: Company) -> some View {
        HStack(spacing: 8) {
            DirectoryQuickAction(title: "Website", symbol: "safari", enabled: !company.domain.isEmpty) {
                DirectoryActions.open(DirectoryActions.website(company.domain))
            }
            DirectoryQuickAction(title: "Copy domain", symbol: "doc.on.doc", enabled: !company.domain.isEmpty) {
                DirectoryActions.copy(company.domain, what: "Domain", toasts: env.toasts)
            }
            DirectoryQuickAction(title: "Add person", symbol: "person.badge.plus", enabled: canWrite) {
                addingPerson = true
            }
        }
    }

    private func stats(_ company: Company) -> [DirectoryStats.Item] {
        let open = store.tickets.filter { !$0.status.isDone }.count
        let people = store.peopleLoaded ? store.people.count : company.contactCount
        let tickets = store.people.reduce(0) { $0 + $1.ticketCount }
        return [
            .init(label: "People", value: "\(max(people, company.contactCount))"),
            .init(label: "Tickets", value: store.peopleLoaded ? "\(tickets)" : "–"),
            .init(label: "Open", value: store.ticketsLoaded ? "\(open)" : "–", tone: open > 0 ? .amber : nil),
        ]
    }

    private func details(_ company: Company) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            DirectorySectionHeader(title: "Details") {
                if canWrite {
                    Button("Edit") { editing = true }
                        .font(ODFont.sans(13, weight: .medium))
                        .foregroundStyle(Theme.fg2)
                }
            }
            Panel {
                DirectoryInfoRow(label: "Domain", symbol: "globe", action: company.domain.isEmpty ? nil : { DirectoryActions.open(DirectoryActions.website(company.domain)) }) {
                    if company.domain.isEmpty { DirectoryNone() } else {
                        Text(company.domain).font(ODFont.mono(13.5)).foregroundStyle(Theme.fg).lineLimit(1)
                    }
                }
                Hairline(inset: 46)
                DirectoryInfoRow(label: "Created", symbol: "calendar") {
                    Text(company.createdAt.formatted(date: .long, time: .omitted)).font(ODFont.sans(14.5)).foregroundStyle(Theme.fg2).lineLimit(1)
                }
            }
            if !company.domain.isEmpty {
                Text("Contacts with this email domain can be matched to the company.")
                    .font(ODFont.footnote).foregroundStyle(Theme.fg3).padding(.horizontal, 4)
            }
        }
    }

    // MARK: People and tickets

    private func people(_ company: Company) -> some View {
        let shown = allPeople ? store.people : Array(store.people.prefix(6))
        return VStack(alignment: .leading, spacing: 8) {
            DirectorySectionHeader(title: "People", count: store.peopleLoaded ? store.people.count : nil) {
                if canWrite {
                    Button { addingPerson = true } label: { Label("Add", systemImage: "plus").labelStyle(.titleAndIcon) }
                        .font(ODFont.sans(13, weight: .medium))
                        .foregroundStyle(Theme.fg2)
                }
            }
            if !store.peopleLoaded {
                Panel { SkeletonRows(count: 3) }
            } else if store.people.isEmpty {
                Panel(padding: 14) {
                    HStack(spacing: 9) {
                        Image(systemName: "person.2").font(.system(size: 14)).foregroundStyle(Theme.fg4)
                        Text("Nobody is linked to this company yet").font(ODFont.sans(14)).foregroundStyle(Theme.fg3)
                    }
                }
            } else {
                Panel {
                    ForEach(Array(shown.enumerated()), id: \.element.id) { _, person in
                        Button { env.router.push(.contact(person.id)) } label: {
                            CompanyPersonRow(contact: person)
                        }
                        .buttonStyle(RowPressStyle())
                    }
                    if store.people.count > 6 {
                        Button {
                            allPeople.toggle()
                        } label: {
                            HStack {
                                Text(allPeople ? "Show fewer" : "Show all \(store.people.count)").font(ODFont.sans(14, weight: .medium))
                                Spacer()
                                Image(systemName: allPeople ? "chevron.up" : "chevron.down").font(.system(size: 11, weight: .semibold))
                            }
                            .foregroundStyle(Theme.fg2)
                            .padding(.horizontal, 14)
                            .frame(height: 44)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(RowPressStyle())
                    }
                }
                if store.morePeople {
                    Text("Showing the first 100 people").font(ODFont.footnote).foregroundStyle(Theme.fg3).padding(.horizontal, 4)
                }
            }
        }
    }

    private var tickets: some View {
        VStack(alignment: .leading, spacing: 8) {
            DirectorySectionHeader(title: "Recent tickets", count: store.ticketsLoaded && !store.tickets.isEmpty ? store.tickets.count : nil)
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
            }
        }
    }

    private var deleteZone: some View {
        Panel {
            Button { confirmDelete = true } label: {
                HStack(spacing: 12) {
                    Image(systemName: "trash").font(.system(size: 14)).frame(width: 20)
                    Text("Delete company").font(ODFont.sans(15, weight: .medium))
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

    private func menu(_ company: Company) -> some View {
        Menu {
            Button { editing = true } label: { Label("Edit company", systemImage: "pencil") }
            Button { labeling = true } label: { Label("Labels", systemImage: "tag") }
            Button { pickingLogo = true } label: { Label("Change logo", systemImage: "photo") }
            Button { addingPerson = true } label: { Label("Add person", systemImage: "person.badge.plus") }
            if !company.domain.isEmpty {
                Divider()
                Button { DirectoryActions.copy(company.domain, what: "Domain", toasts: env.toasts) } label: { Label("Copy domain", systemImage: "doc.on.doc") }
            }
            Divider()
            Button(role: .destructive) { confirmDelete = true } label: { Label("Delete company", systemImage: "trash") }
        } label: {
            Image(systemName: "ellipsis")
        }
        .accessibilityLabel("More")
    }

    @ViewBuilder
    private func editSheet(focusNotes: Bool) -> some View {
        if let company = store.company {
            CompanyEditSheet(company: company, focusNotes: focusNotes, onSave: { body in
                try await store.save(body)
                env.toasts.show("Company saved", kind: .success)
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

    private func delete() async {
        await run {
            try await store.delete()
            Haptics.success()
            env.toasts.show("Company deleted", kind: .success)
        }
    }

    private func upload(_ item: PhotosPickerItem) async {
        defer { logo = nil }
        do {
            guard let data = try await item.loadTransferable(type: Data.self) else { return }
            let (body, type) = ContactDetailView.prepare(data)
            try await store.uploadAvatar(body, mimeType: type)
            Haptics.success()
            env.toasts.show("Logo updated", kind: .success)
        } catch {
            env.toasts.error(error)
        }
    }

    private func removeLogo() async {
        await run {
            try await store.removeAvatar()
            env.toasts.show("Logo removed", kind: .success)
        }
    }

    #if DEBUG
    private func runDemoAction(_ action: String?) {
        guard store.company != nil, let action else { return }
        switch action {
        case "company-edit": editing = true
        case "company-notes": editingNotes = true
        case "company-labels": labeling = true
        case "company-delete": confirmDelete = true
        case "company-add-person": addingPerson = true
        default: return
        }
        env.router.demoAction = nil
    }
    #endif
}

/// One of a company's people: avatar, name, title or email, and their ticket count.
struct CompanyPersonRow: View {
    let contact: Contact

    private var line: String {
        let title = contact.fields["job_title"]?.string ?? ""
        return title.isEmpty ? (contact.email ?? "") : title
    }

    var body: some View {
        HStack(spacing: 12) {
            DirectoryAvatar(name: contact.displayName, url: contact.avatarURL, seed: contact.id, size: 32)
                .opacity(contact.blocked ? 0.55 : 1)
            VStack(alignment: .leading, spacing: 1) {
                Text(contact.displayName)
                    .font(ODFont.sans(15, weight: .medium))
                    .foregroundStyle(contact.blocked ? Theme.fg3 : Theme.fg)
                    .lineLimit(1)
                if !line.isEmpty {
                    Text(line).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).lineLimit(1)
                }
            }
            Spacer(minLength: 8)
            if contact.blocked {
                TonePill(text: "Blocked", tone: .red, height: 18) { EmptyView() }
            } else if contact.ticketCount > 0 {
                HStack(spacing: 3) {
                    Image(systemName: "tray").font(.system(size: 10, weight: .medium))
                    Text("\(contact.ticketCount)").font(ODFont.mono(11.5)).monospacedDigit()
                }
                .foregroundStyle(Theme.fg3)
            }
            Chevron()
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 9)
        .overlay(alignment: .bottom) { Hairline(inset: 58) }
        .contentShape(Rectangle())
    }
}
