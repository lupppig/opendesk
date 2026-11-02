import SwiftUI

// Pieces of the view editor (Profile → Views): the icon picker and the pickers for the
// contacts and companies a view leaves out.

/// Picks a view's icon: an emoji, one of the curated icons, or one or two letters on a
/// tile, with a colour for the last two. Writes the icon whole, or nil for none.
struct ViewIconPicker: View {
    @Binding var icon: ViewIcon?
    @Environment(\.dismiss) private var dismiss

    @State private var kind: ViewIcon.Kind = .icon
    @State private var search = ""
    @State private var text = ""
    @State private var color: String?
    @FocusState private var focused: Bool

    private static let kinds: [ViewIcon.Kind] = [.emoji, .icon, .text]

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 12) {
                Text("Icon").font(ODFont.sans(17, weight: .semibold)).foregroundStyle(Theme.fg)
                Spacer()
                if icon != nil {
                    Button("None") {
                        Haptics.selection()
                        icon = nil
                        dismiss()
                    }
                    .font(ODFont.sans(15, weight: .medium))
                    .foregroundStyle(Theme.fg2)
                }
                Button("Done") { dismiss() }
                    .font(ODFont.sans(15, weight: .semibold))
                    .foregroundStyle(Theme.fg)
            }
            .padding(.horizontal, 20)
            .padding(.top, 18)
            .padding(.bottom, 12)

            AccountSegmented(options: Self.kinds, selection: $kind, label: Self.title)
                .padding(.horizontal, 20)
                .padding(.bottom, 12)

            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    switch kind {
                    case .emoji: emoji
                    case .icon: icons
                    case .text: letters
                    }
                    if kind != .emoji { palette }
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 30)
            }
            .scrollDismissesKeyboard(.interactively)
        }
        .background(Theme.surface)
        .onAppear {
            guard let icon else { return }
            kind = icon.kind
            color = icon.color
            if icon.kind == .text { text = icon.value }
        }
        .onChange(of: kind) { _, _ in search = "" }
    }

    private static func title(_ kind: ViewIcon.Kind) -> String {
        switch kind {
        case .emoji: "Emoji"
        case .icon: "Icon"
        case .text: "Text"
        }
    }

    // MARK: Emoji

    private var emoji: some View {
        VStack(alignment: .leading, spacing: 12) {
            searchField(prompt: "Search or paste an emoji")
            let pasted = search.trimmingCharacters(in: .whitespaces)
            let list = ViewIconCatalog.isOneEmoji(pasted) ? [Self.Emoji(char: pasted, words: "")] : Self.emojis(matching: pasted)
            if list.isEmpty {
                Text("Nothing matches.").font(ODFont.callout).foregroundStyle(Theme.fg3)
            } else {
                LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 6), count: 7), spacing: 6) {
                    ForEach(list, id: \.char) { item in
                        let on = icon?.kind == .emoji && icon?.value == item.char
                        Button {
                            Haptics.selection()
                            icon = ViewIcon(kind: .emoji, value: item.char)
                        } label: {
                            Text(item.char)
                                .font(.system(size: 24))
                                .frame(maxWidth: .infinity)
                                .frame(height: 42)
                                .background(on ? Theme.active : Color.clear, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                                .overlay(RoundedRectangle(cornerRadius: 9, style: .continuous).strokeBorder(on ? Theme.fg.opacity(0.5) : Color.clear, lineWidth: 1.25))
                                .contentShape(Rectangle())
                        }
                        .buttonStyle(TapScaleStyle(scale: 0.9))
                        .accessibilityLabel(item.words.isEmpty ? item.char : item.words.components(separatedBy: " ").first ?? item.char)
                        .accessibilityAddTraits(on ? .isSelected : [])
                    }
                }
            }
            Text("An emoji keeps its own colours.").font(ODFont.footnote).foregroundStyle(Theme.fg3)
        }
    }

    struct Emoji: Hashable {
        let char: String
        let words: String
    }

    static func emojis(matching query: String) -> [Emoji] {
        let terms = query.lowercased().split(whereSeparator: \.isWhitespace).map(String.init)
        guard !terms.isEmpty else { return emojiList }
        return emojiList.filter { item in
            let words = item.words.split(separator: " ")
            return terms.allSatisfy { term in words.contains { $0.hasPrefix(term) } }
        }
    }

    /// Emojis a support team reaches for; any other can be typed or pasted in the search.
    static let emojiList: [Emoji] = [
        Emoji(char: "🔥", words: "fire hot urgent"), Emoji(char: "🚨", words: "siren alarm emergency urgent"),
        Emoji(char: "⚡", words: "lightning fast zap"), Emoji(char: "⏰", words: "alarm clock time sla"),
        Emoji(char: "⏳", words: "hourglass waiting pending"), Emoji(char: "📥", words: "inbox tray incoming"),
        Emoji(char: "📤", words: "outbox sent outgoing"), Emoji(char: "📬", words: "mailbox mail post"),
        Emoji(char: "✉️", words: "envelope email mail"), Emoji(char: "💬", words: "speech chat message bubble"),
        Emoji(char: "📞", words: "phone call telephone"), Emoji(char: "📣", words: "megaphone announce"),
        Emoji(char: "🔔", words: "bell notification"), Emoji(char: "📦", words: "package parcel order box"),
        Emoji(char: "🚚", words: "truck delivery shipping"), Emoji(char: "↩️", words: "return back undo refund"),
        Emoji(char: "🛒", words: "cart shopping order"), Emoji(char: "🛍️", words: "shopping bags purchase"),
        Emoji(char: "💳", words: "card credit payment billing"), Emoji(char: "🧾", words: "receipt invoice bill"),
        Emoji(char: "💰", words: "money bag cash"), Emoji(char: "💸", words: "money refund spend"),
        Emoji(char: "🏷️", words: "tag label price"), Emoji(char: "🎁", words: "gift present voucher"),
        Emoji(char: "⚠️", words: "warning alert problem"), Emoji(char: "❗", words: "exclamation important"),
        Emoji(char: "🛑", words: "stop sign blocked"), Emoji(char: "🚫", words: "prohibited banned spam"),
        Emoji(char: "✅", words: "check done solved resolved"), Emoji(char: "⏸️", words: "pause hold"),
        Emoji(char: "🐛", words: "bug defect engineering"), Emoji(char: "🔧", words: "wrench repair fix"),
        Emoji(char: "⚙️", words: "gear settings"), Emoji(char: "🔑", words: "key password access login"),
        Emoji(char: "🔒", words: "lock private security"), Emoji(char: "🛡️", words: "shield security fraud"),
        Emoji(char: "💡", words: "bulb idea feature request"), Emoji(char: "📱", words: "phone mobile app"),
        Emoji(char: "💻", words: "laptop computer"), Emoji(char: "🌍", words: "globe world international"),
        Emoji(char: "👤", words: "person user customer contact"), Emoji(char: "👥", words: "people team group"),
        Emoji(char: "🏢", words: "office building company business"), Emoji(char: "🤝", words: "handshake partner deal"),
        Emoji(char: "👑", words: "crown vip premium"), Emoji(char: "⭐", words: "star favourite important"),
        Emoji(char: "❤️", words: "heart love"), Emoji(char: "😊", words: "smile happy"),
        Emoji(char: "😡", words: "angry unhappy complaint"), Emoji(char: "👍", words: "thumbs up good"),
        Emoji(char: "👎", words: "thumbs down bad"), Emoji(char: "🚩", words: "flag flagged report"),
        Emoji(char: "📌", words: "pin pinned"), Emoji(char: "📅", words: "calendar date schedule"),
        Emoji(char: "📄", words: "document file page"), Emoji(char: "📁", words: "folder files"),
        Emoji(char: "⚖️", words: "scale legal law"), Emoji(char: "🎓", words: "graduation education training"),
        Emoji(char: "👀", words: "eyes watch review"), Emoji(char: "🔍", words: "search find magnifier"),
        Emoji(char: "✨", words: "sparkles new magic"), Emoji(char: "🎯", words: "target goal focus"),
        Emoji(char: "🧪", words: "test beta experiment"),
    ]

    // MARK: Icons

    private var icons: some View {
        VStack(alignment: .leading, spacing: 12) {
            searchField(prompt: "Search icons")
            let list = ViewIconCatalog.search(search)
            if list.isEmpty {
                Text("Nothing matches.").font(ODFont.callout).foregroundStyle(Theme.fg3)
            } else {
                LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 6), count: 7), spacing: 6) {
                    ForEach(list) { item in
                        let on = icon?.kind == .icon && icon?.value == item.name
                        Button {
                            Haptics.selection()
                            icon = ViewIcon(kind: .icon, value: item.name, color: color)
                        } label: {
                            Image(systemName: item.symbol)
                                .font(.system(size: 18))
                                .foregroundStyle(color == nil ? Theme.fg2 : Tone(label: color).solid)
                                .frame(maxWidth: .infinity)
                                .frame(height: 42)
                                .background(on ? Theme.active : Color.clear, in: RoundedRectangle(cornerRadius: 9, style: .continuous))
                                .overlay(RoundedRectangle(cornerRadius: 9, style: .continuous).strokeBorder(on ? Theme.fg.opacity(0.5) : Color.clear, lineWidth: 1.25))
                                .contentShape(Rectangle())
                        }
                        .buttonStyle(TapScaleStyle(scale: 0.9))
                        .accessibilityLabel(item.name.replacingOccurrences(of: "-", with: " "))
                        .accessibilityAddTraits(on ? .isSelected : [])
                    }
                }
            }
        }
    }

    // MARK: Text

    private var letters: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(spacing: 14) {
                ViewIconMark(icon: ViewIcon.textValid(text) ? ViewIcon(kind: .text, value: text, color: color) : nil, size: 26)
                    .frame(width: 34, height: 34)
                TextField(text: $text) { Text("PO").foregroundStyle(Theme.fg4) }
                    .font(ODFont.sans(17, weight: .medium))
                    .foregroundStyle(Theme.fg)
                    .textInputAutocapitalization(.characters)
                    .autocorrectionDisabled()
                    .focused($focused)
                    .padding(.horizontal, 13)
                    .frame(height: 46)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                    .accessibilityLabel("Letters or digits")
            }
            Text("One or two letters or digits, shown on a small tile.").font(ODFont.footnote).foregroundStyle(Theme.fg3)
        }
        .onChange(of: text) { _, value in
            let trimmed = String(value.filter { $0.isLetter || $0.isNumber }.prefix(2))
            if trimmed != value { text = trimmed; return }
            if ViewIcon.textValid(trimmed) { icon = ViewIcon(kind: .text, value: trimmed, color: color) }
        }
    }

    // MARK: Colour

    private var palette: some View {
        VStack(alignment: .leading, spacing: 8) {
            SectionLabel("Colour")
            LazyVGrid(columns: Array(repeating: GridItem(.flexible(), spacing: 8), count: 8), spacing: 10) {
                swatch(nil)
                ForEach(Tone.allCases, id: \.self) { tone in swatch(tone.rawValue) }
            }
        }
    }

    private func swatch(_ value: String?) -> some View {
        let on = color == value
        return Button {
            Haptics.selection()
            color = value
            if let current = icon, current.kind != .emoji { icon = ViewIcon(kind: current.kind, value: current.value, color: value) }
        } label: {
            ZStack {
                if let value {
                    Circle().fill(Tone(label: value).solid)
                } else {
                    Circle().strokeBorder(Theme.lineStrong, lineWidth: 1.5)
                    Image(systemName: "slash.circle").font(.system(size: 14)).foregroundStyle(Theme.fg3)
                }
            }
            .frame(width: 28, height: 28)
            .padding(3)
            .overlay(Circle().strokeBorder(on ? Theme.fg : Color.clear, lineWidth: 2))
            .frame(maxWidth: .infinity)
            .contentShape(Rectangle())
        }
        .buttonStyle(TapScaleStyle(scale: 0.9))
        .accessibilityLabel(value?.capitalized ?? "No colour")
        .accessibilityAddTraits(on ? .isSelected : [])
    }

    private func searchField(prompt: String) -> some View {
        HStack(spacing: 8) {
            Image(systemName: "magnifyingglass").font(.system(size: 14, weight: .medium)).foregroundStyle(Theme.fg3)
            TextField(text: $search) { Text(prompt).foregroundStyle(Theme.fg4) }
                .font(ODFont.sans(15.5))
                .foregroundStyle(Theme.fg)
                .textInputAutocapitalization(.never)
                .autocorrectionDisabled()
                .focused($focused)
        }
        .padding(.horizontal, 12)
        .frame(height: 40)
        .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
    }
}

