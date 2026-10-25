import Charts
import SwiftUI

// MARK: - Card

/// A report section: a white card on the chrome with a title, an optional
/// line under it and an optional trailing figure.
struct ReportCard<Content: View, Trailing: View>: View {
    let title: String
    var caption: String?
    @ViewBuilder var trailing: () -> Trailing
    @ViewBuilder var content: () -> Content

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .firstTextBaseline, spacing: 8) {
                VStack(alignment: .leading, spacing: 2) {
                    Text(title)
                        .font(ODFont.sans(15, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                    if let caption {
                        Text(caption)
                            .font(ODFont.sans(12.5))
                            .foregroundStyle(Theme.fg3)
                    }
                }
                Spacer(minLength: 8)
                trailing()
            }
            content()
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}

extension ReportCard where Trailing == EmptyView {
    init(title: String, caption: String? = nil, @ViewBuilder content: @escaping () -> Content) {
        self.init(title: title, caption: caption, trailing: { EmptyView() }, content: content)
    }
}

/// Sections rise in one beat apart the first time the report arrives.
struct ReportReveal: ViewModifier {
    let shown: Bool
    let index: Int
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    func body(content: Content) -> some View {
        content
            .opacity(shown || reduceMotion ? 1 : 0)
            .offset(y: shown || reduceMotion ? 0 : 14)
            .animation(reduceMotion ? nil : .spring(response: 0.55, dampingFraction: 0.88).delay(0.04 + Double(min(index, 8)) * 0.05), value: shown)
    }
}

extension View {
    func reportReveal(_ shown: Bool, _ index: Int) -> some View {
        modifier(ReportReveal(shown: shown, index: index))
    }
}

// MARK: - KPI tile

struct ReportKPITile: View {
    let kpi: ReportKPI

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 6) {
                Image(systemName: kpi.symbol)
                    .font(.system(size: 10.5, weight: .semibold))
                    .foregroundStyle(kpi.tone.text)
                    .frame(width: 20, height: 20)
                    .background(kpi.tone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
                Text(kpi.title)
                    .font(ODFont.sans(12.5, weight: .medium))
                    .foregroundStyle(Theme.fg2)
                    .lineLimit(1)
                Spacer(minLength: 0)
            }
            Spacer(minLength: 10)
            HStack(alignment: .lastTextBaseline, spacing: 6) {
                Text(kpi.value)
                    .font(ODFont.mono(23, weight: .medium, relativeTo: .title2))
                    .foregroundStyle(kpi.value == "None" ? Theme.fg4 : Theme.fg)
                    .lineLimit(1)
                    .minimumScaleFactor(0.7)
                    .contentTransition(.numericText())
                Spacer(minLength: 0)
                if kpi.series.count > 2 {
                    ReportSparkline(values: kpi.series, tone: kpi.tone)
                        .frame(width: 54, height: 22)
                }
            }
            HStack(spacing: 5) {
                if let text = kpi.changeText {
                    HStack(spacing: 2) {
                        if let symbol = kpi.changeSymbol {
                            Image(systemName: symbol).font(.system(size: 8.5, weight: .bold))
                        }
                        Text(text).font(ODFont.mono(11, weight: .medium))
                    }
                    .foregroundStyle(kpi.changeTone == .gray ? Theme.fg3 : kpi.changeTone.text)
                    .padding(.horizontal, 5)
                    .frame(height: 18)
                    .background(kpi.changeTone == .gray ? Theme.hover : kpi.changeTone.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.pill, style: .continuous))
                    Text("vs before")
                        .font(ODFont.sans(11.5))
                        .foregroundStyle(Theme.fg4)
                        .lineLimit(1)
                } else if let note = kpi.note {
                    Text(note)
                        .font(ODFont.sans(11.5))
                        .foregroundStyle(Theme.fg4)
                        .lineLimit(1)
                } else {
                    Text(" ").font(ODFont.sans(11.5))
                }
            }
            .padding(.top, 6)
        }
        .padding(12)
        .frame(maxWidth: .infinity, minHeight: 112, alignment: .topLeading)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(kpi.title), \(kpi.value)" + (kpi.changeText.map { ", \($0) against the period before" } ?? ""))
    }
}

