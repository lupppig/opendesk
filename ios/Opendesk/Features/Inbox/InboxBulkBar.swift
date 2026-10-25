import SwiftUI

/// The header while tickets are being chosen: close, how many, select all.
struct InboxSelectHeader: View {
    let count: Int
    let allSelected: Bool
    let onClose: () -> Void
    let onAll: () -> Void

    var body: some View {
        GlassEffectContainer(spacing: 10) {
            HStack(spacing: 10) {
                GlassCircleButton(symbol: "xmark", label: "Done selecting", action: onClose)
                Text(count == 0 ? "Select tickets" : "\(count) selected")
                    .font(ODFont.sans(19, weight: .semibold))
                    .tracking(-0.4)
                    .foregroundStyle(Theme.fg)
                    .monospacedDigit()
                    .contentTransition(.numericText())
                    .padding(.leading, 2)
                Spacer(minLength: 6)
                Button {
                    Haptics.selection()
                    onAll()
                } label: {
                    Text(allSelected ? "Deselect all" : "Select all")
                        .font(ODFont.sans(14.5, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .padding(.horizontal, 16)
                        .frame(height: 44)
                        .contentShape(Capsule())
                }
                .buttonStyle(.plain)
                .glassEffect(.regular.interactive(), in: Capsule())
            }
        }
        .padding(.horizontal, 16)
        .padding(.top, 6)
        .padding(.bottom, 8)
    }
}

/// The floating glass bar of bulk actions (the web's `BulkBar`): assign, status,
/// priority, labels, and more (team, severity, spam, delete). Every change shows at
/// once and is put back if the server refuses.
struct InboxBulkBar: View {
    @Environment(AppEnvironment.self) private var env
    let ids: [String]
    /// Called after a change went through, to leave select mode.
    let onDone: () -> Void

    @State private var sheet: Sheet?
    @State private var confirmDelete = false

    enum Sheet: String, Identifiable {
        case assign, team, labels
        var id: String { rawValue }
    }

    private var tickets: [Ticket] {
        let chosen = Set(ids)
        return env.inbox.tickets.filter { chosen.contains($0.id) }
    }

    private var n: Int { ids.count }

    var body: some View {
        HStack(spacing: 0) {
            action("Assign", symbol: "person.crop.circle") { sheet = .assign }
            Menu {
                ForEach(TicketStatus.allCases) { status in
                    Button {
                        run(TicketPatch(status: status), done: "Marked \(things) \(status.title.lowercased())")
                    } label: {
                        Label(status.title, systemImage: symbol(status))
                    }
                }
            } label: {
                caption("Status", symbol: "circle.dashed")
            }
            .menuOrder(.fixed)
            Menu {
                ForEach(Priority.allCases) { priority in
                    Button(priority.title) {
                        run(TicketPatch(priority: priority), done: "Set \(things) to \(priority.title.lowercased()) priority")
                    }
                }
            } label: {
                caption("Priority", symbol: "cellularbars")
            }
            .menuOrder(.fixed)
            action("Label", symbol: "tag") { sheet = .labels }
            Menu {
                Button { sheet = .team } label: { Label("Move to team\u{2026}", systemImage: "person.3") }
                Menu {
                    ForEach(Severity.allCases) { severity in
                        Button("\(severity.title)  \(severity.hint)") {
                            run(TicketPatch(severity: .set(severity)), done: "Set \(things) to \(severity.title)")
                        }
                    }
                    Button("No severity") {
                        run(TicketPatch(severity: .clear), done: "Cleared severity on \(things)")
                    }
                } label: { Label("Severity", systemImage: "exclamationmark.triangle") }
                Divider()
                if allSpam {
                    Button { spam(false) } label: { Label("Not spam", systemImage: "tray.and.arrow.up") }
                } else {
                    Button { spam(true) } label: { Label("Mark as spam", systemImage: "xmark.octagon") }
                }
                Button(role: .destructive) { confirmDelete = true } label: { Label("Delete", systemImage: "trash") }
            } label: {
                caption("More", symbol: "ellipsis")
            }
            .menuOrder(.fixed)
        }
        .disabled(ids.isEmpty)
        .opacity(ids.isEmpty ? 0.45 : 1)
        .padding(.horizontal, 6)
        .frame(height: 60)
        .background(Theme.surface.opacity(0.55), in: Capsule())
        .glassEffect(.regular, in: Capsule())
        .padding(.horizontal, 16)
        .shadow(color: .black.opacity(0.08), radius: 14, y: 6)
        .sheet(item: $sheet) { sheet in
            Group {
                switch sheet {
                case .assign:
                    AssigneePicker(current: commonAssignee) { id in
                        let name = id.flatMap { env.directory.member($0)?.user.name }
                        let done = id == nil ? "Unassigned \(things)" : id == env.session.user?.id ? "Assigned \(things) to you" : "Assigned \(things) to \(name ?? "a teammate")"
                        run(TicketPatch(assigneeID: id.map { .set($0) } ?? .clear), done: done)
                    }
                case .team:
                    TeamPicker(current: commonTeam) { id in
                        let name = env.directory.team(id)?.name
                        run(TicketPatch(teamID: id.map { .set($0) } ?? .clear), done: id == nil ? "Removed \(things) from their team" : "Moved \(things) to \(name ?? "the team")")
                    }
                case .labels:
                    LabelPicker(current: []) { add, _ in
                        guard !add.isEmpty else { return }
                        let refs = add.map { name in env.directory.tag(named: name)?.ref ?? TagRef(id: "tmp-" + name, name: name, color: nil, groupID: nil) }
                        run(TicketPatch(addTags: add), tags: refs, done: add.count == 1 ? "Labelled \(things) \(add[0])" : "Added \(add.count) labels to \(things)")
                    }
                }
            }
            .presentationDetents([.medium, .large])
            .presentationCornerRadius(Theme.Radius.sheet)
        }
        .confirmationDialog("Delete \(things)?", isPresented: $confirmDelete, titleVisibility: .visible) {
            Button("Delete \(things)", role: .destructive) { delete() }
            Button("Cancel", role: .cancel) {}
        } message: {
            Text("\(n == 1 ? "This ticket" : "\(n) tickets") will be deleted for everyone in the workspace.")
        }
    }

    private var things: String { Format.plural(n, "ticket") }

    private var allSpam: Bool { !tickets.isEmpty && tickets.allSatisfy(\.spam) }

    private var commonAssignee: String? {
        let ids = Set(tickets.map { $0.assignee?.id })
        return ids.count == 1 ? ids.first ?? nil : nil
    }

    private var commonTeam: String? {
        let ids = Set(tickets.map(\.teamID))
        return ids.count == 1 ? ids.first ?? nil : nil
    }

    private func symbol(_ status: TicketStatus) -> String {
        switch status {
        case .open: "circle"
        case .pending: "circle.lefthalf.filled"
        case .onHold: "pause.circle"
        case .solved, .closed: "checkmark.circle"
        }
    }

    private func action(_ title: String, symbol: String, perform: @escaping () -> Void) -> some View {
        Button {
            Haptics.tap()
            perform()
        } label: {
            caption(title, symbol: symbol)
        }
        .buttonStyle(.plain)
    }

    private func caption(_ title: String, symbol: String) -> some View {
        VStack(spacing: 3) {
            Image(systemName: symbol)
                .font(.system(size: 17, weight: .regular))
                .frame(height: 22)
            Text(title).font(ODFont.sans(10.5, weight: .medium))
        }
        .foregroundStyle(Theme.fg)
        .frame(maxWidth: .infinity)
        .frame(height: 52)
        .contentShape(Rectangle())
    }

    private func run(_ change: TicketPatch, tags: [TagRef] = [], done: String, undo: TicketPatch? = nil) {
        let ids = ids
        Task {
            do {
                try await env.inbox.bulk(ids, change, tags: tags, members: env.directory.members, meRef: env.session.userRef)
                Haptics.success()
                onDone()
                if let undo {
                    env.toasts.show(Toast(message: done, kind: .success, actionTitle: "Undo") {
                        Task {
                            do {
                                try await env.inbox.bulk(ids, undo, members: env.directory.members, meRef: env.session.userRef)
                                env.inbox.reload()
                            } catch {
                                env.toasts.error(error)
                            }
                        }
                    })
                } else {
                    env.toasts.show(done, kind: .success)
                }
                env.directory.scheduleCounts()
            } catch {
                Haptics.error()
                env.toasts.error(error)
            }
        }
    }

    private func spam(_ on: Bool) {
        run(TicketPatch(spam: on), done: on ? "Marked \(things) as spam" : "Moved \(things) out of spam", undo: TicketPatch(spam: !on))
    }

    private func delete() {
        let ids = ids
        let things = things
        Task {
            do {
                try await env.inbox.bulkDelete(ids)
                Haptics.success()
                onDone()
                env.toasts.show("Deleted \(things)", kind: .success)
                env.directory.scheduleCounts()
            } catch {
                Haptics.error()
                env.toasts.error(error)
            }
        }
    }
}
