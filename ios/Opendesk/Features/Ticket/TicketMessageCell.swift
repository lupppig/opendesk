import SwiftUI

/// What can be done to one message, from its menu.
enum MessageAction {
    case copy, translate, showOriginalText, loadOriginal, hideOriginal
    case editNote, sendAsReply, retract, delete
    case retry, discard
    case open(Attachment)
}

/// What the caller may do to a message (contracts/api.md "Messages").
struct MessagePermissions {
    var canWrite: Bool
    var isAdmin: Bool
    var me: String?
    var channel: Channel

    func mine(_ message: Message) -> Bool { message.author.type == "user" && message.author.user?.id == me }

    /// The author's own plain note.
    func canEdit(_ message: Message) -> Bool { canWrite && message.isNote && message.threadID == nil && mine(message) && !message.isLocal }

    /// A note of one's own, without files, can become the reply written from it.
    func canSendAsReply(_ message: Message) -> Bool { canEdit(message) && message.attachments.isEmpty }

    /// A chat reply not yet emailed as a follow-up, by its author or an admin.
    func canRetract(_ message: Message) -> Bool {
        canWrite && channel == .chat && message.kind == .reply && !message.isInbound && !message.ai
            && message.author.type == "user" && (mine(message) || isAdmin) && message.deliveryStatus == "none" && !message.isLocal
    }

    /// Agents delete their own plain notes; admins any message outside a side conversation.
    func canDelete(_ message: Message) -> Bool {
        guard canWrite, message.threadID == nil, !message.isLocal else { return false }
        return isAdmin || (mine(message) && message.isNote)
    }
}

/// One message in the thread: a white card for the conversation, a grey dashed one for
/// internal notes, with the author, the body, files, delivery and a menu.
struct TicketMessageCell: View {
    let message: Message
    let permissions: MessagePermissions
    let baseURL: URL?
    var translation: Translation?
    var translating = false
    var original: String?
    var retrying = false
    @Binding var remoteImages: Bool
    let onAction: (MessageAction) -> Void

    private var failed: Bool { message.deliveryStatus == "failed" || message.deliveryStatus == "bounced" }
    private var sending: Bool { message.isLocal && !failed }

