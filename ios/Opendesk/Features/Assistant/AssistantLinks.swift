import SwiftUI

/// Where Ollie's references lead (web `refs.ts`): tickets, contacts,
/// articles and views open in the app behind the sheet, which closes first;
/// automations open on the web; links from a connected product open only
/// when they are https.
@MainActor
struct AssistantLinks {
    let env: AppEnvironment
    let openURL: OpenURLAction

    func open(_ ref: AssistantRef) {
        switch ref.kind {
        case "ticket":
            let id = ref.id.isEmpty ? ref.number.map(String.init) : ref.id
            if let id { go(.ticket(TicketRoute(id: id))) }
        case "contact":
            go(.contact(ref.id))
        case "article":
            go(.article(ref.id))
        case "view":
            env.router.showOllie = false
            env.inbox.setScope(.view(ref.id))
            env.router.show(.tickets, [])
        case "automation":
            if let url = URL(string: "settings/automations/\(ref.id)", relativeTo: env.session.instance.appOrigin) { openURL(url.absoluteURL) }
        case "external":
            if let url = Self.external(ref) { openURL(url) }
        default:
            break
        }
    }

    /// Links inside Ollie's text: `#123`, app paths and the web.
    func handle(_ url: URL) -> OpenURLAction.Result {
        if url.scheme == MarkdownText.scheme {
            switch url.host() {
            case "ticket":
                let number = url.lastPathComponent
                guard Int(number) != nil else { return .discarded }
                go(.ticket(TicketRoute(id: number)))
            case "app":
                let path = URLComponents(url: url, resolvingAgainstBaseURL: false)?.queryItems?.first { $0.name == "path" }?.value ?? "/"
                if let target = URL(string: path, relativeTo: env.session.instance.appOrigin)?.absoluteURL {
                    env.router.showOllie = false
                    env.router.handle(url: target)
                }
            default:
                return .discarded
            }
            return .handled
        }
        // A link to this instance's app opens in the app.
        if url.host() == env.session.instance.appOrigin.host(), url.scheme == "https" || url.scheme == "http" {
            env.router.showOllie = false
            env.router.handle(url: url)
            return .handled
        }
        return .systemAction(url)
    }

    static func external(_ ref: AssistantRef) -> URL? {
        guard ref.kind == "external", let url = URL(string: ref.id), url.scheme == "https" else { return nil }
        return url
    }

    /// Closes the sheet and pushes `route` on the stack behind it.
    private func go(_ route: AppRoute) {
        let router = env.router
        router.showOllie = false
        Task { @MainActor in
            // Let the sheet start leaving so the push reads as one move.
            try? await Task.sleep(for: .milliseconds(260))
            switch router.tab {
            case .notifications:
                if router.notificationsPath.last != route { router.notificationsPath.append(route) }
            case .inbox:
                if router.inboxPath.last != route { router.inboxPath.append(route) }
            default:
                router.show(router.area, router.inboxPath + [route])
            }
        }
    }
}
