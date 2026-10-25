import SwiftUI

/// A contact's or company's custom fields, one row each, edited in place:
/// checkboxes toggle, selects and people pick from a menu, the rest open
/// `DirectoryFieldEditor`.
struct DirectoryFieldsPanel: View {
    let fields: [CustomField]
    let values: [String: JSONValue]
    var canEdit: Bool
    var members: [Member]
    let onChange: (String, JSONValue?) -> Void

    @State private var editing: CustomField?

    var body: some View {
        Panel {
            ForEach(Array(fields.enumerated()), id: \.element.id) { index, field in
                if index > 0 { Hairline(inset: 14) }
                row(field)
            }
        }
        .sheet(item: $editing) { field in
            DirectoryFieldEditor(field: field, value: values[field.key], members: members) { value in
                onChange(field.key, value)
            }
            .presentationDetents(field.type == .date || field.type == .multiSelect || field.type == .longText ? [.medium, .large] : [.height(260)])
            .presentationCornerRadius(Theme.Radius.sheet)
        }
    }

    @ViewBuilder
    private func row(_ field: CustomField) -> some View {
        let value = values[field.key]
        switch field.type {
        case .checkbox:
            HStack(spacing: 12) {
                label(field)
                Spacer(minLength: 8)
                Toggle(isOn: Binding(
                    get: { value?.bool == true },
                    set: { on in
                        Haptics.selection()
                        onChange(field.key, on ? .bool(true) : nil)
                    }
                )) { EmptyView() }
                    .labelsHidden()
                    .tint(Theme.inverse)
                    .disabled(!canEdit)
            }
            .padding(.horizontal, 14)
            .frame(minHeight: 48)
        case .select:
            Menu {
                ForEach(field.options) { option in
                    Button {
                        Haptics.selection()
                        onChange(field.key, .string(option.id))
                    } label: {
                        if value?.string == option.id { Label(option.label, systemImage: "checkmark") } else { Text(option.label) }
                    }
                }
                if value != nil {
                    Divider()
                    Button("Clear", role: .destructive) { onChange(field.key, nil) }
                }
            } label: {
                line(field, value: value) {
                    if let id = value?.string, let option = field.options.first(where: { $0.id == id }) {
                        TonePill(text: option.label, tone: Tone(label: option.color), height: 22) {
                            Circle().fill(Tone(label: option.color).solid).frame(width: 6, height: 6)
                        }
                    } else {
                        DirectoryNone(text: "Empty")
                    }
                }
            }
            .disabled(!canEdit)
        case .user:
            Menu {
                ForEach(members.filter { $0.role != .viewer }) { member in
                    Button {
                        Haptics.selection()
                        onChange(field.key, .string(member.id))
                    } label: {
                        if value?.string == member.id { Label(member.user.name, systemImage: "checkmark") } else { Text(member.user.name) }
                    }
                }
                if value != nil {
                    Divider()
                    Button("Clear", role: .destructive) { onChange(field.key, nil) }
                }
            } label: {
                line(field, value: value) {
                    if let id = value?.string {
                        let member = members.first { $0.id == id }
                        HStack(spacing: 7) {
                            Avatar(name: member?.user.name ?? "?", url: member?.user.avatarURL, seed: id, size: 20)
                            Text(member?.user.name ?? "Someone").font(ODFont.sans(14.5)).foregroundStyle(Theme.fg).lineLimit(1)
                        }
                    } else {
                        DirectoryNone(text: "Empty")
                    }
                }
            }
            .disabled(!canEdit)
        default:
            Button {
                editing = field
            } label: {
                line(field, value: value) { valueText(field, value) }
            }
            .buttonStyle(RowPressStyle())
            .disabled(!canEdit)
        }
    }

    private func label(_ field: CustomField) -> some View {
        Text(field.name)
            .font(ODFont.sans(14))
            .foregroundStyle(Theme.fg3)
            .lineLimit(1)
            .frame(width: 112, alignment: .leading)
    }