    private var accent: Color? {
        if message.isNote || message.isInbound { return nil }
        return message.ai ? Tone.violet.solid : Theme.fg2
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            header
            content
            if message.hasQuoted || original != nil { quotedToggle }
            if !message.attachments.isEmpty {
                TicketAttachmentList(attachments: message.attachments, baseURL: baseURL) { onAction(.open($0)) }
            }
            if failed { failure }
        }
        .padding(.horizontal, 14)
        .padding(.top, 12)
        .padding(.bottom, 14)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(message.isNote ? Theme.note : Theme.surface, in: shape)
        .overlay(alignment: .leading) {
            if let accent { Rectangle().fill(accent).frame(width: 3) }
        }
        .clipShape(shape)
        .overlay {
            if message.isNote {
                shape.strokeBorder(Theme.noteLine, style: StrokeStyle(lineWidth: 1, dash: [4, 3]))
            } else {
                shape.strokeBorder(failed ? Tone.red.line : Theme.line, lineWidth: 1)
            }
        }
        .opacity(sending ? 0.7 : 1)
        .contextMenu { menuItems }
        .accessibilityElement(children: .contain)
    }

    private var shape: RoundedRectangle { RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous) }

    // MARK: Header

    private var header: some View {
        HStack(alignment: .top, spacing: 10) {
            if let bot = message.bot, bot.nativeEmoji != nil || !bot.avatarURL.isEmpty {
                BotAvatar(bot: bot, size: 30)
            } else if message.ai {
                OllieMark(size: 30)
            } else {
                Avatar(name: message.authorName, url: message.authorAvatar, seed: message.authorSeed, size: 30)
            }
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(permissions.mine(message) ? "You" : message.authorName)
                        .font(ODFont.sans(14, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                        .lineLimit(1)
                    if message.bot != nil { AITag() }
                    if message.isNote {
                        TonePill(text: "Internal note", tone: .gray, height: 18) {
                            Image(systemName: "lock.fill").font(.system(size: 8.5))
                        }
                    }
                }
                if let line = secondaryLine {
                    Text(line)
                        .font(ODFont.sans(12))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(1)
                        .truncationMode(.middle)
                }
            }
            Spacer(minLength: 4)
            VStack(alignment: .trailing, spacing: 2) {
                HStack(spacing: 4) {
                    if sending {
                        ProgressView().controlSize(.mini)
                    } else if let glyph = deliveryGlyph {
                        Image(systemName: glyph).font(.system(size: 9.5, weight: .semibold)).foregroundStyle(Theme.fg4)
                    }
                    Text(Format.clock(message.createdAt))
                        .font(ODFont.mono(11))
                        .foregroundStyle(Theme.fg4)
                }
                if message.editedAt != nil {
                    Text("Edited").font(ODFont.sans(10.5)).foregroundStyle(Theme.fg4)
                }
            }
            if hasMenu {
                Menu { menuItems } label: {
                    Image(systemName: "ellipsis")
                        .font(.system(size: 13, weight: .semibold))
                        .foregroundStyle(Theme.fg3)
                        .frame(width: 26, height: 26)
                        .contentShape(Rectangle())
                }
                .padding(.trailing, -6)
                .padding(.top, -3)
                .accessibilityLabel("Message actions")
            }
        }
    }

    private var secondaryLine: String? {
        if message.isNote { return nil }
        if sending { return "Sending\u{2026}" }
        if message.isInbound {
            if permissions.channel == .chat { return "Live chat" }
            return message.fromEmail.isEmpty ? nil : message.fromEmail
        }
        if message.bot != nil { return permissions.channel == .chat ? "Replied in the chat" : nil }
        let to = message.to.filter { !$0.isEmpty }
        if !to.isEmpty {
            var line = "To " + to.joined(separator: ", ")
            if !message.cc.isEmpty { line += ", cc " + message.cc.joined(separator: ", ") }
            return line
        }
        return permissions.channel == .chat ? "Replied in the chat" : nil
    }

    private var deliveryGlyph: String? {
        guard !message.isNote, !message.isInbound else { return nil }
        switch message.deliveryStatus {
        case "delivered": return "checkmark.circle"
        case "sent": return "checkmark"
        case "queued": return "clock"
        default: return nil
        }
    }

    // MARK: Body

    @ViewBuilder private var content: some View {
        if let translation {
            VStack(alignment: .leading, spacing: 8) {
                TicketMessageBody(html: translation.html ?? HTMLText.html(fromPlain: translation.text ?? ""), inbound: message.isInbound, baseURL: baseURL, remoteImages: $remoteImages)
                HStack(spacing: 6) {
                    Image(systemName: "translate").font(.system(size: 10.5, weight: .medium))
                    Text(translation.sourceName.map { "Translated from \($0)" } ?? "Translated")
                    Text("·")
                    Button("Show original") { onAction(.showOriginalText) }
                        .fontWeight(.medium)
                        .foregroundStyle(Theme.fg2)
                }
                .font(ODFont.sans(11.5))
                .foregroundStyle(Tone.violet.text)
            }
        } else if let original {
            TicketMessageBody(html: original, inbound: message.isInbound, baseURL: baseURL, remoteImages: $remoteImages)
        } else if message.bodyHTML.isEmpty, message.bodyText.isEmpty, !message.attachments.isEmpty {
            EmptyView()
        } else {
            TicketMessageBody(html: message.bodyHTML.isEmpty ? HTMLText.html(fromPlain: message.bodyText) : message.bodyHTML, inbound: message.isInbound, baseURL: baseURL, remoteImages: $remoteImages)
        }
        if translating {
            HStack(spacing: 6) {
                OllieMark(size: 14, state: .thinking)
                Text("Translating\u{2026}").font(ODFont.sans(12)).foregroundStyle(Tone.violet.text)
            }
        }
    }

    private var quotedToggle: some View {
        Button {
            Haptics.tap()
            onAction(original == nil ? .loadOriginal : .hideOriginal)
        } label: {
            HStack(spacing: 5) {
                Image(systemName: original == nil ? "ellipsis" : "chevron.up").font(.system(size: 10, weight: .bold))
                Text(original == nil ? "Show quoted text" : "Hide quoted text").font(ODFont.sans(11.5, weight: .medium))
            }
            .foregroundStyle(Theme.fg3)
            .padding(.horizontal, 8)
            .frame(height: 22)
            .background(Theme.active, in: Capsule())
        }
        .buttonStyle(TapScaleStyle())
    }

    private var failure: some View {
        HStack(alignment: .top, spacing: 8) {
            Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 12)).foregroundStyle(Tone.red.solid)
            VStack(alignment: .leading, spacing: 2) {
                Text("Not delivered").font(ODFont.sans(13, weight: .semibold)).foregroundStyle(Tone.red.text)
                if !message.deliveryError.isEmpty {
                    Text(message.deliveryError).font(ODFont.sans(12)).foregroundStyle(Theme.fg2).fixedSize(horizontal: false, vertical: true)
                }
            }
            Spacer(minLength: 6)
            if permissions.canWrite {
                if message.isLocal {
                    Button("Discard") { onAction(.discard) }.buttonStyle(ChipButtonStyle())
                }
                Button { onAction(.retry) } label: { ButtonLabel(title: "Retry", busy: retrying) }
                    .buttonStyle(ChipButtonStyle(tone: .red))
                    .disabled(retrying)
            }
        }
        .padding(10)
        .background(Tone.red.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.control - 2, style: .continuous))
    }

    // MARK: Menu

    private var hasMenu: Bool { !sending }

    @ViewBuilder private var menuItems: some View {
        Button { onAction(.copy) } label: { Label("Copy text", systemImage: "doc.on.doc") }
        if !message.isNote || message.isInbound {
            if translation == nil {
                Button { onAction(.translate) } label: { Label("Translate", systemImage: "translate") }
            } else {
                Button { onAction(.showOriginalText) } label: { Label("Show original", systemImage: "text.quote") }
            }
        }
        if message.hasQuoted, original == nil {
            Button { onAction(.loadOriginal) } label: { Label("Show quoted text", systemImage: "text.append") }
        }
        if permissions.canEdit(message) {
            Divider()
            Button { onAction(.editNote) } label: { Label("Edit note", systemImage: "pencil") }
            if permissions.canSendAsReply(message) {
                Button { onAction(.sendAsReply) } label: { Label("Send as reply", systemImage: "arrowshape.turn.up.left") }
            }
        }
        if permissions.canRetract(message) {
            Divider()
            Button { onAction(.retract) } label: { Label("Turn into internal note", systemImage: "lock") }
        }
        if permissions.canDelete(message) {
            Divider()
            Button(role: .destructive) { onAction(.delete) } label: { Label(message.isNote ? "Delete note" : "Delete message", systemImage: "trash") }
        }
    }
}

