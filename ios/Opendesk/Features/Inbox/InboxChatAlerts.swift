import AudioToolbox
import SwiftUI

/// Live chat alerts (the web's `useWaitingAlerts`): when a visitor starts waiting for a
/// person while the app is in front, a short chime, a haptic and a gentle banner; while
/// anyone still waits, a softer reminder once a minute. Chats waiting when the app
/// starts are taken in silently. The sound can be muted; the banner stays.
@MainActor
@Observable
final class InboxChatAlerts {
    /// The chat the banner shows, while it shows.
    private(set) var banner: Ticket?
    private(set) var waiting: [Ticket] = []
    var quiet: Bool {
        didSet { InboxDefaults.store.set(quiet, forKey: Self.quietKey) }
    }

    static let quietKey = "inbox.chatAlerts.quiet"

    @ObservationIgnored private var chats: [String: Ticket] = [:]
    @ObservationIgnored private var alerted: Set<String> = []
    @ObservationIgnored private var seeded = false
    @ObservationIgnored private var lastChime: Date?
    @ObservationIgnored private var api: APIClient?
    @ObservationIgnored private var listener: UUID?
    @ObservationIgnored private var ticker: Task<Void, Never>?
    @ObservationIgnored private var hideTask: Task<Void, Never>?
    @ObservationIgnored private weak var env: AppEnvironment?
    @ObservationIgnored var active = true

    init() {
        quiet = InboxDefaults.store.bool(forKey: Self.quietKey)
    }

    func start(env: AppEnvironment) {
        guard self.env == nil || api !== env.session.api else { return }
        stop()
        self.env = env
        api = env.session.api
        seeded = false
        chats = [:]
        alerted = []
        listener = env.realtime.subscribe { [weak self] update in self?.handle(update) }
        Task { await load() }
        ticker = Task { [weak self] in
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(5))
                guard !Task.isCancelled else { return }
                self?.evaluate()
            }
        }
    }

    func stop() {
        env?.realtime.unsubscribe(listener)
        listener = nil
        ticker?.cancel()
        ticker = nil
        banner = nil
        waiting = []
    }

    private func load() async {
        guard let api else { return }
        let query = [
            URLQueryItem(name: "channel", value: "chat"),
            URLQueryItem(name: "status", value: "open,pending"),
            URLQueryItem(name: "assignee", value: "none"),
            URLQueryItem(name: "limit", value: "50"),
        ]
        guard let page: Page<Ticket> = try? await api.get("v1/tickets", query: query) else {
            seeded = true
            return
        }
        for ticket in page.data { chats[ticket.id] = ticket }
        evaluate()
    }

    private func handle(_ update: RealtimeUpdate) {
        switch update {
        case .resync:
            Task { await load() }
        case let .envelope(envelope):
            switch envelope.type {
            case "ticket.created", "ticket.updated":
                guard let ticket = envelope.member("ticket", as: Ticket.self), ticket.channel == .chat else { return }
                chats[ticket.id] = ticket
                evaluate()
            case "ticket.deleted", "ticket.hidden":
                if let id = envelope.string("ticket_id") {
                    chats[id] = nil
                    evaluate()
                }
            default:
                break
            }
        }
    }

    /// Who waits for a person right now, and whether that's news.
    private func evaluate() {
        guard let env else { return }
        let agentOn = env.directory.ai.widgetAgentOn
        let now = AppClock.now
        let urgent = chats.values
            .filter { $0.assignee == nil && !$0.spam && !$0.status.isDone && ChatWait.of($0, agentOn: agentOn, now: now)?.state == .urgent }
            .sorted { ($0.waitingSince ?? now) < ($1.waitingSince ?? now) }
        waiting = urgent
        let ids = Set(urgent.map(\.id))
        alerted.formIntersection(ids)
        if let shown = banner, !ids.contains(shown.id) { hideBanner() }
        guard seeded else {
            // What waited before the app started is no news.
            alerted = ids
            seeded = true
            return
        }
        let fresh = urgent.filter { !alerted.contains($0.id) && $0.id != env.router.visibleTicketID }
        alerted.formUnion(ids)
        if let first = fresh.first, active {
            Haptics.warning()
            chime(reminder: false)
            show(first)
        } else if !urgent.isEmpty, active, let last = lastChime, now.timeIntervalSince(last) >= 60 {
            chime(reminder: true)
        } else if urgent.isEmpty {
            lastChime = nil
        }
    }

    private func chime(reminder: Bool) {
        lastChime = AppClock.now
        guard !quiet else { return }
        // System sounds follow the ring switch.
        AudioServicesPlaySystemSound(reminder ? 1057 : 1007)
    }

    private func show(_ ticket: Ticket) {
        banner = ticket
        hideTask?.cancel()
        hideTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(7))
            guard !Task.isCancelled else { return }
            self?.hideBanner()
        }
    }

    func hideBanner() {
        hideTask?.cancel()
        banner = nil
    }

    #if DEBUG
    /// `/demo/chat-alert`: the banner for the longest wait, as a new chat would bring it.
    func preview() {
        evaluate()
        guard let first = waiting.first ?? chats.values.first(where: { $0.assignee == nil }) else { return }
        Haptics.warning()
        show(first)
    }
    #endif
}

