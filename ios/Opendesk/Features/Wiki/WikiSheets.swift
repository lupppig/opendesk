import SwiftUI

// MARK: - Move

/// Move to… (contracts/wiki.md "Tree"): picks the new parent from a searchable
/// list, or the top level, and puts the page last among its new siblings with
/// its subpages. Pages it can't go under (itself, its subpages, past the depth
/// limit) are not offered.
struct WikiMoveSheet: View {
    let node: WikiNode

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    @State private var busy: String?

    private var store: WikiStore { WikiStore.shared }
    private static let top = "top"

    private var targets: [WikiOutlineItem] {
        let all = store.moveTargets(for: node.id)
        let term = query.trimmingCharacters(in: .whitespaces)
        guard !term.isEmpty else { return all }
        return all.filter { $0.node.displayTitle.localizedCaseInsensitiveContains(term) }
    }

    var body: some View {
        NavigationStack {
            List {
                if query.trimmingCharacters(in: .whitespaces).isEmpty {
                    row(id: Self.top, level: 0, here: node.parentID == nil) {
                        Image(systemName: "square.stack.3d.up")
                            .font(.system(size: 13, weight: .medium))
                            .foregroundStyle(Theme.fg3)
                            .frame(width: 22, height: 22)
                        Text("Top level").font(ODFont.sans(15, weight: .medium)).foregroundStyle(Theme.fg)
                    }
                }
                ForEach(targets) { item in
                    row(id: item.node.id, level: item.level + 1, here: node.parentID == item.node.id) {
                        WikiPageIcon(icon: item.node.icon, size: 16)
                        Text(item.node.displayTitle).font(ODFont.sans(15)).foregroundStyle(Theme.fg).lineLimit(1)
                    }
                }
                if targets.isEmpty, !query.trimmingCharacters(in: .whitespaces).isEmpty {
                    Text("No page matches.")
                        .font(ODFont.sans(14))
                        .foregroundStyle(Theme.fg3)
                        .frame(maxWidth: .infinity)
                        .padding(.vertical, 24)
                        .listRowSeparator(.hidden)
                        .listRowBackground(Theme.surface)
                }
            }
            .listStyle(.plain)
            .scrollContentBackground(.hidden)
            .background(Theme.surface)
            .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Search pages")
            .safeAreaInset(edge: .top, spacing: 0) {
                Text("It goes last under the page you pick, with its subpages.")
                    .font(ODFont.sans(13))
                    .foregroundStyle(Theme.fg3)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 16)
                    .padding(.vertical, 8)
                    .background(Theme.surface)
            }
            .navigationTitle("Move \u{201C}\(node.displayTitle)\u{201D}")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
    }

    private func row(id: String, level: Int, here: Bool, @ViewBuilder label: () -> some View) -> some View {
        Button { move(to: id == Self.top ? nil : id) } label: {
            HStack(spacing: 8) {
                label()
                Spacer(minLength: 6)
                if busy == id {
                    ProgressView().controlSize(.small)
                } else if here {
                    Text("Here now").font(ODFont.sans(12)).foregroundStyle(Theme.fg4)
                }
            }
            .padding(.leading, 16 + CGFloat(min(level, 6)) * 16)
            .padding(.trailing, 16)
            .frame(minHeight: 44)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
        .disabled(here || busy != nil)
        .listRowInsets(EdgeInsets())
        .listRowSeparator(.hidden)
        .listRowBackground(Theme.surface)
    }

    private func move(to parentID: String?) {
        busy = parentID ?? Self.top
        Task {
            defer { busy = nil }
            do {
                _ = try await store.move(node.id, under: parentID)
                Haptics.success()
                let place = parentID.flatMap { store.node($0)?.displayTitle }.map { "under \u{201C}\($0)\u{201D}" } ?? "to the top level"
                env.toasts.show("Moved \(place)", kind: .success)
                dismiss()
            } catch {
                Haptics.error()
                env.toasts.error(error)
            }
        }
    }
}

// MARK: - History

/// Page history (contracts/wiki.md "History"): the kept versions, newest first;
/// one opens to read, and agents restore it as a new version.
struct WikiHistorySheet: View {
    let page: WikiPage
    let canRestore: Bool
    let onRestored: (WikiPage) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var revisions: [WikiRevision] = []
    @State private var loaded = false
    @State private var error: String?

    var body: some View {
        NavigationStack {
            Group {
                if !loaded && error == nil {
                    ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
                } else if let error, revisions.isEmpty {
                    ErrorState(title: "Couldn't load the history", message: error) { Task { await load() } }
                        .frame(maxHeight: .infinity)
                } else if revisions.isEmpty {
                    EmptyState(symbol: "clock.arrow.circlepath", title: "No history yet", message: "Versions show up here as the page is edited.")
                        .frame(maxHeight: .infinity)
                } else {
                    List {
                        ForEach(Array(revisions.enumerated()), id: \.element.id) { index, revision in
                            NavigationLink {
                                WikiRevisionView(page: page, revision: revision, current: index == 0, canRestore: canRestore) { restored in
                                    onRestored(restored)
                                    dismiss()
                                }
                            } label: {
                                row(revision, current: index == 0)
                            }
                            .listRowBackground(Theme.surface)
                        }
                    }
                    .listStyle(.plain)
                    .scrollContentBackground(.hidden)
                }
            }
            .background(Theme.surface.ignoresSafeArea())
            .navigationTitle("Page history")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
        .task { await load() }
    }

    private func row(_ revision: WikiRevision, current: Bool) -> some View {
        HStack(spacing: 10) {
            if let author = revision.author {
                Avatar(name: author.name, url: author.avatarURL, seed: author.id, size: 28)
            } else {
                Image(systemName: "key")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundStyle(Theme.fg3)
                    .frame(width: 28, height: 28)
                    .background(Theme.active, in: Circle())
            }
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(revision.author?.displayName ?? "An API key")
                        .font(ODFont.sans(14.5, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    if current { TonePill(text: "Current", tone: .green, height: 18) }
                }
                Text(Format.fullDate(revision.updatedAt))
                    .font(ODFont.sans(12.5))
                    .foregroundStyle(Theme.fg3)
                    .lineLimit(1)
            }
        }
        .padding(.vertical, 4)
    }

    private func load() async {
        do {
            revisions = try await WikiStore.shared.revisions(page.id)
            error = nil
        } catch {
            guard !error.isCancellation else { return }
            self.error = error.userMessage
        }
        loaded = true
    }
}

/// One kept version: its title and text as they were, and Restore.
struct WikiRevisionView: View {
    let page: WikiPage
    let revision: WikiRevision
    let current: Bool
    let canRestore: Bool
    let onRestored: (WikiPage) -> Void

