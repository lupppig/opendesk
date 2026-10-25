import SwiftUI

/// A searchable picker sheet frame.
struct PickerSheet<Content: View>: View {
    let title: String
    @Binding var query: String
    var prompt = "Search"
    var trailing: (title: String, action: () -> Void)?
    @ViewBuilder var content: () -> Content
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(spacing: 14) {
                    HStack(spacing: 8) {
                        Image(systemName: "magnifyingglass").font(.system(size: 14)).foregroundStyle(Theme.fg3)
                        TextField(text: $query) { Text(prompt).foregroundStyle(Theme.fg4) }
                            .font(ODFont.sans(15.5))
                            .textInputAutocapitalization(.never)
                            .autocorrectionDisabled()
                    }
                    .padding(.horizontal, 12)
                    .frame(height: 40)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                    content()
                }
                .padding(16)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                if let trailing {
                    ToolbarItem(placement: .confirmationAction) { Button(trailing.title, action: trailing.action).fontWeight(.semibold) }
                }
            }
        }
    }
}

/// Assign to a teammate (or nobody). Shows who's online and their open load.
struct AssigneePicker: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    let current: String?
    let onPick: (String?) -> Void
    @State private var query = ""

    private var members: [Member] {
        let all = env.directory.assignable
        let me = env.session.user?.id
        let sorted = all.sorted { a, b in
            if a.id == me { return true }
            if b.id == me { return false }
            return a.user.name.localizedCaseInsensitiveCompare(b.user.name) == .orderedAscending
        }
        guard !query.isEmpty else { return sorted }
        return sorted.filter { $0.user.name.localizedCaseInsensitiveContains(query) || ($0.user.email ?? "").localizedCaseInsensitiveContains(query) }
    }

    var body: some View {
        PickerSheet(title: "Assign to", query: $query, prompt: "Search people") {
            Panel {
                if query.isEmpty {
                    row(id: nil, name: "Unassigned", subtitle: nil) {
                        Image(systemName: "person.crop.circle.badge.questionmark").font(.system(size: 20)).foregroundStyle(Theme.fg3).frame(width: 30)
                    }
                    Hairline(inset: 56)
                }
                ForEach(Array(members.enumerated()), id: \.element.id) { index, member in
                    if index > 0 { Hairline(inset: 56) }
                    let presence = member.presence ?? member.availability ?? .offline
                    row(
                        id: member.id,
                        name: member.id == env.session.user?.id ? "\(member.user.name) (you)" : member.user.name,
                        subtitle: [presence.title, member.openTickets.map { Format.plural($0, "open ticket") }].compactMap { $0 }.joined(separator: " · ")
                    ) {
                        Avatar(name: member.user.name, url: member.user.avatarURL, seed: member.id, size: 30)
                            .overlay(alignment: .bottomTrailing) {
                                Circle()
                                    .fill(presence == .online ? Tone.green.solid : presence == .away ? Tone.amber.solid : Theme.fg4)
                                    .frame(width: 9, height: 9)
                                    .overlay(Circle().strokeBorder(Theme.surface, lineWidth: 1.5))
                            }
                    }
                }
            }
        }
    }

    private func row<Leading: View>(id: String?, name: String, subtitle: String?, @ViewBuilder leading: () -> Leading) -> some View {
        Button {
            Haptics.selection()
            onPick(id)
            dismiss()
        } label: {
            HStack(spacing: 12) {
                leading()
                VStack(alignment: .leading, spacing: 1) {
                    Text(name).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                    if let subtitle, !subtitle.isEmpty { Text(subtitle).font(ODFont.footnote).foregroundStyle(Theme.fg3) }
                }
                Spacer()
                if id == current { Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg) }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 9)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
    }
}

struct TeamPicker: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    let current: String?
    let onPick: (String?) -> Void
    @State private var query = ""

    var body: some View {
        let teams = env.directory.teams.filter { query.isEmpty || $0.name.localizedCaseInsensitiveContains(query) }
        PickerSheet(title: "Team", query: $query, prompt: "Search teams") {
            Panel {
                row(id: nil, name: "No team")
                ForEach(teams) { team in
                    Hairline(inset: 14)
                    row(id: team.id, name: team.name)
                }
            }
        }
    }

    private func row(id: String?, name: String) -> some View {
        Button {
            Haptics.selection()
            onPick(id)
            dismiss()
        } label: {
            HStack {
                Text(name).font(ODFont.sans(15)).foregroundStyle(id == nil ? Theme.fg3 : Theme.fg)
                Spacer()
                if id == current { Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg) }
            }
            .padding(.horizontal, 14)
            .frame(height: 46)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
    }
}

