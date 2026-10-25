import SwiftUI

/// Picks a contact's or company's labels: search, tick, or create one by name.
/// Applies on Done.
struct DirectoryLabelPicker: View {
    let entity: DirectoryEntity
    let selected: [String]
    let onDone: ([String], [Tag]) -> Void

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var store: ContactLabelsStore
    @State private var picked: [String] = []
    @State private var query = ""
    @FocusState private var searchFocused: Bool

    init(entity: DirectoryEntity, selected: [String], onDone: @escaping ([String], [Tag]) -> Void) {
        self.entity = entity
        self.selected = selected
        self.onDone = onDone
        _store = State(initialValue: ContactLabelsStore(entity: entity))
    }

    private var trimmed: String { query.trimmingCharacters(in: .whitespacesAndNewlines) }

    private var shown: [Tag] {
        var tags = store.tags
        // Labels on the record the list doesn't know (made elsewhere just now) still show, so they can be removed.
        for name in picked where !tags.contains(where: { $0.name.caseInsensitiveCompare(name) == .orderedSame }) {
            tags.append(Tag(id: "picked-" + name, name: name, color: "gray", description: nil, groupID: nil, archived: false, appliesTo: [entity.tagScope]))
        }
        guard !trimmed.isEmpty else { return tags }
        return tags.filter { $0.name.localizedCaseInsensitiveContains(trimmed) }
    }

    private var canCreate: Bool {
        !trimmed.isEmpty && !shown.contains { $0.name.caseInsensitiveCompare(trimmed) == .orderedSame }
    }