extension ViewIcon {
    static func textValid(_ text: String) -> Bool { ViewIconCatalog.isShortText(text) }
}

/// Contacts or companies a view leaves out: search, tap to add or remove.
struct ViewExclusionPicker: View {
    enum Kind {
        case contacts, companies

        var title: String { self == .contacts ? "Leave out contacts" : "Leave out companies" }
        var prompt: String { self == .contacts ? "Search contacts" : "Search companies" }
    }

    struct Choice: Identifiable, Hashable {
        let id: String
        let name: String
        let detail: String?
    }

    let kind: Kind
    @Binding var chosen: [Choice]
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var search = ""
    @State private var results: [Choice] = []
    @State private var loading = false
    @FocusState private var focused: Bool

    var body: some View {
        VStack(spacing: 0) {
            HStack {
                Text(kind.title).font(ODFont.sans(17, weight: .semibold)).foregroundStyle(Theme.fg)
                Spacer()
                Button("Done") { dismiss() }
                    .font(ODFont.sans(15, weight: .semibold))
                    .foregroundStyle(Theme.fg)
            }
            .padding(.horizontal, 20)
            .padding(.top, 18)
            .padding(.bottom, 12)

            HStack(spacing: 8) {
                Image(systemName: "magnifyingglass").font(.system(size: 14, weight: .medium)).foregroundStyle(Theme.fg3)
                TextField(text: $search) { Text(kind.prompt).foregroundStyle(Theme.fg4) }
                    .font(ODFont.sans(15.5))
                    .foregroundStyle(Theme.fg)
                    .textInputAutocapitalization(.never)
                    .autocorrectionDisabled()
                    .focused($focused)
                if loading { ProgressView().controlSize(.small) }
            }
            .padding(.horizontal, 12)
            .frame(height: 40)
            .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .padding(.horizontal, 20)
            .padding(.bottom, 10)

            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    if !chosen.isEmpty {
                        VStack(alignment: .leading, spacing: 8) {
                            SectionLabel("Left out")
                            Panel {
                                ForEach(Array(chosen.enumerated()), id: \.element.id) { index, choice in
                                    if index > 0 { Hairline(inset: 14) }
                                    row(choice, on: true)
                                }
                            }
                        }
                    }
                    let others = results.filter { result in !chosen.contains { $0.id == result.id } }
                    if !others.isEmpty {
                        Panel {
                            ForEach(Array(others.enumerated()), id: \.element.id) { index, choice in
                                if index > 0 { Hairline(inset: 14) }
                                row(choice, on: false)
                            }
                        }
                    } else if !loading, !search.trimmingCharacters(in: .whitespaces).isEmpty {
                        Text(kind == .contacts ? "No contact matches." : "No company matches.")
                            .font(ODFont.callout)
                            .foregroundStyle(Theme.fg3)
                            .padding(.horizontal, 4)
                    }
                }
                .padding(.horizontal, 20)
                .padding(.bottom, 30)
                .motion(Motion.snappy, value: chosen)
            }
            .scrollDismissesKeyboard(.interactively)
        }
        .background(Theme.surface)
        .task(id: search) { await find() }
    }

    private func row(_ choice: Choice, on: Bool) -> some View {
        Button {
            Haptics.selection()
            if on { chosen.removeAll { $0.id == choice.id } } else { chosen.append(choice) }
        } label: {
            HStack(spacing: 12) {
                Avatar(name: choice.name, seed: choice.id, size: 28)
                VStack(alignment: .leading, spacing: 1) {
                    Text(choice.name).font(ODFont.sans(15, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                    if let detail = choice.detail, !detail.isEmpty, detail != choice.name {
                        Text(detail).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).lineLimit(1)
                    }
                }
                Spacer()
                Image(systemName: on ? "minus.circle.fill" : "plus.circle")
                    .font(.system(size: 18))
                    .foregroundStyle(on ? Tone.red.solid : Theme.fg3)
            }
            .padding(.horizontal, 14)
            .frame(minHeight: 50)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
        .accessibilityLabel(on ? "Stop leaving out \(choice.name)" : "Leave out \(choice.name)")
    }

    private func find() async {
        let text = search.trimmingCharacters(in: .whitespacesAndNewlines)
        if !text.isEmpty { try? await Task.sleep(for: .milliseconds(250)) }
        guard !Task.isCancelled else { return }
        loading = true
        defer { loading = false }
        let api = env.session.api
        var query = [URLQueryItem(name: "limit", value: "20")]
        if !text.isEmpty { query.append(URLQueryItem(name: "q", value: text)) }
        switch kind {
        case .contacts:
            guard let page: Page<Contact> = try? await api.get("v1/contacts", query: query), !Task.isCancelled else { return }
            results = page.data.map { Choice(id: $0.id, name: $0.displayName, detail: $0.email) }
        case .companies:
            guard let page: Page<Company> = try? await api.get("v1/companies", query: query), !Task.isCancelled else { return }
            results = page.data.map { Choice(id: $0.id, name: $0.name.isEmpty ? $0.domain : $0.name, detail: $0.domain) }
        }
    }

    /// The names of ids a view already leaves out, for the editor to show.
    @MainActor
    static func names(kind: Kind, ids: [String], api: APIClient) async -> [Choice] {
        await withTaskGroup(of: (Int, Choice).self) { group in
            for (index, id) in ids.enumerated() {
                group.addTask {
                    switch kind {
                    case .contacts:
                        let contact: Contact? = try? await api.get("v1/contacts/\(id)")
                        return (index, Choice(id: id, name: contact?.displayName ?? "A removed contact", detail: contact?.email))
                    case .companies:
                        let company: Company? = try? await api.get("v1/companies/\(id)")
                        return (index, Choice(id: id, name: company.map { $0.name.isEmpty ? $0.domain : $0.name } ?? "A removed company", detail: company?.domain))
                    }
                }
            }
            var out: [(Int, Choice)] = []
            for await item in group { out.append(item) }
            return out.sorted { $0.0 < $1.0 }.map(\.1)
        }
    }
}