/// Files on a message: image thumbnails, then a chip per other file. Tapping opens a preview.
struct TicketAttachmentList: View {
    let attachments: [Attachment]
    let baseURL: URL?
    let onOpen: (Attachment) -> Void

    var body: some View {
        let images = attachments.filter { $0.isImage && !$0.inline }
        let files = attachments.filter { !$0.isImage }
        VStack(alignment: .leading, spacing: 8) {
            if !images.isEmpty {
                FlowLayout(spacing: 8) {
                    ForEach(images) { image in
                        Button { onOpen(image) } label: { thumbnail(image) }
                            .buttonStyle(TapScaleStyle())
                            .accessibilityLabel("Image \(image.filename)")
                    }
                }
            }
            ForEach(files) { file in
                Button { onOpen(file) } label: { chip(file) }
                    .buttonStyle(TapScaleStyle(scale: 0.98))
                    .accessibilityLabel("File \(file.filename), \(Format.bytes(file.sizeBytes))")
            }
        }
    }

    private func resolved(_ attachment: Attachment) -> URL? {
        URL(string: attachment.url, relativeTo: baseURL)?.absoluteURL
    }

    private func thumbnail(_ attachment: Attachment) -> some View {
        let single = attachments.filter { $0.isImage && !$0.inline }.count == 1
        return ZStack {
            Theme.sunken
            AsyncImage(url: resolved(attachment), transaction: Transaction(animation: Motion.fade)) { phase in
                switch phase {
                case let .success(image): image.resizable().scaledToFill()
                case .failure: Image(systemName: "photo").foregroundStyle(Theme.fg4)
                default: ProgressView().controlSize(.small)
                }
            }
        }
        .frame(width: single ? 220 : 120, height: single ? 138 : 84)
        .clipShape(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }

    private func chip(_ file: Attachment) -> some View {
        HStack(spacing: 10) {
            Image(systemName: symbol(for: file))
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(Theme.fg2)
                .frame(width: 32, height: 32)
                .background(Theme.active, in: RoundedRectangle(cornerRadius: 7, style: .continuous))
            VStack(alignment: .leading, spacing: 1) {
                Text(file.filename).font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg).lineLimit(1).truncationMode(.middle)
                Text(Format.bytes(file.sizeBytes)).font(ODFont.mono(10.5)).foregroundStyle(Theme.fg3)
            }
            Spacer(minLength: 0)
            Image(systemName: "eye").font(.system(size: 12)).foregroundStyle(Theme.fg4)
        }
        .padding(6)
        .padding(.trailing, 6)
        .frame(maxWidth: 300, alignment: .leading)
        .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }

    private func symbol(for file: Attachment) -> String {
        let type = file.contentType.lowercased()
        if type.contains("pdf") { return "doc.richtext" }
        if type.hasPrefix("text/") { return "doc.plaintext" }
        if type.contains("zip") || type.contains("compressed") { return "doc.zipper" }
        if type.hasPrefix("video/") { return "film" }
        if type.hasPrefix("audio/") { return "waveform" }
        if type.contains("sheet") || type.contains("csv") || type.contains("excel") { return "tablecells" }
        return "doc"
    }
}
