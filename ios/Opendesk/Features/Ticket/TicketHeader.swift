import SwiftUI

/// The floating bar over the thread: back, who and which ticket, who else is here,
/// the More menu and Solve (or Reopen).
struct TicketTopBar<More: View>: View {
    let ticket: Ticket?
    let viewers: [PresenceViewer]
    let canWrite: Bool
    let mailbox: ReplyAddress?
    let onBack: () -> Void
    let onDetails: () -> Void
    let onSolve: () -> Void
    let onReopen: () -> Void
    @ViewBuilder var more: () -> More

    var body: some View {
        GlassEffectContainer(spacing: 10) {
            HStack(spacing: 8) {
                GlassCircleButton(symbol: "chevron.left", label: "Back", action: onBack)

                Button(action: onDetails) {
                    HStack(spacing: 9) {
                        if let contact = ticket?.contact {
                            Avatar(name: contact.displayName, url: contact.avatarURL, seed: contact.id, size: 28)
                        }
                        VStack(alignment: .leading, spacing: 1) {
                            Text(ticket?.contactName ?? " ")
                                .font(ODFont.sans(14.5, weight: .semibold))
                                .foregroundStyle(Theme.fg)
                                .lineLimit(1)
                            if let ticket {
                                HStack(spacing: 4) {
                                    Text(verbatim: "#\(ticket.number)").font(ODFont.mono(11)).monospacedDigit()
                                    Text("·")
                                    Image(systemName: ticket.channel.symbol).font(.system(size: 9.5, weight: .semibold))
                                    Text(channelLine(ticket)).lineLimit(1)
                                }
                                .font(ODFont.sans(11.5))
                                .foregroundStyle(Theme.fg3)
                            }
                        }
                        Spacer(minLength: 0)
                        if !viewers.isEmpty {
                            PresenceStack(viewers: viewers, size: 20, max: 2)
                        }
                    }
                    .padding(.leading, 8)
                    .padding(.trailing, 12)
                    .frame(height: 44)
                    .contentShape(Capsule())
                }
                .buttonStyle(TapScaleStyle(scale: 0.98))
                .glassEffect(.regular.interactive(), in: Capsule())
                .accessibilityLabel(ticket.map { "\($0.contactName), ticket \($0.number). Details" } ?? "Details")

                Menu {
                    more()
                } label: {
                    Image(systemName: "ellipsis")
                        .font(.system(size: 16, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .frame(width: 44, height: 44)
                        .contentShape(Circle())
                }
                .glassEffect(.regular.interactive(), in: Circle())
                .accessibilityLabel("More")

                if canWrite, let ticket {
                    if ticket.status.isDone {
                        Button(action: onReopen) {
                            Image(systemName: "arrow.uturn.backward")
                                .font(.system(size: 15, weight: .semibold))
                                .foregroundStyle(Theme.fg)
                                .frame(width: 44, height: 44)
                                .contentShape(Circle())
                        }
                        .buttonStyle(.plain)
                        .glassEffect(.regular.interactive(), in: Circle())
                        .accessibilityLabel("Reopen")
                    } else {
                        HeaderPrimaryButton(symbol: "checkmark", label: "Solve", action: onSolve)
                    }
                }
            }
        }
        .padding(.horizontal, 12)
        .padding(.bottom, 6)
    }

    private func channelLine(_ ticket: Ticket) -> String {
        if ticket.channel == .email, let mailbox { return mailbox.email }
        return ticket.channel.title
    }
}

/// The subject (editable in place) and the ticket's properties as chips.
struct TicketSubjectBlock: View {
    let ticket: Ticket
    let canWrite: Bool
    let teamName: String?
    let me: String?
    @Binding var editing: Bool
    let onRename: (String) -> Void
    let onOpen: (TicketSheet) -> Void
    @State private var draft = ""
    @FocusState private var focused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            subject
            chips
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .padding(.horizontal, 4)
        .padding(.top, 4)
    }

    @ViewBuilder private var subject: some View {
        if editing {
            VStack(alignment: .leading, spacing: 8) {
                TextField("Subject", text: $draft, axis: .vertical)
                    .font(ODFont.sans(20, weight: .semibold))
                    .foregroundStyle(Theme.fg)
                    .focused($focused)
                    .submitLabel(.done)
                    .onSubmit(save)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 8)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.lineStrong, lineWidth: 1))
                HStack(spacing: 8) {
                    Spacer()
                    Button("Cancel") { editing = false }.buttonStyle(ChipButtonStyle())
                    Button("Save", action: save).buttonStyle(ChipButtonStyle(filled: true))
                        .disabled(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
                }
            }
            .onAppear {
                draft = ticket.subject
                focused = true
            }
        } else {
            Text(ticket.displaySubject)
                .font(ODFont.sans(20, weight: .semibold))
                .headingTracking(20)
                .foregroundStyle(Theme.fg)
                .fixedSize(horizontal: false, vertical: true)
                .textSelection(.enabled)
                .onTapGesture {
                    guard canWrite else { return }
                    Haptics.tap()
                    editing = true
                }
                .accessibilityAddTraits(canWrite ? .isButton : [])
                .accessibilityHint(canWrite ? "Edits the subject" : "")
        }
    }

    private func save() {
        let text = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        editing = false
        guard !text.isEmpty, text != ticket.subject else { return }
        onRename(text)
    }

    private var chips: some View {
        FlowLayout(spacing: 6) {
            PropertyChip(tone: ticket.status.tone, action: canWrite ? { onOpen(.status) } : nil) {
                StatusIcon(status: ticket.status, size: 12)
                Text(ticket.status.title)
            }
            PropertyChip(tone: ticket.priority == .urgent || ticket.priority == .high ? ticket.priority.tone : nil, action: canWrite ? { onOpen(.priority) } : nil) {
                PriorityIcon(priority: ticket.priority, size: 12)
                Text(ticket.priority.title)
            }
            if let severity = ticket.severity {
                PropertyChip(tone: severity.tone, action: canWrite ? { onOpen(.severity) } : nil) {
                    Text(severity.title)
                }
            }
            PropertyChip(action: canWrite ? { onOpen(.assignee) } : nil) {
                if let assignee = ticket.assignee {
                    Avatar(name: assignee.displayName, url: assignee.avatarURL, seed: assignee.id, size: 16)
                    Text(assignee.id == me ? "You" : assignee.displayName)
                } else {
                    Image(systemName: "person.crop.circle.badge.questionmark").font(.system(size: 11.5))
                    Text("Unassigned")
                }
            }
            PropertyChip(action: canWrite ? { onOpen(.team) } : nil) {
                Image(systemName: "person.2").font(.system(size: 10.5, weight: .medium))
                Text(teamName ?? "No team")
            }
            if let sla = SLAState.of(ticket) {
                Button { onOpen(.details) } label: { SLAPill(state: sla) }
                    .buttonStyle(TapScaleStyle())
                    .frame(height: 26)
            }
            ForEach(ticket.tags) { tag in
                Button { if canWrite { onOpen(.labels) } } label: { LabelChip(name: tag.name, color: tag.color) }
                    .buttonStyle(TapScaleStyle())
                    .frame(height: 26)
                    .disabled(!canWrite)
            }
            if canWrite {
                Button { onOpen(.labels) } label: {
                    Image(systemName: "plus").font(.system(size: 10, weight: .bold))
                        .foregroundStyle(Theme.fg3)
                        .frame(width: 26, height: 21)
                        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous).strokeBorder(Theme.lineStrong, style: StrokeStyle(lineWidth: 1, dash: [3, 2])))
                }
                .buttonStyle(TapScaleStyle())
                .frame(height: 26)
                .accessibilityLabel("Add a label")
            }
        }
    }
}

