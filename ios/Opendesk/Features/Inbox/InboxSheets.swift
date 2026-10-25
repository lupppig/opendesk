import SwiftUI

/// Search everywhere, folders with live counts, team inboxes, the workspace's views,
/// then the app's other areas.
struct ScopeSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    if let workspace = env.session.workspace?.workspace {
                        HStack(spacing: 10) {
                            WorkspaceAvatar(workspace: workspace, size: 28)
                            Text(workspace.name).font(ODFont.sans(16, weight: .semibold)).foregroundStyle(Theme.fg)
                            Spacer()
                            RealtimeDot(status: env.realtime.status)
                        }
                    }
                    Button {
                        Haptics.selection()
                        env.router.show(.tickets, [.search])
                        dismiss()
                    } label: {
                        HStack(spacing: 10) {
                            Image(systemName: "magnifyingglass").font(.system(size: 15, weight: .medium)).foregroundStyle(Theme.fg3)
                            Text("Search everywhere").font(ODFont.sans(15)).foregroundStyle(Theme.fg3)
                            Spacer()
                        }
                        .padding(.horizontal, 14)
                        .frame(height: 44)
                        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(TapScaleStyle())
                    .accessibilityHint("Tickets, contacts and articles")

                    Panel {
                        ForEach(Array(Folder.allCases.enumerated()), id: \.element) { index, folder in
                            if index > 0 { Hairline(inset: 46) }
                            row(scope: .folder(folder), title: folder.title, symbol: folder.symbol, count: folder.count(in: env.directory.counts))
                        }
                    }
                    if !myTeams.isEmpty {
                        SectionLabel("Teams")
                        Panel {
                            teamRow(id: InboxScope.myTeams, title: "My teams", count: env.directory.counts?.myTeams)
                            ForEach(myTeams) { team in
                                Hairline(inset: 46)
                                teamRow(id: team.id, title: team.name, count: env.directory.counts?.teams[team.id])
                            }
                        }
                    }
                    if !env.directory.views.isEmpty {
                        SectionLabel("Views")
                        Panel {
                            ForEach(Array(env.directory.views.enumerated()), id: \.element.id) { index, view in
                                if index > 0 { Hairline(inset: 46) }
                                row(scope: .view(view.id), title: view.name, symbol: view.shared == true ? "square.stack" : "person.crop.square", count: env.directory.counts?.views[view.id])
                            }
                        }
                    }
                    SectionLabel("Workspace")
                    Panel {
                        ForEach(Array(areas.enumerated()), id: \.element) { index, area in
                            if index > 0 { Hairline(inset: 46) }
                            areaRow(area)
                        }
                    }
                }
                .padding(20)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Inbox")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
    }

    /// The teams the signed-in member is on, as the web's sidebar lists them.
    private var myTeams: [Team] {
        guard let me = env.session.user?.id else { return [] }
        return env.directory.teams.filter { $0.memberIDs?.contains(me) == true }
    }

    private var areas: [AppArea] {
        AppArea.allCases.filter { $0 != .tickets && ($0 != .mail || env.directory.collab.enabled) }
    }

    private func areaRow(_ area: AppArea) -> some View {
        let selected = env.router.area == area
        return Button {
            Haptics.selection()
            env.router.show(area, [])
            dismiss()
        } label: {
            label(title: area.title, count: nil, selected: selected) { symbol(area.symbol, selected: selected) }
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    private func row(scope: InboxScope, title: String, symbol: String, count: Int?) -> some View {
        let selected = env.router.area == .tickets && env.inbox.scope == scope
        return Button {
            pick(scope)
        } label: {
            label(title: title, count: count, selected: selected) { self.symbol(symbol, selected: selected) }
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    /// A team inbox opens on its Unassigned tab; the count is what waits there.
    private func teamRow(id: String, title: String, count: TicketCounts.TeamCount?) -> some View {
        let selected = env.router.area == .tickets && env.inbox.scope.teamID == id
        return Button {
            pick(.team(id, .unassigned))
        } label: {
            label(title: title, count: count?.unassigned, selected: selected) {
                if id == InboxScope.myTeams {
                    symbol("person.3", selected: selected)
                } else {
                    InboxTeamDot(id: id, name: title, size: 9)
                }
            }
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(selected ? .isSelected : [])
        .accessibilityHint(count.map { "\($0.unassigned) unassigned, \($0.open) open" } ?? "")
    }

    private func pick(_ scope: InboxScope) {
        Haptics.selection()
        env.inbox.setScope(scope)
        env.router.show(.tickets, [])
        dismiss()
    }

    private func symbol(_ name: String, selected: Bool) -> some View {
        Image(systemName: name)
            .font(.system(size: 15))
            .foregroundStyle(selected ? Theme.fg : Theme.fg3)
    }

    private func label<Mark: View>(title: String, count: Int?, selected: Bool, @ViewBuilder mark: () -> Mark) -> some View {
        HStack(spacing: 12) {
            mark().frame(width: 20)
            Text(title)
                .font(ODFont.sans(15, weight: selected ? .semibold : .regular))
                .foregroundStyle(Theme.fg)
                .lineLimit(1)
            Spacer()
            if let count, count > 0 {
                Text("\(count)")
                    .font(ODFont.mono(12.5))
                    .monospacedDigit()
                    .foregroundStyle(Theme.fg3)
                    .contentTransition(.numericText())
            }
            if selected {
                Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg)
            }
        }
        .padding(.horizontal, 14)
        .frame(height: 46)
        .background(selected ? Theme.hover : .clear)
        .contentShape(Rectangle())
    }
}

/// The socket's state, as a small dot (green live, amber reconnecting).
struct RealtimeDot: View {
    let status: RealtimeService.Status

    var body: some View {
        let (color, label): (Color, String) = switch status {
        case .live: (Tone.green.solid, "Live")
        case .connecting, .reconnecting: (Tone.amber.solid, "Connecting")
        case .offline: (Tone.red.solid, "Offline")
        case .idle: (Theme.fg4, "Idle")
        }
        HStack(spacing: 5) {
            Circle().fill(color).frame(width: 7, height: 7)
            Text(label).font(ODFont.sans(12, weight: .medium)).foregroundStyle(Theme.fg3)
        }
        .accessibilityElement(children: .combine)
    }
}

/// Every sort with its direction, then priority, severity, channel, team, address,
/// label and the workspace's custom fields (the web's inbox filter menu).
struct FilterSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss

    private var inbox: InboxStore { env.inbox }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    sortSection
                    section("Priority") {
                        ForEach(Priority.allCases) { priority in
                            InboxFilterChip(title: priority.title, on: inbox.filters.priorities.contains(priority)) {
                                toggle(&env.inbox.filters.priorities, priority)
                            } mark: { PriorityIcon(priority: priority, size: 13) }
                        }
                    }
                    section("Severity") {
                        ForEach(Severity.allCases) { severity in
                            InboxFilterChip(title: severity.title, on: inbox.filters.severities.contains(severity.rawValue)) {
                                toggle(&env.inbox.filters.severities, severity.rawValue)
                            } mark: { SeverityChip(severity: severity) }
                        }
                        InboxFilterChip(title: "No severity", on: inbox.filters.severities.contains(InboxFilters.noSeverity)) {
                            toggle(&env.inbox.filters.severities, InboxFilters.noSeverity)
                        } mark: { EmptyView() }
                    }
                    section("Channel") {
                        ForEach(Channel.allCases) { channel in
                            InboxFilterChip(title: channel.title, on: inbox.filters.channel == channel) {
                                env.inbox.filters.channel = inbox.filters.channel == channel ? nil : channel
                            } mark: {
                                Image(systemName: channel.symbol).font(.system(size: 11)).foregroundStyle(Theme.fg3)
                            }
                        }
                    }
                    if inbox.scope.teamID == nil, !env.directory.teams.isEmpty {
                        section("Team") {
                            if !myTeams.isEmpty {
                                teamChip(InboxScope.myTeams, "My teams")
                            }
                            ForEach(env.directory.teams) { team in teamChip(team.id, team.name) }
                            teamChip(InboxFilters.noTeam, "No team")
                        }
                    }
                    if inbox.mailboxes.count > 1 || inbox.filters.mailboxID != nil {
                        section("Address") {
                            ForEach(inbox.mailboxes) { mailbox in
                                InboxFilterChip(title: mailbox.email, on: inbox.filters.mailboxID == mailbox.id) {
                                    env.inbox.filters.mailboxID = inbox.filters.mailboxID == mailbox.id ? nil : mailbox.id
                                } mark: {
                                    Image(systemName: "at").font(.system(size: 11)).foregroundStyle(Theme.fg3)
                                }
                            }
                        }
                    }
                    if !labels.isEmpty {
                        section("Label") {
                            ForEach(labels) { tag in
                                InboxFilterChip(title: tag.name, on: inbox.filters.tag?.caseInsensitiveCompare(tag.name) == .orderedSame) {
                                    env.inbox.filters.tag = inbox.filters.tag == tag.name ? nil : tag.name
                                } mark: {
                                    Circle().fill(Tone(label: tag.color).solid).frame(width: 7, height: 7)
                                }
                            }
                        }
                    }
                    ForEach(fields) { field in
                        InboxFieldFilter(field: field)
                    }
                }
                .padding(20)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Filter and sort")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Clear") {
                        Haptics.selection()
                        env.inbox.filters = InboxFilters()
                        env.inbox.sort = .latest
                    }
                    .disabled(inbox.filters.isEmpty && inbox.sort.isDefault)
                }
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
            .task { await inbox.loadMailboxes() }
        }
    }

    private var myTeams: [Team] {
        guard let me = env.session.user?.id else { return [] }
        return env.directory.teams.filter { $0.memberIDs?.contains(me) == true }
    }

    private var labels: [Tag] {
        env.directory.tags.filter { $0.archived != true && ($0.appliesTo.map { $0.contains("ticket") } ?? true) }
    }

    private var fields: [CustomField] { env.directory.fields(for: "ticket") }

    /// Fields the API can order by: text, number, date, checkbox and select.
    private var sortKeys: [TicketSortKey] {
        TicketSortKey.builtIn + fields.filter { [.text, .number, .date, .checkbox, .select].contains($0.type) }.map { .field($0.key) }
    }

    private func title(of key: TicketSortKey) -> String {
        if case let .field(name) = key { return fields.first { $0.key == name }?.name ?? name }
        return key.title
    }

    private var sortSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            SectionLabel("Sort by")
            Panel {
                ForEach(Array(sortKeys.enumerated()), id: \.element) { index, key in
                    if index > 0 { Hairline(inset: 14) }
                    Button {
                        Haptics.selection()
                        guard inbox.sort.key != key else { return }
                        env.inbox.sort = TicketSort(key: key, descending: key.defaultDescending)
                    } label: {
                        HStack {
                            Text(title(of: key)).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                            if case .field = key {
                                Text("Field").font(ODFont.sans(11.5)).foregroundStyle(Theme.fg4)
                            }
                            Spacer()
                            if inbox.sort.key == key {
                                Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg)
                            }
                        }
                        .padding(.horizontal, 14)
                        .frame(height: 44)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(RowPressStyle())
                    .accessibilityAddTraits(inbox.sort.key == key ? .isSelected : [])
                }
            }
            InboxSegmented(
                options: [true, false].map { (value: $0, title: inbox.sort.key.directionTitle(descending: $0)) },
                selection: inbox.sort.descending
            ) { descending in
                env.inbox.sort.descending = descending
            }
            .padding(.top, 2)
        }
    }

    private func teamChip(_ value: String, _ title: String) -> some View {
        InboxFilterChip(title: title, on: inbox.filters.teams.contains(value)) {
            toggle(&env.inbox.filters.teams, value)
        } mark: {
            InboxTeamDot(id: value == InboxFilters.noTeam ? nil : value, name: title)
        }
    }

    private func section<Content: View>(_ title: String, @ViewBuilder content: @escaping () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            SectionLabel(title)
            FlowLayout(spacing: 7) { content() }
        }
    }

    private func toggle<T: Hashable>(_ set: inout Set<T>, _ value: T) {
        if set.contains(value) { set.remove(value) } else { set.insert(value) }
    }
}

