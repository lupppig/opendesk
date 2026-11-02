import SwiftUI

extension WikiStore {
    /// Binds the shared store to the signed-in workspace and keeps it live while `listener` is held.
    func attach(_ env: AppEnvironment) {
        attach(api: env.session.api, workspaceID: env.session.workspace?.workspace.id)
    }
}

@MainActor
enum WikiLink {
    /// The page on the web app, for copying and sharing.
    static func url(_ id: String, in env: AppEnvironment) -> URL? {
        env.session.instance.webURL(path: "/wiki/\(id)")
    }

    static func copy(_ id: String, in env: AppEnvironment) {
        guard let url = url(id, in: env) else { return }
        UIPasteboard.general.url = url
        Haptics.success()
        env.toasts.show("Link copied", kind: .success)
    }
}

/// A page's emoji, or a plain page mark when it has none.
struct WikiPageIcon: View {
    let icon: String
    var size: CGFloat = 16

    var body: some View {
        Group {
            if icon.isEmpty {
                Image(systemName: "doc.text")
                    .font(.system(size: size * 0.82, weight: .regular))
                    .foregroundStyle(Theme.fg3)
            } else {
                Text(icon).font(.system(size: size))
            }
        }
        .frame(width: size + 6, height: size + 6)
        .accessibilityHidden(true)
    }
}

/// One page in the tree: indented by its level, with a disclosure for its subpages
/// beside the row that opens it (two buttons side by side, never one inside the other).
struct WikiTreeRow: View {
    let node: WikiNode
    let level: Int
    let subpages: Int
    let expanded: Bool
    let onToggle: () -> Void
    let onOpen: () -> Void

    var body: some View {
        HStack(spacing: 0) {
            Group {
                if subpages > 0 {
                    Button(action: onToggle) {
                        Image(systemName: "chevron.right")
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundStyle(Theme.fg3)
                            .rotationEffect(.degrees(expanded ? 90 : 0))
                            .frame(width: 34, height: 44)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.plain)
                    .accessibilityLabel(expanded ? "Hide subpages of \(node.displayTitle)" : "Show subpages of \(node.displayTitle)")
                } else {
                    Color.clear.frame(width: 34, height: 44)
                }
            }
            .padding(.leading, 4 + CGFloat(min(level, 6)) * 18)
            Button(action: onOpen) {
                HStack(spacing: 6) {
                    WikiPageIcon(icon: node.icon, size: 16)
                    Text(node.displayTitle)
                        .font(ODFont.sans(15, weight: level == 0 ? .medium : .regular))
                        .foregroundStyle(node.title.trimmingCharacters(in: .whitespaces).isEmpty ? Theme.fg3 : Theme.fg)
                        .lineLimit(1)
                    Spacer(minLength: 6)
                    if subpages > 0 {
                        Text("\(subpages)")
                            .font(ODFont.mono(11.5))
                            .foregroundStyle(Theme.fg4)
                            .accessibilityLabel(Format.plural(subpages, "subpage"))
                    }
                }
                .padding(.trailing, 16)
                .frame(minHeight: 44)
                .contentShape(Rectangle())
            }
            .buttonStyle(RowPressStyle())
        }
    }
}

/// A recently edited page: icon, title, where it sits and when.
struct WikiRecentRow: View {
    let node: WikiNode
    let path: [WikiNode]

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                WikiPageIcon(icon: node.icon, size: 18)
                    .frame(width: 32, height: 32)
                    .background(Theme.hover, in: RoundedRectangle(cornerRadius: Theme.Radius.control - 2, style: .continuous))
                VStack(alignment: .leading, spacing: 2) {
                    Text(node.displayTitle)
                        .font(ODFont.sans(15, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    if !path.isEmpty {
                        Text(path.map(\.displayTitle).joined(separator: " / "))
                            .font(ODFont.sans(12.5))
                            .foregroundStyle(Theme.fg3)
                            .lineLimit(1)
                    }
                }
                Spacer(minLength: 6)
                Text(Format.relShort(node.updatedAt))
                    .font(ODFont.sans(12))
                    .foregroundStyle(Theme.fg3)
                    .monospacedDigit()
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 10)
            Hairline(inset: 60)
        }
        .contentShape(Rectangle())
    }
}

/// A search result: the page, where it sits, and the text around the match.
struct WikiSearchRow: View {
    let hit: WikiSearchHit