    private func line<Value: View>(_ field: CustomField, value: JSONValue?, @ViewBuilder content: () -> Value) -> some View {
        HStack(spacing: 12) {
            label(field)
            content().frame(maxWidth: .infinity, alignment: .leading)
            if canEdit {
                Image(systemName: field.type == .select || field.type == .user ? "chevron.up.chevron.down" : "chevron.right")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundStyle(Theme.fg4)
            }
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 48)
        .contentShape(Rectangle())
    }

    @ViewBuilder
    private func valueText(_ field: CustomField, _ value: JSONValue?) -> some View {
        if field.type == .multiSelect, let ids = value?.array?.compactMap(\.string), !ids.isEmpty {
            DirectoryFlow(spacing: 5, lineSpacing: 5) {
                ForEach(ids, id: \.self) { id in
                    let option = field.options.first { $0.id == id }
                    TonePill(text: option?.label ?? id, tone: Tone(label: option?.color), height: 22)
                }
            }
            .padding(.vertical, 8)
        } else if let text = DirectoryFieldText.text(value, field: field, members: members) {
            Text(text)
                .font(field.type == .number || field.type == .email || field.type == .url ? ODFont.mono(13.5) : ODFont.sans(14.5))
                .foregroundStyle(field.type == .url ? Tone.blue.text : Theme.fg)
                .lineLimit(field.type == .longText ? 3 : 1)
                .multilineTextAlignment(.leading)
                .padding(.vertical, field.type == .longText ? 10 : 0)
        } else {
            DirectoryNone(text: "Empty")
        }
    }
}

