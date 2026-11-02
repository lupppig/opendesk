import SwiftUI

// MARK: - Pills

/// A changelog category as a soft pill in its name's tone.
struct ChangelogCategoryPill: View {
    let name: String
    var onRemove: (() -> Void)?

    var body: some View {
        let tone = ChangelogCategories.tone(name)
        HStack(spacing: 4) {
            RoundedRectangle(cornerRadius: 1.5).fill(tone.solid).frame(width: 6, height: 6)
            Text(name).font(ODFont.sans(11.5, weight: .medium)).lineLimit(1)
            if let onRemove {
                Button(action: onRemove) {
                    Image(systemName: "xmark").font(.system(size: 8.5, weight: .bold))
                        .frame(width: 16, height: 16)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Remove \(name)")
            }
        }
        .foregroundStyle(tone.text)
        .padding(.leading, 6)
        .padding(.trailing, onRemove == nil ? 7 : 2)
        .frame(height: 20)
        .background(tone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous).strokeBorder(tone.line, lineWidth: 1))
    }
}

/// Draft, published or scheduled, waking once at the moment a scheduled entry goes live.
struct ChangelogStatePill: View {
    let entry: ChangelogEntry
    var height: CGFloat = 20

    var body: some View {
        if entry.state() == .scheduled, let at = entry.publishedAt {
            TimelineView(.explicit([at.addingTimeInterval(1)])) { context in
                pill(entry.state(now: context.date))
            }
        } else {
            pill(entry.state())
        }
    }

    private func pill(_ state: ChangelogState) -> some View {
        TonePill(text: state.title, tone: state.tone, height: height)
    }
}

// MARK: - Row

struct ChangelogRow: View {
    let entry: ChangelogEntry

    var body: some View {
        VStack(spacing: 0) {
            HStack(alignment: .top, spacing: 12) {
                VStack(spacing: 1) {
                    Text(month)
                        .font(ODFont.sans(10, weight: .semibold))
                        .foregroundStyle(Theme.fg3)
                    Text(day)
                        .font(ODFont.mono(15, weight: .medium))
                        .foregroundStyle(entry.publishedAt == nil ? Theme.fg4 : Theme.fg)
                }
                .frame(width: 38, height: 40)
                .background(Theme.hover, in: RoundedRectangle(cornerRadius: Theme.Radius.control - 2, style: .continuous))
                .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: 3) {
                    Text(entry.displayTitle)
                        .font(ODFont.sans(15, weight: .semibold))
                        .foregroundStyle(entry.title.isEmpty ? Theme.fg3 : Theme.fg)
                        .lineLimit(2)
                    if !entry.excerpt.isEmpty {
                        Text(entry.excerpt)
                            .font(ODFont.sans(13.5))
                            .foregroundStyle(Theme.fg2)
                            .lineLimit(2)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    HStack(spacing: 6) {
                        ChangelogStatePill(entry: entry, height: 18)
                        ForEach(entry.categories.prefix(3), id: \.self) { ChangelogCategoryPill(name: $0) }
                        if entry.categories.count > 3 {
                            Text("+\(entry.categories.count - 3)").font(ODFont.mono(11)).foregroundStyle(Theme.fg3)
                        }
                        Spacer(minLength: 0)
                    }
                    .padding(.top, 3)
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            Hairline(inset: 66)
        }
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
        .accessibilityLabel("\(entry.displayTitle), \(entry.state().title), \(entry.publishedAt.map { Format.shortDate($0) } ?? "not dated")")
    }

    private var month: String {
        guard let date = entry.publishedAt else { return "DRAFT" }
        return date.formatted(.dateTime.month(.abbreviated)).uppercased()
    }

    private var day: String {
        guard let date = entry.publishedAt else { return "-" }
        return date.formatted(.dateTime.day())
    }
}

// MARK: - Category filter

/// All categories, then each one the workspace uses, as chips.
struct ChangelogCategoryStrip: View {
    let categories: [ChangelogCategory]
    let selected: String?
    let onSelect: (String?) -> Void

    var body: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 6) {
                chip(nil, title: "All categories", tone: nil)
                ForEach(categories) { category in
                    chip(category.name, title: category.name, tone: ChangelogCategories.tone(category.name), count: category.count)
                }
            }
            .padding(.horizontal, 16)
        }
    }

    private func chip(_ name: String?, title: String, tone: Tone?, count: Int? = nil) -> some View {
        let on = selected?.lowercased() == name?.lowercased()
        return Button { onSelect(name) } label: {
            HStack(spacing: 5) {
                if let tone { RoundedRectangle(cornerRadius: 1.5).fill(tone.solid).frame(width: 7, height: 7) }
                Text(title).font(ODFont.sans(12.5, weight: on ? .semibold : .medium))
                if let count { Text("\(count)").font(ODFont.mono(11)).foregroundStyle(Theme.fg4) }
            }
            .foregroundStyle(on ? Theme.fg : Theme.fg2)
            .padding(.horizontal, 10)
            .frame(height: 30)
            .background(on ? Theme.active : Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous).strokeBorder(on ? Theme.lineStrong : Theme.line, lineWidth: 1))
            .contentShape(Rectangle())
        }
        .buttonStyle(TapScaleStyle())
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

