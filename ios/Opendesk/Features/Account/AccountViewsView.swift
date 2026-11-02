import SwiftUI

/// Ticket views: the saved filters in the inbox's view list, under their sections. Reorder,
/// add, duplicate, edit, remove.
struct AccountViewsView: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @State private var editing: AccountViewEdit?
    @State private var confirm: AccountConfirm?
    @State private var editMode: EditMode = .inactive

    var body: some View {
        content
            .background(Theme.chrome.ignoresSafeArea())
            .accountTitle("Views")
            .environment(\.editMode, $editMode)
            .toolbar {
                if store.views.count > 1 {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button {
                            Haptics.tap()
                            withAnimation(Motion.snappy) { editMode = editMode.isEditing ? .inactive : .active }
                        } label: {
                            Image(systemName: editMode.isEditing ? "checkmark" : "arrow.up.arrow.down")
                        }
                        .accessibilityLabel(editMode.isEditing ? "Done reordering" : "Reorder views")
                    }
                }
                if env.session.canWrite {
                    ToolbarItem(placement: .topBarTrailing) {
                        Button { editing = AccountViewEdit(view: nil) } label: { Image(systemName: "plus") }
                            .accessibilityLabel("New view")
                    }
                }
            }
            .sheet(item: $editing) { edit in
                AccountViewEditor(view: edit.view, copy: edit.copy)
                    .presentationDetents([.large])
                    .presentationCornerRadius(Theme.Radius.sheet)
            }
            .accountConfirm($confirm)
            .task { await store.loadViews() }
            .onChange(of: env.directory.views) { _, _ in Task { await store.loadViews() } }
            #if DEBUG
            .onAppear(perform: handleDemo)
            .onChange(of: env.router.demoAction) { _, _ in handleDemo() }
            .onChange(of: store.viewsLoaded) { _, _ in handleDemo() }
            #endif
    }

    @ViewBuilder
    private var content: some View {
        if !store.viewsLoaded {
            ScrollView { Panel { SkeletonRows(count: 4) }.padding(16) }
        } else if let error = store.viewsError, store.views.isEmpty {
            ErrorState(message: error) { Task { await store.loadViews() } }.frame(maxHeight: .infinity)
        } else if store.views.isEmpty {
            EmptyState(
                symbol: "line.3.horizontal.decrease.circle",
                title: "No views yet",
                message: "A view saves a set of filters, like open urgent tickets for your team, one tap away in the inbox.",
                actionTitle: env.session.canWrite ? "Add a view" : nil,
                action: env.session.canWrite ? { editing = AccountViewEdit(view: nil) } : nil
            )
            .frame(maxHeight: .infinity)
        } else {
            List {
                if editMode.isEditing {
                    // Reordering moves views one by one, in one list.
                    Section {
                        ForEach(store.views) { view in
                            row(view)
                        }
                        .onMove { source, destination in
                            Haptics.selection()
                            Task { await store.moveViews(from: source, to: destination) }
                        }
                    } footer: {
                        footerText
                    }
                } else {
                    let runs = Self.runs(ViewSections.group(store.views))
                    ForEach(Array(runs.enumerated()), id: \.offset) { index, run in
                        Section {
                            ForEach(run.views) { view in row(view) }
                        } header: {
                            if let name = run.section {
                                HStack(spacing: 6) {
                                    Image(systemName: "folder").font(.system(size: 11, weight: .medium))
                                    Text(name)
                                }
                                .font(ODFont.sans(12.5, weight: .medium))
                                .foregroundStyle(Theme.fg3)
                                .textCase(nil)
                                .accessibilityAddTraits(.isHeader)
                            }
                        } footer: {
                            if index == runs.count - 1 { footerText }
                        }
                    }
                }
            }
            .listStyle(.insetGrouped)
            .scrollContentBackground(.hidden)
            .contentMargins(.bottom, 100, for: .scrollContent)
        }
    }

    struct Run {
        var section: String?
        var views: [AccountTicketView]
    }

    /// The list as panels: each section in its own under its name, and the views between
    /// sections together.
    static func runs(_ entries: [ViewEntry<AccountTicketView>]) -> [Run] {
        var out: [Run] = []
        for entry in entries {
            switch entry {
            case let .view(view):
                if let last = out.last, last.section == nil {
                    out[out.count - 1].views.append(view)
                } else {
                    out.append(Run(section: nil, views: [view]))
                }
            case let .section(_, name, views):
                out.append(Run(section: name, views: views))
            }
        }
        return out
    }

    private func row(_ view: AccountTicketView) -> some View {
        Button { if store.canEdit(view) { editing = AccountViewEdit(view: view) } } label: {
            AccountViewRow(view: view, editable: store.canEdit(view))
        }
        .buttonStyle(RowPressStyle())
        .listRowInsets(EdgeInsets())
        .listRowBackground(Theme.surface)
        .swipeActions(edge: .trailing) {
            if store.canEdit(view) {
                Button(role: .destructive) { ask(view) } label: { Label("Delete", systemImage: "trash") }
            }
            if env.session.canWrite {
                Button { duplicate(view) } label: { Label("Duplicate", systemImage: "plus.square.on.square") }
                    .tint(Tone.indigo.solid)
            }
        }
        .contextMenu {
            if store.canEdit(view) {
                Button { editing = AccountViewEdit(view: view) } label: { Label("Edit", systemImage: "pencil") }
            }
            if env.session.canWrite {
                Button { duplicate(view) } label: { Label("Duplicate", systemImage: "plus.square.on.square") }
            }
            if store.canEdit(view) {
                Divider()
                Button(role: .destructive) { ask(view) } label: { Label("Delete", systemImage: "trash") }
            }
        }
    }

    private var footerText: some View {
        Text(footer).font(ODFont.footnote).foregroundStyle(Theme.fg3)
    }

    private var footer: String {
        if !env.session.canWrite { return "Viewers can use views but not change them." }
        if store.canShareViews { return "Views show in this order in the inbox, a section at its first view's place. Shared views are the same for everyone." }
        return "Views show in this order in the inbox, a section at its first view's place. Admins manage the views shared with everyone."
    }

    private func duplicate(_ view: AccountTicketView) {
        Haptics.tap()
        editing = AccountViewEdit(view: nil, copy: .copy(of: view, canWrite: store.canEdit(view)))
    }

    private func ask(_ view: AccountTicketView) {
        confirm = AccountConfirm(
            title: "Delete \u{201C}\(view.name)\u{201D}?",
            message: view.shared ? "It's removed for everyone in the workspace. The tickets stay as they are." : "The tickets in it stay as they are.",
            confirmTitle: "Delete"
        ) {
            do {
                try await store.deleteView(view)
                Haptics.success()
            } catch {
                env.toasts.error(error)
            }
        }
    }

    private func handleDemo() {
        if AccountDemo.take("new-view", from: env.router) { editing = AccountViewEdit(view: nil) }
        guard store.viewsLoaded else { return }
        if AccountDemo.take("edit-view", from: env.router), let first = store.views.first(where: store.canEdit) {
            editing = AccountViewEdit(view: first)
        }
        if AccountDemo.take("duplicate-view", from: env.router), let first = store.views.first {
            duplicate(first)
        }
    }
}