/// A tappable property of the ticket: a soft tinted pill when it carries meaning, else a white one.
struct PropertyChip<Content: View>: View {
    var tone: Tone?
    var action: (() -> Void)?
    @ViewBuilder var content: () -> Content

    var body: some View {
        let label = HStack(spacing: 5) { content() }
            .font(ODFont.sans(12.5, weight: .medium))
            .foregroundStyle(tone?.text ?? Theme.fg2)
            .lineLimit(1)
            .padding(.horizontal, 8)
            .frame(height: 26)
            .background(tone?.soft ?? Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous).strokeBorder(tone?.line ?? Theme.line, lineWidth: 1))
        if let action {
            Button {
                Haptics.tap()
                action()
            } label: { label }
            .buttonStyle(TapScaleStyle(scale: 0.95))
        } else {
            label
        }
    }
}

/// A line above the thread that says something about the whole ticket (snoozed, spam,
/// handed off, read only), with one action.
struct TicketBanner: View {
    let symbol: String
    let text: String
    var tone: Tone = .gray
    var actionTitle: String?
    var action: (() -> Void)?

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 9) {
            Image(systemName: symbol)
                .font(.system(size: 12.5, weight: .semibold))
                .foregroundStyle(tone == .gray ? Theme.fg3 : tone.text)
            Text(text)
                .font(ODFont.sans(13.5))
                .foregroundStyle(Theme.fg2)
                .fixedSize(horizontal: false, vertical: true)
            Spacer(minLength: 6)
            if let actionTitle, let action {
                Button(actionTitle) {
                    Haptics.tap()
                    action()
                }
                .font(ODFont.sans(13, weight: .semibold))
                .foregroundStyle(Theme.fg)
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
        .background(tone == .gray ? Theme.surface : tone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(tone == .gray ? Theme.line : tone.line, lineWidth: 1))
    }
}

/// The day a run of messages happened, between them.
struct TicketDaySeparator: View {
    let date: Date

    var body: some View {
        HStack(spacing: 10) {
            Rectangle().fill(Theme.line).frame(height: 1)
            Text(Format.dayLabel(date))
                .font(ODFont.sans(11.5, weight: .medium))
                .foregroundStyle(Theme.fg3)
                .fixedSize()
            Rectangle().fill(Theme.line).frame(height: 1)
        }
        .padding(.vertical, 6)
        .accessibilityAddTraits(.isHeader)
    }
}