/// One custom field's filter, by its type: option chips for select, multi-select and
/// user fields, Yes / No for a checkbox, a typed value for the rest.
struct InboxFieldFilter: View {
    @Environment(AppEnvironment.self) private var env
    let field: CustomField
    @State private var text = ""
    @State private var date = AppClock.now

    private var filter: FieldFilter? { env.inbox.filters.fields.first { $0.key == field.key } }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            SectionLabel(field.name)
            switch field.type {
            case .select, .multiSelect:
                FlowLayout(spacing: 7) {
                    ForEach(field.options) { option in
                        chip(option.label, value: option.id) {
                            Circle().fill(Tone(label: option.color).solid).frame(width: 7, height: 7)
                        }
                    }
                }
            case .user:
                FlowLayout(spacing: 7) {
                    chip("Me", value: "me") { EmptyView() }
                    ForEach(env.directory.members.filter { $0.id != env.session.user?.id }) { member in
                        chip(member.user.name, value: member.id) {
                            Avatar(name: member.user.name, url: member.user.avatarURL, seed: member.id, size: 16)
                        }
                    }
                }
            case .checkbox:
                FlowLayout(spacing: 7) {
                    chip("Yes", value: "true") { EmptyView() }
                    chip("No", value: "false") { EmptyView() }
                }
            case .date:
                HStack {
                    Text("On or after").font(ODFont.sans(15)).foregroundStyle(Theme.fg2)
                    Spacer()
                    if filter != nil {
                        DatePicker("", selection: $date, displayedComponents: .date).labelsHidden()
                        Button {
                            set(nil)
                        } label: {
                            Image(systemName: "xmark.circle.fill").font(.system(size: 16)).foregroundStyle(Theme.fg4)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Remove filter \(field.name)")
                    } else {
                        Button("Pick a date") { set(Self.day.string(from: date)) }
                            .font(ODFont.sans(14, weight: .medium))
                            .foregroundStyle(Theme.fg)
                    }
                }
                .padding(.horizontal, 14)
                .frame(height: 46)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                .onChange(of: date) { _, value in if filter != nil { set(Self.day.string(from: value)) } }
            default:
                HStack(spacing: 8) {
                    Text(field.type == .number ? "Equals" : "Contains").font(ODFont.sans(13)).foregroundStyle(Theme.fg4)
                    TextField(text: $text) { Text(field.type == .number ? "A number" : "Any text").foregroundStyle(Theme.fg4) }
                        .font(ODFont.sans(15))
                        .keyboardType(field.type == .number ? .decimalPad : .default)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .submitLabel(.done)
                        .onSubmit { commit() }
                    if filter != nil {
                        Button {
                            text = ""
                            set(nil)
                        } label: {
                            Image(systemName: "xmark.circle.fill").font(.system(size: 16)).foregroundStyle(Theme.fg4)
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel("Remove filter \(field.name)")
                    }
                }
                .padding(.horizontal, 14)
                .frame(height: 46)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                .onDisappear { commit() }
            }
        }
        .onAppear {
            guard let filter else { return }
            if field.type == .date, let day = Self.day.date(from: filter.value) { date = day } else { text = filter.value }
        }
    }

