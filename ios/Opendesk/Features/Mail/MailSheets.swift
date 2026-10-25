import SwiftUI

// MARK: - Share

/// Share just this conversation with everyone, teams or people. The account's own
/// audience sees it already.
struct MailShareSheet: View {
    let store: MailThreadStore
    let members: [Member]
    let teams: [Team]
    let me: String?
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var audience = MailAudience()
    @State private var saving = false
    @State private var loaded = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    Text("Share just this conversation. The rest of the account stays as it is.")
                        .font(ODFont.sans(14))
                        .foregroundStyle(Theme.fg2)
                    Panel {
                        Button {
                            Haptics.selection()
                            audience.everyone.toggle()
                        } label: {
                            MailPickRow(title: "Everyone in the workspace", subtitle: "Every member can open this conversation.", selected: audience.everyone) {
                                Image(systemName: "building.2").font(.system(size: 14)).foregroundStyle(Theme.fg2)
                                    .frame(width: 30, height: 30).background(Theme.sunken, in: Circle())
                            }
                        }
                        .buttonStyle(RowPressStyle())
                    }
                    if !audience.everyone {
                        if !teams.isEmpty {
                            SectionLabel("Teams")
                            Panel {
                                ForEach(Array(teams.enumerated()), id: \.element.id) { index, team in
                                    if index > 0 { Hairline(inset: 56) }
                                    Button { toggle(team: team.id) } label: {
                                        MailPickRow(title: team.name, subtitle: Format.plural(team.memberIDs?.count ?? 0, "member"), selected: audience.teamIDs.contains(team.id)) {
                                            Text(Format.initials(team.name)).font(ODFont.sans(11.5, weight: .semibold)).foregroundStyle(Theme.avatarTone(for: team.id).text)
                                                .frame(width: 30, height: 30)
                                                .background(Theme.avatarTone(for: team.id).soft, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                                        }
                                    }
                                    .buttonStyle(RowPressStyle())
                                }
                            }
                        }
                        SectionLabel("People")
                        Panel {
                            ForEach(Array(people.enumerated()), id: \.element.id) { index, member in
                                if index > 0 { Hairline(inset: 56) }
                                Button { toggle(user: member.id) } label: {
                                    MailPickRow(title: member.user.name, subtitle: member.user.email, selected: audience.userIDs.contains(member.id)) {
                                        Avatar(name: member.user.name, url: member.user.avatarURL, seed: member.id, size: 30)
                                    }
                                }
                                .buttonStyle(RowPressStyle())
                            }
                        }
                    }
                    Notice(text: "People who see the account see this conversation too.", symbol: "person.2")
                    if !(store.detail?.shares.isEmpty ?? true) {
                        Button(role: .destructive) { save(.nobody) } label: {
                            Text("Stop sharing").font(ODFont.sans(15, weight: .medium)).foregroundStyle(Tone.red.text)
                                .frame(maxWidth: .infinity).frame(height: 44)
                        }
                        .buttonStyle(TapScaleStyle())
                    }
                }
                .padding(20)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .safeAreaInset(edge: .bottom) {
                Button { save(audience) } label: { ButtonLabel(title: "Save", busy: saving) }
                    .buttonStyle(InkButtonStyle())
                    .disabled(saving || audience == store.detail?.shares)
                    .padding(.horizontal, 20)
                    .padding(.top, 12)
                    .padding(.bottom, 8)
                    .background { MailFooterFade() }
            }
            .navigationTitle("Share conversation")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
        .onAppear {
            guard !loaded else { return }
            loaded = true
            audience = store.detail?.shares ?? MailAudience()
        }
    }

    private var people: [Member] {
        members.filter { $0.id != me }.sorted { $0.user.name < $1.user.name }
    }

    private func toggle(team id: String) {
        Haptics.selection()
        if let index = audience.teamIDs.firstIndex(of: id) { audience.teamIDs.remove(at: index) } else { audience.teamIDs.append(id) }
    }

    private func toggle(user id: String) {
        Haptics.selection()
        if let index = audience.userIDs.firstIndex(of: id) { audience.userIDs.remove(at: index) } else { audience.userIDs.append(id) }
    }

    private func save(_ value: MailAudience) {
        saving = true
        Task {
            defer { saving = false }
            do {
                try await store.share(value)
                Haptics.success()
                env.toasts.show(value.isEmpty ? "No longer shared on its own" : "Shared", kind: .success)
                dismiss()
            } catch {
                env.toasts.error(error)
            }
        }
    }
}

