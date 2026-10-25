import SwiftUI

// MARK: - Contact

/// Creates a contact or edits one: name, email, phone, company, external id,
/// notes and (editing) blocked. Sends only what changed.
struct ContactEditSheet: View {
    var contact: Contact?
    var defaultCompany: CompanyRef?
    var focusNotes = false
    let onSave: (DirectoryPatch) async throws -> Void
    var onDelete: (() -> Void)?

    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var email = ""
    @State private var phone = ""
    @State private var company: CompanyRef?
    @State private var externalID = ""
    @State private var notes = ""
    @State private var blocked = false
    @State private var error: String?
    @State private var saving = false
    @State private var pickingCompany = false
    @State private var confirmDiscard = false
    @State private var confirmDelete = false
    @State private var filled = false
    @FocusState private var notesFocused: Bool

    private var isNew: Bool { contact == nil }

    private var dirty: Bool {
        guard let contact else {
            return !name.isEmpty || !email.isEmpty || !phone.isEmpty || !notes.isEmpty || !externalID.isEmpty || company?.id != defaultCompany?.id
        }
        return name != contact.name || email != (contact.email ?? "") || phone != contact.phone || company?.id != contact.company?.id
            || externalID != (contact.externalID ?? "") || notes != contact.notes || blocked != contact.blocked
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    ODTextField(label: "Name", text: $name, prompt: "Ada Lovelace", contentType: .name)
                    ODTextField(label: "Email", text: $email, prompt: "ada@example.com", contentType: .emailAddress, keyboard: .emailAddress)
                    ODTextField(label: "Phone", text: $phone, prompt: "+44 20 7946 0000", contentType: .telephoneNumber, keyboard: .phonePad)
                    companyField
                    ODTextField(label: "External id", text: $externalID, prompt: "Your system's id for them", keyboard: .asciiCapable)
                    notesField
                    if !isNew { blockField }
                    FormError(text: error)
                    if !isNew, onDelete != nil {
                        Button(role: .destructive) { confirmDelete = true } label: {
                            Label("Delete contact", systemImage: "trash")
                        }
                        .buttonStyle(OutlineButtonStyle(tint: Tone.red.text))
                        .padding(.top, 8)
                    }
                }
                .padding(20)
                .motion(Motion.snappy, value: error)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle(isNew ? "New contact" : "Edit contact")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { if dirty { confirmDiscard = true } else { dismiss() } }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if saving {
                        ProgressView()
                    } else {
                        Button(isNew ? "Create" : "Save") { Task { await save() } }
                            .fontWeight(.semibold)
                            .disabled(!dirty && !isNew)
                    }
                }
            }
            .confirmationDialog("Discard your changes?", isPresented: $confirmDiscard, titleVisibility: .visible) {
                Button("Discard changes", role: .destructive) { dismiss() }
                Button("Keep editing", role: .cancel) {}
            }
            .confirmationDialog("Delete \(contact?.displayName ?? "this contact")?", isPresented: $confirmDelete, titleVisibility: .visible) {
                Button("Delete contact", role: .destructive) {
                    dismiss()
                    onDelete?()
                }
            } message: {
                Text("Their tickets stay, but the contact record and its notes are removed.")
            }
            .sheet(isPresented: $pickingCompany) {
                CompanyPickerSheet(selected: company) { company = $0 }
                    .presentationDetents([.medium, .large])
                    .presentationCornerRadius(Theme.Radius.sheet)
            }
        }
        .interactiveDismissDisabled(dirty)
        .onAppear(perform: fill)
    }

    private var companyField: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Company").font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg2)
            Button { pickingCompany = true } label: {
                HStack(spacing: 10) {
                    if let company {
                        DirectoryAvatar(name: company.name, url: company.avatarURL, seed: company.id, size: 24, square: true)
                        Text(company.name).font(ODFont.sans(16)).foregroundStyle(Theme.fg).lineLimit(1)
                        if !company.domain.isEmpty {
                            Text(company.domain).font(ODFont.mono(12.5)).foregroundStyle(Theme.fg3).lineLimit(1)
                        }
                    } else {
                        Text("No company").font(ODFont.sans(16)).foregroundStyle(Theme.fg4)
                    }
                    Spacer()
                    Image(systemName: "chevron.up.chevron.down").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.fg4)
                }
                .padding(.horizontal, 13)
                .frame(height: 46)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                .contentShape(Rectangle())
            }
            .buttonStyle(TapScaleStyle(scale: 0.99))
        }
    }

    private var notesField: some View {
        DirectoryNotesField(text: $notes, focused: $notesFocused, hint: "Your team sees this on every ticket from this contact.")
    }

    private var blockField: some View {
        HStack(spacing: 12) {
            VStack(alignment: .leading, spacing: 2) {
                Text("Block contact").font(ODFont.sans(15, weight: .medium)).foregroundStyle(Theme.fg)
                Text("New emails and chats from them go to spam.").font(ODFont.footnote).foregroundStyle(Theme.fg3)
            }
            Spacer()
            Toggle("", isOn: $blocked).labelsHidden().tint(Tone.red.solid)
        }
        .padding(14)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(blocked ? Tone.red.line : Theme.line, lineWidth: 1))
    }

    private func fill() {
        guard !filled else { return }
        filled = true
        if let contact {
            name = contact.name
            email = contact.email ?? ""
            phone = contact.phone
            company = contact.company
            externalID = contact.externalID ?? ""
            notes = contact.notes
            blocked = contact.blocked
        } else {
            company = defaultCompany
        }
        if focusNotes {
            Task {
                try? await Task.sleep(for: .milliseconds(350))
                notesFocused = true
            }
        }
    }

    private func save() async {
        let cleanName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        let cleanEmail = email.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanName.isEmpty || !cleanEmail.isEmpty else {
            error = isNew ? "Add a name or an email." : "A contact needs a name or an email."
            Haptics.warning()
            return
        }
        if !cleanEmail.isEmpty, cleanEmail.range(of: #"^[^\s@]+@[^\s@]+\.[^\s@]+$"#, options: .regularExpression) == nil {
            error = "That email doesn't look right."
            Haptics.warning()
            return
        }
        var body = DirectoryPatch()
        if isNew || name != contact?.name { body.text("name", name) }
        if isNew ? !cleanEmail.isEmpty : email != (contact?.email ?? "") { body.text("email", email, clearsWhenEmpty: true) }
        if isNew ? !phone.isEmpty : phone != contact?.phone { body.text("phone", phone) }
        if company?.id != (isNew ? nil : contact?.company?.id) { body["company_id"] = company.map { .string($0.id) } ?? .null }
        if isNew ? !externalID.isEmpty : externalID != (contact?.externalID ?? "") { body.text("external_id", externalID, clearsWhenEmpty: true) }
        if isNew ? !notes.isEmpty : notes != contact?.notes { body["notes"] = .string(notes) }
        if !isNew, blocked != contact?.blocked { body["blocked"] = .bool(blocked) }
        error = nil
        saving = true
        defer { saving = false }
        do {
            try await onSave(body)
            Haptics.success()
            dismiss()
        } catch {
            guard !error.isCancellation else { return }
            Haptics.error()
            self.error = error.userMessage
        }
    }
}

