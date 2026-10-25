import SwiftUI

/// Ticket views: the saved filters in the inbox's view list. Reorder, add, edit, remove.
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
                AccountViewEditor(view: edit.view)
                    .presentationDetents([.large])
                    .presentationCornerRadius(Theme.Radius.sheet)
            }
            .accountConfirm($confirm)
            .task { await store.loadViews() }
            .onChange(of: env.directory.views) { _, _ in Task { await store.loadViews() } }
            .onAppear(perform: handleDemo)
            .onChange(of: env.router.demoAction) { _, _ in handleDemo() }
            .onChange(of: store.viewsLoaded) { _, _ in handleDemo() }
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
                Section {
                    ForEach(store.views) { view in
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
                        }
                    }
                    .onMove { source, destination in
                        Haptics.selection()
                        Task { await store.moveViews(from: source, to: destination) }
                    }
                } footer: {
                    Text(footer).font(ODFont.footnote).foregroundStyle(Theme.fg3)
                }
            }
            .listStyle(.insetGrouped)
            .scrollContentBackground(.hidden)
            .contentMargins(.bottom, 100, for: .scrollContent)
        }
    }

    private var footer: String {
        if !env.session.canWrite { return "Viewers can use views but not change them." }
        if store.canShareViews { return "Views show in this order in the inbox. Shared views are the same for everyone." }
        return "Views show in this order in the inbox. Admins manage the views shared with everyone."
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
    }
}

struct AccountViewEdit: Identifiable {
    let id = UUID()
    var view: AccountTicketView?
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
        HStack(alignment: .top, spacing: 12) {
            AccountIconTile(symbol: "line.3.horizontal.decrease", tone: audience.tone)
            VStack(alignment: .leading, spacing: 4) {
                HStack(spacing: 8) {
                    Text(view.name)
                        .font(ODFont.sans(15.5, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    TonePill(text: audience.text, tone: audience.tone, height: 19) {
                        Image(systemName: audience.symbol).font(.system(size: 9, weight: .semibold))
                    }
                }
                Text(AccountViewSummary.text(view.filters, directory: env.directory))
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
    static func text(_ filters: TicketFilters, directory: DirectoryStore) -> String {
        var parts: [String] = []
        if let status = filters.status, !status.isEmpty {
            parts.append(status.compactMap { TicketStatus(rawValue: $0)?.title }.joined(separator: ", "))
        }
        if let priority = filters.priority, !priority.isEmpty {
            parts.append(priority.compactMap { Priority(rawValue: $0)?.title }.joined(separator: ", "))
        }
        if let assignee = filters.assignee, !assignee.isEmpty {
            parts.append(AccountViewFilterChoice.assigneeTitle(assignee, directory: directory))
        }
        if let team = filters.teamID, !team.isEmpty {
            parts.append(AccountViewFilterChoice.teamTitle(team, directory: directory))
        }
        if let tag = filters.tag, !tag.isEmpty { parts.append("Label \(tag)") }
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

/// A new view, or changes to one: its name, who sees it, and its filters.
struct AccountViewEditor: View {
    let view: AccountTicketView?

    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var draft = AccountStore.ViewDraft()
    @State private var saving = false
    @State private var error: String?

    private var directory: DirectoryStore { env.directory }

    private var audiences: [AccountTicketView.Audience] {
        store.canShareViews ? AccountTicketView.Audience.allCases : [.personal]
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                AccountSheetHeader(title: view == nil ? "New view" : "Edit view") { dismiss() }
                ODTextField(label: "Name", text: $draft.name, prompt: "Urgent for billing", submitLabel: .done)
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
        .onAppear { if let view { draft = AccountStore.ViewDraft(view) } }
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
            Text(audienceHint).font(ODFont.footnote).foregroundStyle(Theme.fg3).fixedSize(horizontal: false, vertical: true)
        }
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
        VStack(alignment: .leading, spacing: 16) {
            VStack(alignment: .leading, spacing: 8) {
                label("Status")
                AccountChipFlow {
                    ForEach(TicketStatus.allCases) { status in
                        chip(status.title, on: has(\.status, status.rawValue)) { toggle(\.status, status.rawValue) } icon: {
                            StatusIcon(status: status, size: 12)
                        }
                    }
                }
            }
            VStack(alignment: .leading, spacing: 8) {
                label("Priority")
                AccountChipFlow {
                    ForEach(Priority.allCases) { priority in
                        chip(priority.title, on: has(\.priority, priority.rawValue)) { toggle(\.priority, priority.rawValue) } icon: {
                            PriorityIcon(priority: priority, size: 12)
                        }
                    }
                }
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
            VStack(alignment: .leading, spacing: 8) {
                label("Team")
                menu(title: AccountViewFilterChoice.teamTitle(draft.filters.teamID, directory: directory), placeholder: (draft.filters.teamID ?? "").isEmpty) {
                    Button("Any team") { draft.filters.teamID = nil }
                    Button("My teams") { draft.filters.teamID = "mine" }
                    Button("No team") { draft.filters.teamID = "none" }
                    if !directory.teams.isEmpty { Divider() }
                    ForEach(directory.teams) { team in
                        Button(team.name) { draft.filters.teamID = team.id }
                    }
                }
            }
            let tags = directory.tags.filter { $0.archived != true }
            if !tags.isEmpty {
                VStack(alignment: .leading, spacing: 8) {
                    label("Label")
                    menu(title: (draft.filters.tag ?? "").isEmpty ? "Any label" : draft.filters.tag ?? "", placeholder: (draft.filters.tag ?? "").isEmpty) {
                        Button("Any label") { draft.filters.tag = nil }
                        Divider()
                        ForEach(tags) { tag in
                            Button(tag.name) { draft.filters.tag = tag.name }
                        }
                    }
                }
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

    // MARK: Pieces

    private func label(_ text: String) -> some View {
        Text(text).font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg2)
    }

    private func chip(_ title: String, on: Bool, action: @escaping () -> Void, @ViewBuilder icon: () -> some View) -> some View {
        Button(action: action) {
            HStack(spacing: 5) {
                icon()
                Text(title)
            }
        }
        .buttonStyle(AccountFilterChipStyle(on: on))
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
        Task {
            do {
                try await store.saveView(draft, existing: view)
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

/// A filter chip: outlined when off, ink when on.
struct AccountFilterChipStyle: ButtonStyle {
    let on: Bool

    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(ODFont.sans(13.5, weight: .medium))
            .foregroundStyle(on ? Theme.fg : Theme.fg2)
            .padding(.horizontal, 10)
            .frame(height: 32)
            .background(on ? Theme.active : Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous)
                    .strokeBorder(on ? Theme.fg.opacity(0.55) : Theme.line, lineWidth: on ? 1.25 : 1)
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