    private func chip<Mark: View>(_ title: String, value: String, @ViewBuilder mark: @escaping () -> Mark) -> some View {
        InboxFilterChip(title: title, on: env.inbox.filters.values(of: field.key).contains(value)) {
            if field.type == .checkbox, env.inbox.filters.values(of: field.key) != [value] {
                set(value)
            } else {
                env.inbox.filters.toggle(field: field, value: value)
            }
        } mark: { mark() }
    }

    private func commit() {
        let value = text.trimmingCharacters(in: .whitespaces)
        guard value != (filter?.value ?? "") else { return }
        set(value.isEmpty ? nil : value)
    }

    private func set(_ value: String?) {
        var filters = env.inbox.filters
        filters.fields.removeAll { $0.key == field.key }
        if let value {
            let op = switch field.type {
            case .number, .checkbox: "eq"
            case .date: "gte"
            default: "contains"
            }
            filters.fields.append(FieldFilter(key: field.key, op: op, value: value))
        }
        env.inbox.filters = filters
    }

    private static let day: DateFormatter = {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter
    }()
}

/// A filter chip: a small mark, its title, ink when on.
struct InboxFilterChip<Mark: View>: View {
    let title: String
    let on: Bool
    let action: () -> Void
    @ViewBuilder var mark: () -> Mark