/// Edits one custom field value: text, number, link, email, long text, date,
/// or several options. Saving an empty value clears the field.
struct DirectoryFieldEditor: View {
    let field: CustomField
    let value: JSONValue?
    var members: [Member] = []
    let onSave: (JSONValue?) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var text = ""
    @State private var date = AppClock.now
    @State private var hasDate = false
    @State private var picked: Set<String> = []
    @State private var error: String?
    @FocusState private var focused: Bool

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    if !field.description.isEmpty {
                        Text(field.description).font(ODFont.footnote).foregroundStyle(Theme.fg3)
                    }
                    editor
                    FormError(text: error)
                }
                .padding(20)
            }
            .scrollBounceBehavior(.basedOnSize)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle(field.name)
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save") { save() }.fontWeight(.semibold)
                }
                if value != nil {
                    ToolbarItem(placement: .bottomBar) {
                        Button("Clear value", role: .destructive) {
                            onSave(nil)
                            dismiss()
                        }
                        .foregroundStyle(Tone.red.text)
                    }
                }
            }
        }
        .onAppear(perform: fill)
    }

    @ViewBuilder
    private var editor: some View {
        switch field.type {
        case .longText:
            TextEditor(text: $text)
                .font(ODFont.sans(16))
                .foregroundStyle(Theme.fg)
                .scrollContentBackground(.hidden)
                .focused($focused)
                .frame(minHeight: 140)
                .padding(8)
                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        case .date:
            Panel(padding: 8) {
                DatePicker("", selection: Binding(get: { date }, set: { date = $0; hasDate = true }), displayedComponents: .date)
                    .datePickerStyle(.graphical)
                    .labelsHidden()
                    .tint(Theme.inverse)
            }
        case .multiSelect, .select:
            Panel {
                ForEach(Array(field.options.enumerated()), id: \.element.id) { index, option in
                    if index > 0 { Hairline(inset: 40) }
                    Button {
                        Haptics.selection()
                        if field.type == .select {
                            picked = picked.contains(option.id) ? [] : [option.id]
                        } else if picked.contains(option.id) {
                            picked.remove(option.id)
                        } else {
                            picked.insert(option.id)
                        }
                    } label: {
                        HStack(spacing: 12) {
                            Circle().fill(Tone(label: option.color).solid).frame(width: 8, height: 8).frame(width: 16)
                            Text(option.label).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                            Spacer()
                            if picked.contains(option.id) {
                                Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg)
                            }
                        }
                        .padding(.horizontal, 14)
                        .frame(height: 46)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(RowPressStyle())
                }
            }
        case .user:
            Panel {
                ForEach(Array(members.enumerated()), id: \.element.id) { index, member in
                    if index > 0 { Hairline(inset: 46) }
                    Button {
                        picked = [member.id]
                    } label: {
                        HStack(spacing: 10) {
                            Avatar(name: member.user.name, url: member.user.avatarURL, seed: member.id, size: 24)
                            Text(member.user.name).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                            Spacer()
                            if picked.contains(member.id) { Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)) }
                        }
                        .padding(.horizontal, 14)
                        .frame(height: 46)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(RowPressStyle())
                }
            }
        default:
            HStack(spacing: 8) {
                Image(systemName: field.type.symbol).font(.system(size: 14)).foregroundStyle(Theme.fg3)
                TextField(text: $text) { Text(prompt).foregroundStyle(Theme.fg4) }
                    .font(field.type == .text ? ODFont.sans(16) : ODFont.mono(15))
                    .foregroundStyle(Theme.fg)
                    .keyboardType(keyboard)
                    .textInputAutocapitalization(field.type == .text ? .sentences : .never)
                    .autocorrectionDisabled(field.type != .text)
                    .submitLabel(.done)
                    .focused($focused)
                    .onSubmit(save)
            }
            .padding(.horizontal, 13)
            .frame(height: 48)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(focused ? Theme.lineStrong : Theme.line, lineWidth: focused ? 1.5 : 1))
        }
    }

    private var prompt: String {
        switch field.type {
        case .number: "0"
        case .url: "https://"
        case .email: "name@example.com"
        default: "Add a value"
        }
    }

    private var keyboard: UIKeyboardType {
        switch field.type {
        case .number: .decimalPad
        case .url: .URL
        case .email: .emailAddress
        default: .default
        }
    }

    private func fill() {
        switch field.type {
        case .date:
            if let raw = value?.string, let parsed = DirectoryFieldText.date(raw) {
                date = parsed
                hasDate = true
            }
        case .multiSelect:
            picked = Set(value?.array?.compactMap(\.string) ?? [])
        case .select, .user:
            picked = value?.string.map { [$0] } ?? []
        default:
            text = value?.displayText ?? ""
            Task {
                try? await Task.sleep(for: .milliseconds(250))
                focused = true
            }
        }
    }

    private func save() {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        var next: JSONValue?
        switch field.type {
        case .number:
            if trimmed.isEmpty {
                next = nil
            } else if let number = Double(trimmed.replacingOccurrences(of: ",", with: ".")) {
                next = .number(number)
            } else {
                error = "Enter a number."
                return
            }
        case .url:
            if !trimmed.isEmpty {
                let link = trimmed.contains("://") ? trimmed : "https://" + trimmed
                guard let url = URL(string: link), url.scheme == "https" || url.scheme == "http", url.host() != nil else {
                    error = "That link doesn't look right."
                    return
                }
                next = .string(link)
            }
        case .email:
            if !trimmed.isEmpty {
                guard trimmed.range(of: #"^[^\s@]+@[^\s@]+\.[^\s@]+$"#, options: .regularExpression) != nil else {
                    error = "That email doesn't look right."
                    return
                }
                next = .string(trimmed)
            }
        case .date:
            next = hasDate ? .string(DirectoryFieldText.string(date)) : nil
        case .multiSelect:
            let ordered = field.options.map(\.id).filter(picked.contains)
            next = ordered.isEmpty ? nil : .array(ordered.map(JSONValue.string))
        case .select, .user:
            next = picked.first.map(JSONValue.string)
        default:
            next = trimmed.isEmpty ? nil : .string(trimmed)
        }
        Haptics.tap()
        onSave(next)
        dismiss()
    }
}