/// A notes editor with the field look: grows with its text.
struct DirectoryNotesField: View {
    @Binding var text: String
    var focused: FocusState<Bool>.Binding
    var hint: String

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text("Notes").font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg2)
            TextField(text: $text, axis: .vertical) { Text("Anything your team should know").foregroundStyle(Theme.fg4) }
                .font(ODFont.sans(16))
                .foregroundStyle(Theme.fg)
                .lineLimit(4 ... 12)
                .focused(focused)
                .padding(.horizontal, 13)
                .padding(.vertical, 12)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(
                    RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous)
                        .strokeBorder(focused.wrappedValue ? Theme.lineStrong : Theme.line, lineWidth: focused.wrappedValue ? 1.5 : 1)
                )
            Text(hint).font(ODFont.footnote).foregroundStyle(Theme.fg3)
        }
    }
}

// MARK: - Company

/// Creates a company or edits one: name, domain and notes.
struct CompanyEditSheet: View {
    var company: Company?
    var focusNotes = false
    let onSave: (DirectoryPatch) async throws -> Void
    var onDelete: (() -> Void)?

    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var domain = ""
    @State private var notes = ""
    @State private var error: String?
    @State private var saving = false
    @State private var confirmDiscard = false
    @State private var confirmDelete = false
    @State private var filled = false
    @FocusState private var notesFocused: Bool

