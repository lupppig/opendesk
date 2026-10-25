import SwiftUI

/// Each team's created, solved, open and first response; a tap narrows the report to it.
struct ReportTeamTable: View {
    let rows: [ReportTeamRow]
    let name: (String?) -> String
    let tone: (String?) -> Tone
    let onSelect: (String?) -> Void

    var body: some View {
        VStack(spacing: 0) {
            header
            ForEach(Array(rows.enumerated()), id: \.offset) { index, row in
                Button { onSelect(row.teamID) } label: {
                    HStack(spacing: 8) {
                        HStack(spacing: 7) {
                            RoundedRectangle(cornerRadius: 2).fill(tone(row.teamID).solid).frame(width: 8, height: 8)
                            Text(name(row.teamID))
                                .font(ODFont.sans(14, weight: .medium))
                                .foregroundStyle(row.teamID == nil ? Theme.fg2 : Theme.fg)
                                .lineLimit(1)
                        }
                        .frame(maxWidth: .infinity, alignment: .leading)
                        cell(row.created.formatted())
                        cell(row.solved.formatted())
                        cell(row.openNow.formatted())
                        cell(row.firstResponseP50.map(Format.minutes) ?? "None", width: 62, dim: row.firstResponseP50 == nil)
                    }
                    .padding(.vertical, 11)
                    .contentShape(Rectangle())
                }
                .buttonStyle(RowPressStyle())
                if index < rows.count - 1 { Hairline() }
            }
        }
    }

    private var header: some View {
        HStack(spacing: 8) {
            Text("Team").frame(maxWidth: .infinity, alignment: .leading)
            Text("Created").frame(width: 50, alignment: .trailing)
            Text("Solved").frame(width: 50, alignment: .trailing)
            Text("Open").frame(width: 50, alignment: .trailing)
            Text("Reply").frame(width: 62, alignment: .trailing)
        }
        .font(ODFont.sans(11.5, weight: .medium))
        .foregroundStyle(Theme.fg3)
        .padding(.bottom, 6)
        .overlay(alignment: .bottom) { Hairline() }
    }

    private func cell(_ text: String, width: CGFloat = 50, dim: Bool = false) -> some View {
        Text(text)
            .font(ODFont.mono(12.5))
            .foregroundStyle(dim ? Theme.fg4 : Theme.fg)
            .lineLimit(1)
            .minimumScaleFactor(0.8)
            .frame(width: width, alignment: .trailing)
    }
}

/// Who solved what in the period and how fast they first replied.
struct ReportAgentTable: View {
    let rows: [ReportAgentRow]
    var shown = true
    @State private var expanded = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var peak: Double { Double(max(rows.map(\.solved).max() ?? 0, 1)) }
    private var fastest: String? {
        rows.filter { $0.firstResponseP50 != nil && $0.solved > 0 }.min { ($0.firstResponseP50 ?? 0) < ($1.firstResponseP50 ?? 0) }?.id
    }
    private var visible: [ReportAgentRow] { expanded ? rows : Array(rows.prefix(6)) }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 8) {
                Text("Agent").frame(maxWidth: .infinity, alignment: .leading)
                Text("Solved").frame(width: 92, alignment: .leading)
                Text("Reply").frame(width: 62, alignment: .trailing)
            }
            .font(ODFont.sans(11.5, weight: .medium))
            .foregroundStyle(Theme.fg3)
            .padding(.bottom, 6)
            .overlay(alignment: .bottom) { Hairline() }

            ForEach(Array(visible.enumerated()), id: \.element.id) { index, row in
                HStack(spacing: 8) {
                    HStack(spacing: 9) {
                        Avatar(name: row.user.name, url: row.user.avatarURL, seed: row.user.id, size: 26)
                        Text(row.user.name)
                            .font(ODFont.sans(14, weight: .medium))
                            .foregroundStyle(Theme.fg)
                            .lineLimit(1)
                    }
                    .frame(maxWidth: .infinity, alignment: .leading)
                    HStack(spacing: 6) {
                        GeometryReader { proxy in
                            ZStack(alignment: .leading) {
                                Capsule().fill(Theme.hover)
                                Capsule().fill(ReportsPalette.solved.solid.opacity(0.85))
                                    .frame(width: proxy.size.width * (shown ? Double(row.solved) / peak : 0))
                                    .animation(reduceMotion ? nil : .spring(response: 0.6, dampingFraction: 0.86).delay(Double(index) * 0.04), value: shown)
                            }
                        }
                        .frame(height: 6)
                        Text(row.solved.formatted())
                            .font(ODFont.mono(12.5))
                            .foregroundStyle(Theme.fg)
                            .frame(width: 30, alignment: .trailing)
                    }
                    .frame(width: 92)
                    HStack(spacing: 3) {
                        if row.id == fastest {
                            Image(systemName: "bolt.fill").font(.system(size: 8.5)).foregroundStyle(Tone.violet.solid)
                        }
                        Text(row.firstResponseP50.map(Format.minutes) ?? "None")
                            .font(ODFont.mono(12.5))
                            .foregroundStyle(row.firstResponseP50 == nil ? Theme.fg4 : Theme.fg)
                            .lineLimit(1)
                    }
                    .frame(width: 62, alignment: .trailing)
                }
                .padding(.vertical, 9)
                .accessibilityElement(children: .combine)
                if index < visible.count - 1 { Hairline(inset: 35) }
            }
            if rows.count > 6 {
                Button {
                    Haptics.tap()
                    withAnimation(reduceMotion ? nil : Motion.snappy) { expanded.toggle() }
                } label: {
                    Text(expanded ? "Show fewer" : "Show all \(rows.count)")
                        .font(ODFont.sans(13, weight: .medium))
                        .foregroundStyle(Theme.fg2)
                        .frame(maxWidth: .infinity)
                        .frame(height: 36)
                }
                .buttonStyle(RowPressStyle())
                .padding(.top, 4)
            }
        }
    }
}
