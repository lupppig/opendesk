import SwiftUI
import UIKit

/// The conversation: what you asked in a soft bubble, Ollie's answers in
/// full-width type with their steps in the order they ran, the change waiting
/// for approval at the end, and a retry when a run failed.
struct AssistantConversationView: View {
    let chat: AssistantStore
    let assistantName: String
    let onRef: (AssistantRef) -> Void
    let onRetry: () -> Void

    @State private var atBottom = true
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    /// Changes whenever the stream grows, to keep the newest words in view.
    private var growth: Int {
        chat.turns.reduce(0) { total, turn in
            total + turn.blocks.reduce(turn.text.count + 1) { $0 + ($1.text?.count ?? 3) }
        } + (chat.approval == nil ? 0 : 1)
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                VStack(alignment: .leading, spacing: 22) {
                    ForEach(chat.turns) { turn in
                        let live = chat.running && turn.id == chat.turns.last?.id
                        if turn.role == .user {
                            AssistantUserBubble(text: turn.text)
                                .transition(reduceMotion ? .opacity : .asymmetric(insertion: .scale(scale: 0.94, anchor: .bottomTrailing).combined(with: .opacity).combined(with: .offset(y: 8)), removal: .opacity))
                        } else {
                            AssistantAnswer(turn: turn, live: live, deciding: chat.deciding, waiting: chat.approval != nil, assistantName: assistantName, onRef: onRef, onRetry: onRetry)
                                .transition(.opacity)
                        }
                    }
                    if let approval = chat.approval {
                        AssistantApprovalCard(approval: approval, busy: chat.running || chat.deciding, onRef: onRef) { approve in
                            chat.decide(approve: approve)
                        }
                        .transition(reduceMotion ? .opacity : .asymmetric(insertion: .scale(scale: 0.96, anchor: .bottom).combined(with: .opacity).combined(with: .offset(y: 10)), removal: .opacity.combined(with: .scale(scale: 0.98))))
                    }
                    Color.clear.frame(height: 1).id("end")
                }
                .padding(.horizontal, 20)
                .padding(.top, 14)
                .padding(.bottom, 20)
                .frame(maxWidth: .infinity, alignment: .leading)
                .animation(reduceMotion ? nil : Motion.smooth, value: chat.approval?.actionID)
                .animation(reduceMotion ? nil : Motion.snappy, value: chat.turns.count)
            }
            .defaultScrollAnchor(.bottom)
            .defaultScrollAnchor(.bottom, for: .sizeChanges)
            .scrollDismissesKeyboard(.interactively)
            .scrollEdgeEffectStyle(.soft, for: .vertical)
            .onScrollGeometryChange(for: Bool.self) { geometry in
                geometry.contentSize.height <= geometry.containerSize.height || geometry.visibleRect.maxY >= geometry.contentSize.height - 80
            } action: { _, bottom in
                atBottom = bottom
            }
            .onChange(of: chat.turns.count) {
                withAnimation(reduceMotion ? nil : Motion.snappy) { proxy.scrollTo("end", anchor: .bottom) }
            }
            .onChange(of: growth) {
                guard atBottom else { return }
                proxy.scrollTo("end", anchor: .bottom)
            }
            .overlay(alignment: .bottom) {
                if !atBottom {
                    Button {
                        withAnimation(reduceMotion ? nil : Motion.snappy) { proxy.scrollTo("end", anchor: .bottom) }
                    } label: {
                        Image(systemName: "arrow.down")
                            .font(.system(size: 14, weight: .semibold))
                            .foregroundStyle(Theme.fg)
                            .frame(width: 40, height: 40)
                    }
                    .buttonStyle(TapScaleStyle(scale: 0.9))
                    .glassEffect(.regular.interactive(), in: Circle())
                    .padding(.bottom, 10)
                    .transition(.scale(scale: 0.6).combined(with: .opacity))
                    .accessibilityLabel("Jump to the latest")
                }
            }
            .motion(Motion.snappy, value: atBottom)
        }
    }
}

