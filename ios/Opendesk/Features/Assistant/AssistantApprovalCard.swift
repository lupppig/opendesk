import SwiftUI

/// Exactly what a change will do before it runs (web `ApprovalCard`): the
/// title, each field from now to after, the reply or note it writes, the
/// tickets it touches, and how long it waits. Nothing happens until Approve.
struct AssistantApprovalCard: View {
    let approval: AssistantApproval
    let busy: Bool
    let onRef: (AssistantRef) -> Void
    let onDecide: (Bool) -> Void

    @State private var expired = false

    private var preview: ApprovalPreview { approval.preview }
    private var targets: [AssistantRef] { preview.targets ?? [] }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
                .padding(.horizontal, 14)
                .padding(.top, 14)
                .padding(.bottom, 12)

            if let changes = preview.changes, !changes.isEmpty {
                VStack(spacing: 0) {
                    ForEach(Array(changes.enumerated()), id: \.offset) { index, change in
                        if index > 0 { Hairline() }
                        AssistantChangeRow(change: change)
                    }
                }
                .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                .padding(.horizontal, 14)
                .padding(.bottom, 12)
                .accessibilityElement(children: .contain)
                .accessibilityLabel("Changes")
            }

            if let body = preview.body, !body.isEmpty {
                bodyPreview(body)
                    .padding(.horizontal, 14)
                    .padding(.bottom, 12)
            }

            if targets.count > 1 {
                VStack(alignment: .leading, spacing: 6) {
                    AssistantFlowLayout(spacing: 5) {
                        ForEach(Array(targets.prefix(6).enumerated()), id: \.offset) { _, ref in
                            AssistantRefChip(ref: ref, onTap: onRef)
                        }
                        if targets.count > 6 {
                            Text("and \(targets.count - 6) more")
                                .font(ODFont.sans(12))
                                .foregroundStyle(Theme.fg4)
                                .frame(height: 26)
                        }
                    }
                    if let total = preview.total, total > targets.count {
                        Text("\(targets.count) of \(total) matching")
                            .font(ODFont.sans(12))
                            .foregroundStyle(Theme.fg4)
                    }
                }
                .padding(.horizontal, 14)
                .padding(.bottom, 12)
            }

