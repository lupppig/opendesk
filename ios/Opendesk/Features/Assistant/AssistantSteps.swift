import SwiftUI

/// Tool activity rows ("Searching tickets…" turning into a check), with what
/// the step found as tappable chips under it (web `Steps.tsx`).
struct AssistantStepRow: View {
    let step: AssistantStep
    let onRef: (AssistantRef) -> Void

    @Environment(AppEnvironment.self) private var env

    private var working: Bool { step.status == "running" || step.status == "approved" }

    private var refs: [AssistantRef] {
        (step.refs ?? []).filter { $0.kind != "member" || step.refs?.count == 1 }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 7) {
            if let peer = step.peer {
                peerLine(peer)
            } else {
                toolLine
            }
            if step.status == "done", !refs.isEmpty {
                AssistantFlowLayout(spacing: 5) {
                    ForEach(Array(refs.prefix(4).enumerated()), id: \.offset) { _, ref in
                        AssistantRefChip(ref: ref, onTap: onRef)
                    }
                    if refs.count > 4 {
                        Text("+\(refs.count - 4)")
                            .font(ODFont.mono(11.5, weight: .medium))
                            .foregroundStyle(Theme.fg3)
                            .padding(.horizontal, 7)
                            .frame(height: 24)
                            .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous))
                    }
                }
                .padding(.leading, 26)
            }
        }
        .accessibilityElement(children: .contain)
    }

    private var toolLine: some View {
        HStack(alignment: .center, spacing: 8) {
            AssistantStepIcon(status: step.status)
            HStack(spacing: 5) {
                Text((step.label ?? step.tool ?? "Working") + (working ? "…" : ""))
                    .foregroundStyle(working ? Theme.fg2 : Theme.fg3)
                    .assistantShimmer(working)
                    .lineLimit(1)
                    .layoutPriority(1)
                if let summary = step.summary, !summary.isEmpty, !working {
                    Text("· " + summary)
                        .foregroundStyle(step.status == "error" ? Tone.red.text : Theme.fg4)
                        .lineLimit(1)
                }
                if step.status == "denied" || step.status == "expired" {
                    Text(step.status == "denied" ? "· Declined" : "· Skipped")
                        .foregroundStyle(Theme.fg4)
                        .lineLimit(1)
                }
            }
            .font(ODFont.sans(13.5))
            Spacer(minLength: 0)
        }
        .frame(minHeight: 22)
        .motion(Motion.snappy, value: step.status)
    }

    /// `ask_teammate`: whose Ollie answered, and how.
    private func peerLine(_ peer: AssistantStepPeer) -> some View {
        let name = peer.user.name.isEmpty ? (step.label ?? "a teammate") : peer.user.name
        let first = name.split(separator: " ").first.map(String.init) ?? name
        let assistant = env.directory.ai.assistantName ?? "Ollie"
        let outcome: String = switch peer.result {
        case "answered": (peer.sources ?? 0) == 0 ? "answered" : "answered from \(Format.plural(peer.sources ?? 0, "conversation"))"
        case "declined": "declined"
        case "not_shared": "not shared with you"
        default: "could not answer"
        }
        return HStack(spacing: 8) {
            AssistantStepIcon(status: step.status)
            Avatar(name: name, url: peer.user.avatarURL, seed: peer.user.id, size: 18)
            Text(working ? "Asking \(first)'s \(assistant)…" : "Asked \(first)'s \(assistant)")
                .foregroundStyle(working ? Theme.fg2 : Theme.fg3)
                .assistantShimmer(working)
                .lineLimit(1)
            if !working {
                Text("· " + (step.status == "error" ? (step.summary ?? outcome) : outcome))
                    .foregroundStyle(peer.result == "answered" ? Theme.fg4 : Tone.amber.text)
                    .lineLimit(1)
            }
            Spacer(minLength: 0)
        }
        .font(ODFont.sans(13.5))
        .frame(minHeight: 22)
    }
}

/// The small mark before a step: Ollie at work, a check, a cross, or why it didn't run.
struct AssistantStepIcon: View {
    let status: String

    var body: some View {
        ZStack {
            switch status {
            case "running", "approved":
                OllieMark(size: 16, state: .thinking, eyes: false)
            case "awaiting_approval":
                OllieMark(size: 16, state: .attention, eyes: false)
            case "done":
                badge("checkmark", tone: .green)
            case "error":
                badge("xmark", tone: .red)
            case "denied":
                Image(systemName: "nosign").font(.system(size: 12.5, weight: .medium)).foregroundStyle(Theme.fg4)
            default:
                Image(systemName: "clock").font(.system(size: 12.5, weight: .medium)).foregroundStyle(Theme.fg4)
            }
        }
        .frame(width: 18, height: 18)
        .transition(.scale(scale: 0.6).combined(with: .opacity))
        .id(status)
        .accessibilityLabel(Self.label(status))
    }

    private func badge(_ symbol: String, tone: Tone) -> some View {
        Image(systemName: symbol)
            .font(.system(size: 8.5, weight: .heavy))
            .foregroundStyle(.white)
            .frame(width: 16, height: 16)
            .background(tone.solid, in: RoundedRectangle(cornerRadius: 5, style: .continuous))
    }