struct AccountViewEdit: Identifiable {
    let id = UUID()
    var view: AccountTicketView?
    /// A duplicate the editor opens with, not yet saved.
    var copy: AccountStore.ViewDraft?
}

private struct AccountViewRow: View {
    let view: AccountTicketView
    let editable: Bool
    @Environment(AppEnvironment.self) private var env

    private var audience: (text: String, tone: Tone, symbol: String) {
        if let team = env.directory.team(view.teamID) { return (team.name, .indigo, "person.3") }
        return view.shared ? ("Everyone", .blue, "person.2") : ("Only me", .gray, "lock")
    }

    var body: some View {
        let audience = audience
        let gray = audience.tone == .gray
        HStack(alignment: .top, spacing: 12) {
            ViewIconMark(icon: view.icon, size: 14, tint: gray ? Theme.fg2 : audience.tone.text)
                .frame(width: 30, height: 30)
                .background(gray ? Theme.active : audience.tone.solid.opacity(0.13), in: RoundedRectangle(cornerRadius: 8.4, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 8.4, style: .continuous).strokeBorder(gray ? Theme.line : audience.tone.line, lineWidth: 0.5))
                .accessibilityHidden(true)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(view.displayName)
                        .font(ODFont.sans(15.5, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    TonePill(text: audience.text, tone: audience.tone, height: 19) {
                        Image(systemName: audience.symbol).font(.system(size: 9, weight: .semibold))
                    }
                }
                Text(AccountViewSummary.text(view.filters, directory: env.directory, mailboxes: env.inbox.mailboxes))
                    .font(ODFont.sans(13.5))
                    .foregroundStyle(Theme.fg3)
                    .lineLimit(2)
            }
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .opacity(editable ? 1 : 0.92)
    }
}

/// A one-line reading of a view's filters.
enum AccountViewSummary {
    @MainActor
    static func text(_ filters: TicketFilters, directory: DirectoryStore, mailboxes: [InboxMailbox] = []) -> String {
        var parts: [String] = []
        let or = { (list: [String]) in list.joined(separator: " / ") }
        if let status = filters.status, !status.isEmpty {
            parts.append(or(status.compactMap { TicketStatus(rawValue: $0)?.title }))
        }
        if let priority = filters.priority, !priority.isEmpty {
            parts.append(or(priority.compactMap { Priority(rawValue: $0)?.title }))
        }
        if let severity = filters.severity, !severity.isEmpty {
            parts.append(or(severity.map { $0 == "none" ? "No severity" : Severity(rawValue: $0)?.title ?? $0 }))
        }
        if let assignee = filters.assignee, !assignee.isEmpty {
            parts.append(AccountViewFilterChoice.assigneeTitle(assignee, directory: directory))
        }
        let teams = { (list: String) in or(ViewFilterOps.split(list).map { AccountViewFilterChoice.teamTitle($0, directory: directory) }) }
        if let team = filters.teamID, !team.isEmpty { parts.append(teams(team)) }
        if let team = filters.teamNot, !team.isEmpty { parts.append("Not \(teams(team))") }
        let boxes = { (list: String) in or(ViewFilterOps.split(list).map { id in mailboxes.first { $0.id == id }?.email ?? "an address" }) }
        if let box = filters.mailboxID, !box.isEmpty { parts.append("To \(boxes(box))") }
        if let box = filters.mailboxNot, !box.isEmpty { parts.append("Not to \(boxes(box))") }
        let tags = ViewFilterOps.split(filters.tag)
        if !tags.isEmpty { parts.append(tags.count == 1 ? "Label \(tags[0])" : "Labels \(or(tags))") }
        let tagsNot = ViewFilterOps.split(filters.tagNot)
        if !tagsNot.isEmpty { parts.append("Without \(tagsNot.joined(separator: ", "))") }
        if filters.untagged == true { parts.append("No label") }
        if let channel = filters.channel, !channel.isEmpty { parts.append(Channel(rawValue: channel)?.title ?? channel) }
        if let channels = filters.channelNot, !channels.isEmpty { parts.append("Not \(or(channels.map { Channel(rawValue: $0)?.title ?? $0 }))") }
        let contacts = ViewFilterOps.split(filters.contactNot).count
        if contacts > 0 { parts.append("Leaves out \(Format.plural(contacts, "contact"))") }
        let companies = ViewFilterOps.split(filters.companyNot).count
        if companies > 0 { parts.append(companies == 1 ? "Leaves out 1 company" : "Leaves out \(companies) companies") }
        if let q = filters.q, !q.isEmpty { parts.append("\u{201C}\(q)\u{201D}") }
        if filters.hasOtherFilters && parts.isEmpty { parts.append("Filters set on the web") }
        return parts.isEmpty ? "Every ticket" : parts.joined(separator: " · ")
    }
}

enum AccountViewFilterChoice {
    @MainActor
    static func assigneeTitle(_ value: String?, directory: DirectoryStore) -> String {
        switch value {
        case nil, "": "Anyone"
        case "me": "Assigned to me"
        case "none": "Unassigned"
        case let id?: directory.members.first { $0.user.id == id }.map { "Assigned to \($0.user.name)" } ?? "Assigned to someone"
        }
    }

