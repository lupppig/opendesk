import SwiftUI

/// What the inbox hands search when it opens it with words already typed
/// (`AppRoute.search` carries nothing).
@MainActor
enum InboxSearchHandoff {
    static var query: String?
    static var ollie = false
}

/// Search across tickets, contacts and articles, with Ollie's search by meaning beside it.
struct SearchView: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var store = SearchStore()
    @State private var text = ""
    @FocusState private var focused: Bool

    private var ollieOn: Bool { env.directory.ai.enabled }

    var body: some View {
        VStack(spacing: 0) {
            header
            ScrollView {
                content
                    .padding(.horizontal, 16)
                    .padding(.top, 6)
                    .padding(.bottom, 40)
            }
            .scrollDismissesKeyboard(.immediately)
        }
        .background(Theme.chrome.ignoresSafeArea())
        .toolbarVisibility(.hidden, for: .navigationBar)
        .tabBarHidden()
        .onAppear {
            store.start(api: env.session.api, workspaceID: env.session.workspace?.id)
            if InboxSearchHandoff.ollie, ollieOn { store.setMode(.ollie) }
            if let handed = InboxSearchHandoff.query {
                text = handed
                store.setQuery(handed)
            }
            InboxSearchHandoff.query = nil
            InboxSearchHandoff.ollie = false
            if text.isEmpty {
                Task {
                    try? await Task.sleep(for: .milliseconds(250))
                    focused = true
                }
            }
        }
        .onChange(of: text) { _, value in store.setQuery(value) }
    }

    // MARK: Header

    private var header: some View {
        VStack(spacing: 10) {
            GlassEffectContainer(spacing: 10) {
                HStack(spacing: 10) {
                    GlassCircleButton(symbol: "chevron.left", label: "Back") { dismiss() }
                    HStack(spacing: 8) {
                        if store.mode == .ollie {
                            OllieMark(size: 18, state: store.searching ? .thinking : .idle)
                        } else {
                            Image(systemName: "magnifyingglass").font(.system(size: 15, weight: .medium)).foregroundStyle(Theme.fg2)
                        }
                        TextField(text: $text) {
                            Text(store.mode == .ollie ? "Describe what you're looking for" : "Search tickets, contacts and articles").foregroundStyle(Theme.fg4)
                        }
                        .font(ODFont.sans(16))
                        .foregroundStyle(Theme.fg)
                        .textInputAutocapitalization(.never)
                        .autocorrectionDisabled()
                        .submitLabel(.search)
                        .focused($focused)
                        .onSubmit { store.remember() }
                        if store.searching {
                            ProgressView().controlSize(.small)
                        } else if !text.isEmpty {
                            Button {
                                text = ""
                                focused = true
                            } label: {
                                Image(systemName: "xmark.circle.fill").font(.system(size: 16)).foregroundStyle(Theme.fg4)
                            }
                            .buttonStyle(.plain)
                            .accessibilityLabel("Clear search")
                        }
                    }
                    .padding(.horizontal, 15)
                    .frame(height: 44)
                    .glassEffect(.regular.interactive(), in: Capsule())
                }
            }
            if ollieOn {
                SearchModeToggle(mode: store.mode) { mode in
                    store.setMode(mode)
                    if mode == .ollie { focused = true }
                }
            }
        }
        .padding(.horizontal, 16)
        .padding(.top, 6)
        .padding(.bottom, 8)
    }

    // MARK: Content

    @ViewBuilder
    private var content: some View {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        if trimmed.count < 2 {
            idle
        } else if let error = store.error, store.hits == nil {
            ErrorState(title: "Couldn't search", message: error) { store.retry() }
        } else if let hits = store.hits {
            if hits.isEmpty {
                if store.mode == .ollie {
                    EmptyState(title: "Nothing related yet", message: "Nothing related yet. Try describing the problem in other words.") {
                        OllieMark(size: 30, state: .muted)
                    }
                } else {
                    EmptyState(symbol: "magnifyingglass", title: "No results for \u{201C}\(trimmed)\u{201D}", message: "Check the spelling or try fewer words.")
                }
            } else {
                results(hits)
            }
        } else {
            SearchSkeleton()
        }
    }

    @ViewBuilder
    private var idle: some View {
        if store.mode == .ollie {
            VStack(alignment: .leading, spacing: 14) {
                HStack(alignment: .top, spacing: 10) {
                    OllieMark(size: 26)
                    Text("Describe what you're looking for in plain words. Ollie finds tickets and articles about the same thing, even when they use other words.")
                        .font(ODFont.callout)
                        .foregroundStyle(Theme.fg2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Tone.violet.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Tone.violet.line, lineWidth: 1))
                SectionLabel("Try asking")
                Panel {
                    ForEach(Array(Self.examples.enumerated()), id: \.offset) { index, example in
                        if index > 0 { Hairline(inset: 44) }
                        Button {
                            Haptics.selection()
                            text = example
                            focused = false
                        } label: {
                            HStack(spacing: 12) {
                                Image(systemName: "sparkle").font(.system(size: 13)).foregroundStyle(Tone.violet.solid).frame(width: 18)
                                Text(example).font(ODFont.sans(15)).foregroundStyle(Theme.fg)
                                Spacer()
                            }
                            .padding(.horizontal, 14)
                            .frame(height: 46)
                            .contentShape(Rectangle())
                        }
                        .buttonStyle(RowPressStyle())
                    }
                }
            }
        } else if !store.recent.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                HStack {
                    SectionLabel("Recent searches")
                    Button("Clear") {
                        Haptics.selection()
                        store.clearRecent()
                    }
                    .font(ODFont.sans(12.5, weight: .medium))
                    .foregroundStyle(Theme.fg3)
                }
                Panel {
                    ForEach(Array(store.recent.enumerated()), id: \.element) { index, recent in
                        if index > 0 { Hairline(inset: 44) }
                        HStack(spacing: 0) {
                            Button {
                                Haptics.selection()
                                text = recent
                                focused = false
                            } label: {
                                HStack(spacing: 12) {
                                    Image(systemName: "clock.arrow.circlepath").font(.system(size: 14)).foregroundStyle(Theme.fg4).frame(width: 18)
                                    Text(recent).font(ODFont.sans(15)).foregroundStyle(Theme.fg).lineLimit(1)
                                    Spacer()
                                }
                                .padding(.leading, 14)
                                .frame(height: 46)
                                .contentShape(Rectangle())
                            }
                            .buttonStyle(RowPressStyle())
                            Button {
                                Haptics.selection()
                                store.forget(recent)
                            } label: {
                                Image(systemName: "xmark").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.fg4)
                                    .frame(width: 44, height: 46)
                                    .contentShape(Rectangle())
                            }
                            .buttonStyle(.plain)
                            .accessibilityLabel("Remove \(recent)")
                        }
                    }
                }
            }
            .motion(Motion.snappy, value: store.recent)
        } else {
            EmptyState(symbol: "magnifyingglass", title: "Search everywhere", message: "Search tickets, contacts and articles.")
                .padding(.top, 40)
        }
    }

    private static let examples = [
        "Customers who were charged twice",
        "Sign-in problems after a password reset",
        "Requests to export data",
    ]

    private func results(_ hits: SearchHits) -> some View {
        let ollie = store.mode == .ollie
        return VStack(alignment: .leading, spacing: 18) {
            if ollie {
                HStack(spacing: 7) {
                    OllieMark(size: 16)
                    Text("Searching by meaning").font(ODFont.sans(12.5, weight: .medium)).foregroundStyle(Tone.violet.text)
                }
            }
            if !hits.tickets.isEmpty {
                group(ollie ? "Related tickets" : "Tickets", count: hits.tickets.count) {
                    ForEach(Array(hits.tickets.enumerated()), id: \.element.id) { index, ticket in
                        if index > 0 { Hairline(inset: 40) }
                        Button {
                            store.remember()
                            env.router.push(.ticket(ticket))
                        } label: {
                            SearchTicketRow(ticket: ticket)
                        }
                        .buttonStyle(RowPressStyle())
                    }
                }
            }
            if !hits.contacts.isEmpty {
                group("Contacts", count: hits.contacts.count) {
                    ForEach(Array(hits.contacts.enumerated()), id: \.element.id) { index, contact in
                        if index > 0 { Hairline(inset: 54) }
                        Button {
                            store.remember()
                            env.router.push(.contact(contact.id))
                        } label: {
                            SearchContactRow(contact: contact)
                        }
                        .buttonStyle(RowPressStyle())
                    }
                }
            }
            if !hits.articles.isEmpty {
                group(ollie ? "Related articles" : "Articles", count: hits.articles.count) {
                    ForEach(Array(hits.articles.enumerated()), id: \.element.id) { index, article in
                        if index > 0 { Hairline(inset: 40) }
                        Button {
                            store.remember()
                            env.router.push(.article(article.id))
                        } label: {
                            SearchArticleRow(article: article)
                        }
                        .buttonStyle(RowPressStyle())
                    }
                }
            }
        }
        .motion(Motion.fade, value: hits.tickets.map(\.id) + hits.contacts.map(\.id) + hits.articles.map(\.id))
    }

    private func group<Content: View>(_ title: String, count: Int, @ViewBuilder content: @escaping () -> Content) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            HStack(spacing: 6) {
                Text(title).font(ODFont.sans(12.5, weight: .medium)).foregroundStyle(Theme.fg3)
                Text("\(count)").font(ODFont.mono(11)).foregroundStyle(Theme.fg4)
                Spacer()
            }
            Panel { content() }
        }
    }
}