/// A row you pick: its mark, name, a line under it, and a check.
struct MailPickRow<Mark: View>: View {
    let title: String
    var subtitle: String?
    let selected: Bool
    @ViewBuilder var mark: () -> Mark

    var body: some View {
        HStack(spacing: 12) {
            mark()
            VStack(alignment: .leading, spacing: 1) {
                Text(title).font(ODFont.sans(15, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                if let subtitle, !subtitle.isEmpty {
                    Text(subtitle).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).lineLimit(2)
                }
            }
            Spacer(minLength: 8)
            ZStack {
                Circle().strokeBorder(selected ? Color.clear : Theme.lineStrong, lineWidth: 1.5)
                if selected {
                    Circle().fill(Theme.inverse)
                    Image(systemName: "checkmark").font(.system(size: 10.5, weight: .bold)).foregroundStyle(Theme.inverseFg)
                }
            }
            .frame(width: 22, height: 22)
            .animation(Motion.press, value: selected)
        }
        .padding(.horizontal, 14)
        .frame(minHeight: 56)
        .contentShape(Rectangle())
        .accessibilityElement(children: .combine)
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

// MARK: - Assign

/// Teammates who should answer it. Assignees can see it even without the account.
struct MailAssignSheet: View {
    let store: MailThreadStore
    let members: [Member]
    let me: String?
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var picked: [String] = []
    @State private var loaded = false
    @State private var saving = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    Text("Assignees can see and answer this conversation.")
                        .font(ODFont.sans(14))
                        .foregroundStyle(Theme.fg2)
                    Panel {
                        ForEach(Array(people.enumerated()), id: \.element.id) { index, member in
                            if index > 0 { Hairline(inset: 56) }
                            Button { toggle(member.id) } label: {
                                MailPickRow(title: member.id == me ? "\(member.user.name) (you)" : member.user.name, subtitle: member.role.title, selected: picked.contains(member.id)) {
                                    Avatar(name: member.user.name, url: member.user.avatarURL, seed: member.id, size: 30)
                                }
                            }
                            .buttonStyle(RowPressStyle())
                        }
                    }
                }
                .padding(20)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .safeAreaInset(edge: .bottom) {
                Button { save() } label: { ButtonLabel(title: picked.isEmpty ? "Unassign" : "Save", busy: saving) }
                    .buttonStyle(InkButtonStyle())
                    .disabled(saving || picked == (store.thread?.assignees.map(\.id) ?? []))
                    .padding(.horizontal, 20)
                    .padding(.top, 12)
                    .padding(.bottom, 8)
                    .background { MailFooterFade() }
            }
            .navigationTitle("Assign")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) { Button("Cancel") { dismiss() } }
            }
        }
        .onAppear {
            guard !loaded else { return }
            loaded = true
            picked = store.thread?.assignees.map(\.id) ?? []
        }
    }

    /// You first, then the rest; viewers only read.
    private var people: [Member] {
        members.filter { $0.role.canWrite }.sorted { a, b in
            if (a.id == me) != (b.id == me) { return a.id == me }
            return a.user.name < b.user.name
        }
    }

    private func toggle(_ id: String) {
        Haptics.selection()
        if let index = picked.firstIndex(of: id) { picked.remove(at: index) } else { picked.append(id) }
    }

    private func save() {
        saving = true
        Task {
            defer { saving = false }
            do {
                try await store.assign(picked, members: members)
                Haptics.success()
                dismiss()
            } catch {
                env.toasts.error(error)
            }
        }
    }
}

// MARK: - Move and labels

/// Move the conversation to a folder, or put labels on it and take them off.
struct MailFolderSheet: View {
    enum Mode { case move, labels }

    let store: MailThreadStore
    let mode: Mode
    let apply: (MailAction, String?) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var on: Set<String> = []