    @MainActor
    static func teamTitle(_ value: String?, directory: DirectoryStore) -> String {
        switch value {
        case nil, "": "Any team"
        case "mine": "My teams"
        case "none": "No team"
        case let id?: directory.team(id)?.name ?? "A team"
        }
    }
}

/// The view editor's rows that take an operator (the web's `viewFilters.ts`): label (any of,
/// none of, empty), team, address and channel (is, is not). Each operator writes its own key:
/// tag / tag_not / untagged, team_id / team_not, mailbox_id / mailbox_not, channel / channel_not.
enum ViewFilterOps {
    enum Field { case tag, team, mailbox, channel }

    static func split(_ list: String?) -> [String] {
        (list ?? "").split(separator: ",").map { $0.trimmingCharacters(in: .whitespaces) }.filter { !$0.isEmpty }
    }

    static func join(_ list: [String]) -> String? { list.isEmpty ? nil : list.joined(separator: ",") }

    /// The operator a row shows: the one whose key is set, "is" when none is.
    static func op(_ f: TicketFilters, _ field: Field) -> InboxFilterOp {
        switch field {
        case .tag:
            if f.untagged == true { return .empty }
            return (f.tagNot ?? "").isEmpty || !(f.tag ?? "").isEmpty ? .is : .not
        case .team: return (f.teamNot ?? "").isEmpty || !(f.teamID ?? "").isEmpty ? .is : .not
        case .mailbox: return (f.mailboxNot ?? "").isEmpty || !(f.mailboxID ?? "").isEmpty ? .is : .not
        case .channel: return (f.channelNot ?? []).isEmpty || !(f.channel ?? "").isEmpty ? .is : .not
        }
    }

