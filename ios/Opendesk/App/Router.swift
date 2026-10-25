import Observation
import SwiftUI

/// A ticket pushed on the inbox stack. `seed` shows the row's copy while the full ticket loads.
struct TicketRoute: Hashable {
    let id: String
    var seed: Ticket?

    static func == (lhs: TicketRoute, rhs: TicketRoute) -> Bool { lhs.id == rhs.id }
    func hash(into hasher: inout Hasher) { hasher.combine(id) }
}

/// Anything pushed on the Inbox or Notifications stack (`AppRouteDestination` draws it).
enum AppRoute: Hashable {
    case ticket(TicketRoute)
    case contact(String)
    case company(String)
    /// A Collab conversation.
    case mailThread(String)
    /// A knowledge base article; `"new"` starts one.
    case article(String)
    case search

    static func ticket(_ ticket: Ticket) -> AppRoute { .ticket(TicketRoute(id: ticket.id, seed: ticket)) }
}

/// What the Inbox tab shows at its root, chosen from the menu.
enum AppArea: String, CaseIterable, Identifiable, Sendable {
    case tickets, mail, contacts, companies, kb, reports

    var id: String { rawValue }

    var title: String {
        switch self {
        case .tickets: "Tickets"
        case .mail: "Mail"
        case .contacts: "Contacts"
        case .companies: "Companies"
        case .kb: "Knowledge base"
        case .reports: "Reports"
        }
    }

    var symbol: String {
        switch self {
        case .tickets: "tray"
        case .mail: "envelope"
        case .contacts: "person.2"
        case .companies: "building.2"
        case .kb: "book.closed"
        case .reports: "chart.bar.xaxis"
        }
    }
}

/// Tabs, the stacks, and links from outside the app (universal links,
/// `opendesk://open?path=`, notification taps).
@MainActor
@Observable
final class Router {
    var tab: AppTab = .inbox
    var area: AppArea = .tickets
    var inboxPath: [AppRoute] = []
    var notificationsPath: [AppRoute] = []
    var accountPath: [AccountRoute] = []
    var showOllie = false
    /// The ticket on screen, so a push for it stays quiet in the foreground.
    var visibleTicketID: String?

    @ObservationIgnored weak var environment: AppEnvironment?
    @ObservationIgnored private var pending: DeepLink.Target?

    func reset() {
        area = .tickets
        inboxPath = []
        notificationsPath = []
        accountPath = []
        visibleTicketID = nil
        showOllie = false
    }

    /// A URL the app was asked to open.
    func handle(url: URL) {
        guard let env = environment else { return }
        // OAuth callbacks are answered by the web authentication session itself.
        if url.scheme == "opendesk", url.host() == "auth" { return }
        #if DEBUG
        if url.scheme == "opendesk", url.host() == "demo" {
            if DemoMode.isOn { return openDemo(url.path()) }
            // `opendesk://demo` on the sign-in screen opens the demo workspace.
            if env.session.phase == .signedOut { Task { await env.session.enterDemo() } }
            return
        }
        #endif
        guard let target = DeepLink.parse(url: url, instance: env.session.instance) else {
            if url.scheme == "opendesk" { env.toasts.show("That link isn't one the app can open.") }
            return
        }
        open(target)
    }

    func open(_ target: DeepLink.Target) {
        guard let env = environment else { return }
        let session = env.session
        guard session.phase == .ready else {
            pending = target
            return
        }
        if let server = target.server, let url = URL(string: server), PushPayload.origin(url) != PushPayload.origin(session.instance.apiURL) {
            env.toasts.show("That's from another opendesk server (\(url.host() ?? server)). Switch servers in Account to open it.")
            return
        }
        if let workspaceID = target.workspaceID, workspaceID != session.workspace?.id {
            guard let membership = session.memberships.first(where: { $0.id == workspaceID }) else {
                env.toasts.show("You're not a member of that workspace.")
                return
            }
            pending = DeepLink.Target(link: target.link, server: nil, workspaceID: nil)
            env.switchWorkspace(to: membership)
            return
        }
        go(target.link)
    }

    /// Opens what waited for sign-in or a workspace switch.
    func workspaceReady() {
        #if DEBUG
        if let screen = pendingDemoScreen {
            pendingDemoScreen = nil
            openDemo(screen)
        }
        #endif
        guard let target = pending else { return }
        pending = nil
        open(target)
    }