/// Everything / Ask Ollie; Ollie's side is violet.
struct SearchModeToggle: View {
    let mode: SearchStore.Mode
    let onPick: (SearchStore.Mode) -> Void
    @Namespace private var thumb
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        HStack(spacing: 2) {
            item(.words, "Everything") {
                Image(systemName: "magnifyingglass").font(.system(size: 12, weight: .semibold))
            }
            item(.ollie, "Ask Ollie") {
                OllieMark(size: 15, state: mode == .ollie ? .idle : .muted, eyes: false)
            }
        }
        .padding(3)
        .background(Theme.sunken, in: Capsule())
        .overlay(Capsule().strokeBorder(Theme.lineSoft, lineWidth: 1))
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func item<Mark: View>(_ value: SearchStore.Mode, _ title: String, @ViewBuilder mark: () -> Mark) -> some View {
        let on = value == mode
        let tint = value == .ollie ? Tone.violet.text : Theme.fg
        return Button {
            guard !on else { return }
            Haptics.selection()
            withAnimation(reduceMotion ? nil : Motion.snappy) { onPick(value) }
        } label: {
            HStack(spacing: 6) {
                mark()
                Text(title).font(ODFont.sans(13.5, weight: on ? .semibold : .medium))
            }
            .foregroundStyle(on ? tint : Theme.fg3)
            .padding(.horizontal, 13)
            .frame(height: 30)
            .background {
                if on {
                    Capsule()
                        .fill(value == .ollie ? Tone.violet.soft.mix(with: Theme.surface, by: 0.4) : Theme.surface)
                        .overlay(Capsule().strokeBorder(value == .ollie ? Tone.violet.line : Color.clear, lineWidth: 1))
                        .shadow(color: .black.opacity(0.05), radius: 2, y: 1)
                        .matchedGeometryEffect(id: "thumb", in: thumb)
                }
            }
            .contentShape(Capsule())
        }
        .buttonStyle(.plain)
        .accessibilityAddTraits(on ? .isSelected : [])
    }
}