    private func isPicked(_ name: String) -> Bool {
        picked.contains { $0.caseInsensitiveCompare(name) == .orderedSame }
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    search
                    if !store.loaded {
                        Panel { SkeletonRows(count: 4) }
                    } else if shown.isEmpty && !canCreate {
                        EmptyState(symbol: "tag", title: "No labels yet", message: "Type a name to create the first \(entity == .contact ? "contact" : "company") label.")
                    } else {
                        Panel {
                            if canCreate {
                                Button { toggle(trimmed) } label: {
                                    HStack(spacing: 12) {
                                        Image(systemName: "plus").font(.system(size: 13, weight: .semibold)).frame(width: 18)
                                        Text("Create \u{201C}\(trimmed)\u{201D}").font(ODFont.sans(15, weight: .medium))
                                        Spacer()
                                    }
                                    .foregroundStyle(Theme.fg)
                                    .padding(.horizontal, 14)
                                    .frame(height: 46)
                                    .contentShape(Rectangle())
                                }
                                .buttonStyle(RowPressStyle())
                                if !shown.isEmpty { Hairline(inset: 44) }
                            }
                            ForEach(Array(shown.enumerated()), id: \.element.id) { index, tag in
                                if index > 0 { Hairline(inset: 44) }
                                row(tag)
                            }
                        }
                    }
                }
                .padding(20)
            }
            .scrollDismissesKeyboard(.interactively)
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Labels")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Done") {
                        if picked != selected { onDone(picked, store.tags) }
                        dismiss()
                    }
                    .fontWeight(.semibold)
                }
            }
            .task { await store.load(api: env.session.api) }
        }
        .onAppear { picked = selected }
    }

    private var search: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass").font(.system(size: 14, weight: .medium)).foregroundStyle(Theme.fg3)
            TextField(text: $query) { Text("Find or create a label").foregroundStyle(Theme.fg4) }
                .font(ODFont.sans(16))
                .foregroundStyle(Theme.fg)
                .autocorrectionDisabled()
                .submitLabel(.done)
                .focused($searchFocused)
                .onSubmit {
                    if canCreate { toggle(trimmed) } else if let first = shown.first { toggle(first.name) }
                    query = ""
                }
        }
        .padding(.horizontal, 13)
        .frame(height: 44)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(searchFocused ? Theme.lineStrong : Theme.line, lineWidth: 1))
    }

    private func row(_ tag: Tag) -> some View {
        let on = isPicked(tag.name)
        return Button { toggle(tag.name) } label: {
            HStack(spacing: 12) {
                Circle().fill(Tone(label: tag.color).solid).frame(width: 9, height: 9).frame(width: 18)
                VStack(alignment: .leading, spacing: 1) {
                    Text(tag.name).font(ODFont.sans(15, weight: on ? .medium : .regular)).foregroundStyle(Theme.fg)
                    if let description = tag.description, !description.isEmpty {
                        Text(description).font(ODFont.footnote).foregroundStyle(Theme.fg3).lineLimit(1)
                    }
                }
                Spacer()
                Image(systemName: on ? "checkmark.circle.fill" : "circle")
                    .font(.system(size: 19))
                    .foregroundStyle(on ? Theme.fg : Theme.lineStrong)
                    .contentTransition(.symbolEffect(.replace))
            }
            .padding(.horizontal, 14)
            .frame(minHeight: 48)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    private func toggle(_ name: String) {
        Haptics.selection()
        if let index = picked.firstIndex(where: { $0.caseInsensitiveCompare(name) == .orderedSame }) {
            picked.remove(at: index)
        } else {
            picked.append(name)
        }
    }
}

/// Order and label filter for a directory list.
struct DirectoryFilterSheet: View {
    let entity: DirectoryEntity
    var sort: Binding<ContactSort>?
    @Binding var tag: String?

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var store: ContactLabelsStore

    init(entity: DirectoryEntity, sort: Binding<ContactSort>? = nil, tag: Binding<String?>) {
        self.entity = entity
        self.sort = sort
        _tag = tag
        _store = State(initialValue: ContactLabelsStore(entity: entity))
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 10) {
                    if let sort {
                        SectionLabel("Order")
                        Panel {
                            ForEach(Array(ContactSort.allCases.enumerated()), id: \.element) { index, option in
                                if index > 0 { Hairline(inset: 46) }
                                choice(title: option.title, symbol: option.symbol, selected: sort.wrappedValue == option) {
                                    sort.wrappedValue = option
                                }
                            }
                        }
                        .padding(.bottom, 8)
                    }
                    SectionLabel("Label")
                    Panel {
                        choice(title: entity == .contact ? "All contacts" : "All companies", symbol: "square.grid.2x2", selected: tag == nil) { tag = nil }
                        ForEach(store.tags) { label in
                            Hairline(inset: 46)
                            choice(title: label.name, dot: Tone(label: label.color), selected: tag?.caseInsensitiveCompare(label.name) == .orderedSame) { tag = label.name }
                        }
                    }
                    if store.loaded && store.tags.isEmpty {
                        Text("Labels you add to \(entity == .contact ? "contacts" : "companies") show up here.")
                            .font(ODFont.footnote).foregroundStyle(Theme.fg3).padding(.horizontal, 4)
                    }
                }
                .padding(20)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle(sort == nil ? "Filter" : "Order and filter")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() }.fontWeight(.semibold) }
            }
            .task { await store.load(api: env.session.api) }
        }
    }

    private func choice(title: String, symbol: String? = nil, dot: Tone? = nil, selected: Bool, action: @escaping () -> Void) -> some View {
        Button {
            Haptics.selection()
            action()
        } label: {
            HStack(spacing: 12) {
                Group {
                    if let symbol {
                        Image(systemName: symbol).font(.system(size: 14)).foregroundStyle(selected ? Theme.fg : Theme.fg3)
                    } else if let dot {
                        Circle().fill(dot.solid).frame(width: 9, height: 9)
                    }
                }
                .frame(width: 20)
                Text(title).font(ODFont.sans(15, weight: selected ? .semibold : .regular)).foregroundStyle(Theme.fg)
                Spacer()
                if selected { Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg) }
            }
            .padding(.horizontal, 14)
            .frame(height: 46)
            .background(selected ? Theme.hover : .clear)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
    }
}