    /// The filters with the row switched to `op`, carrying the chosen values over where the new operator can hold them.
    static func with(_ f: TicketFilters, _ field: Field, op: InboxFilterOp) -> TicketFilters {
        var out = f
        switch field {
        case .tag:
            let names = join(split(f.tag) + split(f.tagNot))
            out.tag = nil
            out.tagNot = nil
            out.untagged = nil
            switch op {
            case .empty: out.untagged = true
            case .not: out.tagNot = names
            case .is: out.tag = names
            }
        case .team:
            let teams = join(split(f.teamID) + split(f.teamNot))
            out.teamID = op == .not ? nil : teams
            out.teamNot = op == .not ? teams : nil
        case .mailbox:
            let boxes = split(f.mailboxID) + split(f.mailboxNot)
            out.mailboxID = op == .not ? nil : boxes.first
            out.mailboxNot = op == .not ? join(boxes) : nil
        case .channel:
            var channels: [String] = []
            for channel in [f.channel].compactMap({ $0 }) + (f.channelNot ?? []) where !channel.isEmpty && !channels.contains(channel) {
                channels.append(channel)
            }
            out.channel = op == .not ? nil : channels.first
            out.channelNot = op == .not ? (channels.isEmpty ? nil : channels) : nil
        }
        return out
    }
}

/// A new view, a copy of one, or changes to one: its name, icon, section, who sees it, and its filters.
struct AccountViewEditor: View {
    let view: AccountTicketView?
    var copy: AccountStore.ViewDraft?

    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var draft = AccountStore.ViewDraft()
    @State private var saving = false
    @State private var error: String?
    @State private var pickingIcon = false
    @State private var excluding: ViewExclusionPicker.Kind?
    @State private var contacts: [ViewExclusionPicker.Choice] = []
    @State private var companies: [ViewExclusionPicker.Choice] = []
    @FocusState private var sectionFocused: Bool

    private var directory: DirectoryStore { env.directory }

    private var audiences: [AccountTicketView.Audience] {
        store.canShareViews ? AccountTicketView.Audience.allCases : [.personal]
    }