            Hairline()
            footer
                .padding(.horizontal, 14)
                .padding(.vertical, 12)
                .background(Theme.sunken.opacity(0.6))
        }
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card + 2, style: .continuous))
        .clipShape(RoundedRectangle(cornerRadius: Theme.Radius.card + 2, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card + 2, style: .continuous).strokeBorder(Tone.amber.solid.opacity(0.32), lineWidth: 1))
        .shadow(color: Tone.amber.solid.opacity(0.10), radius: 18, y: 8)
        .accessibilityElement(children: .contain)
        .accessibilityLabel("Approve this change?")
    }

    private var header: some View {
        HStack(alignment: .top, spacing: 11) {
            OllieMark(size: 26, state: expired ? .muted : .attention)
            VStack(alignment: .leading, spacing: 3) {
                Text(preview.title)
                    .font(ODFont.sans(15.5, weight: .semibold))
                    .tracking(-0.2)
                    .foregroundStyle(Theme.fg)
                    .fixedSize(horizontal: false, vertical: true)
                if let summary = preview.summary, !summary.isEmpty {
                    Text(summary)
                        .font(ODFont.sans(13))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(2)
                }
                if preview.external == true || preview.system != nil {
                    HStack(spacing: 5) {
                        if preview.external == true {
                            AssistantBadge(text: "Reaches the customer", symbol: "paperplane.fill", tone: .amber)
                        }
                        if let system = preview.system {
                            AssistantBadge(text: "Runs in \(system)", symbol: "link", tone: .violet)
                        }
                    }
                    .padding(.top, 5)
                }
            }
            Spacer(minLength: 0)
        }
    }

    private func bodyPreview(_ body: String) -> some View {
        let external = preview.external == true
        return VStack(alignment: .leading, spacing: 7) {
            HStack(spacing: 5) {
                Image(systemName: external ? "arrowshape.turn.up.left.fill" : "note.text")
                    .font(.system(size: 10.5, weight: .semibold))
                Text(external ? "Reply" : approval.tool == "add_note" ? "Internal note" : "Preview")
                    .font(ODFont.sans(11.5, weight: .medium))
            }
            .foregroundStyle(external ? Tone.green.text : Theme.fg3)
            ScrollView {
                MarkdownText(text: body, size: 14)
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
            .scrollBounceBehavior(.basedOnSize)
            .frame(maxHeight: 230)
            .fixedSize(horizontal: false, vertical: true)
        }
        .padding(12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(external ? Tone.green.soft : Theme.note, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(external ? Tone.green.line : Theme.noteLine, lineWidth: 1))
    }

    private var footer: some View {
        HStack(spacing: 8) {
            AssistantExpiry(expiresAt: approval.expiresAt, expired: $expired)
            Spacer(minLength: 4)
            Button {
                Haptics.selection()
                onDecide(false)
            } label: {
                Label("Deny", systemImage: "xmark")
                    .labelStyle(AssistantCompactLabel())
            }
            .buttonStyle(OutlineButtonStyle(fullWidth: false, height: 38))
            .disabled(busy)
            Button {
                Haptics.tap()
                onDecide(true)
            } label: {
                ZStack {
                    Label("Approve", systemImage: "checkmark")
                        .labelStyle(AssistantCompactLabel())
                        .opacity(busy ? 0 : 1)
                    if busy { ProgressView().controlSize(.small).tint(Theme.inverseFg) }
                }
            }
            .buttonStyle(InkButtonStyle(fullWidth: false, height: 38))
            .disabled(busy || expired)
            .keyboardShortcut(.return, modifiers: .command)
        }
    }
}

/// "Now → After" for one field; long values (a signature, a template) as blocks.
struct AssistantChangeRow: View {
    let change: PreviewChange

    private var from: String { change.from ?? "" }
    private var to: String { change.to ?? "" }
    private var long: Bool { from.count > 60 || to.count > 60 || from.contains("\n") || to.contains("\n") }

    var body: some View {
        Group {
            if long {
                VStack(alignment: .leading, spacing: 6) {
                    fieldLabel
                    if !from.isEmpty { block(from, tone: .red, struck: true) }
                    block(to, tone: .green, struck: false)
                }
            } else {
                HStack(alignment: .firstTextBaseline, spacing: 10) {
                    fieldLabel.frame(width: 76, alignment: .leading)
                    AssistantFlowLayout(spacing: 5) {
                        if !from.isEmpty {
                            pill(from, tone: .red, struck: true)
                            Image(systemName: "arrow.right")
                                .font(.system(size: 10, weight: .semibold))
                                .foregroundStyle(Theme.fg4)
                                .frame(height: 22)
                        }
                        pill(to.isEmpty ? "None" : to, tone: .green, struck: false)
                    }
                }
            }
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(change.field): \(from.isEmpty ? "" : "from \(from) ")to \(to.isEmpty ? "none" : to)")
    }

    private var fieldLabel: some View {
        Text(change.field)
            .font(ODFont.sans(12.5))
            .foregroundStyle(Theme.fg3)
            .lineLimit(1)
    }

    private func pill(_ text: String, tone: Tone, struck: Bool) -> some View {
        Text(text)
            .font(ODFont.sans(13, weight: struck ? .regular : .medium))
            .strikethrough(struck, color: tone.text.opacity(0.6))
            .foregroundStyle(tone.text)
            .lineLimit(2)
            .padding(.horizontal, 6)
            .frame(minHeight: 22)
            .background(tone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
    }

    private func block(_ text: String, tone: Tone, struck: Bool) -> some View {
        ScrollView {
            Text(text)
                .font(ODFont.mono(11.5))
                .strikethrough(struck, color: tone.text.opacity(0.5))
                .foregroundStyle(tone.text)
                .frame(maxWidth: .infinity, alignment: .leading)
                .padding(8)
        }
        .frame(maxHeight: 120)
        .fixedSize(horizontal: false, vertical: true)
        .background(tone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous))
    }
}

/// How long the proposal waits, ticking in this small view alone.
struct AssistantExpiry: View {
    let expiresAt: Date?
    @Binding var expired: Bool

    var body: some View {
        if let expiresAt {
            TimelineView(.periodic(from: .now, by: 1)) { context in
                let left = expiresAt.timeIntervalSince(AppClock.now(at: context.date))
                HStack(spacing: 5) {
                    Image(systemName: left > 0 ? "clock" : "clock.badge.xmark")
                        .font(.system(size: 11, weight: .medium))
                    Text(left > 0 ? Self.format(left) : "Expired")
                        .font(ODFont.mono(12, weight: .medium))
                        .monospacedDigit()
                        .contentTransition(.numericText(countsDown: true))
                }
                .foregroundStyle(left > 0 && left < 120 ? Tone.amber.text : Theme.fg4)
                .onChange(of: left <= 0, initial: true) { _, now in expired = now }
                .accessibilityElement(children: .ignore)
                .accessibilityLabel(left > 0 ? "Waits \(Int(left / 60) + 1) more minutes" : "Expired")
            }
        } else {
            Text("Waiting for you")
                .font(ODFont.sans(12))
                .foregroundStyle(Theme.fg4)
        }
    }

    static func format(_ seconds: TimeInterval) -> String {
        let total = Int(seconds.rounded(.up))
        return String(format: "%d:%02d", total / 60, total % 60)
    }
}

/// What became of a proposal, where it was: approved, declined, or on its way.
struct AssistantDecisionRow: View {
    let decision: AssistantStore.Decision
    let pending: Bool

    var body: some View {
        HStack(spacing: 10) {
            ZStack {
                if pending {
                    ProgressView().controlSize(.mini)
                } else {
                    Image(systemName: decision.approved ? "checkmark" : "xmark")
                        .font(.system(size: 10, weight: .heavy))
                        .foregroundStyle(decision.approved ? .white : Theme.fg3)
                        .frame(width: 20, height: 20)
                        .background(decision.approved ? Tone.green.solid : Theme.active, in: Circle())
                        .transition(.scale(scale: 0.5).combined(with: .opacity))
                }
            }
            .frame(width: 20, height: 20)
            VStack(alignment: .leading, spacing: 1) {
                Text(pending ? (decision.approved ? "Approving" : "Declining") : (decision.approved ? "You approved this change" : "You declined this change"))
                    .font(ODFont.sans(12.5, weight: .medium))
                    .foregroundStyle(decision.approved && !pending ? Tone.green.text : Theme.fg2)
                Text(decision.approval.preview.title)
                    .font(ODFont.sans(12.5))
                    .foregroundStyle(Theme.fg3)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 9)
        .background(decision.approved ? Tone.green.soft : Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(decision.approved ? Tone.green.line : Theme.line, lineWidth: 1))
        .motion(Motion.snappy, value: pending)
        .accessibilityElement(children: .combine)
    }
}

/// A small tinted pill with a symbol.
struct AssistantBadge: View {
    let text: String
    var symbol: String?
    var tone: Tone = .violet

    var body: some View {
        HStack(spacing: 4) {
            if let symbol { Image(systemName: symbol).font(.system(size: 9.5, weight: .semibold)) }
            Text(text).font(ODFont.sans(11.5, weight: .medium)).lineLimit(1)
        }
        .foregroundStyle(tone.text)
        .padding(.horizontal, 7)
        .frame(height: 21)
        .background(tone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous).strokeBorder(tone.line, lineWidth: 1))
    }
}

/// A symbol and a title, tight, for the card's buttons.
struct AssistantCompactLabel: LabelStyle {
    var size: CGFloat = 14.5

    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 6) {
            configuration.icon.font(.system(size: size - 2, weight: .bold))
            configuration.title.font(ODFont.sans(size, weight: .semibold))
        }
    }
}
