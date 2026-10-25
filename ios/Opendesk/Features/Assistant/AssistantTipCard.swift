import SwiftUI

/// A gentle nudge under the header blob when something in the queue needs a
/// look ("3 tickets breach SLA within the hour"), with its question one tap away.
struct AssistantTipCard: View {
    let tip: AssistantTip
    let assistantName: String
    let onAsk: () -> Void
    let onLater: () -> Void
    let onPause: () -> Void

    private var tone: Tone {
        switch tip.severity {
        case "high": .red
        case "medium": .amber
        default: .violet
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 11) {
            HStack(alignment: .top, spacing: 10) {
                Circle()
                    .fill(tone.solid)
                    .frame(width: 7, height: 7)
                    .padding(.top, 6)
                Text(tip.text)
                    .font(ODFont.sans(14.5))
                    .foregroundStyle(Theme.fg)
                    .fixedSize(horizontal: false, vertical: true)
                Spacer(minLength: 0)
                Button(action: onLater) {
                    Image(systemName: "xmark")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(Theme.fg4)
                        .frame(width: 26, height: 26)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .padding(.top, -3)
                .padding(.trailing, -6)
                .accessibilityLabel("Dismiss")
            }
            HStack(spacing: 6) {
                Button(action: onAsk) {
                    Text("Ask \(assistantName)")
                }
                .buttonStyle(ChipButtonStyle(filled: true))
                Button("Not now", action: onLater)
                    .buttonStyle(ChipButtonStyle())
                Spacer(minLength: 4)
                Button(action: onPause) {
                    Text("Pause for a week")
                        .font(ODFont.sans(12))
                        .foregroundStyle(Theme.fg4)
                        .underline(color: Theme.fg4.opacity(0.4))
                }
                .buttonStyle(.plain)
            }
            .padding(.leading, 17)
        }
        .padding(13)
        .background(Theme.raised, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        .overlay(alignment: .top) {
            // The tail points up at the blob in the header.
            AssistantTipTail()
                .fill(Theme.raised)
                .overlay(AssistantTipTail().stroke(Theme.line, lineWidth: 1).mask(Rectangle().padding(.bottom, 1)))
                .frame(width: 16, height: 7)
                .offset(y: -6.5)
        }
        .shadow(color: .black.opacity(0.06), radius: 14, y: 6)
        .accessibilityElement(children: .contain)
    }
}

struct AssistantTipTail: Shape {
    func path(in rect: CGRect) -> Path {
        var path = Path()
        path.move(to: CGPoint(x: rect.minX, y: rect.maxY))
        path.addQuadCurve(to: CGPoint(x: rect.midX, y: rect.minY), control: CGPoint(x: rect.midX - rect.width * 0.18, y: rect.maxY))
        path.addQuadCurve(to: CGPoint(x: rect.maxX, y: rect.maxY), control: CGPoint(x: rect.midX + rect.width * 0.18, y: rect.maxY))
        return path
    }
}