    static func label(_ status: String) -> String {
        switch status {
        case "running": "Working"
        case "done": "Done"
        case "error": "Failed"
        case "awaiting_approval": "Needs approval"
        case "approved": "Approved"
        case "denied": "Declined"
        default: "Skipped"
        }
    }
}

/// One thing Ollie found or will change: a ticket, a contact, an article, a view, a person or a link.
struct AssistantRefChip: View {
    let ref: AssistantRef
    let onTap: (AssistantRef) -> Void

    private var tappable: Bool {
        switch ref.kind {
        case "member": false
        case "external": AssistantLinks.external(ref) != nil
        default: true
        }
    }

    private var symbol: String? {
        switch ref.kind {
        case "contact": "person"
        case "article": "doc.text"
        case "view": "line.3.horizontal.decrease"
        case "automation": "bolt"
        default: nil
        }
    }

    private var label: String {
        if !ref.label.isEmpty { return ref.label }
        if ref.kind == "external", let url = URL(string: ref.id) { return url.host() ?? ref.id }
        return ref.kind.capitalized
    }

    var body: some View {
        Button {
            Haptics.selection()
            onTap(ref)
        } label: {
            HStack(spacing: 5) {
                if ref.kind == "ticket", let number = ref.number {
                    Text(verbatim: "#\(number)")
                        .font(ODFont.mono(11.5, weight: .medium))
                        .foregroundStyle(Tone.violet.text)
                } else if ref.kind == "member" {
                    Avatar(name: label, seed: ref.id, size: 15)
                } else if let symbol {
                    Image(systemName: symbol).font(.system(size: 10.5, weight: .medium)).foregroundStyle(Theme.fg3)
                }
                Text(label)
                    .font(ODFont.sans(12.5))
                    .foregroundStyle(Theme.fg2)
                    .lineLimit(1)
                if ref.kind == "external" {
                    Image(systemName: "arrow.up.right").font(.system(size: 9.5, weight: .semibold)).foregroundStyle(Theme.fg4)
                }
            }
            .padding(.horizontal, 8)
            .frame(maxWidth: 230, minHeight: 26, alignment: .leading)
            .fixedSize(horizontal: true, vertical: false)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            .contentShape(RoundedRectangle(cornerRadius: Theme.Radius.chip, style: .continuous))
        }
        .buttonStyle(TapScaleStyle(scale: 0.96))
        .disabled(!tappable)
        .accessibilityLabel(ref.kind == "ticket" && ref.number != nil ? "Ticket \(ref.number ?? 0), \(label)" : label)
        .accessibilityHint(tappable ? "Opens it" : "")
    }
}

/// Wraps chips onto as many lines as they need.
struct AssistantFlowLayout: Layout {
    var spacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let rows = arrange(width: proposal.width ?? .infinity, subviews: subviews)
        let height = rows.reduce(0) { $0 + $1.height } + spacing * CGFloat(max(rows.count - 1, 0))
        let width = rows.map(\.width).max() ?? 0
        return CGSize(width: proposal.width ?? width, height: height)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var y = bounds.minY
        for row in arrange(width: bounds.width, subviews: subviews) {
            var x = bounds.minX
            for index in row.indices {
                let size = subviews[index].sizeThatFits(.unspecified)
                subviews[index].place(at: CGPoint(x: x, y: y + (row.height - size.height) / 2), proposal: ProposedViewSize(width: min(size.width, bounds.width), height: size.height))
                x += min(size.width, bounds.width) + spacing
            }
            y += row.height + spacing
        }
    }

    private struct Row {
        var indices: [Int] = []
        var width: CGFloat = 0
        var height: CGFloat = 0
    }

    private func arrange(width: CGFloat, subviews: Subviews) -> [Row] {
        var rows: [Row] = []
        var row = Row()
        for index in subviews.indices {
            let size = subviews[index].sizeThatFits(.unspecified)
            let itemWidth = min(size.width, width)
            if !row.indices.isEmpty, row.width + spacing + itemWidth > width {
                rows.append(row)
                row = Row()
            }
            row.width += (row.indices.isEmpty ? 0 : spacing) + itemWidth
            row.height = max(row.height, size.height)
            row.indices.append(index)
        }
        if !row.indices.isEmpty { rows.append(row) }
        return rows
    }
}

/// A soft light passing over text while Ollie works on it. Still under Reduce Motion.
struct AssistantShimmer: ViewModifier {
    let active: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func body(content: Content) -> some View {
        if active, !reduceMotion {
            content.overlay {
                TimelineView(.animation(minimumInterval: 1 / 30)) { context in
                    let phase = context.date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 1.6) / 1.6
                    GeometryReader { proxy in
                        LinearGradient(colors: [.clear, Theme.surface.opacity(0.75), .clear], startPoint: .leading, endPoint: .trailing)
                            .frame(width: proxy.size.width * 0.45)
                            .offset(x: (proxy.size.width * 1.45) * phase - proxy.size.width * 0.45)
                    }
                }
                .mask(content)
                .allowsHitTesting(false)
            }
        } else {
            content
        }
    }
}

extension View {
    func assistantShimmer(_ active: Bool) -> some View { modifier(AssistantShimmer(active: active)) }
}