    private var isNew: Bool { company == nil }

    private var dirty: Bool {
        guard let company else { return !name.isEmpty || !domain.isEmpty || !notes.isEmpty }
        return name != company.name || domain != company.domain || notes != company.notes
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    ODTextField(label: "Name", text: $name, prompt: "Acme Inc", contentType: .organizationName)
                    VStack(alignment: .leading, spacing: 6) {
                        ODTextField(label: "Domain", text: $domain, prompt: "acme.com", contentType: .URL, keyboard: .URL)
                        Text("Contacts with this email domain can be matched to the company.").font(ODFont.footnote).foregroundStyle(Theme.fg3)
                    }
                    DirectoryNotesField(text: $notes, focused: $notesFocused, hint: "Your team sees this on the company and its people.")
                    FormError(text: error)
                    if !isNew, onDelete != nil {
                        Button(role: .destructive) { confirmDelete = true } label: {
                            Label("Delete company", systemImage: "trash")
                        }
                        .buttonStyle(OutlineButtonStyle(tint: Tone.red.text))
                        .padding(.top, 8)
                    }
                }
                .padding(20)
                .motion(Motion.snappy, value: error)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle(isNew ? "New company" : "Edit company")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { if dirty { confirmDiscard = true } else { dismiss() } }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if saving {
                        ProgressView()
                    } else {
                        Button(isNew ? "Create" : "Save") { Task { await save() } }
                            .fontWeight(.semibold)
                            .disabled(!dirty && !isNew)
                    }
                }
            }
            .confirmationDialog("Discard your changes?", isPresented: $confirmDiscard, titleVisibility: .visible) {
                Button("Discard changes", role: .destructive) { dismiss() }
                Button("Keep editing", role: .cancel) {}
            }
            .confirmationDialog("Delete \(company?.name ?? "this company")?", isPresented: $confirmDelete, titleVisibility: .visible) {
                Button("Delete company", role: .destructive) {
                    dismiss()
                    onDelete?()
                }
            } message: {
                Text("People stay, but they are no longer linked to this company.")
            }
        }
        .interactiveDismissDisabled(dirty)
        .onAppear {
            guard !filled else { return }
            filled = true
            name = company?.name ?? ""
            domain = company?.domain ?? ""
            notes = company?.notes ?? ""
            if focusNotes {
                Task {
                    try? await Task.sleep(for: .milliseconds(350))
                    notesFocused = true
                }
            }
        }
    }

    private func save() async {
        let cleanName = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !cleanName.isEmpty else {
            error = isNew ? "Add a name." : "A company needs a name."
            Haptics.warning()
            return
        }
        var cleanDomain = domain.trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
        for prefix in ["https://", "http://", "www."] where cleanDomain.hasPrefix(prefix) { cleanDomain.removeFirst(prefix.count) }
        if let slash = cleanDomain.firstIndex(of: "/") { cleanDomain = String(cleanDomain[..<slash]) }
        var body = DirectoryPatch()
        if isNew || cleanName != company?.name { body["name"] = .string(cleanName) }
        if isNew ? !cleanDomain.isEmpty : cleanDomain != company?.domain { body["domain"] = .string(cleanDomain) }
        if isNew ? !notes.isEmpty : notes != company?.notes { body["notes"] = .string(notes) }
        error = nil
        saving = true
        defer { saving = false }
        do {
            try await onSave(body)
            Haptics.success()
            dismiss()
        } catch {
            guard !error.isCancellation else { return }
            Haptics.error()
            self.error = error.userMessage
        }
    }
}