struct SearchTicketRow: View {
    let ticket: Ticket

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            StatusIcon(status: ticket.status, size: 15).padding(.top, 2)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(ticket.displaySubject)
                        .font(ODFont.sans(15, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    Spacer(minLength: 4)
                    Text(Format.relShort(ticket.lastMessageAt))
                        .font(ODFont.sans(12))
                        .foregroundStyle(Theme.fg4)
                        .monospacedDigit()
                }
                HStack(spacing: 6) {
                    TicketNumber(number: ticket.number)
                    Text(ticket.contactName).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).lineLimit(1)
                    if !ticket.lastMessagePreview.isEmpty {
                        Text(ticket.lastMessagePreview).font(ODFont.sans(13)).foregroundStyle(Theme.fg4).lineLimit(1)
                    }
                }
            }
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }
}

struct SearchContactRow: View {
    let contact: Contact

    var body: some View {
        HStack(spacing: 12) {
            Avatar(name: contact.displayName, url: contact.avatarURL, seed: contact.id, size: 30)
            VStack(alignment: .leading, spacing: 1) {
                Text(contact.displayName).font(ODFont.sans(15, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                if let email = contact.email, email != contact.displayName {
                    Text(email).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).lineLimit(1)
                }
            }
            Spacer()
            if contact.ticketCount > 0 {
                Text(Format.plural(contact.ticketCount, "ticket")).font(ODFont.sans(12)).foregroundStyle(Theme.fg4)
            }
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 52)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }
}

struct SearchArticleRow: View {
    let article: SearchArticle

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: "doc.text").font(.system(size: 15)).foregroundStyle(Theme.fg3).padding(.top, 1).frame(width: 15)
            VStack(alignment: .leading, spacing: 3) {
                HStack(spacing: 6) {
                    Text(article.title.isEmpty ? "Untitled" : article.title)
                        .font(ODFont.sans(15, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    if article.isDraft {
                        TonePill(text: "Draft", tone: .gray, height: 18)
                    }
                }
                if !article.excerpt.isEmpty {
                    Text(article.excerpt).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).lineLimit(2)
                }
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
    }
}

/// Grey result rows while the first search runs.
struct SearchSkeleton: View {
    @State private var dim = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        Panel {
            ForEach(0 ..< 4, id: \.self) { index in
                if index > 0 { Hairline(inset: 40) }
                HStack(alignment: .top, spacing: 12) {
                    Circle().fill(Theme.active).frame(width: 15, height: 15)
                    VStack(alignment: .leading, spacing: 7) {
                        RoundedRectangle(cornerRadius: 4).fill(Theme.active).frame(width: [180, 140, 200, 120][index], height: 11)
                        RoundedRectangle(cornerRadius: 4).fill(Theme.hover).frame(height: 10).padding(.trailing, [60, 30, 90, 50][index])
                    }
                }
                .padding(14)
            }
        }
        .opacity(dim ? 0.55 : 1)
        .onAppear {
            guard !reduceMotion else { return }
            withAnimation(.easeInOut(duration: 0.9).repeatForever()) { dim = true }
        }
        .accessibilityLabel("Searching")
    }
}