/// What you asked, on the right. Long pastes fold until opened.
struct AssistantUserBubble: View {
    let text: String
    @State private var open = false

    private var long: Bool { text.count > 700 || text.components(separatedBy: "\n").count > 12 }

    var body: some View {
        HStack {
            Spacer(minLength: 52)
            VStack(alignment: .trailing, spacing: 6) {
                Text(text)
                    .font(ODFont.sans(15.5))
                    .lineSpacing(3)
                    .foregroundStyle(Theme.fg)
                    .lineLimit(long && !open ? 12 : nil)
                    .textSelection(.enabled)
                if long {
                    Button(open ? "Show less" : "Show more") { open.toggle() }
                        .font(ODFont.sans(13, weight: .medium))
                        .foregroundStyle(Theme.fg3)
                        .buttonStyle(.plain)
                }
            }
            .padding(.horizontal, 14)
            .padding(.vertical, 10)
            .background(Theme.hover, in: UnevenRoundedRectangle(topLeadingRadius: 20, bottomLeadingRadius: 20, bottomTrailingRadius: 6, topTrailingRadius: 20, style: .continuous))
        }
    }
}

/// One of Ollie's answers: text and steps in the order they happened.
struct AssistantAnswer: View {
    let turn: AssistantStore.Turn
    let live: Bool
    let deciding: Bool
    let waiting: Bool
    let assistantName: String
    let onRef: (AssistantRef) -> Void
    let onRetry: () -> Void

    @State private var copied = false

    private enum Segment: Identifiable {
        case steps([AssistantStep])
        case text(String, Int)
        case decision(AssistantStore.Decision, Int)

        var id: String {
            switch self {
            case let .steps(steps): "steps-" + (steps.first?.id ?? "")
            case let .text(_, index): "text-\(index)"
            case let .decision(decision, _): "decision-" + decision.approval.actionID
            }
        }
    }

    private var segments: [Segment] {
        var out: [Segment] = []
        for (index, block) in turn.blocks.enumerated() {
            switch block.kind {
            case let .step(step):
                if case var .steps(steps)? = out.last {
                    steps.append(step)
                    out[out.count - 1] = .steps(steps)
                } else {
                    out.append(.steps([step]))
                }
            case let .text(text):
                out.append(.text(text, index))
            case let .decision(decision):
                out.append(.decision(decision, index))
            }
        }
        return out
    }

    /// Ollie is between words: nothing yet, or a step just finished.
    private var thinking: Bool {
        guard live, !waiting else { return false }
        guard let last = turn.blocks.last else { return true }
        if let step = last.step { return step.status != "running" && step.status != "awaiting_approval" }
        if case .decision = last.kind { return !deciding }
        return false
    }

    /// A change in this answer still waits on a decision, so the answer isn't finished.
    private var awaiting: Bool { turn.blocks.contains { $0.step?.status == "awaiting_approval" } }

    private var lastTextIndex: Int? { turn.blocks.lastIndex { $0.text != nil } }

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            ForEach(segments) { segment in
                switch segment {
                case let .steps(steps):
                    VStack(alignment: .leading, spacing: 8) {
                        ForEach(steps) { step in
                            AssistantStepRow(step: step, onRef: onRef)
                                .transition(.opacity.combined(with: .offset(y: 4)))
                        }
                    }
                case let .text(text, index):
                    AssistantAnswerText(text: text, live: live && index == lastTextIndex && index == turn.blocks.count - 1)
                case let .decision(decision, index):
                    AssistantDecisionRow(decision: decision, pending: deciding && live && index == turn.blocks.count - 1)
                }
            }
            if thinking {
                AssistantThinking(name: assistantName)
                    .transition(.opacity)
            }
            if let error = turn.error {
                AssistantErrorRow(message: error, name: assistantName, onRetry: onRetry)
            }
            if !live, turn.error == nil, !turn.answer.isEmpty, !awaiting {
                actions.transition(.opacity)
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .motion(Motion.snappy, value: thinking)
        .motion(Motion.snappy, value: turn.blocks.count)
    }