    private var title: String {
        if view != nil { return "Edit view" }
        return copy != nil ? "Duplicate view" : "New view"
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                AccountSheetHeader(title: title) { dismiss() }
                ODTextField(label: "Name", text: $draft.name, prompt: "Billing queue", submitLabel: .done)
                iconRow
                section
                audience
                filters
                if draft.filters.hasOtherFilters {
                    Notice(text: "This view has more filters set on the web. They're kept as they are.", symbol: "slider.horizontal.3")
                }
                FormError(text: error)
                Button(action: save) { ButtonLabel(title: view == nil ? "Add view" : "Save changes", busy: saving) }
                    .buttonStyle(InkButtonStyle())
                    .disabled(!draft.isValid || saving)
            }
            .padding(22)
            .motion(Motion.snappy, value: draft.audience)
            .motion(Motion.snappy, value: error)
        }
        .scrollDismissesKeyboard(.interactively)
        .background(Theme.surface)
        .onAppear {
            if let view { draft = AccountStore.ViewDraft(view) } else if let copy { draft = copy }
        }
        .task { await loadExclusions() }
        .sheet(isPresented: $pickingIcon) {
            ViewIconPicker(icon: $draft.icon)
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(item: $excluding) { kind in
            ViewExclusionPicker(kind: kind, chosen: kind == .contacts ? $contacts : $companies)
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .onChange(of: contacts) { _, list in draft.filters.contactNot = ViewFilterOps.join(list.map(\.id)) }
        .onChange(of: companies) { _, list in draft.filters.companyNot = ViewFilterOps.join(list.map(\.id)) }
    }

    /// The names of the contacts and companies the view already leaves out.
    private func loadExclusions() async {
        let start = view?.filters ?? copy?.filters ?? TicketFilters()
        let api = env.session.api
        let people = await ViewExclusionPicker.names(kind: .contacts, ids: ViewFilterOps.split(start.contactNot), api: api)
        let firms = await ViewExclusionPicker.names(kind: .companies, ids: ViewFilterOps.split(start.companyNot), api: api)
        if contacts.isEmpty { contacts = people }
        if companies.isEmpty { companies = firms }
    }

    // MARK: Icon

    private var iconRow: some View {
        VStack(alignment: .leading, spacing: 8) {
            label("Icon")
            Button {
                Haptics.tap()
                pickingIcon = true
            } label: {
                HStack(spacing: 12) {
                    ViewIconMark(icon: draft.icon, size: 17, tint: Theme.fg2)
                        .frame(width: 24, height: 24)
                    Text(iconTitle)
                        .font(ODFont.sans(16))
                        .foregroundStyle(draft.icon == nil ? Theme.fg3 : Theme.fg)
                    Spacer()
                    Text(draft.icon == nil ? "Choose" : "Change").font(ODFont.sans(14, weight: .medium)).foregroundStyle(Theme.fg2)
                }
                .padding(.horizontal, 13)
                .frame(height: 46)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            Text("Shown beside the name in the inbox, so the view is easy to spot.").font(ODFont.footnote).foregroundStyle(Theme.fg3)
        }
    }

    private var iconTitle: String {
        guard let icon = draft.icon else { return "None" }
        switch icon.kind {
        case .emoji: return "Emoji"
        case .icon: return ViewIconCatalog.item(icon.value).map { $0.name.replacingOccurrences(of: "-", with: " ").capitalized } ?? "Icon"
        case .text: return "Text \u{201C}\(icon.value)\u{201D}"
        }
    }

    // MARK: Section

    private var section: some View {
        let existing = ViewSections.names(store.views)
        let typed = ViewSections.clean(draft.section)
        let suggestions = existing.filter { (typed.isEmpty || $0.localizedCaseInsensitiveContains(typed)) && ViewSections.key($0) != ViewSections.key(typed) }
        return VStack(alignment: .leading, spacing: 8) {
            label("Section")
            HStack(spacing: 8) {
                Image(systemName: "folder").font(.system(size: 14)).foregroundStyle(Theme.fg3)
                TextField(text: $draft.section) { Text("No section").foregroundStyle(Theme.fg4) }
                    .font(ODFont.sans(16))
                    .foregroundStyle(Theme.fg)
                    .submitLabel(.done)
                    .focused($sectionFocused)
                if !draft.section.isEmpty {
                    Button {
                        draft.section = ""
                    } label: {
                        Image(systemName: "xmark.circle.fill").font(.system(size: 16)).foregroundStyle(Theme.fg4)
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel("No section")
                }
            }
            .padding(.horizontal, 13)
            .frame(height: 46)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(draft.sectionValid ? Theme.line : Tone.red.solid, lineWidth: 1))
            if !suggestions.isEmpty {
                AccountChipFlow {
                    ForEach(suggestions, id: \.self) { name in
                        chip(name, on: false) {
                            Haptics.selection()
                            draft.section = name
                            sectionFocused = false
                        } icon: {
                            Image(systemName: "folder").font(.system(size: 10))
                        }
                    }
                }
            }
            Text(sectionHint).font(ODFont.footnote).foregroundStyle(draft.sectionValid ? Theme.fg3 : Tone.red.text).fixedSize(horizontal: false, vertical: true)
        }
    }

    private var sectionHint: String {
        if draft.section.contains("<") || draft.section.contains(">") { return "Section names can't contain < or >." }
        if ViewSections.clean(draft.section).count > ViewSections.maxLength { return "Section names run to \(ViewSections.maxLength) characters at most." }
        return "Views of one section show together in the inbox, under a heading that folds."
    }

    // MARK: Who sees it

    private var audience: some View {
        VStack(alignment: .leading, spacing: 8) {
            label("Who sees it")
            if audiences.count > 1 {
                AccountSegmented(options: audiences, selection: $draft.audience, label: \.title)
                if draft.audience == .team {
                    menu(title: directory.team(draft.teamID)?.name ?? "Choose a team", placeholder: draft.teamID == nil) {
                        ForEach(directory.teams) { team in
                            Button(team.name) { draft.teamID = team.id }
                        }
                    }
                    .transition(.opacity.combined(with: .move(edge: .top)))
                }
            }
            if let note = copyNote {
                Notice(text: note, symbol: "lock")
            }
            Text(audienceHint).font(ODFont.footnote).foregroundStyle(Theme.fg3).fixedSize(horizontal: false, vertical: true)
        }
    }

    /// Why a copy of a shared or team view is the person's own.
    private var copyNote: String? {
        guard let source = copy?.copyOf, source.audience != .personal, draft.audience == .personal, !store.canEdit(source) else { return nil }
        guard let teamID = source.teamID else { return "This copy is yours alone, since only admins create views for everyone." }
        if let team = directory.team(teamID)?.name { return "This copy is yours alone, since only admins and the leads of \(team) create its views." }
        return "This copy is yours alone, since only admins and the team's leads create its views."
    }

    private var audienceHint: String {
        switch draft.audience {
        case .personal: store.canShareViews ? "Only you see it in your inbox." : "Only you see it in your inbox. Admins can share views with everyone."
        case .shared: "Everyone in the workspace sees it."
        case .team: "The team's members see it."
        }
    }

    // MARK: Filters

    private var filters: some View {
        VStack(alignment: .leading, spacing: 18) {
            Text("Show tickets where").font(ODFont.sans(15, weight: .semibold)).foregroundStyle(Theme.fg)
            group("Status") {
                ForEach(TicketStatus.allCases) { status in
                    chip(status.title, on: has(\.status, status.rawValue)) { toggle(\.status, status.rawValue) } icon: {
                        StatusIcon(status: status, size: 12)
                    }
                }
            }
            group("Priority") {
                ForEach(Priority.allCases) { priority in
                    chip(priority.title, on: has(\.priority, priority.rawValue)) { toggle(\.priority, priority.rawValue) } icon: {
                        PriorityIcon(priority: priority, size: 12)
                    }
                }
            }
            group("Severity") {
                ForEach(Severity.allCases) { severity in
                    chip(severity.title, on: has(\.severity, severity.rawValue)) { toggle(\.severity, severity.rawValue) } icon: {
                        EmptyView()
                    }
                }
                chip("No severity", on: has(\.severity, "none")) { toggle(\.severity, "none") } icon: { EmptyView() }
            }
            VStack(alignment: .leading, spacing: 8) {
                label("Assignee")
                menu(title: AccountViewFilterChoice.assigneeTitle(draft.filters.assignee, directory: directory), placeholder: (draft.filters.assignee ?? "").isEmpty) {
                    Button("Anyone") { draft.filters.assignee = nil }
                    Button("Me") { draft.filters.assignee = "me" }
                    Button("Unassigned") { draft.filters.assignee = "none" }
                    Divider()
                    ForEach(directory.members, id: \.user.id) { member in
                        Button(member.user.name) { draft.filters.assignee = member.user.id }
                    }
                }
            }
            teamRow
            labelRow
            mailboxRow
            channelRow
            excludeRow(.contacts, chosen: contacts)
            excludeRow(.companies, chosen: companies)
            VStack(alignment: .leading, spacing: 8) {
                label("Keyword")
                TextField(text: Binding(get: { draft.filters.q ?? "" }, set: { draft.filters.q = $0.isEmpty ? nil : $0 })) {
                    Text("Subject, contact or text").foregroundStyle(Theme.fg4)
                }
                .font(ODFont.sans(16))
                .foregroundStyle(Theme.fg)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .submitLabel(.done)
                .padding(.horizontal, 13)
                .frame(height: 46)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            }
        }
    }

    private struct Option: Identifiable {
        let id: String
        let name: String
    }

    private var teamRow: some View {
        let op = ViewFilterOps.op(draft.filters, .team)
        let key: WritableKeyPath<TicketFilters, String?> = op == .not ? \.teamNot : \.teamID
        let values = ViewFilterOps.split(draft.filters[keyPath: key])
        let options = [Option(id: "mine", name: "My teams"), Option(id: "none", name: "No team")] + directory.teams.map { Option(id: $0.id, name: $0.name) }
        return opGroup("Team", field: .team, op: op, ops: [.is, .not]) {
            ForEach(options) { option in
                chip(option.name, on: values.contains(option.id), excluded: op == .not) { toggleList(key, option.id) } icon: {
                    EmptyView()
                }
            }
        }
    }

    @ViewBuilder
    private var labelRow: some View {
        let tags = directory.tags.filter { $0.archived != true }
        let op = ViewFilterOps.op(draft.filters, .tag)
        if !tags.isEmpty || op != .is || !(draft.filters.tag ?? "").isEmpty {
            let key: WritableKeyPath<TicketFilters, String?> = op == .not ? \.tagNot : \.tag
            let values = ViewFilterOps.split(draft.filters[keyPath: key]).map { $0.lowercased() }
            opGroup("Label", field: .tag, op: op, ops: [.is, .not, .empty]) {
                if op == .empty {
                    Text("Tickets without a label.").font(ODFont.callout).foregroundStyle(Theme.fg3)
                } else {
                    ForEach(tags) { tag in
                        chip(tag.name, on: values.contains(tag.name.lowercased()), excluded: op == .not) { toggleList(key, tag.name) } icon: {
                            Circle().fill(Tone(label: tag.color).solid).frame(width: 7, height: 7)
                        }
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var mailboxRow: some View {
        let mailboxes = env.inbox.mailboxes
        if mailboxes.count > 1 || !(draft.filters.mailboxID ?? "").isEmpty || !(draft.filters.mailboxNot ?? "").isEmpty {
            let op = ViewFilterOps.op(draft.filters, .mailbox)
            let notValues = ViewFilterOps.split(draft.filters.mailboxNot)
            opGroup("Address", field: .mailbox, op: op, ops: [.is, .not]) {
                ForEach(mailboxes) { mailbox in
                    let on = op == .not ? notValues.contains(mailbox.id) : draft.filters.mailboxID == mailbox.id
                    chip(mailbox.email, on: on, excluded: op == .not) {
                        if op == .not {
                            toggleList(\.mailboxNot, mailbox.id)
                        } else {
                            Haptics.selection()
                            draft.filters.mailboxID = on ? nil : mailbox.id
                        }
                    } icon: { EmptyView() }
                }
            }
        }
    }

    private var channelRow: some View {
        let op = ViewFilterOps.op(draft.filters, .channel)
        let notValues = draft.filters.channelNot ?? []
        return opGroup("Channel", field: .channel, op: op, ops: [.is, .not]) {
            ForEach(Channel.allCases) { channel in
                let on = op == .not ? notValues.contains(channel.rawValue) : draft.filters.channel == channel.rawValue
                chip(channel.title, on: on, excluded: op == .not) {
                    Haptics.selection()
                    if op == .not {
                        var list = notValues
                        if let index = list.firstIndex(of: channel.rawValue) { list.remove(at: index) } else { list.append(channel.rawValue) }
                        draft.filters.channelNot = list.isEmpty ? nil : list
                    } else {
                        draft.filters.channel = on ? nil : channel.rawValue
                    }
                } icon: {
                    Image(systemName: channel.symbol).font(.system(size: 10))
                }
            }
        }
    }

    private func excludeRow(_ kind: ViewExclusionPicker.Kind, chosen: [ViewExclusionPicker.Choice]) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            label(kind.title)
            AccountChipFlow {
                ForEach(chosen) { choice in
                    chip(choice.name, on: true, excluded: true) {
                        Haptics.selection()
                        if kind == .contacts { contacts.removeAll { $0.id == choice.id } } else { companies.removeAll { $0.id == choice.id } }
                    } icon: {
                        Image(systemName: "xmark").font(.system(size: 9, weight: .semibold))
                    }
                }
                Button {
                    Haptics.tap()
                    excluding = kind
                } label: {
                    HStack(spacing: 5) {
                        Image(systemName: "plus").font(.system(size: 10, weight: .semibold))
                        Text(kind == .contacts ? "Choose contacts" : "Choose companies")
                    }
                }
                .buttonStyle(AccountFilterChipStyle(on: false))
            }
        }
    }

    private func has(_ key: WritableKeyPath<TicketFilters, [String]?>, _ value: String) -> Bool {
        draft.filters[keyPath: key]?.contains(value) ?? false
    }

    private func toggle(_ key: WritableKeyPath<TicketFilters, [String]?>, _ value: String) {
        Haptics.selection()
        var list = draft.filters[keyPath: key] ?? []
        if let index = list.firstIndex(of: value) { list.remove(at: index) } else { list.append(value) }
        draft.filters[keyPath: key] = list.isEmpty ? nil : list
    }

    /// Adds or removes a value of a comma-separated filter (label names match in any case).
    private func toggleList(_ key: WritableKeyPath<TicketFilters, String?>, _ value: String) {
        Haptics.selection()
        var list = ViewFilterOps.split(draft.filters[keyPath: key])
        if let index = list.firstIndex(where: { $0.caseInsensitiveCompare(value) == .orderedSame }) { list.remove(at: index) } else { list.append(value) }
        draft.filters[keyPath: key] = ViewFilterOps.join(list)
    }

    // MARK: Pieces

    private func label(_ text: String) -> some View {
        Text(text).font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg2)
    }

    private func group<Content: View>(_ title: String, @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            label(title)
            AccountChipFlow { content() }
        }
    }

    /// A row whose chips match one way or another: any of them, none of them, or (labels) none at all.
    private func opGroup<Content: View>(_ title: String, field: ViewFilterOps.Field, op: InboxFilterOp, ops: [InboxFilterOp], @ViewBuilder content: () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                label(title)
                Spacer(minLength: 8)
                Menu {
                    ForEach(ops) { option in
                        Button {
                            Haptics.selection()
                            draft.filters = ViewFilterOps.with(draft.filters, field, op: option)
                        } label: {
                            if option == op { Label(Self.opTitle(option, field), systemImage: "checkmark") } else { Text(Self.opTitle(option, field)) }
                        }
                    }
                } label: {
                    HStack(spacing: 4) {
                        Text(Self.opTitle(op, field))
                        Image(systemName: "chevron.up.chevron.down").font(.system(size: 9, weight: .semibold))
                    }
                    .font(ODFont.sans(12.5, weight: .medium))
                    .foregroundStyle(op == .is ? Theme.fg3 : Tone.red.text)
                    .padding(.horizontal, 8)
                    .frame(height: 24)
                    .background(op == .is ? Theme.hover : Tone.red.soft, in: RoundedRectangle(cornerRadius: 6, style: .continuous))
                    .contentShape(Rectangle())
                }
                .menuOrder(.fixed)
                .accessibilityLabel("\(title) matches")
                .accessibilityValue(Self.opTitle(op, field))
            }
            AccountChipFlow { content() }
        }
    }

    private static func opTitle(_ op: InboxFilterOp, _ field: ViewFilterOps.Field) -> String {
        switch op {
        case .is: field == .tag ? "Any of" : "Is"
        case .not: field == .tag ? "None of" : "Is not"
        case .empty: "No label"
        }
    }

    private func chip(_ title: String, on: Bool, excluded: Bool = false, action: @escaping () -> Void, @ViewBuilder icon: () -> some View) -> some View {
        Button(action: action) {
            HStack(spacing: 5) {
                icon()
                Text(title).strikethrough(on && excluded, color: Tone.red.text)
            }
        }
        .buttonStyle(AccountFilterChipStyle(on: on, excluded: excluded))
        .accessibilityLabel(on && excluded ? "Not \(title)" : title)
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    private func menu(title: String, placeholder: Bool, @ViewBuilder items: () -> some View) -> some View {
        Menu {
            items()
        } label: {
            HStack {
                Text(title)
                    .font(ODFont.sans(16))
                    .foregroundStyle(placeholder ? Theme.fg3 : Theme.fg)
                    .lineLimit(1)
                Spacer()
                Image(systemName: "chevron.up.chevron.down").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.fg3)
            }
            .padding(.horizontal, 13)
            .frame(height: 46)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            .contentShape(Rectangle())
        }
    }

    private func save() {
        guard draft.isValid else { return }
        saving = true
        error = nil
        var next = draft
        let q = next.filters.q?.trimmingCharacters(in: .whitespacesAndNewlines)
        next.filters.q = q?.isEmpty == false ? q : nil
        Task {
            do {
                try await store.saveView(next, existing: view)
                Haptics.success()
                env.toasts.show(view == nil ? "View added" : "View updated", kind: .success)
                dismiss()
            } catch {
                Haptics.error()
                self.error = error.userMessage
            }
            saving = false
        }
    }
}

extension ViewExclusionPicker.Kind: Identifiable {
    var id: String { self == .contacts ? "contacts" : "companies" }
}

/// A filter chip: outlined when off, ink when on, red and struck through when on and leaving out.
struct AccountFilterChipStyle: ButtonStyle {
    let on: Bool
    var excluded = false