    var body: some View {
        VStack(spacing: 0) {
            HStack(alignment: .top, spacing: 12) {
                WikiPageIcon(icon: hit.icon, size: 18)
                    .frame(width: 32, height: 32)
                    .background(Theme.hover, in: RoundedRectangle(cornerRadius: Theme.Radius.control - 2, style: .continuous))
                VStack(alignment: .leading, spacing: 3) {
                    Text(hit.displayTitle)
                        .font(ODFont.sans(15, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    if !hit.path.isEmpty {
                        Text(hit.path.map(\.displayTitle).joined(separator: " / "))
                            .font(ODFont.sans(12))
                            .foregroundStyle(Theme.fg3)
                            .lineLimit(1)
                    }
                    if !hit.snippet.isEmpty {
                        Text(hit.snippet)
                            .font(ODFont.sans(13.5))
                            .foregroundStyle(Theme.fg2)
                            .lineLimit(2)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 12)
            Hairline(inset: 60)
        }
        .contentShape(Rectangle())
    }
}

/// "Last edited by Maya, 3 minutes ago": its own clock, so only this line redraws.
struct WikiEditedLine: View {
    let page: WikiPage

    var body: some View {
        TimelineView(.periodic(from: .now, by: 30)) { context in
            HStack(spacing: 7) {
                if let who = page.updatedBy {
                    Avatar(name: who.name, url: who.avatarURL, seed: who.id, size: 20)
                    Text("Last edited by \(who.displayName), \(Format.relLong(page.updatedAt, now: context.date))")
                } else {
                    Text("Last edited \(Format.relLong(page.updatedAt, now: context.date))")
                }
            }
            .font(ODFont.sans(12.5))
            .foregroundStyle(Theme.fg3)
            .lineLimit(1)
        }
    }
}

/// The page menu's actions shared by the tree and the page: add a subpage, move,
/// copy the link, delete (or why it can't be deleted).
struct WikiPageMenu: View {
    let node: WikiNode
    let onAddSubpage: () -> Void
    let onMove: () -> Void
    let onDelete: () -> Void
    var onHistory: (() -> Void)?

    @Environment(AppEnvironment.self) private var env

    private var store: WikiStore { WikiStore.shared }

    var body: some View {
        let rule = store.deleteRule(node.id, role: env.session.role, me: env.session.user?.id)
        if env.session.canWrite {
            Button(action: onAddSubpage) { Label("Add a subpage", systemImage: "plus") }
            Button(action: onMove) { Label("Move to…", systemImage: "folder") }
        }
        Button { WikiLink.copy(node.id, in: env) } label: { Label("Copy link", systemImage: "link") }
        if let url = WikiLink.url(node.id, in: env) {
            ShareLink(item: url) { Label("Share", systemImage: "square.and.arrow.up") }
        }
        if let onHistory {
            Button(action: onHistory) { Label("History", systemImage: "clock.arrow.circlepath") }
        }
        if rule != .no {
            Divider()
            if let reason = rule.reason {
                Button {} label: {
                    Label("Delete page", systemImage: "trash")
                    Text(reason)
                }
                .disabled(true)
            } else {
                Button(role: .destructive, action: onDelete) { Label("Delete page", systemImage: "trash") }
            }
        }
    }
}

/// Asks before deleting a page, naming the subpages that go with it.
struct WikiDeleteConfirmation: ViewModifier {
    @Binding var node: WikiNode?
    let onDeleted: (WikiNode) -> Void

    @Environment(AppEnvironment.self) private var env

    func body(content: Content) -> some View {
        let under = node.map { WikiStore.shared.descendants(of: $0.id).count } ?? 0
        content.confirmationDialog(
            "Delete \u{201C}\(node?.displayTitle ?? "")\u{201D}?",
            isPresented: Binding(get: { node != nil }, set: { if !$0 { node = nil } }),
            titleVisibility: .visible,
            presenting: node
        ) { node in
            Button(under > 0 ? "Delete \(Format.plural(under + 1, "page"))" : "Delete page", role: .destructive) { delete(node) }
        } message: { _ in
            Text(under == 0
                ? "The page and its history are deleted for good."
                : under == 1
                ? "Its subpage goes with it. Both pages and their history are deleted for good."
                : "Its \(under) subpages go with it. All of them and their history are deleted for good.")
        }
    }

    private func delete(_ node: WikiNode) {
        Task {
            do {
                try await WikiStore.shared.delete(node.id)
                Haptics.success()
                env.toasts.show("Deleted \u{201C}\(node.displayTitle)\u{201D}", kind: .success)
                onDeleted(node)
            } catch {
                Haptics.error()
                // An agent's refusal says who can delete it (contracts/wiki.md "Access").
                if let api = error as? APIError, api.status == 403, let detail = api.detail, !detail.isEmpty {
                    env.toasts.show(detail, kind: .error)
                } else {
                    env.toasts.error(error)
                }
            }
        }
    }
}

extension View {
    func wikiDeleteConfirmation(_ node: Binding<WikiNode?>, onDeleted: @escaping (WikiNode) -> Void = { _ in }) -> some View {
        modifier(WikiDeleteConfirmation(node: node, onDeleted: onDeleted))
    }
}

/// Creates a page and opens it with its title ready to type.
@MainActor
enum WikiCreate {
    static func page(_ body: WikiPageCreate, env: AppEnvironment) async {
        do {
            let page = try await WikiStore.shared.create(body)
            Haptics.tap()
            env.router.push(.wikiPage(page.id))
        } catch {
            Haptics.error()
            env.toasts.error(error)
        }
    }
}