    var body: some View {
        Button {
            Haptics.selection()
            action()
        } label: {
            HStack(spacing: 6) {
                mark()
                Text(title)
                    .font(ODFont.sans(13.5, weight: .medium))
                    .lineLimit(1)
            }
            .foregroundStyle(on ? Theme.inverseFg : Theme.fg)
            .padding(.horizontal, 11)
            .frame(height: 32)
            .background(on ? Theme.inverse : Theme.surface, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(on ? .clear : Theme.line, lineWidth: 1))
        }
        .buttonStyle(TapScaleStyle())
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

/// A small two-or-more choice control in the app's greys (a sunken track, a white thumb).
struct InboxSegmented<Value: Hashable>: View {
    let options: [(value: Value, title: String)]
    let selection: Value
    let onPick: (Value) -> Void
    @Namespace private var thumb
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 2) {
            ForEach(Array(options.enumerated()), id: \.offset) { _, option in
                let on = option.value == selection
                Button {
                    guard !on else { return }
                    Haptics.selection()
                    withAnimation(reduceMotion ? nil : Motion.snappy) { onPick(option.value) }
                } label: {
                    Text(option.title)
                        .font(ODFont.sans(13.5, weight: on ? .semibold : .medium))
                        .foregroundStyle(on ? Theme.fg : Theme.fg3)
                        .frame(maxWidth: .infinity)
                        .frame(height: 32)
                        .background {
                            if on {
                                RoundedRectangle(cornerRadius: 8, style: .continuous)
                                    .fill(Theme.surface)
                                    .shadow(color: .black.opacity(0.06), radius: 2, y: 1)
                                    .matchedGeometryEffect(id: "thumb", in: thumb)
                            }
                        }
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(on ? .isSelected : [])
            }
        }
        .padding(3)
        .background(Theme.sunken, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: 10, style: .continuous).strokeBorder(Theme.lineSoft, lineWidth: 1))
    }
}