/// Add and remove labels; typing a new name offers to create it (the server decides who may).
struct LabelPicker: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    let current: [TagRef]
    let onSave: (_ add: [String], _ remove: [String]) -> Void
    @State private var query = ""
    @State private var selected: Set<String> = []

    private var initial: Set<String> { Set(current.map { $0.name.lowercased() }) }

    var body: some View {
        let tags = env.directory.tags.filter { query.isEmpty || $0.name.localizedCaseInsensitiveContains(query) }
        let trimmed = query.trimmingCharacters(in: .whitespaces)
        let exists = env.directory.tag(named: trimmed) != nil || selected.contains(trimmed.lowercased())
        PickerSheet(title: "Labels", query: $query, prompt: "Search or add a label", trailing: ("Save", save)) {
            Panel {
                if !trimmed.isEmpty, !exists {
                    Button {
                        selected.insert(trimmed.lowercased())
                        names[trimmed.lowercased()] = trimmed
                        query = ""
                    } label: {
                        HStack(spacing: 10) {
                            Image(systemName: "plus").font(.system(size: 13, weight: .semibold))
                            Text("Add \u{201C}\(trimmed)\u{201D}").font(ODFont.sans(15))
                            Spacer()
                        }
                        .foregroundStyle(Theme.fg)
                        .padding(.horizontal, 14)
                        .frame(height: 46)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(RowPressStyle())
                    Hairline(inset: 14)
                }
                ForEach(extraNames, id: \.self) { name in
                    toggleRow(name: name, color: nil)
                    Hairline(inset: 14)
                }
                ForEach(Array(tags.enumerated()), id: \.element.id) { index, tag in
                    if index > 0 { Hairline(inset: 14) }
                    toggleRow(name: tag.name, color: tag.color)
                }
            }
        }
        .onAppear { selected = initial }
    }

    @State private var names: [String: String] = [:]

    /// Selected names that aren't (or are no longer) workspace labels: new ones typed here, archived ones on the ticket.
    private var extraNames: [String] {
        selected.filter { env.directory.tag(named: $0) == nil }
            .compactMap { key in names[key] ?? current.first { $0.name.lowercased() == key }?.name }
            .sorted()
    }

    private func toggleRow(name: String, color: String?) -> some View {
        let key = name.lowercased()
        let on = selected.contains(key)
        return Button {
            Haptics.selection()
            if on { selected.remove(key) } else { selected.insert(key); names[key] = name }
        } label: {
            HStack(spacing: 10) {
                Circle().fill(Tone(label: color).solid).frame(width: 9, height: 9)
                Text(name).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                Spacer()
                Image(systemName: on ? "checkmark.square.fill" : "square")
                    .font(.system(size: 18))
                    .foregroundStyle(on ? Theme.fg : Theme.fg4)
            }
            .padding(.horizontal, 14)
            .frame(height: 46)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    private func save() {
        let add = selected.subtracting(initial).map { names[$0] ?? env.directory.tag(named: $0)?.name ?? $0 }
        let remove = initial.subtracting(selected).compactMap { key in current.first { $0.name.lowercased() == key }?.name }
        onSave(add, remove)
        dismiss()
    }
}

/// A short list of choices in a sheet, without search: status, priority, severity.
struct ChoiceSheet<Content: View>: View {
    let title: String
    var footnote: String?
    @ViewBuilder var content: () -> Content
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 12) {
                    Panel { content() }
                    if let footnote {
                        Text(footnote).font(ODFont.footnote).foregroundStyle(Theme.fg3).padding(.horizontal, 4)
                    }
                }
                .padding(16)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle(title)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
    }
}

/// One row of a `ChoiceSheet`: a glyph, a title, an optional hint, and a check on the current one.
struct ChoiceRow<Leading: View>: View {
    let title: String
    var hint: String?
    var selected = false
    let action: () -> Void
    @ViewBuilder var leading: () -> Leading
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        Button {
            Haptics.selection()
            action()
            dismiss()
        } label: {
            HStack(spacing: 12) {
                leading().frame(width: 22)
                VStack(alignment: .leading, spacing: 1) {
                    Text(title).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                    if let hint { Text(hint).font(ODFont.footnote).foregroundStyle(Theme.fg3) }
                }
                Spacer()
                if selected { Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg) }
            }
            .padding(.horizontal, 14)
            .frame(minHeight: 48)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

struct StatusPicker: View {
    let current: TicketStatus
    let onPick: (TicketStatus) -> Void

    var body: some View {
        ChoiceSheet(title: "Status", footnote: "Pending waits on the customer; their next message opens the ticket again.") {
            ForEach(Array(TicketStatus.allCases.enumerated()), id: \.element) { index, status in
                if index > 0 { Hairline(inset: 48) }
                ChoiceRow(title: status.title, hint: status.hint, selected: status == current, action: { onPick(status) }) {
                    StatusIcon(status: status, size: 16)
                }
            }
        }
    }
}

struct PriorityPicker: View {
    let current: Priority
    let onPick: (Priority) -> Void

    var body: some View {
        ChoiceSheet(title: "Priority") {
            ForEach(Array(Priority.allCases.enumerated()), id: \.element) { index, priority in
                if index > 0 { Hairline(inset: 48) }
                ChoiceRow(title: priority.title, selected: priority == current, action: { onPick(priority) }) {
                    PriorityIcon(priority: priority, size: 16)
                }
            }
        }
    }
}

struct SeverityPicker: View {
    let current: Severity?
    let onPick: (Severity?) -> Void

    var body: some View {
        ChoiceSheet(title: "Severity", footnote: "How much of the customer's work is affected, from a full outage to a small annoyance.") {
            ChoiceRow(title: "No severity", selected: current == nil, action: { onPick(nil) }) {
                Image(systemName: "minus").font(.system(size: 13, weight: .medium)).foregroundStyle(Theme.fg4)
            }
            ForEach(Severity.allCases) { severity in
                Hairline(inset: 48)
                ChoiceRow(title: severity.title, hint: severity.hint, selected: severity == current, action: { onPick(severity) }) {
                    SeverityChip(severity: severity)
                }
            }
        }
    }
}

extension TicketStatus {
    var hint: String {
        switch self {
        case .open: "Needs an answer from the team"
        case .pending: "Waiting on the customer"
        case .onHold: "Waiting on someone else, or snoozed"
        case .solved: "Answered; a reply opens it again"
        case .closed: "Done and kept for reference"
        }
    }
}
