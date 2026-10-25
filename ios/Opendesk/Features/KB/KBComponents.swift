import SwiftUI

// MARK: - Overview

/// Published, drafts and views at a glance, and the most read article.
struct KBOverview: View {
    let published: Int
    let drafts: Int
    let views: Int
    let top: KBArticle?
    let onTop: (KBArticle) -> Void
    let onDrafts: () -> Void

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 0) {
                tile("Published", value: published, symbol: "checkmark.circle", tone: .green)
                divider
                Button(action: onDrafts) {
                    tile("Drafts", value: drafts, symbol: "pencil.line", tone: .amber)
                }
                .buttonStyle(TapScaleStyle())
                .accessibilityHint("Shows the drafts")
                divider
                tile("Views", value: views, symbol: "eye", tone: .blue)
            }
            .padding(.vertical, 12)
            if let top, top.views > 0 {
                Hairline()
                Button { onTop(top) } label: {
                    HStack(spacing: 8) {
                        Image(systemName: "flame")
                            .font(.system(size: 11.5, weight: .semibold))
                            .foregroundStyle(Tone.orange.text)
                            .frame(width: 20, height: 20)
                            .background(Tone.orange.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
                        Text("Most read")
                            .font(ODFont.sans(12.5, weight: .medium))
                            .foregroundStyle(Theme.fg3)
                        Text(top.displayTitle)
                            .font(ODFont.sans(13.5, weight: .medium))
                            .foregroundStyle(Theme.fg)
                            .lineLimit(1)
                        Spacer(minLength: 4)
                        Text(top.views.formatted())
                            .font(ODFont.mono(12))
                            .foregroundStyle(Theme.fg3)
                        Chevron()
                    }
                    .padding(.horizontal, 12)
                    .frame(height: 40)
                    .contentShape(Rectangle())
                }
                .buttonStyle(RowPressStyle())
            }
        }
        .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        .clipShape(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
    }

    private var divider: some View {
        Rectangle().fill(Theme.line).frame(width: 1, height: 34)
    }

    private func tile(_ title: String, value: Int, symbol: String, tone: Tone) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(spacing: 5) {
                Image(systemName: symbol).font(.system(size: 10.5, weight: .semibold)).foregroundStyle(tone.solid)
                Text(title).font(ODFont.sans(12, weight: .medium)).foregroundStyle(Theme.fg3)
            }
            Text(KBNumber.compact(value))
                .font(ODFont.mono(21, weight: .medium, relativeTo: .title3))
                .foregroundStyle(Theme.fg)
                .contentTransition(.numericText(value: Double(value)))
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.leading, 14)
        .accessibilityElement(children: .combine)
    }
}

enum KBNumber {
    /// 12,480 as "12.5k"; small numbers as they are.
    static func compact(_ value: Int) -> String {
        if value < 10000 { return value.formatted() }
        return value.formatted(.number.notation(.compactName).precision(.fractionLength(0 ... 1)))
    }
}

// MARK: - Ollie

/// The way into Ollie's draft: pick tickets, get an article to edit.
struct KBOllieCard: View {
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 12) {
                OllieMark(size: 30)
                VStack(alignment: .leading, spacing: 2) {
                    Text("Draft an article from tickets")
                        .font(ODFont.sans(14.5, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                    Text("Ollie reads the conversations you pick and writes a first version.")
                        .font(ODFont.sans(12.5))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 4)
                Image(systemName: "sparkles")
                    .font(.system(size: 13, weight: .semibold))
                    .foregroundStyle(Tone.violet.text)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 12)
            .background(Tone.violet.soft.opacity(0.7), in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Tone.violet.line, lineWidth: 1))
            .contentShape(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        }
        .buttonStyle(TapScaleStyle())
    }
}

// MARK: - Collections

/// The collections as a strip of cards: all articles, each collection with its
/// hue, count and drafts, and a card to add one. Long press renames or deletes.
struct KBCollectionStrip: View {
    let selected: String?
    let canWrite: Bool
    let onSelect: (String?) -> Void
    let onNew: () -> Void
    let onRename: (KBCollection) -> Void
    let onDelete: (KBCollection) -> Void

