import SwiftUI

/// Merge this ticket into another: the customer's other open tickets first, then search
/// by number or subject. The conversation moves over and this ticket closes.
struct TicketMergeSheet: View {
    let c: TicketController
    @Environment(\.dismiss) private var dismiss
    @State private var query = ""
    @State private var results: [Ticket] = []
    @State private var searching = false
    @State private var target: Ticket?
    @State private var confirm = false
    @State private var merging = false

    private var store: TicketStore { c.store }
    private var trimmed: String { query.trimmingCharacters(in: .whitespacesAndNewlines) }

    private var suggestions: [Ticket] {
        store.otherTickets.filter { !$0.status.isDone }
    }

    var body: some View {
        PickerSheet(title: "Merge into", query: $query, prompt: "Ticket number or subject") {
            VStack(alignment: .leading, spacing: 14) {
                if let ticket = store.ticket {
                    Text(verbatim: "#\(ticket.number)'s messages, notes and attachments move to the ticket you pick, and #\(ticket.number) closes.")
                        .font(ODFont.sans(13))
                        .foregroundStyle(Theme.fg3)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .padding(.horizontal, 2)
                }
                if trimmed.isEmpty {
                    if suggestions.isEmpty {
                        EmptyState(symbol: "arrow.triangle.merge", title: "Find the ticket to keep", message: "Search by number or subject. Tickets from the same customer show up here.")
                            .padding(.top, 20)
                    } else {
                        section("Same customer", suggestions)
                    }
                } else if searching, results.isEmpty {
                    HStack { Spacer(); ProgressView(); Spacer() }.padding(.top, 30)
                } else if results.isEmpty {
                    EmptyState(symbol: "magnifyingglass", title: "No tickets match", message: "Try the ticket number, like 1042, or a word from its subject.")
                        .padding(.top, 20)
                } else {
                    section("Results", results)
                }
            }
        }
        .task(id: trimmed) { await search() }
        .confirmationDialog(confirmTitle, isPresented: $confirm, titleVisibility: .visible, presenting: target) { target in
            Button(String("Merge into #\(target.number)"), role: .destructive) {
                merging = true
                Task {
                    let ok = await c.merge(into: target)
                    merging = false
                    if ok { dismiss() }
                }
            }
            Button("Cancel", role: .cancel) {}
        } message: { _ in
            Text("This can't be undone.")
        }
        .overlay {
            if merging {
                ProgressView().controlSize(.large).padding(24).glassEffect(.regular, in: RoundedRectangle(cornerRadius: 18, style: .continuous))
            }
        }
    }

    private var confirmTitle: String {
        guard let target, let ticket = store.ticket else { return "Merge?" }
        return "Merge #\(ticket.number) into #\(target.number)?"
    }

    private func section(_ title: String, _ tickets: [Ticket]) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            SectionLabel(title)
            Panel {
                ForEach(Array(tickets.enumerated()), id: \.element.id) { index, ticket in
                    if index > 0 { Hairline(inset: 40) }
                    TicketMiniRow(ticket: ticket, selected: target?.id == ticket.id) {
                        Haptics.selection()
                        target = ticket
                        confirm = true
                    }
                }
            }
        }
    }

    private func search() async {
        let text = trimmed
        guard !text.isEmpty else {
            results = []
            return
        }
        try? await Task.sleep(for: .milliseconds(250))
        guard !Task.isCancelled else { return }
        searching = true
        defer { searching = false }
        if let found = try? await store.searchTickets(text), !Task.isCancelled {
            results = found
        }
    }
}
