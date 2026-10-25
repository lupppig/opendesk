import SwiftUI

/// Ollie's draft (web `KbDraftDialog`): pick up to ten tickets about one
/// question and, if you like, say what the article should explain. Ollie reads
/// them, finds what they share and writes a first version into the editor.
/// Nothing is saved until you save it.
struct KBDraftSheet: View {
    let onDraft: (KBArticleDraft) -> Void

    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    @State private var query = ""
    @State private var results: [Ticket] = []
    @State private var selected: [Ticket] = []
    @State private var topic = ""
    @State private var searching = false
    @State private var step: Int?
    @State private var error: String?
    @State private var searchTask: Task<Void, Never>?
    @State private var run: Task<Void, Never>?

    static let maxTickets = 10
    static let maxTopic = 300

    private var ready: Bool { !selected.isEmpty || !topic.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }
    private var steps: [String] {
        let read = selected.isEmpty ? "Reading the topic" : "Reading \(Format.plural(selected.count, "ticket"))"
        return [read, "Finding what customers ask", "Writing the article"]
    }

    var body: some View {
        NavigationStack {
            Group {
                if let step {
                    working(step)
                } else {
                    picker
                }
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Draft with Ollie")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        run?.cancel()
                        dismiss()
                    }
                }
            }
        }
        .task {
            await search("")
            #if DEBUG
            demo()
            #endif
        }
        .onChange(of: query) { _, value in
            searchTask?.cancel()
            searchTask = Task {
                try? await Task.sleep(for: .milliseconds(250))
                guard !Task.isCancelled else { return }
                await search(value)
            }
        }
    }

    // MARK: Picking

    private var picker: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 16) {
                HStack(alignment: .top, spacing: 12) {
                    OllieMark(size: 38)
                    Text("Pick tickets about the same question. Ollie writes a first version for you to edit; nothing is published until you do.")
                        .font(ODFont.sans(14))
                        .foregroundStyle(Theme.fg2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(14)
                .background(Tone.violet.soft.opacity(0.7), in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Tone.violet.line, lineWidth: 1))

                VStack(alignment: .leading, spacing: 6) {
                    HStack {
                        Text("Topic").font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg2)
                        Text("optional").font(ODFont.sans(12)).foregroundStyle(Theme.fg4)
                        Spacer()
                        if !topic.isEmpty {
                            Text("\(topic.count)/\(Self.maxTopic)").font(ODFont.mono(11)).foregroundStyle(topic.count > Self.maxTopic - 30 ? Tone.amber.text : Theme.fg4)
                        }
                    }
                    TextField(text: Binding(get: { topic }, set: { topic = String($0.prefix(Self.maxTopic)) }), axis: .vertical) {
                        Text("What should the article explain?").foregroundStyle(Theme.fg4)
                    }
                    .font(ODFont.sans(15.5))
                    .foregroundStyle(Theme.fg)
                    .lineLimit(1 ... 3)
                    .padding(.horizontal, 13)
                    .padding(.vertical, 12)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                }

                VStack(alignment: .leading, spacing: 8) {
                    HStack {
                        Text("Tickets").font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg2)
                        Spacer()
                        Text("\(selected.count) of \(Self.maxTickets)")
                            .font(ODFont.mono(11.5))
                            .foregroundStyle(selected.count == Self.maxTickets ? Tone.amber.text : Theme.fg3)
                            .contentTransition(.numericText())
                    }
                    if !selected.isEmpty {
                        ScrollView(.horizontal, showsIndicators: false) {
                            HStack(spacing: 6) {
                                ForEach(selected) { ticket in
                                    Button { toggle(ticket) } label: {
                                        HStack(spacing: 5) {
                                            Text(verbatim: "#\(ticket.number)").font(ODFont.mono(11.5))
                                            Text(ticket.subject).lineLimit(1).frame(maxWidth: 140)
                                            Image(systemName: "xmark").font(.system(size: 9, weight: .bold))
                                        }
                                    }
                                    .buttonStyle(ChipButtonStyle(tone: .violet))
                                    .transition(.scale(scale: 0.9).combined(with: .opacity))
                                }
                            }
                        }
                    }
                    searchField
                    Panel {
                        if results.isEmpty {
                            Text(searching ? "Searching" : "No tickets match.")
                                .font(ODFont.sans(14))
                                .foregroundStyle(Theme.fg3)
                                .frame(maxWidth: .infinity, alignment: .center)
                                .padding(.vertical, 22)
                        } else {
                            ForEach(Array(results.enumerated()), id: \.element.id) { index, ticket in
                                row(ticket)
                                if index < results.count - 1 { Hairline(inset: 48) }
                            }
                        }
                    }
                }

                FormError(text: error)
            }
            .padding(16)
            .animation(reduceMotion ? nil : Motion.snappy, value: selected.map(\.id))
        }
        .scrollDismissesKeyboard(.interactively)
        .safeAreaInset(edge: .bottom) {
            Button(action: generate) {
                HStack(spacing: 8) {
                    Image(systemName: "sparkles").font(.system(size: 14, weight: .semibold))
                    Text(selected.isEmpty ? "Write the draft" : "Write from \(Format.plural(selected.count, "ticket"))")
                }
            }
            .buttonStyle(KBOllieButtonStyle())
            .disabled(!ready)
            .padding(.horizontal, 16)
            .padding(.top, 8)
            .padding(.bottom, 8)
            .background(Theme.chrome.opacity(0.9))
        }
    }

    private var searchField: some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass").font(.system(size: 14, weight: .medium)).foregroundStyle(Theme.fg3)
            TextField(text: $query) { Text("Search tickets by subject or number").foregroundStyle(Theme.fg4) }
                .font(ODFont.sans(15.5))
                .foregroundStyle(Theme.fg)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .submitLabel(.search)
            if searching { ProgressView().controlSize(.small) }
        }
        .padding(.horizontal, 13)
        .frame(height: 44)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }

    private func row(_ ticket: Ticket) -> some View {
        let on = selected.contains { $0.id == ticket.id }
        let full = selected.count >= Self.maxTickets && !on
        return Button { toggle(ticket) } label: {
            HStack(spacing: 12) {
                ZStack {
                    Circle().strokeBorder(on ? Tone.violet.solid : Theme.lineStrong, lineWidth: 1.5)
                    if on {
                        Circle().fill(Tone.violet.solid)
                        Image(systemName: "checkmark").font(.system(size: 10, weight: .bold)).foregroundStyle(.white)
                    }
                }
                .frame(width: 22, height: 22)
                VStack(alignment: .leading, spacing: 2) {
                    Text(ticket.subject)
                        .font(ODFont.sans(14.5, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    HStack(spacing: 6) {
                        Text(verbatim: "#\(ticket.number)").font(ODFont.mono(11.5))
                        if let contact = ticket.contact { Text(contact.name).lineLimit(1) }
                        Text(Format.relShort(ticket.lastMessageAt))
                    }
                    .font(ODFont.sans(12))
                    .foregroundStyle(Theme.fg3)
                }
                Spacer(minLength: 4)
                StatusIcon(status: ticket.status)
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .contentShape(Rectangle())
            .opacity(full ? 0.45 : 1)
        }
        .buttonStyle(RowPressStyle())
        .disabled(full)
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    // MARK: Working

    private func working(_ current: Int) -> some View {
        VStack(spacing: 26) {
            Spacer()
            OllieMark(size: 72, state: .thinking)
            VStack(alignment: .leading, spacing: 14) {
                ForEach(steps.indices, id: \.self) { index in
                    HStack(spacing: 12) {
                        ZStack {
                            if index < current {
                                Image(systemName: "checkmark.circle.fill").foregroundStyle(Tone.violet.solid)
                            } else if index == current {
                                ProgressView().controlSize(.small).tint(Tone.violet.solid)
                            } else {
                                Circle().strokeBorder(Theme.lineStrong, lineWidth: 1.5).frame(width: 16, height: 16)
                            }
                        }
                        .font(.system(size: 18))
                        .frame(width: 22, height: 22)
                        Text(steps[index])
                            .font(ODFont.sans(15.5, weight: index == current ? .semibold : .regular))
                            .foregroundStyle(index <= current ? Theme.fg : Theme.fg3)
                    }
                    .transition(.opacity)
                }
            }
            .padding(20)
            .frame(maxWidth: 320, alignment: .leading)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            .animation(reduceMotion ? nil : Motion.snappy, value: current)
            Text("This takes a few seconds.")
                .font(ODFont.sans(13))
                .foregroundStyle(Theme.fg3)
            Spacer()
            Spacer()
        }
        .frame(maxWidth: .infinity)
        .padding(24)
    }

    #if DEBUG
    /// QA states: `/demo/kb-ollie-pick` picks three tickets, `/demo/kb-ollie-run` also writes the draft.
    private func demo() {
        guard let action = env.router.demoAction, action == "kb-ollie-pick" || action == "kb-ollie-run" else { return }
        env.router.demoAction = nil
        selected = Array(results.prefix(3))
        if action == "kb-ollie-run" { generate() }
    }
    #endif

    // MARK: Actions

    private func toggle(_ ticket: Ticket) {
        Haptics.selection()
        if let index = selected.firstIndex(where: { $0.id == ticket.id }) {
            selected.remove(at: index)
        } else if selected.count < Self.maxTickets {
            selected.append(ticket)
        }
    }

    private func search(_ text: String) async {
        searching = true
        let found = await KBStore.shared.searchTickets(text)
        guard !Task.isCancelled else { return }
        results = found
        searching = false
    }

    private func generate() {
        guard ready else { return }
        error = nil
        Haptics.tap()
        withAnimation(reduceMotion ? nil : Motion.fade) { step = 0 }
        let ids = selected.map(\.id)
        let subject = topic.trimmingCharacters(in: .whitespacesAndNewlines)
        run = Task {
            let ticker = Task {
                try? await Task.sleep(for: .milliseconds(900))
                guard !Task.isCancelled else { return }
                step = 1
                try? await Task.sleep(for: .milliseconds(1300))
                guard !Task.isCancelled else { return }
                step = 2
            }
            defer { ticker.cancel() }
            do {
                async let draft = KBStore.shared.draft(ticketIDs: ids, topic: subject)
                // Let the steps read; a fast answer still shows each one briefly.
                try? await Task.sleep(for: .milliseconds(2600))
                let value = try await draft
                guard !Task.isCancelled else { return }
                step = 3
                try? await Task.sleep(for: .milliseconds(350))
                onDraft(value)
                dismiss()
            } catch {
                guard !error.isCancellation, !Task.isCancelled else { return }
                Haptics.error()
                self.error = error.userMessage
                withAnimation(reduceMotion ? nil : Motion.fade) { step = nil }
            }
        }
    }
}