    private var store: KBStore { KBStore.shared }

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Collections")
                .font(ODFont.sans(12.5, weight: .medium))
                .foregroundStyle(Theme.fg3)
                .padding(.horizontal, 16)
            ScrollViewReader { proxy in
                ScrollView(.horizontal, showsIndicators: false) {
                    HStack(spacing: 8) {
                        card(id: nil, name: "All articles", count: store.everything.count, drafts: store.draftCount, tone: .gray, symbol: "square.stack")
                            .id("all")
                        ForEach(store.collections) { collection in
                            card(
                                id: collection.id,
                                name: collection.name,
                                count: collection.articleCount,
                                drafts: store.drafts(in: collection.id),
                                tone: store.tone(for: collection.id),
                                symbol: "folder"
                            )
                            .id(collection.id)
                            .contextMenu {
                                if canWrite {
                                    Button { onRename(collection) } label: { Label("Rename", systemImage: "pencil") }
                                    Button(role: .destructive) { onDelete(collection) } label: { Label("Delete", systemImage: "trash") }
                                }
                            }
                        }
                        if canWrite {
                            Button(action: onNew) {
                                VStack(spacing: 6) {
                                    Image(systemName: "plus").font(.system(size: 14, weight: .semibold))
                                    Text("New collection").font(ODFont.sans(12.5, weight: .medium))
                                }
                                .foregroundStyle(Theme.fg3)
                                .frame(width: 112, height: 86)
                                .overlay(
                                    RoundedRectangle(cornerRadius: Theme.Radius.control + 2, style: .continuous)
                                        .strokeBorder(Theme.lineStrong, style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
                                )
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(TapScaleStyle())
                        }
                    }
                    .padding(.horizontal, 16)
                    .padding(.vertical, 1)
                }
                .onChange(of: selected) { _, id in
                    withAnimation(Motion.smooth) { proxy.scrollTo(id ?? "all", anchor: .center) }
                }
            }
        }
    }

    private func card(id: String?, name: String, count: Int, drafts: Int, tone: Tone, symbol: String) -> some View {
        let on = selected == id
        return Button { onSelect(id) } label: {
            VStack(alignment: .leading, spacing: 0) {
                HStack {
                    Image(systemName: on ? symbol + ".fill" : symbol)
                        .font(.system(size: 12, weight: .semibold))
                        .foregroundStyle(tone == .gray ? Theme.fg2 : tone.text)
                        .frame(width: 26, height: 26)
                        .background(tone == .gray ? Theme.active : tone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous))
                    Spacer(minLength: 4)
                    if drafts > 0 {
                        Text("\(drafts) draft\(drafts == 1 ? "" : "s")")
                            .font(ODFont.sans(10.5, weight: .medium))
                            .foregroundStyle(Tone.amber.text)
                    }
                }
                Spacer(minLength: 6)
                Text(name)
                    .font(ODFont.sans(13.5, weight: .semibold))
                    .foregroundStyle(Theme.fg)
                    .lineLimit(1)
                Text(Format.plural(count, "article"))
                    .font(ODFont.sans(12))
                    .foregroundStyle(Theme.fg3)
                    .monospacedDigit()
            }
            .padding(10)
            .frame(width: 150, height: 86, alignment: .leading)
            .background(on ? (tone == .gray ? Theme.hover : tone.soft) : Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control + 2, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: Theme.Radius.control + 2, style: .continuous)
                    .strokeBorder(on ? (tone == .gray ? Theme.fg3 : tone.solid.opacity(0.7)) : Theme.line, lineWidth: on ? 1.5 : 1)
            )
            .contentShape(RoundedRectangle(cornerRadius: Theme.Radius.control + 2, style: .continuous))
        }
        .buttonStyle(TapScaleStyle())
        .accessibilityAddTraits(on ? .isSelected : [])
        .accessibilityLabel("\(name), \(Format.plural(count, "article"))")
    }
}

// MARK: - Status filter

/// All, published or drafts: a segmented control with counts and a sliding pill.
struct KBStatusPicker: View {
    @Binding var selection: KBStatusFilter
    let counts: [KBStatusFilter: Int]
    @Namespace private var pill

    var body: some View {
        HStack(spacing: 2) {
            ForEach(KBStatusFilter.allCases) { filter in
                let on = selection == filter
                Button { selection = filter } label: {
                    HStack(spacing: 5) {
                        Text(filter.title)
                            .font(ODFont.sans(13, weight: on ? .semibold : .medium))
                        Text("\(counts[filter] ?? 0)")
                            .font(ODFont.mono(11.5))
                            .foregroundStyle(on ? Theme.fg2 : Theme.fg4)
                    }
                    .foregroundStyle(on ? Theme.fg : Theme.fg3)
                    .frame(maxWidth: .infinity)
                    .frame(height: 30)
                    .background {
                        if on {
                            RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous)
                                .fill(Theme.raised)
                                .shadow(color: .black.opacity(0.06), radius: 2, y: 1)
                                .matchedGeometryEffect(id: "pill", in: pill)
                        }
                    }
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityAddTraits(on ? .isSelected : [])
            }
        }
        .padding(3)
        .background(Theme.active.opacity(0.7), in: RoundedRectangle(cornerRadius: Theme.Radius.control - 1, style: .continuous))
    }
}