    private var mail: MailStore { .shared }
    private var accountID: String { store.detail?.account.id ?? "" }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    if mode == .move {
                        Panel { list(systemFolders) }
                        let custom = mail.customFolders(of: accountID).filter { !$0.isLabel || isGoogle }
                        if !custom.isEmpty {
                            SectionLabel(isGoogle ? "Labels" : "Folders")
                            Panel { list(custom) }
                        }
                    } else {
                        let labels = mail.customFolders(of: accountID).filter(\.isLabel)
                        if labels.isEmpty {
                            EmptyState(symbol: "tag", title: "No labels yet", message: "Labels made in the mail account show up here.")
                        } else {
                            Panel { list(labels) }
                            Text("A conversation can carry several labels. Changes reach the mail account too.")
                                .font(ODFont.sans(12.5))
                                .foregroundStyle(Theme.fg3)
                        }
                    }
                }
                .padding(20)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle(mode == .move ? "Move to" : "Labels")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
        .onAppear { on = Set(store.thread?.folderIDs ?? []) }
    }

    private var isGoogle: Bool { store.detail?.account.provider == .google }

    private var systemFolders: [MailFolder] {
        let roles: [MailFolderRole] = isGoogle ? [.inbox, .spam, .trash] : [.inbox, .archive, .spam, .trash]
        return roles.compactMap { mail.folder(role: $0, in: accountID) }
    }

    private func list(_ folders: [MailFolder]) -> some View {
        ForEach(Array(folders.enumerated()), id: \.element.id) { index, folder in
            if index > 0 { Hairline(inset: 46) }
            Button { pick(folder) } label: {
                HStack(spacing: 12) {
                    Group {
                        if folder.isLabel && folder.isCustom {
                            Image(systemName: "tag.fill").foregroundStyle(MailTone.color(folder.color))
                        } else {
                            Image(systemName: symbol(folder)).foregroundStyle(Theme.fg3)
                        }
                    }
                    .font(.system(size: 14))
                    .frame(width: 20)
                    Text(folder.name).font(ODFont.sans(15)).foregroundStyle(Theme.fg).lineLimit(1)
                    Spacer()
                    if on.contains(folder.id) {
                        Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg)
                    }
                }
                .padding(.horizontal, 14)
                .frame(height: 46)
                .contentShape(Rectangle())
            }
            .buttonStyle(RowPressStyle())
            .accessibilityAddTraits(on.contains(folder.id) ? .isSelected : [])
        }
    }

    private func symbol(_ folder: MailFolder) -> String {
        switch folder.role {
        case .inbox: "tray"
        case .archive: "archivebox"
        case .spam: "xmark.shield"
        case .trash: "trash"
        default: "folder"
        }
    }

    private func pick(_ folder: MailFolder) {
        Haptics.selection()
        if mode == .labels {
            let removing = on.contains(folder.id)
            if removing { on.remove(folder.id) } else { on.insert(folder.id) }
            apply(removing ? .unlabel : .label, folder.id)
            return
        }
        switch folder.role {
        case .inbox: apply(.inbox, nil)
        case .archive: apply(.archive, nil)
        case .spam: apply(.spam, nil)
        case .trash: apply(.trash, nil)
        default: apply(.move, folder.id)
        }
        dismiss()
    }
}

// MARK: - Details