    private func go(_ link: DeepLink) {
        guard let env = environment else { return }
        showOllie = false
        switch link {
        case let .ticket(id, scope):
            // The link's folder or view becomes the list behind the ticket, as on the web.
            if let scope { env.inbox.setScope(scope) }
            show(.tickets, [.ticket(TicketRoute(id: id))])
        case let .scope(scope):
            env.inbox.setScope(scope)
            show(.tickets, [])
        case .notifications:
            tab = .notifications
            notificationsPath = []
        case let .contact(id):
            show(.contacts, id.map { [.contact($0)] } ?? [])
        case let .company(id):
            show(.companies, id.map { [.company($0)] } ?? [])
        case let .mail(thread):
            show(.mail, thread.map { [.mailThread($0)] } ?? [])
        case let .article(id):
            show(.kb, id.map { [.article($0)] } ?? [])
        case .kb:
            show(.kb, [])
        case .reports:
            show(.reports, [])
        }
    }

    /// The Inbox tab at `area` with `path` on top of it.
    func show(_ area: AppArea, _ path: [AppRoute]) {
        self.area = area
        tab = .inbox
        inboxPath = path
    }

    /// Pushes `route` on the stack of the tab on screen.
    func push(_ route: AppRoute) {
        switch tab {
        case .notifications: notificationsPath.append(route)
        default: inboxPath.append(route)
        }
    }

    /// What Ollie is told about the screen behind it (contracts/ai.md page context).
    var assistantContext: AssistantPageContext {
        guard let env = environment else { return AssistantPageContext(page: "inbox") }
        let top = tab == .notifications ? notificationsPath.last : inboxPath.last
        if case let .ticket(route)? = top {
            return AssistantPageContext(page: "ticket", path: env.inbox.scope.webPath(ticketID: route.id), ticketID: route.id)
        }
        switch (tab, area) {
        case (.inbox, .tickets):
            var context = AssistantPageContext(page: "inbox", path: env.inbox.scope.webPath())
            switch env.inbox.scope {
            case let .folder(folder): context.folder = folder.rawValue
            case let .view(id):
                context.page = "view"
                context.viewID = id
            case .team: break
            }
            return context
        case (.inbox, .contacts), (.inbox, .companies): return AssistantPageContext(page: "contacts", path: "/" + area.rawValue)
        case (.inbox, .kb): return AssistantPageContext(page: "kb", path: "/kb")
        case (.inbox, .reports): return AssistantPageContext(page: "reports", path: "/reports")
        case (.inbox, .mail): return AssistantPageContext(page: "mail", path: "/mail")
        case (.account, _): return AssistantPageContext(page: "settings", path: "/settings/profile")
        default: return AssistantPageContext(page: "inbox", path: env.inbox.scope.webPath())
        }
    }

    #if DEBUG
    @ObservationIgnored private var pendingDemoScreen: String?

    /// A screen state the QA harness asked for (`/demo/<action>`, e.g. `/demo/ticket-details`).
    /// Screens with sheets or modes no path names watch this and clear it once handled.
    var demoAction: String?

    /// Demo mode takes app paths without the system's "Open in" prompt, for the QA harness:
    /// `OPENDESK_DEMO_OPEN=<path>` at launch, and during a run the path written to the app's
    /// `tmp/opendesk-demo-open` followed by the Darwin notification `sh.opendesk.demo.open`.
    func startDemoLinks() {
        if let path = AppInfo.debugValue("OPENDESK_DEMO_OPEN") { openDemo(path) }
        DemoLinks.watch { [weak self] path in self?.openDemo(path) }
    }

    /// Any path `DeepLink` reads (`/inbox/unassigned/<id>`, `/views/<id>`, `/notifications`),
    /// an `opendesk://` URL, or a screen no link names: `/account`, `/ollie`.
    func openDemo(_ raw: String) {
        let path = raw.trimmingCharacters(in: .whitespacesAndNewlines)
        if path.hasPrefix("opendesk://"), let url = URL(string: path) { return handle(url: url) }
        guard environment?.session.phase == .ready else {
            pendingDemoScreen = path
            return
        }
        let trimmed = path.trimmingCharacters(in: CharacterSet(charactersIn: "/"))
        let parts = trimmed.split(separator: "/").map(String.init)
        switch parts.first ?? "" {
        case "account":
            showOllie = false
            tab = .account
            accountPath = parts.dropFirst().compactMap(AccountRoute.init(rawValue:))
        case "ollie":
            showOllie = true
        case "inbox" where parts.count == 1:
            showOllie = false
            show(.tickets, [])
        case "mail" where parts.count == 1:
            showOllie = false
            show(.mail, [])
        case "companies" where parts.count == 1:
            showOllie = false
            show(.companies, [])
        case "search":
            showOllie = false
            show(area, [.search])
        case "demo":
            demoAction = parts.dropFirst().joined(separator: "/")
        default:
            guard let link = DeepLink.parse(path: path) else { return }
            open(DeepLink.Target(link: link, server: nil, workspaceID: nil))
        }
    }
    #endif

    func openTicket(_ ticket: Ticket) {
        push(.ticket(ticket))
    }
}
