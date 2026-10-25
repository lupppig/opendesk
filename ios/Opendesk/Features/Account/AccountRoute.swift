import SwiftUI

/// A page pushed on the Account tab (`/account/<rawValue>` in the demo).
enum AccountRoute: String, Hashable, CaseIterable {
    case profile, security, notifications, phone, savedReplies = "saved-replies", views, workspaces, appearance, about

    var title: String {
        switch self {
        case .profile: "Profile"
        case .security: "Security"
        case .notifications: "Notifications"
        case .phone: "This iPhone"
        case .savedReplies: "Saved replies"
        case .views: "Views"
        case .workspaces: "Workspaces"
        case .appearance: "Appearance"
        case .about: "About"
        }
    }

    var symbol: String {
        switch self {
        case .profile: "person.text.rectangle"
        case .security: "lock.shield"
        case .notifications: "bell.badge"
        case .phone: "iphone"
        case .savedReplies: "text.bubble"
        case .views: "line.3.horizontal.decrease.circle"
        case .workspaces: "square.stack.3d.up"
        case .appearance: "circle.lefthalf.filled"
        case .about: "info.circle"
        }
    }

    var tone: Tone {
        switch self {
        case .profile: .indigo
        case .security: .green
        case .notifications: .rose
        case .phone: .gray
        case .savedReplies: .teal
        case .views: .blue
        case .workspaces: .orange
        case .appearance: .amber
        case .about: .cyan
        }
    }
}

/// Draws an Account page.
struct AccountRouteDestination: View {
    let route: AccountRoute

    var body: some View {
        switch route {
        case .profile: AccountProfileView()
        case .security: AccountSecurityView()
        case .notifications: AccountNotificationSettingsView()
        case .phone: AccountPhoneView()
        case .savedReplies: AccountSavedRepliesView()
        case .views: AccountViewsView()
        case .workspaces: AccountWorkspacesView()
        case .appearance: AccountAppearanceView()
        case .about: AccountAboutView()
        }
    }
}