/// A tiny line of the period, no axes.
struct ReportSparkline: View {
    let values: [Double]
    let tone: Tone

    var body: some View {
        Chart {
            ForEach(Array(values.enumerated()), id: \.offset) { index, value in
                AreaMark(x: .value("Day", index), y: .value("Count", value))
                    .interpolationMethod(.monotone)
                    .foregroundStyle(LinearGradient(colors: [tone.solid.opacity(0.22), tone.solid.opacity(0)], startPoint: .top, endPoint: .bottom))
                LineMark(x: .value("Day", index), y: .value("Count", value))
                    .interpolationMethod(.monotone)
                    .foregroundStyle(tone.solid)
                    .lineStyle(StrokeStyle(lineWidth: 1.5, lineCap: .round, lineJoin: .round))
            }
        }
        .chartXAxis(.hidden)
        .chartYAxis(.hidden)
        .chartLegend(.hidden)
        .chartYScale(domain: 0 ... max(1, (values.max() ?? 1) * 1.1))
        .accessibilityHidden(true)
    }
}

// MARK: - Volume

/// Created and solved per day, scrubbable: drag across it and the readout
/// shows that day. The lines draw in on arrival and when the period changes.
struct ReportVolumeChart: View {
    let days: [ReportDay]
    let shown: Bool

    @State private var selected: Date?
    @State private var drawn: CGFloat = 0
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var selectedDay: ReportDay? {
        guard let selected else { return nil }
        return days.first { Calendar.current.isDate($0.day, inSameDayAs: selected) }
    }

    private var peak: Double { Double(days.map { max($0.created, $0.solved) }.max() ?? 0) }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            readout
            chart
        }
        .sensoryFeedback(.selection, trigger: selectedDay?.date)
        .onAppear { if shown { draw(0.25) } }
        .onChange(of: shown) { _, now in if now { draw(0.25) } }
        .onChange(of: days.first?.date) {
            selected = nil
            drawn = 0
            draw(0.05)
        }
    }

    private func draw(_ delay: Double) {
        if reduceMotion {
            drawn = 1
            return
        }
        withAnimation(.easeOut(duration: 0.9).delay(delay)) { drawn = 1 }
    }

    private var readout: some View {
        HStack(alignment: .top, spacing: 20) {
            figure("Created", tone: ReportsPalette.created, value: selectedDay?.created ?? days.reduce(0) { $0 + $1.created })
            figure("Solved", tone: ReportsPalette.solved, value: selectedDay?.solved ?? days.reduce(0) { $0 + $1.solved })
            Spacer(minLength: 0)
            Text(selectedDay.map { $0.day.formatted(.dateTime.weekday(.abbreviated).month(.abbreviated).day()) } ?? "Per day")
                .font(ODFont.sans(12.5))
                .foregroundStyle(Theme.fg3)
                .contentTransition(.opacity)
        }
        .animation(reduceMotion ? nil : .snappy(duration: 0.22), value: selectedDay?.date)
    }

    private func figure(_ title: String, tone: Tone, value: Int) -> some View {
        VStack(alignment: .leading, spacing: 2) {
            HStack(spacing: 5) {
                Capsule().fill(tone.solid).frame(width: 10, height: 3)
                Text(title).font(ODFont.sans(12.5, weight: .medium)).foregroundStyle(Theme.fg2)
            }
            Text(value.formatted())
                .font(ODFont.mono(20, weight: .medium))
                .foregroundStyle(Theme.fg)
                .contentTransition(.numericText(value: Double(value)))
        }
    }

    private var chart: some View {
        Chart {
            ForEach(days) { day in
                AreaMark(x: .value("Day", day.day, unit: .day), y: .value("Tickets", day.created), series: .value("Series", "area"))
                    .interpolationMethod(.monotone)
                    .foregroundStyle(LinearGradient(colors: [ReportsPalette.created.solid.opacity(0.18), ReportsPalette.created.solid.opacity(0.0)], startPoint: .top, endPoint: .bottom))
            }
            ForEach(days) { day in
                LineMark(x: .value("Day", day.day, unit: .day), y: .value("Tickets", day.created), series: .value("Series", "Created"))
                    .interpolationMethod(.monotone)
                    .foregroundStyle(ReportsPalette.created.solid)
                    .lineStyle(StrokeStyle(lineWidth: 2.2, lineCap: .round, lineJoin: .round))
            }
            ForEach(days) { day in
                LineMark(x: .value("Day", day.day, unit: .day), y: .value("Tickets", day.solved), series: .value("Series", "Solved"))
                    .interpolationMethod(.monotone)
                    .foregroundStyle(ReportsPalette.solved.solid)
                    .lineStyle(StrokeStyle(lineWidth: 2, lineCap: .round, lineJoin: .round, dash: [5, 3]))
            }
            if let day = selectedDay {
                RuleMark(x: .value("Day", day.day, unit: .day))
                    .foregroundStyle(Theme.fg.opacity(0.14))
                    .lineStyle(StrokeStyle(lineWidth: 1.5))
                PointMark(x: .value("Day", day.day, unit: .day), y: .value("Tickets", day.created))
                    .symbolSize(70)
                    .foregroundStyle(ReportsPalette.created.solid)
                PointMark(x: .value("Day", day.day, unit: .day), y: .value("Tickets", day.solved))
                    .symbolSize(70)
                    .foregroundStyle(ReportsPalette.solved.solid)
            }
        }
        .chartXSelection(value: $selected)
        .chartXAxis {
            AxisMarks(values: .automatic(desiredCount: 4, roundLowerBound: false, roundUpperBound: false)) { _ in
                AxisValueLabel(format: .dateTime.month(.abbreviated).day(), verticalSpacing: 8)
                    .font(ODFont.sans(11))
                    .foregroundStyle(Theme.fg3)
            }
        }
        .chartYAxis {
            AxisMarks(position: .leading, values: .automatic(desiredCount: 3)) { _ in
                AxisGridLine(stroke: StrokeStyle(lineWidth: 1, dash: [2, 3])).foregroundStyle(Theme.line)
                AxisValueLabel().font(ODFont.mono(10)).foregroundStyle(Theme.fg4)
            }
        }
        .chartYScale(domain: 0 ... max(4, peak * 1.15))
        .chartLegend(.hidden)
        .frame(height: 190)
        .mask(alignment: .leading) {
            GeometryReader { proxy in
                Rectangle().frame(width: proxy.size.width * drawn)
            }
        }
        .accessibilityLabel("Tickets created and solved per day")
    }
}

