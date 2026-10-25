import SwiftUI

/// Reading and checking custom field values by type (contracts/api.md "Custom fields").
enum TicketFieldValue {
    /// Empty for solving: nothing set, blank text, no options, or a checkbox not ticked.
    static func isEmpty(_ value: JSONValue?, type: CustomField.Kind) -> Bool {
        guard let value, !value.isNull else { return true }
        switch type {
        case .checkbox: return value.bool != true
        case .multiSelect: return (value.array ?? []).isEmpty
        case .number: return value.double == nil && (value.string ?? "").isEmpty
        default: return (value.string ?? "").trimmingCharacters(in: .whitespaces).isEmpty
        }
    }

    static func display(_ value: JSONValue?, field: CustomField, members: [Member]) -> String? {
        guard let value, !value.isNull else { return nil }
        switch field.type {
        case .select:
            guard let id = value.string else { return nil }
            return field.options.first { $0.id == id || $0.label == id }?.label ?? id
        case .multiSelect:
            let ids = value.array?.compactMap(\.string) ?? []
            let labels = ids.map { id in field.options.first { $0.id == id || $0.label == id }?.label ?? id }
            return labels.isEmpty ? nil : labels.joined(separator: ", ")
        case .date:
            guard let raw = value.string, let date = day(raw) else { return value.string }
            return date.formatted(.dateTime.day().month(.abbreviated).year())
        case .checkbox:
            return value.bool == true ? "Yes" : "No"
        case .number:
            if let number = value.double { return number.formatted(.number.grouping(.automatic)) }
            return value.string
        case .user:
            guard let id = value.string else { return nil }
            return members.first { $0.id == id }?.user.name ?? "Someone"
        default:
            guard let text = value.string ?? value.displayText, !text.isEmpty else { return nil }
            return text
        }
    }

    static func day(_ raw: String) -> Date? {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = .current
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.date(from: String(raw.prefix(10)))
    }

    static func dayString(_ date: Date) -> String {
        let formatter = DateFormatter()
        formatter.calendar = Calendar(identifier: .gregorian)
        formatter.locale = Locale(identifier: "en_US_POSIX")
        formatter.timeZone = .current
        formatter.dateFormat = "yyyy-MM-dd"
        return formatter.string(from: date)
    }

    static func symbol(_ type: CustomField.Kind) -> String {
        switch type {
        case .text: "textformat"
        case .longText: "text.alignleft"
        case .number: "number"
        case .select: "chevron.up.chevron.down"
        case .multiSelect: "list.bullet"
        case .date: "calendar"
        case .checkbox: "checkmark.square"
        case .url: "link"
        case .email: "envelope"
        case .user: "person"
        }
    }
}

/// An editor for one value, shaped by the field's type.
struct FieldValueEditor: View {
    let field: CustomField
    @Binding var value: JSONValue?
    let members: [Member]
    @State private var text = ""
    @State private var date = AppClock.now

    var body: some View {
        Group {
            switch field.type {
            case .text, .url, .email, .number:
                TextField(text: $text) { Text(prompt).foregroundStyle(Theme.fg4) }
                    .font(ODFont.sans(15.5))
                    .keyboardType(keyboard)
                    .textInputAutocapitalization(field.type == .text ? .sentences : .never)
                    .autocorrectionDisabled(field.type != .text)
                    .padding(.horizontal, 12)
                    .frame(height: 44)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                    .onChange(of: text) { _, new in value = encode(new) }
            case .longText:
                TextField(text: $text, axis: .vertical) { Text(prompt).foregroundStyle(Theme.fg4) }
                    .font(ODFont.sans(15.5))
                    .lineLimit(4 ... 12)
                    .padding(12)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                    .onChange(of: text) { _, new in value = encode(new) }
            case .select:
                options(multiple: false)
            case .multiSelect:
                options(multiple: true)
            case .date:
                VStack(alignment: .trailing, spacing: 6) {
                    DatePicker("Date", selection: $date, displayedComponents: .date)
                        .datePickerStyle(.graphical)
                        .tint(Theme.fg)
                        .padding(.horizontal, 8)
                        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                        .onChange(of: date) { _, new in value = .string(TicketFieldValue.dayString(new)) }
                    if value != nil {
                        Button("Clear date") { value = nil }
                            .font(ODFont.sans(13, weight: .medium))
                            .foregroundStyle(Theme.fg3)
                    }
                }
            case .checkbox:
                Toggle(isOn: Binding(get: { value?.bool == true }, set: { value = .bool($0) })) {
                    Text(field.name).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                }
                .tint(Tone.green.solid)
                .padding(.horizontal, 14)
                .frame(height: 50)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            case .user:
                Panel {
                    choice(title: "Nobody", selected: value?.string == nil) { value = nil } leading: {
                        Image(systemName: "minus").font(.system(size: 12, weight: .medium)).foregroundStyle(Theme.fg4).frame(width: 24)
                    }
                    ForEach(members) { member in
                        Hairline(inset: 48)
                        choice(title: member.user.name, selected: value?.string == member.id) { value = .string(member.id) } leading: {
                            Avatar(name: member.user.name, url: member.user.avatarURL, seed: member.id, size: 24)
                        }
                    }
                }
            }
        }
        .onAppear(perform: load)
    }

    private var prompt: String {
        switch field.type {
        case .url: "https://"
        case .email: "name@company.com"
        case .number: "0"
        default: field.description.isEmpty ? field.name : field.description
        }
    }

