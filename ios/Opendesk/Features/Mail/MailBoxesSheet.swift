import SwiftUI

/// Where the list looks: which account (all of them or one), which box, and the account's
/// own folders and labels, each with its unread count.
struct MailBoxesSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    let mail: MailStore
    var onManage: () -> Void

    private let primary: [MailBox] = [.inbox, .unread, .starred, .sent, .drafts]
    private let more: [MailBox] = [.archive, .all, .spam, .trash]
    private let team: [MailBox] = [.shared, .assigned]

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    if mail.accounts.count > 1 { accountSwitcher }
                    Panel { rows(primary.filter(mail.offers)) }
                    SectionLabel("With your team")
                    Panel { rows(team) }
                    SectionLabel("More")
                    Panel { rows(more.filter(mail.offers)) }
                    ForEach(folderGroups, id: \.account.id) { group in
                        SectionLabel(group.title)
                        Panel {
                            ForEach(Array(group.folders.enumerated()), id: \.element.id) { index, folder in
                                if index > 0 { Hairline(inset: 46) }
                                folderRow(folder)
                            }
                        }
                    }
                    Button {
                        dismiss()
                        onManage()
                    } label: {
                        HStack(spacing: 8) {
                            Image(systemName: "gearshape").font(.system(size: 13, weight: .medium))
                            Text("Mail accounts").font(ODFont.sans(14, weight: .medium))
                            Image(systemName: "arrow.up.right").font(.system(size: 10.5, weight: .semibold)).foregroundStyle(Theme.fg4)
                        }
                        .foregroundStyle(Theme.fg2)
                        .frame(maxWidth: .infinity)
                        .frame(height: 40)
                    }
                    .buttonStyle(TapScaleStyle())
                }
                .padding(20)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Mailboxes")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) { Button("Done") { dismiss() } }
            }
        }
    }

    // MARK: Accounts

    private var accountSwitcher: some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 8) {
                accountChip(nil)
                ForEach(mail.accounts) { account in accountChip(account) }
            }
            .padding(.horizontal, 20)
        }
        .padding(.horizontal, -20)
    }

    private func accountChip(_ account: MailAccount?) -> some View {
        let selected = mail.scope.accountID == account?.id
        let unread = account.map { mail.counts.accounts[$0.id] ?? $0.unread } ?? (mail.counts.boxes["inbox"] ?? 0)
        return Button {
            Haptics.selection()
            mail.setAccount(account?.id)
        } label: {
            HStack(spacing: 7) {
                if let account {
                    MailAccountDot(color: account.color, size: 8)
                    VStack(alignment: .leading, spacing: 0) {
                        Text(account.label).font(ODFont.sans(13.5, weight: .semibold))
                        Text(account.email).font(ODFont.sans(11)).foregroundStyle(selected ? Theme.inverseFg.opacity(0.7) : Theme.fg3)
                    }
                    if !account.isPrivate {
                        Image(systemName: "person.2.fill").font(.system(size: 9.5)).opacity(0.65)
                    }
                } else {
                    Image(systemName: "tray.2").font(.system(size: 12.5, weight: .medium))
                    Text("All accounts").font(ODFont.sans(13.5, weight: .semibold))
                }
                if unread > 0 {
                    Text("\(unread)").font(ODFont.mono(11.5)).monospacedDigit().opacity(0.7)
                }
            }
            .foregroundStyle(selected ? Theme.inverseFg : Theme.fg)
            .padding(.horizontal, 12)
            .frame(height: 48)
            .background(selected ? Theme.inverse : Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(selected ? .clear : Theme.line, lineWidth: 1))
        }
        .buttonStyle(TapScaleStyle())
        .accessibilityAddTraits(selected ? .isSelected : [])
    }

    // MARK: Boxes

    @ViewBuilder
    private func rows(_ boxes: [MailBox]) -> some View {
        ForEach(Array(boxes.enumerated()), id: \.element) { index, box in
            if index > 0 { Hairline(inset: 46) }
            let selected = mail.scope.folderID == nil && mail.scope.box == box
            Button {
                Haptics.selection()
                mail.setBox(box)
                dismiss()
            } label: {
                MailBoxLabel(title: box == .inbox && mail.spansAccounts ? "All inboxes" : box.title, symbol: box.symbol, count: mail.unread(for: box), selected: selected)
            }
            .buttonStyle(RowPressStyle())
            .accessibilityAddTraits(selected ? .isSelected : [])
        }
    }

    // MARK: Folders

    private struct FolderGroup {
        var account: MailAccount
        var title: String
        var folders: [MailFolder]
    }

    private var folderGroups: [FolderGroup] {
        mail.accountsInView.compactMap { account in
            let folders = mail.customFolders(of: account.id)
            guard !folders.isEmpty else { return nil }
            let kind = folders.allSatisfy(\.isLabel) ? "Labels" : "Folders"
            let title = mail.accountsInView.count > 1 ? "\(kind) · \(account.label)" : kind
            return FolderGroup(account: account, title: title, folders: folders)
        }
    }

    private func folderRow(_ folder: MailFolder) -> some View {
        let selected = mail.scope.folderID == folder.id
        let unread = mail.unread(folder: folder)
        return Button {
            Haptics.selection()
            mail.setFolder(folder)
            dismiss()
        } label: {
            HStack(spacing: 12) {
                Group {
                    if folder.isLabel {
                        Image(systemName: "tag.fill").foregroundStyle(MailTone.color(folder.color))
                    } else {
                        Image(systemName: "folder").foregroundStyle(selected ? Theme.fg : Theme.fg3)
                    }
                }
                .font(.system(size: 14))
                .frame(width: 20)
                Text(folder.name).font(ODFont.sans(15, weight: selected ? .semibold : .regular)).foregroundStyle(Theme.fg).lineLimit(1)
                Spacer()
                if unread > 0 {
                    Text("\(unread)").font(ODFont.mono(12.5)).monospacedDigit().foregroundStyle(Theme.fg3)
                } else if folder.total > 0 {
                    Text("\(folder.total)").font(ODFont.mono(12.5)).monospacedDigit().foregroundStyle(Theme.fg4)
                }
                if selected { Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg) }
            }
            .padding(.horizontal, 14)
            .frame(height: 46)
            .background(selected ? Theme.hover : .clear)
            .contentShape(Rectangle())
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(selected ? .isSelected : [])
    }
}

/// A box row: its icon, name, unread count and a check when it is the one shown.
struct MailBoxLabel: View {
    let title: String
    let symbol: String
    var count: Int?
    var selected = false

    var body: some View {
        HStack(spacing: 12) {
            Image(systemName: symbol)
                .font(.system(size: 15))
                .foregroundStyle(selected ? Theme.fg : Theme.fg3)
                .frame(width: 20)
            Text(title)
                .font(ODFont.sans(15, weight: selected ? .semibold : .regular))
                .foregroundStyle(Theme.fg)
            Spacer()
            if let count, count > 0 {
                Text("\(count)")
                    .font(ODFont.mono(12.5))
                    .monospacedDigit()
                    .foregroundStyle(Theme.fg3)
                    .contentTransition(.numericText())
            }
            if selected {
                Image(systemName: "checkmark").font(.system(size: 13, weight: .semibold)).foregroundStyle(Theme.fg)
            }
        }
        .padding(.horizontal, 14)
        .frame(height: 46)
        .background(selected ? Theme.hover : .clear)
        .contentShape(Rectangle())
    }
}