    @Environment(AppEnvironment.self) private var env
    @State private var full: WikiRevision?
    @State private var error: String?
    @State private var height: CGFloat = 200
    @State private var confirm = false
    @State private var restoring = false

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 10) {
                if !revision.icon.isEmpty {
                    Text(revision.icon).font(.system(size: 36))
                }
                Text(revision.displayTitle)
                    .font(ODFont.sans(24, weight: .semibold, relativeTo: .title))
                    .tracking(-0.5)
                    .foregroundStyle(Theme.fg)
                Text("\(revision.author?.displayName ?? "An API key"), \(Format.fullDate(revision.updatedAt))")
                    .font(ODFont.sans(12.5))
                    .foregroundStyle(Theme.fg3)
                Hairline().padding(.vertical, 10)
                if let full {
                    let body = full.bodyHTML ?? ""
                    if HTMLText.plain(from: body).trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                        Text("This version had no text.").font(ODFont.sans(15)).foregroundStyle(Theme.fg4)
                    } else {
                        KBArticleWebView(html: body, height: $height)
                            .frame(height: height)
                    }
                } else if let error {
                    Notice(text: error, tone: .red, symbol: "exclamationmark.triangle")
                } else {
                    ProgressView().frame(maxWidth: .infinity).padding(.vertical, 30)
                }
            }
            .padding(20)
        }
        .background(Theme.surface.ignoresSafeArea())
        .navigationTitle(current ? "Current version" : "Version")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            if canRestore, !current {
                ToolbarItem(placement: .topBarTrailing) {
                    Button { confirm = true } label: {
                        if restoring { ProgressView() } else { Text("Restore").fontWeight(.semibold) }
                    }
                    .disabled(restoring || full == nil)
                }
            }
        }
        .confirmationDialog("Restore this version?", isPresented: $confirm, titleVisibility: .visible) {
            Button("Restore this version") { Task { await restore() } }
        } message: {
            Text("The page goes back to this version. What it says now stays in the history, so you can go back to it.")
        }
        .task {
            do {
                full = try await WikiStore.shared.revision(page.id, revision.id)
            } catch {
                guard !error.isCancellation else { return }
                self.error = error.userMessage
            }
        }
    }

    private func restore() async {
        restoring = true
        defer { restoring = false }
        do {
            let restored = try await WikiStore.shared.restore(page.id, revision.id)
            Haptics.success()
            env.toasts.show("Version restored", kind: .success)
            onRestored(restored)
        } catch {
            Haptics.error()
            env.toasts.error(error)
        }
    }
}

// MARK: - Icon

/// The page's emoji: search by word or browse the groups, or remove it.
struct WikiIconSheet: View {
    let current: String
    let onPick: (String) -> Void

    @Environment(\.dismiss) private var dismiss
    @State private var query = ""

    private let columns = Array(repeating: GridItem(.flexible(), spacing: 4), count: 8)

    var body: some View {
        NavigationStack {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 16) {
                    let term = query.trimmingCharacters(in: .whitespaces)
                    if term.isEmpty {
                        ForEach(WikiEmoji.groups) { group in
                            VStack(alignment: .leading, spacing: 6) {
                                SectionLabel(group.title)
                                grid(group.emoji.map(\.0))
                            }
                        }
                    } else {
                        let found = WikiEmoji.search(term)
                        if found.isEmpty {
                            Text("No emoji match.")
                                .font(ODFont.sans(14))
                                .foregroundStyle(Theme.fg3)
                                .frame(maxWidth: .infinity)
                                .padding(.vertical, 30)
                        } else {
                            grid(found)
                        }
                    }
                }
                .padding(16)
            }
            .background(Theme.surface.ignoresSafeArea())
            .searchable(text: $query, placement: .navigationBarDrawer(displayMode: .always), prompt: "Search emoji")
            .navigationTitle("Emoji")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
                if !current.isEmpty {
                    ToolbarItem(placement: .destructiveAction) {
                        Button("Remove") { pick("") }
                    }
                }
            }
        }
    }

    private func grid(_ emoji: [String]) -> some View {
        LazyVGrid(columns: columns, spacing: 4) {
            ForEach(emoji, id: \.self) { item in
                Button { pick(item) } label: {
                    Text(item)
                        .font(.system(size: 26))
                        .frame(maxWidth: .infinity)
                        .frame(height: 42)
                        .background(item == current ? Theme.active : .clear, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                        .contentShape(Rectangle())
                }
                .buttonStyle(TapScaleStyle())
            }
        }
    }

    private func pick(_ emoji: String) {
        Haptics.selection()
        onPick(emoji)
        dismiss()
    }
}