    private var keyboard: UIKeyboardType {
        switch field.type {
        case .url: .URL
        case .email: .emailAddress
        case .number: .decimalPad
        default: .default
        }
    }

    private func load() {
        switch field.type {
        case .number: text = value?.double.map { $0.formatted(.number.grouping(.never)) } ?? value?.string ?? ""
        case .date: date = value?.string.flatMap(TicketFieldValue.day) ?? AppClock.now
        default: text = value?.string ?? ""
        }
    }

    private func encode(_ raw: String) -> JSONValue? {
        let trimmed = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        if field.type == .number {
            let normalized = trimmed.replacingOccurrences(of: ",", with: ".")
            return Double(normalized).map(JSONValue.number) ?? .string(trimmed)
        }
        return .string(field.type == .longText ? raw : trimmed)
    }

    private func options(multiple: Bool) -> some View {
        Panel {
            ForEach(Array(field.options.enumerated()), id: \.element.id) { index, option in
                if index > 0 { Hairline(inset: 44) }
                let selected = multiple ? (value?.array?.contains(.string(option.id)) ?? false) : value?.string == option.id
                choice(title: option.label, selected: selected) {
                    if multiple {
                        var ids = value?.array ?? []
                        if selected { ids.removeAll { $0 == .string(option.id) } } else { ids.append(.string(option.id)) }
                        value = ids.isEmpty ? nil : .array(ids)
                    } else {
                        value = selected ? nil : .string(option.id)
                    }
                } leading: {
                    Circle().fill(Tone(label: option.color).solid).frame(width: 9, height: 9).frame(width: 20)
                }
            }
        }
    }

    private func choice<Leading: View>(title: String, selected: Bool, action: @escaping () -> Void, @ViewBuilder leading: () -> Leading) -> some View {
        Button {
            Haptics.selection()
            action()
        } label: {
            HStack(spacing: 12) {
                leading()
                Text(title).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                Spacer()
                if selected { Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg) }
            }
            .padding(.horizontal, 14)
            .frame(minHeight: 46)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// Edit one custom field of the ticket.
struct FieldEditorSheet: View {
    let field: CustomField
    let initial: JSONValue?
    let members: [Member]
    let onSave: (JSONValue?) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var value: JSONValue?

    init(field: CustomField, initial: JSONValue?, members: [Member], onSave: @escaping (JSONValue?) -> Void) {
        self.field = field
        self.initial = initial
        self.members = members
        self.onSave = onSave
        _value = State(initialValue: initial)
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    if !field.description.isEmpty {
                        Text(field.description).font(ODFont.footnote).foregroundStyle(Theme.fg3).padding(.horizontal, 4)
                    }
                    FieldValueEditor(field: field, value: $value, members: members)
                    if field.requiredOnSolve {
                        Label("Needed before solving", systemImage: "checkmark.seal")
                            .font(ODFont.sans(12.5))
                            .foregroundStyle(Theme.fg3)
                            .padding(.horizontal, 4)
                    }
                }
                .padding(16)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle(field.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") {
                        onSave(value)
                        dismiss()
                    }
                    .fontWeight(.semibold)
                    .disabled(value == initial)
                }
            }
        }
    }
}

/// The fields a ticket needs before it can be solved, filled in place, then solved.
struct RequiredFieldsSheet: View {
    let fields: [CustomField]
    let initial: [String: JSONValue]
    let members: [Member]
    let onSolve: ([String: JSONValue?]) async -> Bool
    @Environment(\.dismiss) private var dismiss
    @State private var values: [String: JSONValue?] = [:]
    @State private var saving = false

    private var missing: [CustomField] {
        fields.filter { TicketFieldValue.isEmpty(values[$0.key] ?? nil, type: $0.type) }
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    HStack(alignment: .top, spacing: 10) {
                        Image(systemName: "checkmark.seal").font(.system(size: 15, weight: .semibold)).foregroundStyle(Tone.amber.text)
                            .frame(width: 30, height: 30).background(Tone.amber.soft, in: Circle())
                        Text(fields.count == 1 ? "Fill in \(fields[0].name) before solving." : "This ticket needs \(Format.plural(fields.count, "field")) before it can be solved.")
                            .font(ODFont.sans(14.5))
                            .foregroundStyle(Theme.fg2)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    ForEach(fields) { field in
                        VStack(alignment: .leading, spacing: 8) {
                            HStack(spacing: 6) {
                                Image(systemName: TicketFieldValue.symbol(field.type)).font(.system(size: 11, weight: .medium)).foregroundStyle(Theme.fg3)
                                Text(field.name).font(ODFont.sans(13.5, weight: .semibold)).foregroundStyle(Theme.fg)
                            }
                            .padding(.horizontal, 4)
                            FieldValueEditor(field: field, value: binding(field.key), members: members)
                        }
                    }
                }
                .padding(16)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Before solving")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        saving = true
                        Task {
                            let ok = await onSolve(values)
                            saving = false
                            if ok { dismiss() }
                        }
                    } label: {
                        if saving { ProgressView() } else { Text("Solve").fontWeight(.semibold) }
                    }
                    .disabled(!missing.isEmpty || saving)
                }
            }
            .onAppear {
                for field in fields where values[field.key] == nil { values[field.key] = .some(initial[field.key]) }
            }
        }
    }

    private func binding(_ key: String) -> Binding<JSONValue?> {
        Binding(get: { values[key] ?? nil }, set: { values[key] = .some($0) })
    }
}
