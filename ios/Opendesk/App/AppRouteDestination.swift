import SwiftUI

/// The screen for a route pushed on the Inbox or Notifications stack.
struct AppRouteDestination: View {
    let route: AppRoute

    var body: some View {
        switch route {
        case let .ticket(route): TicketView(route: route)
        case let .contact(id): ContactDetailView(id: id)
        case let .company(id): CompanyDetailView(id: id)
        case let .mailThread(id): MailThreadView(id: id)
        case let .article(id): ArticleView(id: id)
        case .search: SearchView()
        }
    }
}

/// The Inbox tab's root for the area picked in the menu.
struct AreaRootView: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        switch env.router.area {
        case .tickets: InboxListView()
        case .mail: MailListView()
        case .contacts: ContactsListView()
        case .companies: CompaniesListView()
        case .kb: KBListView()
        case .reports: ReportsView()
        }
    }
}
