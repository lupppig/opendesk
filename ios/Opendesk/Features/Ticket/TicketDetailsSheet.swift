import SwiftUI

/// Everything about the ticket: properties with their pickers, custom fields, the
/// customer and their other tickets, SLA timers, side conversations, links and where it came from.
struct TicketDetailsSheet: View {
    @Bindable var c: TicketController
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss

    private var store: TicketStore { c.store }

    var body: some View {
        NavigationStack {
            ScrollView {
                if let ticket = store.ticket {
                    VStack(alignment: .leading, spacing: 18) {
                        heading(ticket)
                        properties(ticket)
                        fields(ticket)
                        contact(ticket)
                        timers(ticket)
                        sides
                        links
                        source(ticket)
                    }
                    .padding(16)
                }
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Details")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() }.fontWeight(.semibold) }
            }
            .sheet(item: $c.detailSheet) { sheet in
                TicketSheetContent(c: c, sheet: sheet)
            }
        }
        .presentationDetents([.large])
        .presentationDragIndicator(.visible)
    }

    // MARK: Heading

    private func heading(_ ticket: Ticket) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 6) {
                TicketNumber(number: ticket.number)
                Image(systemName: ticket.channel.symbol).font(.system(size: 11, weight: .medium)).foregroundStyle(Theme.fg3)
                Text(ticket.channel.title).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3)
                Text("·").foregroundStyle(Theme.fg4)
                Text("Opened \(Format.relLong(ticket.createdAt))").font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3)
            }
            Text(ticket.displaySubject)
                .font(ODFont.sans(19, weight: .semibold))
                .headingTracking(19)
                .foregroundStyle(Theme.fg)
                .fixedSize(horizontal: false, vertical: true)
        }
        .padding(.horizontal, 2)
    }

    // MARK: Properties

    private func properties(_ ticket: Ticket) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            SectionLabel("Properties")
            Panel {
                row("Status", sheet: .status) {
                    StatusIcon(status: ticket.status, size: 13)
                    Text(ticket.status.title)
                }
                Hairline(inset: 14)
                row("Priority", sheet: .priority) {
                    PriorityIcon(priority: ticket.priority, size: 13)
                    Text(ticket.priority.title)
                }
                Hairline(inset: 14)
                row("Severity", sheet: .severity) {
                    if let severity = ticket.severity { SeverityChip(severity: severity) } else { Text("None").foregroundStyle(Theme.fg4) }
                }
                Hairline(inset: 14)
                row("Assignee", sheet: .assignee) {
                    if let assignee = ticket.assignee {
                        Avatar(name: assignee.name, url: assignee.avatarURL, seed: assignee.id, size: 20)
                        Text(assignee.id == c.me?.id ? "You" : assignee.name)
                    } else {
                        Text("Unassigned").foregroundStyle(Theme.fg4)
                    }
                }
                Hairline(inset: 14)
                row("Team", sheet: .team) {
                    if let team = ticket.teamID.flatMap({ env.directory.team($0) }) {
                        Text(team.name)
                    } else {
                        Text("No team").foregroundStyle(Theme.fg4)
                    }
                }
                Hairline(inset: 14)
                labels(ticket)
            }
        }
    }

    private func row<Value: View>(_ title: String, sheet: TicketSheet, @ViewBuilder value: () -> Value) -> some View {
        let content = HStack(spacing: 10) {
            Text(title).font(ODFont.sans(14.5)).foregroundStyle(Theme.fg2)
            Spacer(minLength: 8)
            HStack(spacing: 6) { value() }
                .font(ODFont.sans(14.5, weight: .medium))
                .foregroundStyle(Theme.fg)
                .lineLimit(1)
            if c.canWrite { Chevron() }
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 46)
        .contentShape(Rectangle())
        return Button { c.open(sheet) } label: { content }
            .buttonStyle(RowPressStyle())
            .disabled(!c.canWrite)
    }

    private func labels(_ ticket: Ticket) -> some View {
        Button { c.open(.labels) } label: {
            HStack(alignment: .top, spacing: 10) {
                Text("Labels").font(ODFont.sans(14.5)).foregroundStyle(Theme.fg2).frame(height: 22)
                Spacer(minLength: 8)
                if ticket.tags.isEmpty {
                    Text("None").font(ODFont.sans(14.5, weight: .medium)).foregroundStyle(Theme.fg4).frame(height: 22)
                } else {
                    FlowLayout(spacing: 5) {
                        ForEach(ticket.tags) { tag in LabelChip(name: tag.name, color: tag.color) }
                    }
                    .frame(maxWidth: 220, alignment: .trailing)
                }
                if c.canWrite { Chevron().frame(height: 22) }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 12)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
        .disabled(!c.canWrite)
    }

    // MARK: Custom fields

    @ViewBuilder private func fields(_ ticket: Ticket) -> some View {
        let defined = env.directory.fields(for: "ticket")
        if !defined.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                SectionLabel("Fields")
                Panel {
                    ForEach(Array(defined.enumerated()), id: \.element.id) { index, field in
                        if index > 0 { Hairline(inset: 14) }
                        fieldRow(field, value: ticket.fields[field.key], solved: ticket.status.isDone)
                    }
                }
            }
        }
    }

    private func fieldRow(_ field: CustomField, value: JSONValue?, solved: Bool) -> some View {
        let shown = TicketFieldValue.display(value, field: field, members: env.directory.members)
        let missing = field.requiredOnSolve && shown == nil
        return Button { c.open(.field(field)) } label: {
            HStack(spacing: 10) {
                Image(systemName: TicketFieldValue.symbol(field.type))
                    .font(.system(size: 12.5, weight: .medium))
                    .foregroundStyle(Theme.fg3)
                    .frame(width: 18)
                HStack(spacing: 4) {
                    Text(field.name).font(ODFont.sans(14.5)).foregroundStyle(Theme.fg2).lineLimit(1)
                    if field.requiredOnSolve {
                        Text("Required to solve")
                            .font(ODFont.sans(10.5, weight: .medium))
                            .foregroundStyle(missing && !solved ? Tone.amber.text : Theme.fg4)
                            .padding(.horizontal, 5)
                            .frame(height: 17)
                            .background(missing && !solved ? Tone.amber.soft : Theme.active, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
                    }
                }
                Spacer(minLength: 8)
                if field.type == .select, let id = value?.string, let option = field.options.first(where: { $0.id == id || $0.label == id }) {
                    LabelChip(name: option.label, color: option.color)
                } else {
                    Text(shown ?? "Empty")
                        .font(ODFont.sans(14.5, weight: shown == nil ? .regular : .medium))
                        .foregroundStyle(shown == nil ? Theme.fg4 : Theme.fg)
                        .lineLimit(1)
                }
                if c.canWrite { Chevron() }
            }
            .padding(.horizontal, 14)
            .frame(minHeight: 46)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
        .disabled(!c.canWrite)
    }

    // MARK: Contact

    @ViewBuilder private func contact(_ ticket: Ticket) -> some View {
        if let ref = ticket.contact {
            let full = store.contact
            VStack(alignment: .leading, spacing: 8) {
                SectionLabel("Customer")
                Panel {
                    Button(action: c.openContact) {
                        HStack(spacing: 12) {
                            Avatar(name: ref.name, url: ref.avatarURL, seed: ref.id, size: 40)
                            VStack(alignment: .leading, spacing: 2) {
                                Text(ref.name.isEmpty ? (ref.email ?? "Customer") : ref.name)
                                    .font(ODFont.sans(15.5, weight: .semibold)).foregroundStyle(Theme.fg).lineLimit(1)
                                if let email = ref.email {
                                    Text(email).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).lineLimit(1)
                                }
                            }
                            Spacer(minLength: 8)
                            Chevron()
                        }
                        .padding(14)
                        .contentShape(Rectangle())
                    }
                    .buttonStyle(RowPressStyle())
                    if let full {
                        if let company = full.company {
                            Hairline(inset: 14)
                            fact("building.2", company.name, detail: company.domain.isEmpty ? nil : company.domain)
                        }
                        if !full.phone.isEmpty {
                            Hairline(inset: 14)
                            fact("phone", full.phone)
                        }
                        if !full.tags.isEmpty {
                            Hairline(inset: 14)
                            FlowLayout(spacing: 5) {
                                ForEach(full.tags) { tag in LabelChip(name: tag.name, color: tag.color) }
                            }
                            .padding(.horizontal, 14)
                            .padding(.vertical, 10)
                        }
                        ForEach(contactFields(full), id: \.0) { item in
                            Hairline(inset: 14)
                            fact("square.text.square", item.0, detail: item.1)
                        }
                    } else if store.contactLoading {
                        Hairline(inset: 14)
                        HStack { ProgressView().controlSize(.small); Spacer() }.padding(14)
                    }
                }
                otherTickets(full)
            }
        }
    }

    private func contactFields(_ contact: Contact) -> [(String, String)] {
        let defined = env.directory.fields(for: "contact")
        return defined.compactMap { field in
            TicketFieldValue.display(contact.fields[field.key], field: field, members: env.directory.members).map { (field.name, $0) }
        }
    }

    private func fact(_ symbol: String, _ title: String, detail: String? = nil) -> some View {
        HStack(spacing: 10) {
            Image(systemName: symbol).font(.system(size: 12.5, weight: .medium)).foregroundStyle(Theme.fg3).frame(width: 18)
            Text(title).font(ODFont.sans(14)).foregroundStyle(detail == nil ? Theme.fg : Theme.fg2).lineLimit(1)
            Spacer(minLength: 8)
            if let detail { Text(detail).font(ODFont.sans(14, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1) }
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 42)
    }

    @ViewBuilder private func otherTickets(_ contact: Contact?) -> some View {
        let others = store.otherTickets
        if !others.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                SectionLabel(contact.map { $0.ticketCount > 1 ? "Other tickets (\($0.ticketCount - 1))" : "Other tickets" } ?? "Other tickets")
                    .padding(.top, 6)
                Panel {
                    ForEach(Array(others.prefix(5).enumerated()), id: \.element.id) { index, other in
                        if index > 0 { Hairline(inset: 40) }
                        TicketMiniRow(ticket: other) { c.openTicket(other) }
                    }
                }
            }
        }
    }

    // MARK: Timers

    @ViewBuilder private func timers(_ ticket: Ticket) -> some View {
        let sla = ticket.sla
        let snoozed = ticket.isSnoozed ? ticket.snoozedUntil : nil
        if sla?.firstResponseDueAt != nil || sla?.resolutionDueAt != nil || snoozed != nil || ticket.csat != nil {
            VStack(alignment: .leading, spacing: 8) {
                SectionLabel("Timers")
                Panel {
                    if let due = sla?.firstResponseDueAt {
                        timer("First response", due: due, met: ticket.firstRespondedAt, breached: sla?.breached == true && ticket.firstRespondedAt == nil)
                    }
                    if let due = sla?.resolutionDueAt {
                        if sla?.firstResponseDueAt != nil { Hairline(inset: 14) }
                        timer("Resolution", due: due, met: ticket.solvedAt, breached: sla?.breached == true && ticket.firstRespondedAt != nil && ticket.solvedAt == nil)
                    }
                    if let snoozed {
                        if sla?.firstResponseDueAt != nil || sla?.resolutionDueAt != nil { Hairline(inset: 14) }
                        HStack(spacing: 10) {
                            Image(systemName: "moon.zzz").font(.system(size: 12.5, weight: .medium)).foregroundStyle(Tone.indigo.solid).frame(width: 18)
                            VStack(alignment: .leading, spacing: 1) {
                                Text("Snoozed").font(ODFont.sans(14.5)).foregroundStyle(Theme.fg)
                                Text("Until \(Format.fullDate(snoozed))").font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3)
                            }
                            Spacer()
                            if c.canWrite {
                                Button("Unsnooze", action: c.unsnooze).buttonStyle(ChipButtonStyle())
                            }
                        }
                        .padding(.horizontal, 14)
                        .frame(minHeight: 52)
                    }
                    if let csat = ticket.csat {
                        if sla != nil || snoozed != nil { Hairline(inset: 14) }
                        HStack(spacing: 10) {
                            Image(systemName: "star.fill").font(.system(size: 12.5)).foregroundStyle(Tone.amber.solid).frame(width: 18)
                            VStack(alignment: .leading, spacing: 1) {
                                Text("Satisfaction \(csat.score) of 5").font(ODFont.sans(14.5)).foregroundStyle(Theme.fg)
                                if let comment = csat.comment, !comment.isEmpty {
                                    Text("\u{201C}\(comment)\u{201D}").font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).lineLimit(2)
                                }
                            }
                            Spacer()
                        }
                        .padding(.horizontal, 14)
                        .padding(.vertical, 10)
                    }
                }
            }
        }
    }

    private func timer(_ title: String, due: Date, met: Date?, breached: Bool) -> some View {
        let tone: Tone
        let detail: String
        if let met {
            tone = met <= due ? .green : .red
            detail = met <= due ? "Met \(Format.relLong(met))" : "Met late, \(Format.relLong(met))"
        } else {
            let left = due.timeIntervalSince(AppClock.now) / 60
            if breached || left < 0 {
                tone = .red
                detail = "Overdue by \(Format.minutes(-left))"
            } else {
                tone = left < 60 ? .amber : .gray
                detail = "Due in \(Format.minutes(left))"
            }
        }
        return HStack(spacing: 10) {
            Image(systemName: met == nil ? "timer" : "checkmark.circle").font(.system(size: 12.5, weight: .medium)).foregroundStyle(tone == .gray ? Theme.fg3 : tone.solid).frame(width: 18)
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(ODFont.sans(14.5)).foregroundStyle(Theme.fg)
                Text("Target \(Format.fullDate(due))").font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3)
            }
            Spacer(minLength: 8)
            TonePill(text: detail, tone: tone) { EmptyView() }
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 52)
    }

    // MARK: Side conversations

    @ViewBuilder private var sides: some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack {
                SectionLabel("Side conversations")
                if c.canWrite {
                    Button { c.open(.newSide) } label: {
                        Label("New", systemImage: "plus").font(ODFont.sans(12.5, weight: .medium))
                    }
                    .buttonStyle(.plain)
                    .foregroundStyle(Theme.fg2)
                }
            }
            if store.threads.isEmpty {
                Text("Email someone outside the ticket, like a supplier or another team, without the customer seeing it.")
                    .font(ODFont.sans(13))
                    .foregroundStyle(Theme.fg3)
                    .padding(.horizontal, 2)
            } else {
                Panel {
                    ForEach(Array(store.threads.enumerated()), id: \.element.id) { index, thread in
                        if index > 0 { Hairline(inset: 44) }
                        SideThreadRow(thread: thread) { c.open(.thread(thread.id)) }
                    }
                }
            }
        }
    }

    // MARK: Links

    @ViewBuilder private var links: some View {
        if !store.links.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                SectionLabel("Linked")
                Panel {
                    ForEach(Array(store.links.enumerated()), id: \.element.id) { index, link in
                        if index > 0 { Hairline(inset: 44) }
                        if let url = URL(string: link.url), url.scheme == "https" {
                            Link(destination: url) { linkRow(link) }.buttonStyle(RowPressStyle())
                        } else {
                            linkRow(link)
                        }
                    }
                }
            }
        }
    }

    private func linkRow(_ link: TicketLink) -> some View {
        HStack(spacing: 10) {
            Image(systemName: "link")
                .font(.system(size: 12, weight: .semibold))
                .foregroundStyle(Theme.fg2)
                .frame(width: 26, height: 26)
                .background(Theme.active, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
            VStack(alignment: .leading, spacing: 1) {
                Text(link.title.isEmpty ? link.url : link.title).font(ODFont.sans(14.5, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                Text(link.provider.capitalizedFirst + (link.actorType == "ai" ? " · linked by Ollie" : link.createdBy.map { " · linked by \($0.name)" } ?? ""))
                    .font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).lineLimit(1)
            }
            Spacer(minLength: 6)
            Image(systemName: "arrow.up.right").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.fg4)
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 52)
        .contentShape(Rectangle())
    }

    // MARK: Source

    private func source(_ ticket: Ticket) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            SectionLabel("Conversation")
            Panel {
                info("Channel", ticket.channel.title)
                if ticket.channel == .email, let mailbox = store.replyAddress {
                    Hairline(inset: 14)
                    info("Mailbox", mailbox.email)
                }
                Hairline(inset: 14)
                info("Received", Format.fullDate(ticket.createdAt))
                Hairline(inset: 14)
                info("Last message", Format.fullDate(ticket.lastMessageAt))
                if let first = ticket.firstRespondedAt {
                    Hairline(inset: 14)
                    info("First response", Format.fullDate(first))
                }
                if let solved = ticket.solvedAt {
                    Hairline(inset: 14)
                    info("Solved", Format.fullDate(solved))
                }
                Hairline(inset: 14)
                info("Messages", "\(ticket.messageCount)")
            }
            Button(action: c.copyLink) {
                Label("Copy link", systemImage: "link")
            }
            .buttonStyle(OutlineButtonStyle(fullWidth: true, height: 42))
            .padding(.top, 4)
        }
    }

    private func info(_ title: String, _ value: String) -> some View {
        HStack(spacing: 10) {
            Text(title).font(ODFont.sans(14.5)).foregroundStyle(Theme.fg2)
            Spacer(minLength: 8)
            Text(value).font(ODFont.sans(14.5, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1).truncationMode(.middle)
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 44)
    }
}

/// A compact ticket row for the customer's other tickets and merge targets.
struct TicketMiniRow: View {
    let ticket: Ticket
    var selected = false
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            HStack(spacing: 10) {
                StatusIcon(status: ticket.status, size: 15)
                    .frame(width: 18)
                VStack(alignment: .leading, spacing: 2) {
                    Text(ticket.displaySubject).font(ODFont.sans(14.5, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                    HStack(spacing: 5) {
                        Text(verbatim: "#\(ticket.number)").font(ODFont.mono(11.5))
                        Text("·")
                        Text(ticket.status.title)
                        Text("·")
                        Text(Format.relShort(ticket.lastMessageAt))
                    }
                    .font(ODFont.sans(12))
                    .foregroundStyle(Theme.fg3)
                    .lineLimit(1)
                }
                Spacer(minLength: 6)
                if selected {
                    Image(systemName: "checkmark.circle.fill").font(.system(size: 18)).foregroundStyle(Theme.fg)
                } else {
                    Chevron()
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
    }
}