/// Toggle chips that wrap.
struct FlowChips: View {
    let items: [(String, String)]
    let selected: Set<String>
    let toggle: (String) -> Void

    var body: some View {
        FlowLayout(spacing: 7) {
            ForEach(items, id: \.0) { item in
                let on = selected.contains(item.0)
                Button {
                    Haptics.selection()
                    toggle(item.0)
                } label: {
                    Text(item.1)
                        .font(ODFont.sans(13.5, weight: .medium))
                        .foregroundStyle(on ? Theme.inverseFg : Theme.fg)
                        .padding(.horizontal, 12)
                        .frame(height: 32)
                        .background(on ? Theme.inverse : Theme.surface, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: 8, style: .continuous).strokeBorder(on ? .clear : Theme.line, lineWidth: 1))
                }
                .buttonStyle(TapScaleStyle())
                .accessibilityAddTraits(on ? .isSelected : [])
            }
        }
    }
}

/// A simple wrapping layout.
struct FlowLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let width = proposal.width ?? .infinity
        var x: CGFloat = 0, y: CGFloat = 0, row: CGFloat = 0, widest: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > 0, x + size.width > width {
                y += row + spacing
                x = 0
                row = 0
            }
            x += size.width + spacing
            row = max(row, size.height)
            widest = max(widest, x - spacing)
        }
        return CGSize(width: proposal.width ?? widest, height: y + row)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX, y = bounds.minY, row: CGFloat = 0
        for view in subviews {
            let size = view.sizeThatFits(.unspecified)
            if x > bounds.minX, x + size.width > bounds.maxX {
                y += row + spacing
                x = bounds.minX
                row = 0
            }
            view.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize(size))
            x += size.width + spacing
            row = max(row, size.height)
        }
    }
}

/// Snooze presets, or a date and time.
struct SnoozeSheet: View {
    @Environment(\.dismiss) private var dismiss
    let onPick: (Date) -> Void
    @State private var custom = Calendar.current.date(byAdding: .day, value: 1, to: AppClock.now) ?? AppClock.now
    @State private var showCustom = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 14) {
                    Panel {
                        ForEach(Array(SnoozePreset.allCases.enumerated()), id: \.element) { index, preset in
                            if index > 0 { Hairline(inset: 14) }
                            Button {
                                pick(preset.date())
                            } label: {
                                HStack {
                                    Text(preset.title).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                                    Spacer()
                                    Text(preset.subtitle()).font(ODFont.sans(13.5)).foregroundStyle(Theme.fg3)
                                }
                                .padding(.horizontal, 14)
                                .frame(height: 46)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(RowPressStyle())
                        }
                    }
                    Panel(padding: 14) {
                        DatePicker("Pick a date", selection: $custom, in: AppClock.now..., displayedComponents: [.date, .hourAndMinute])
                            .font(ODFont.sans(15))
                        Button("Snooze until then") { pick(custom) }
                            .buttonStyle(InkButtonStyle(height: 40))
                            .padding(.top, 10)
                    }
                    Text("The ticket goes on hold and comes back when the time comes or the customer replies.")
                        .font(ODFont.footnote)
                        .foregroundStyle(Theme.fg3)
                }
                .padding(20)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Snooze until")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
    }

    private func pick(_ date: Date) {
        Haptics.tap()
        onPick(date)
        dismiss()
    }
}