    func makeBody(configuration: Configuration) -> some View {
        let red = on && excluded
        return configuration.label
            .font(ODFont.sans(13.5, weight: .medium))
            .foregroundStyle(red ? Tone.red.text : on ? Theme.fg : Theme.fg2)
            .padding(.horizontal, 10)
            .frame(height: 32)
            .background(red ? Tone.red.soft : on ? Theme.active : Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous)
                    .strokeBorder(red ? Tone.red.line : on ? Theme.fg.opacity(0.55) : Theme.line, lineWidth: on ? 1.25 : 1)
            )
            .scaleEffect(configuration.isPressed ? 0.96 : 1)
            .animation(Motion.press, value: configuration.isPressed)
            .animation(Motion.snappy, value: on)
    }
}

/// Chips that wrap onto as many lines as they need.
struct AccountChipFlow: Layout {
    var spacing: CGFloat = 8

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var x: CGFloat = 0
        var y: CGFloat = 0
        var line: CGFloat = 0
        var widest: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > 0 && x + size.width > width {
                y += line + spacing
                x = 0
                line = 0
            }
            x += size.width + spacing
            line = max(line, size.height)
            widest = max(widest, x - spacing)
        }
        return CGSize(width: proposal.width ?? widest, height: y + line)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX
        var y = bounds.minY
        var line: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > bounds.minX && x + size.width > bounds.maxX {
                y += line + spacing
                x = bounds.minX
                line = 0
            }
            view.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            line = max(line, size.height)
        }
    }
}