// MARK: - Donut

/// A share of a whole: a ring with the total in the middle and a legend beside it.
struct ReportDonut: View {
    let slices: [ReportSlice]
    var centerLabel = "tickets"
    @State private var selected: Double?

    private var total: Double { slices.reduce(0) { $0 + $1.value } }

    private var active: ReportSlice? {
        guard let selected else { return nil }
        var running = 0.0
        for slice in slices {
            running += slice.value
            if selected <= running { return slice }
        }
        return nil
    }

    var body: some View {
        HStack(spacing: 20) {
            Chart(slices) { slice in
                SectorMark(angle: .value("Tickets", slice.value), innerRadius: .ratio(0.64), angularInset: 1.5)
                    .cornerRadius(3)
                    .foregroundStyle(slice.tone.solid)
                    .opacity(active == nil || active?.id == slice.id ? 1 : 0.35)
            }
            .chartAngleSelection(value: $selected)
            .chartLegend(.hidden)
            .frame(width: 128, height: 128)
            .overlay {
                VStack(spacing: 0) {
                    Text(Int(active?.value ?? total).formatted())
                        .font(ODFont.mono(19, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .contentTransition(.numericText())
                    Text(active?.label.lowercased() ?? centerLabel)
                        .font(ODFont.sans(11.5))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(1)
                }
                .allowsHitTesting(false)
            }
            .sensoryFeedback(.selection, trigger: active?.id)
            .animation(Motion.snappy, value: active?.id)

            VStack(alignment: .leading, spacing: 9) {
                ForEach(slices) { slice in
                    HStack(spacing: 8) {
                        RoundedRectangle(cornerRadius: 2).fill(slice.tone.solid).frame(width: 8, height: 8)
                        Text(slice.label)
                            .font(ODFont.sans(13.5))
                            .foregroundStyle(Theme.fg2)
                            .lineLimit(1)
                        Spacer(minLength: 4)
                        Text(total > 0 ? "\(Int((slice.value / total * 100).rounded()))%" : "0%")
                            .font(ODFont.mono(12.5))
                            .foregroundStyle(Theme.fg)
                    }
                    .opacity(active == nil || active?.id == slice.id ? 1 : 0.45)
                }
            }
        }
    }
}

// MARK: - Bars

/// Horizontal bars with a label and the count, each in its tone.
struct ReportBars: View {
    let slices: [ReportSlice]
    var shown = true
    var maxValue: Double?
    var labelWidth: CGFloat = 104
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var peak: Double { max(maxValue ?? slices.map(\.value).max() ?? 0, 1) }

    var body: some View {
        VStack(spacing: 10) {
            ForEach(Array(slices.enumerated()), id: \.element.id) { index, slice in
                HStack(spacing: 10) {
                    HStack(spacing: 6) {
                        if let symbol = slice.symbol {
                            Image(systemName: symbol).font(.system(size: 10.5, weight: .semibold)).foregroundStyle(slice.tone.text).frame(width: 14)
                        } else {
                            RoundedRectangle(cornerRadius: 2).fill(slice.tone.solid).frame(width: 8, height: 8).frame(width: 14)
                        }
                        Text(slice.label)
                            .font(ODFont.sans(13.5))
                            .foregroundStyle(Theme.fg2)
                            .lineLimit(1)
                    }
                    .frame(width: labelWidth, alignment: .leading)
                    GeometryReader { proxy in
                        ZStack(alignment: .leading) {
                            Capsule().fill(Theme.hover)
                            Capsule()
                                .fill(slice.tone.solid.opacity(0.85))
                                .frame(width: max(slice.value > 0 ? 4 : 0, proxy.size.width * (shown ? slice.value / peak : 0)))
                                .animation(reduceMotion ? nil : .spring(response: 0.6, dampingFraction: 0.86).delay(Double(index) * 0.04), value: shown)
                                .animation(reduceMotion ? nil : Motion.smooth, value: slice.value)
                        }
                    }
                    .frame(height: 8)
                    Text(slice.display ?? Int(slice.value).formatted())
                        .font(ODFont.mono(12.5))
                        .foregroundStyle(slice.value > 0 ? Theme.fg : Theme.fg4)
                        .frame(minWidth: 34, alignment: .trailing)
                        .contentTransition(.numericText())
                }
                .accessibilityElement(children: .combine)
            }
        }
    }
}

/// One bar split into parts, with a legend under it (the open backlog by status).
struct ReportStackBar: View {
    let slices: [ReportSlice]
    var shown = true
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    private var total: Double { slices.reduce(0) { $0 + $1.value } }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            GeometryReader { proxy in
                HStack(spacing: 2) {
                    ForEach(slices.filter { $0.value > 0 }) { slice in
                        RoundedRectangle(cornerRadius: 3, style: .continuous)
                            .fill(slice.tone.solid.opacity(0.85))
                            .frame(width: max(6, (proxy.size.width - 4) * (total > 0 ? slice.value / total : 0)))
                    }
                }
                .frame(width: proxy.size.width, alignment: .leading)
                .scaleEffect(x: shown || reduceMotion ? 1 : 0.001, anchor: .leading)
                .animation(reduceMotion ? nil : .spring(response: 0.7, dampingFraction: 0.88), value: shown)
            }
            .frame(height: 12)
            .background(Theme.hover, in: RoundedRectangle(cornerRadius: 3, style: .continuous))
            HStack(spacing: 16) {
                ForEach(slices) { slice in
                    VStack(alignment: .leading, spacing: 2) {
                        HStack(spacing: 5) {
                            RoundedRectangle(cornerRadius: 2).fill(slice.tone.solid).frame(width: 8, height: 8)
                            Text(slice.label).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3)
                        }
                        Text(Int(slice.value).formatted())
                            .font(ODFont.mono(16, weight: .medium))
                            .foregroundStyle(Theme.fg)
                            .contentTransition(.numericText())
                    }
                }
                Spacer(minLength: 0)
            }
        }
    }
}