// MARK: - Article row

struct KBArticleRow: View {
    let article: KBArticle
    let collection: KBCollection?
    let tone: Tone
    var showCollection = true

    var body: some View {
        VStack(spacing: 0) {
            HStack(alignment: .top, spacing: 12) {
                Image(systemName: article.status == .published ? "doc.text" : "doc.badge.ellipsis")
                    .font(.system(size: 13.5, weight: .medium))
                    .foregroundStyle(collection == nil ? Theme.fg3 : tone.text)
                    .frame(width: 32, height: 32)
                    .background(collection == nil ? Theme.hover : tone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.control - 2, style: .continuous))
                VStack(alignment: .leading, spacing: 3) {
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text(article.displayTitle)
                            .font(ODFont.sans(15, weight: .semibold))
                            .foregroundStyle(article.title.isEmpty ? Theme.fg3 : Theme.fg)
                            .lineLimit(1)
                        Spacer(minLength: 4)
                        Text(Format.relShort(article.updatedAt))
                            .font(ODFont.sans(12))
                            .foregroundStyle(Theme.fg3)
                            .monospacedDigit()
                    }
                    if !article.excerpt.isEmpty {
                        Text(article.excerpt)
                            .font(ODFont.sans(13.5))
                            .foregroundStyle(Theme.fg2)
                            .lineLimit(2)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    HStack(spacing: 8) {
                        TonePill(text: article.status.title, tone: article.status.tone, height: 18)
                        if showCollection, let collection {
                            HStack(spacing: 4) {
                                Circle().fill(tone.solid).frame(width: 6, height: 6)
                                Text(collection.name).lineLimit(1)
                            }
                            .font(ODFont.sans(12))
                            .foregroundStyle(Theme.fg3)
                        }
                        if article.status == .published {
                            HStack(spacing: 3) {
                                Image(systemName: "eye").font(.system(size: 10, weight: .medium))
                                Text(KBNumber.compact(article.views)).font(ODFont.mono(11.5))
                            }
                            .foregroundStyle(Theme.fg3)
                            .accessibilityLabel("\(article.views) views")
                        }
                        Spacer(minLength: 0)
                    }
                    .padding(.top, 3)
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            Hairline(inset: 60)
        }
        .contentShape(Rectangle())
    }
}

// MARK: - Collection sheet

struct KBCollectionDraft: Identifiable {
    let id = UUID()
    let collection: KBCollection?
}

/// Create or rename a collection.
struct KBCollectionSheet: View {
    let draft: KBCollectionDraft
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var description = ""
    @State private var saving = false
    @State private var error: String?

    private var trimmed: String { name.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    ODTextField(label: "Name", text: $name, prompt: "Billing and plans", submitLabel: .next)
                    ODTextField(label: "Description", text: $description, prompt: "Shown under the name on the help center", submitLabel: .done) { save() }
                    if !trimmed.isEmpty {
                        HStack(spacing: 6) {
                            Image(systemName: "link").font(.system(size: 11))
                            Text("/c/" + KBSlug.make(trimmed)).font(ODFont.mono(12))
                        }
                        .foregroundStyle(Theme.fg3)
                        .transition(.opacity)
                    }
                    FormError(text: error)
                    Button(action: save) {
                        ButtonLabel(title: draft.collection == nil ? "Create collection" : "Save", busy: saving)
                    }
                    .buttonStyle(InkButtonStyle())
                    .disabled(trimmed.isEmpty || saving)
                }
                .padding(20)
                .motion(Motion.fade, value: trimmed.isEmpty)
            }
            .background(Theme.chrome)
            .navigationTitle(draft.collection == nil ? "New collection" : "Rename collection")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
        }
        .onAppear {
            name = draft.collection?.name ?? ""
            description = draft.collection?.description ?? ""
        }
    }

    private func save() {
        guard !trimmed.isEmpty, !saving else { return }
        saving = true
        error = nil
        Task {
            defer { saving = false }
            do {
                if let collection = draft.collection {
                    try await KBStore.shared.updateCollection(collection.id, name: trimmed, description: description)
                    env.toasts.show("Collection renamed", kind: .success)
                } else {
                    let collection = try await KBStore.shared.createCollection(name: trimmed, description: description)
                    KBStore.shared.collectionID = collection.id
                    env.toasts.show("Collection created", kind: .success)
                }
                Haptics.success()
                dismiss()
            } catch {
                self.error = error.userMessage
                Haptics.error()
            }
        }
    }
}