// MARK: - Category picker

/// Adds a category to an entry: one the workspace already uses, or a new name.
/// At most five, 32 characters each; another spelling of one already there is refused.
struct ChangelogCategorySheet: View {
    let current: [String]
    let onChange: ([String]) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var problem: String?
    @FocusState private var focused: Bool

    private var store: ChangelogStore { ChangelogStore.shared }

    private var suggestions: [ChangelogCategory] {
        let chosen = Set(current.map { $0.lowercased() })
        let term = name.trimmingCharacters(in: .whitespaces)
        let defaults = ["New", "Improved", "Fixed"].map { ChangelogCategory(name: $0, count: 0) }
        var seen = Set<String>()
        return (store.categories + defaults).filter { category in
            guard !chosen.contains(category.id), seen.insert(category.id).inserted else { return false }
            return term.isEmpty || category.name.localizedCaseInsensitiveContains(term)
        }
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    HStack(spacing: 8) {
                        TextField(text: $name) { Text("New category").foregroundStyle(Theme.fg4) }
                            .font(ODFont.sans(16))
                            .foregroundStyle(Theme.fg)
                            .focused($focused)
                            .submitLabel(.done)
                            .onSubmit { add(name) }
                        Button("Add") { add(name) }
                            .buttonStyle(InkButtonStyle(fullWidth: false, height: 34))
                            .disabled(ChangelogCategories.clean(name) == nil)
                    }
                    .padding(.leading, 13)
                    .padding(.trailing, 6)
                    .frame(height: 46)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                    FormError(text: problem)
                    Text("Up to \(ChangelogCategories.max), such as New, Improved or Fixed.")
                        .font(ODFont.footnote)
                        .foregroundStyle(Theme.fg3)
                    if !suggestions.isEmpty {
                        SectionLabel("Used before")
                        Panel {
                            ForEach(Array(suggestions.enumerated()), id: \.element.id) { index, category in
                                if index > 0 { Hairline(inset: 36) }
                                Button { add(category.name) } label: {
                                    HStack(spacing: 10) {
                                        RoundedRectangle(cornerRadius: 2).fill(ChangelogCategories.tone(category.name).solid).frame(width: 9, height: 9)
                                            .frame(width: 16)
                                        Text(category.name).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                                        Spacer()
                                        if category.count > 0 {
                                            Text("\(category.count)").font(ODFont.mono(12)).foregroundStyle(Theme.fg4)
                                        }
                                    }
                                    .padding(.horizontal, 12)
                                    .frame(height: 44)
                                    .contentShape(Rectangle())
                                }
                                .buttonStyle(RowPressStyle())
                            }
                        }
                    }
                }
                .padding(20)
                .animation(Motion.fade, value: problem)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Add a category")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
        .onAppear { focused = true }
        .onChange(of: name) { _, _ in problem = nil }
    }

    private func add(_ text: String) {
        switch ChangelogCategories.add(text, to: current) {
        case let .success(list):
            Haptics.selection()
            onChange(list)
            dismiss()
        case let .failure(problem):
            Haptics.warning()
            self.problem = problem.message
        }
    }
}