/// The banner a new waiting chat brings: who, what, and a way in.
struct InboxChatBanner: View {
    let ticket: Ticket
    let quiet: Bool
    let onOpen: () -> Void
    let onQuiet: () -> Void
    let onClose: () -> Void

    var body: some View {
        HStack(spacing: 11) {
            Button(action: onOpen) {
                HStack(spacing: 11) {
                    ZStack(alignment: .bottomTrailing) {
                        Avatar(name: ticket.contactName, url: ticket.contact?.avatarURL, seed: ticket.contact?.id ?? ticket.id, size: 34)
                        PingDot(color: Tone.red.solid, size: 8)
                            .padding(2)
                            .background(Theme.surface, in: Circle())
                            .offset(x: 3, y: 3)
                    }
                    VStack(alignment: .leading, spacing: 2) {
                        Text("\(ticket.contactName) is waiting")
                            .font(ODFont.sans(14, weight: .semibold))
                            .foregroundStyle(Theme.fg)
                            .lineLimit(1)
                        Text("Live chat \u{00B7} " + (ticket.lastMessagePreview.isEmpty ? ticket.displaySubject : ticket.lastMessagePreview))
                            .font(ODFont.sans(13))
                            .foregroundStyle(Theme.fg3)
                            .lineLimit(1)
                    }
                    Spacer(minLength: 4)
                    Text("Reply")
                        .font(ODFont.sans(13, weight: .semibold))
                        .foregroundStyle(Theme.inverseFg)
                        .padding(.horizontal, 12)
                        .frame(height: 30)
                        .background(Theme.inverse, in: Capsule())
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(TapScaleStyle())
            .accessibilityHint("Opens the chat")
            Button(action: onQuiet) {
                Image(systemName: quiet ? "speaker.slash" : "speaker.wave.2")
                    .font(.system(size: 13, weight: .medium))
                    .foregroundStyle(Theme.fg3)
                    .frame(width: 28, height: 30)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityLabel(quiet ? "Play chat alert sounds" : "Mute chat alert sounds")
        }
        .padding(.leading, 10)
        .padding(.trailing, 8)
        .padding(.vertical, 9)
        .background(Theme.surface.opacity(0.7), in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .glassEffect(.regular, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .shadow(color: .black.opacity(0.1), radius: 16, y: 6)
        .padding(.horizontal, 12)
        .gesture(DragGesture(minimumDistance: 8).onEnded { value in
            if value.translation.height < -10 { onClose() }
        })
        .accessibilityElement(children: .contain)
    }
}