// MARK: - Company picker

/// Picks the company a contact works for, or none; can create one by name.
struct CompanyPickerSheet: View {
    let selected: CompanyRef?
    let onPick: (CompanyRef?) -> Void

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var store = CompaniesStore()
    @State private var query = ""
    @State private var creating = false
    @FocusState private var focused: Bool

    private var trimmed: String { query.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    HStack(spacing: 8) {
                        Image(systemName: "magnifyingglass").font(.system(size: 14, weight: .medium)).foregroundStyle(Theme.fg3)
                        TextField(text: $query) { Text("Search companies or domains").foregroundStyle(Theme.fg4) }
                            .font(ODFont.sans(16))
                            .foregroundStyle(Theme.fg)
                            .autocorrectionDisabled()
                            .textInputAutocapitalization(.words)
                            .focused($focused)
                    }
                    .padding(.horizontal, 13)
                    .frame(height: 44)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(focused ? Theme.lineStrong : Theme.line, lineWidth: 1))

                    Panel {
                        if trimmed.isEmpty {
                            option(nil)
                            if !store.companies.isEmpty { Hairline(inset: 50) }
                        } else if !store.companies.contains(where: { $0.name.caseInsensitiveCompare(trimmed) == .orderedSame }), env.session.canWrite {
                            Button { Task { await create() } } label: {
                                HStack(spacing: 12) {
                                    Image(systemName: "plus").font(.system(size: 13, weight: .semibold)).frame(width: 26)
                                    Text("Create \u{201C}\(trimmed)\u{201D}").font(ODFont.sans(15, weight: .medium))
                                    Spacer()
                                    if creating { ProgressView().controlSize(.small) }
                                }
                                .foregroundStyle(Theme.fg)
                                .padding(.horizontal, 14)
                                .frame(height: 50)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(RowPressStyle())
                            .disabled(creating)
                            if !store.companies.isEmpty { Hairline(inset: 50) }
                        }
                        if !store.loadedOnce {
                            SkeletonRows(count: 3)
                        }
                        ForEach(Array(store.companies.enumerated()), id: \.element.id) { index, company in
                            if index > 0 { Hairline(inset: 50) }
                            option(company.ref)
                        }
                    }
                }
                .padding(20)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Company")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
        .onAppear { store.start(api: env.session.api, realtime: env.realtime) }
        .onDisappear { store.stop() }
        .onChange(of: query) { _, value in store.setSearch(value) }
    }

    private func option(_ company: CompanyRef?) -> some View {
        let isSelected = company?.id == selected?.id
        return Button {
            Haptics.selection()
            onPick(company)
            dismiss()
        } label: {
            HStack(spacing: 12) {
                if let company {
                    DirectoryAvatar(name: company.name, url: company.avatarURL, seed: company.id, size: 26, square: true)
                    VStack(alignment: .leading, spacing: 1) {
                        Text(company.name).font(ODFont.sans(15, weight: isSelected ? .semibold : .regular)).foregroundStyle(Theme.fg).lineLimit(1)
                        if !company.domain.isEmpty { Text(company.domain).font(ODFont.mono(12)).foregroundStyle(Theme.fg3).lineLimit(1) }
                    }
                } else {
                    Image(systemName: "minus.circle").font(.system(size: 16)).foregroundStyle(Theme.fg3).frame(width: 26)
                    Text("No company").font(ODFont.sans(15, weight: isSelected ? .semibold : .regular)).foregroundStyle(Theme.fg)
                }
                Spacer()
                if isSelected { Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg) }
            }
            .padding(.horizontal, 14)
            .frame(height: 52)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
    }

    private func create() async {
        creating = true
        defer { creating = false }
        var body = DirectoryPatch()
        body["name"] = .string(trimmed)
        do {
            let company = try await store.create(body)
            Haptics.success()
            onPick(company.ref)
            dismiss()
        } catch {
            env.toasts.error(error)
        }
    }
}