    private var actions: some View {
        HStack(spacing: 2) {
            Button {
                UIPasteboard.general.string = turn.answer
                Haptics.selection()
                copied = true
                Task {
                    try? await Task.sleep(for: .seconds(1.6))
                    copied = false
                }
            } label: {
                Image(systemName: copied ? "checkmark" : "doc.on.doc")
                    .font(.system(size: 12.5, weight: .medium))
                    .foregroundStyle(copied ? Tone.green.text : Theme.fg4)
                    .frame(width: 30, height: 26)
                    .contentShape(Rectangle())
                    .contentTransition(.symbolEffect(.replace))
            }
            .buttonStyle(.plain)
            .accessibilityLabel(copied ? "Copied" : "Copy answer")
            ShareLink(item: turn.answer) {
                Image(systemName: "square.and.arrow.up")
                    .font(.system(size: 12.5, weight: .medium))
                    .foregroundStyle(Theme.fg4)
                    .frame(width: 30, height: 26)
                    .contentShape(Rectangle())
            }
            .accessibilityLabel("Share answer")
        }
        .padding(.leading, -8)
        .padding(.top, -6)
    }
}

/// Ollie's text: as it arrives it is revealed a little ahead of the reader,
/// smoothly, with a soft caret; once done it is plain markdown.
struct AssistantAnswerText: View {
    let text: String
    let live: Bool

    @State private var shown: Int?
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        let visible = live && !reduceMotion ? String(text.prefix(shown ?? 0)) : text
        MarkdownText(text: visible, size: 15.5, caret: live)
            .textSelection(.enabled)
            .task(id: text) {
                guard live, !reduceMotion else { return }
                if shown == nil { shown = 0 }
                while !Task.isCancelled {
                    let total = text.count
                    let current = shown ?? 0
                    guard current < total else { break }
                    shown = min(total, current + max(1, Int((Double(total - current) / 10).rounded(.up))))
                    try? await Task.sleep(for: .milliseconds(16))
                }
            }
            .onChange(of: live) { _, isLive in
                if !isLive { shown = nil }
            }
    }
}

/// "Ollie is thinking", between steps and words.
struct AssistantThinking: View {
    let name: String

    var body: some View {
        HStack(spacing: 8) {
            OllieMark(size: 16, state: .thinking, eyes: false)
                .frame(width: 18, height: 18)
            Text("\(name) is thinking")
                .font(ODFont.sans(13.5))
                .foregroundStyle(Theme.fg3)
                .assistantShimmer(true)
        }
        .frame(minHeight: 22)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("\(name) is thinking")
    }
}

/// A run that failed, with a way to try it again.
struct AssistantErrorRow: View {
    let message: String
    let name: String
    let onRetry: () -> Void

    var body: some View {
        HStack(alignment: .top, spacing: 11) {
            Image(systemName: "exclamationmark.circle.fill")
                .font(.system(size: 16))
                .foregroundStyle(Tone.red.solid)
                .padding(.top, 1)
            VStack(alignment: .leading, spacing: 3) {
                Text("\(name) couldn't finish")
                    .font(ODFont.sans(14.5, weight: .semibold))
                    .foregroundStyle(Theme.fg)
                Text(message)
                    .font(ODFont.sans(13.5))
                    .foregroundStyle(Theme.fg2)
                    .fixedSize(horizontal: false, vertical: true)
                Button {
                    Haptics.tap()
                    onRetry()
                } label: {
                    Label("Try again", systemImage: "arrow.clockwise")
                        .labelStyle(AssistantCompactLabel(size: 13))
                }
                .buttonStyle(ChipButtonStyle())
                .padding(.top, 6)
            }
            Spacer(minLength: 0)
        }
        .padding(13)
        .background(Tone.red.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Tone.red.line, lineWidth: 1))
    }
}