/// Who sent it, to whom, and how it arrived: the sender checks, the encryption, and the
/// original headers on request.
struct MailDetailsSheet: View {
    let message: MailMessage
    let store: MailThreadStore
    @Environment(\.dismiss) private var dismiss
    @State private var details: MailMessageDetails?
    @State private var failed = false
    @State private var showHeaders = false

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 16) {
                    if let warning = warning { warning }
                    Panel {
                        row("From", message.from.name.isEmpty ? message.from.email : "\(message.from.name) <\(message.from.email)>")
                        if !message.replyTo.isEmpty { Hairline(inset: 14); row("Reply to", list(message.replyTo)) }
                        Hairline(inset: 14); row("To", list(message.to))
                        if !message.cc.isEmpty { Hairline(inset: 14); row("Cc", list(message.cc)) }
                        if !message.bcc.isEmpty { Hairline(inset: 14); row("Bcc", list(message.bcc)) }
                        Hairline(inset: 14); row("Date", Format.fullDate(message.sentAt))
                        Hairline(inset: 14); row("Subject", message.subject.isEmpty ? "(no subject)" : message.subject)
                        if let details, !details.mailedBy.isEmpty { Hairline(inset: 14); row("Mailed by", details.mailedBy) }
                        if let details, !details.signedBy.isEmpty { Hairline(inset: 14); row("Signed by", details.signedBy.joined(separator: ", ")) }
                        if let details, !details.via.isEmpty, details.via != details.mailedBy { Hairline(inset: 14); row("Via", details.via) }
                        if let details, details.encryption == "tls" {
                            Hairline(inset: 14)
                            HStack(spacing: 8) {
                                Text("Security").font(ODFont.sans(13)).foregroundStyle(Theme.fg3).frame(width: 74, alignment: .leading)
                                Image(systemName: "lock.fill").font(.system(size: 11)).foregroundStyle(Tone.green.solid)
                                Text("Standard encryption (TLS)").font(ODFont.sans(14)).foregroundStyle(Theme.fg)
                                Spacer()
                            }
                            .padding(.horizontal, 14)
                            .padding(.vertical, 11)
                        }
                    }
                    if let details, !details.spf.isEmpty || !details.dkim.isEmpty || !details.dmarc.isEmpty {
                        HStack(spacing: 6) {
                            check("SPF", details.spf)
                            check("DKIM", details.dkim)
                            check("DMARC", details.dmarc)
                        }
                    }
                    if let details, !details.headers.isEmpty {
                        Button {
                            withAnimation(Motion.snappy) { showHeaders.toggle() }
                        } label: {
                            HStack {
                                Text(showHeaders ? "Hide original headers" : "Show original headers").font(ODFont.sans(14, weight: .medium))
                                Spacer()
                                Image(systemName: "chevron.down").font(.system(size: 11, weight: .semibold)).rotationEffect(.degrees(showHeaders ? 180 : 0))
                            }
                            .foregroundStyle(Theme.fg2)
                            .padding(.horizontal, 14)
                            .frame(height: 44)
                            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.lineSoft, lineWidth: 1))
                        }
                        .buttonStyle(TapScaleStyle())
                        if showHeaders {
                            VStack(alignment: .leading, spacing: 8) {
                                ForEach(Array(details.headers.enumerated()), id: \.offset) { _, header in
                                    Text("\(Text(header.name + ": ").foregroundStyle(Theme.fg))\(Text(header.value).foregroundStyle(Theme.fg2))")
                                        .font(ODFont.mono(11.5))
                                        .textSelection(.enabled)
                                        .fixedSize(horizontal: false, vertical: true)
                                }
                            }
                            .padding(14)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                        }
                    }
                    if details == nil, !failed {
                        ProgressView().frame(maxWidth: .infinity).padding(.top, 8)
                    }
                }
                .padding(20)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Details")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
        .task {
            do { details = try await store.details(of: message) } catch { failed = true }
        }
    }

    private var domain: String { String(message.from.email.split(separator: "@").last ?? "") }

    private var warning: Notice? {
        guard let details else { return nil }
        switch details.warning {
        case "spoofed", "failed":
            return Notice(text: "Be careful with this message. It failed its sender's checks, so it may not really be from \(domain). Don't open its links or files unless you are sure.", tone: .red, symbol: "exclamationmark.triangle.fill")
        case "unverified":
            return Notice(text: "This sender couldn't be verified. Nothing confirms this message came from \(domain). Take care with its links and files.", tone: .amber, symbol: "questionmark.diamond")
        default:
            return nil
        }
    }

    private func list(_ addresses: [MailAddress]) -> String {
        addresses.map { $0.name.isEmpty ? $0.email : "\($0.name) <\($0.email)>" }.joined(separator: "\n")
    }

    private func row(_ label: String, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(label).font(ODFont.sans(13)).foregroundStyle(Theme.fg3).frame(width: 74, alignment: .leading)
            Text(value).font(ODFont.sans(14)).foregroundStyle(Theme.fg).textSelection(.enabled).fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 11)
    }

    private func check(_ name: String, _ result: String) -> some View {
        let tone: Tone = switch result {
        case "pass": .green
        case "fail": .red
        case "": .gray
        default: .amber
        }
        return HStack(spacing: 5) {
            Image(systemName: result == "pass" ? "checkmark.circle.fill" : (result == "fail" ? "xmark.circle.fill" : "minus.circle.fill"))
                .font(.system(size: 11))
            Text("\(name) \(result.isEmpty ? "none" : result)").font(ODFont.sans(12, weight: .medium))
        }
        .foregroundStyle(tone.text)
        .padding(.horizontal, 8)
        .frame(height: 26)
        .background(tone.soft, in: Capsule())
    }
}

// MARK: - People

/// Someone in the conversation: how much mail you share with them, the other
/// conversations and the files they sent, from the mail you can see.
struct MailPersonSheet: View {
    enum Action {
        case open(MailThread)
        case write(String)
        case file(MailPerson.File)
    }

    let address: MailAddress
    let store: MailThreadStore
    let act: (Action) -> Void
    @Environment(\.dismiss) private var dismiss
    @State private var person: MailPerson?
    @State private var failed: String?

    private var mail: MailStore { .shared }
    private var name: String {
        if let person, !person.name.isEmpty { return person.name }
        return address.display
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    VStack(spacing: 10) {
                        Avatar(name: name, seed: address.email, size: 64)
                        VStack(spacing: 2) {
                            Text(name).font(ODFont.sans(20, weight: .semibold)).tracking(-0.3).foregroundStyle(Theme.fg)
                            Text(address.email).font(ODFont.sans(14)).foregroundStyle(Theme.fg3).textSelection(.enabled)
                        }
                        Button { act(.write(address.email)) } label: { ButtonLabel(title: "Write", symbol: "square.and.pencil") }
                            .buttonStyle(InkButtonStyle(fullWidth: false, height: 38))
                            .padding(.top, 4)
                    }
                    .frame(maxWidth: .infinity)
                    if let person {
                        HStack(spacing: 10) {
                            stat("Conversations", "\(person.conversations)")
                            stat("Since", person.firstAt.map { Format.shortDate($0) } ?? "–")
                            stat("Last", person.lastAt.map { Format.relShort($0) } ?? "–")
                        }
                        SectionLabel("Other conversations")
                        if person.threads.isEmpty {
                            Text("No other conversations with \(name) in the mail you can see.")
                                .font(ODFont.sans(14))
                                .foregroundStyle(Theme.fg3)
                                .frame(maxWidth: .infinity, alignment: .leading)
                                .padding(14)
                                .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                        } else {
                            VStack(spacing: 0) {
                                ForEach(person.threads) { thread in
                                    Button { act(.open(thread)) } label: {
                                        MailThreadRow(thread: thread, own: mail.ownEmails, account: mail.account(thread.accountID), showAccount: mail.accounts.count > 1, labels: mail.labels(on: thread))
                                    }
                                    .buttonStyle(RowPressStyle())
                                }
                            }
                            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                            .clipShape(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                        }
                        if !person.files.isEmpty {
                            SectionLabel("Files from \(name)")
                            VStack(spacing: 8) {
                                ForEach(person.files) { file in
                                    Button { act(.file(file)) } label: {
                                        HStack(spacing: 10) {
                                            Image(systemName: file.attachment.symbol).font(.system(size: 14)).foregroundStyle(Theme.fg2)
                                                .frame(width: 32, height: 32).background(Theme.sunken, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                                            VStack(alignment: .leading, spacing: 1) {
                                                Text(file.attachment.filename).font(ODFont.sans(14, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1)
                                                Text("\(Format.bytes(file.attachment.size)) · \(Format.shortDate(file.sentAt))").font(ODFont.sans(12)).foregroundStyle(Theme.fg3)
                                            }
                                            Spacer()
                                        }
                                        .padding(10)
                                        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                                    }
                                    .buttonStyle(TapScaleStyle())
                                }
                            }
                        }
                        Text("Counts the mail opendesk has synced that you can see. Trash and spam are left out.")
                            .font(ODFont.sans(12))
                            .foregroundStyle(Theme.fg4)
                    } else if let failed {
                        ErrorState(message: failed) { Task { await load() } }
                    } else {
                        ProgressView().frame(maxWidth: .infinity).padding(.top, 20)
                    }
                }
                .padding(20)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
        .task { await load() }
    }

    private func load() async {
        do {
            person = try await store.person(address.email)
            failed = nil
        } catch {
            failed = error.userMessage
        }
    }

    private func stat(_ label: String, _ value: String) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            Text(value).font(ODFont.sans(18, weight: .semibold)).foregroundStyle(Theme.fg).monospacedDigit().lineLimit(1).minimumScaleFactor(0.7)
            Text(label).font(ODFont.sans(12)).foregroundStyle(Theme.fg3)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(12)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
    }
}

/// The chrome a sheet's pinned button sits on, so rows scrolling under it fade out.
struct MailFooterFade: View {
    var body: some View {
        LinearGradient(stops: [.init(color: Theme.chrome.opacity(0), location: 0), .init(color: Theme.chrome, location: 0.3)], startPoint: .top, endPoint: .bottom)
            .padding(.top, -16)
            .ignoresSafeArea()
            .allowsHitTesting(false)
    }
}
