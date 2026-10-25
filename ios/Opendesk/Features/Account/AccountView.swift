import SwiftUI

/// The Account tab: who you are and how you show up, your settings, the
/// workspace's settings on the web for admins, and signing out.
struct AccountView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var store = AccountStore()

    var body: some View {
        @Bindable var router = env.router
        NavigationStack(path: $router.accountPath) {
            AccountHome()
                .navigationDestination(for: AccountRoute.self) { route in
                    AccountRouteDestination(route: route)
                }
        }
        .environment(store)
        .task {
            store.attach(session: env.session, directory: env.directory)
            store.workspaceChanged(env.session.workspace?.id)
        }
        .onChange(of: env.session.workspace?.id) { _, id in store.workspaceChanged(id) }
    }
}

/// The tab's root page.
private struct AccountHome: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @State private var web: AccountWebPage?
    @State private var confirm: AccountConfirm?
    @State private var deleting = false

    private var session: SessionStore { env.session }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 24) {
                TitleHeader(title: "Account")
                    .padding(.horizontal, -16)
                    .padding(.bottom, -10)
                AccountProfileCard()
                personal
                workspace
                if session.isAdmin { workspaceSettings }
                help
                ending
                AccountFootnote()
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 120)
        }
        .background(Theme.chrome.ignoresSafeArea())
        .toolbarVisibility(.hidden, for: .navigationBar)
        .accountWebSheet($web)
        .accountConfirm($confirm)
        .sheet(isPresented: $deleting) {
            AccountDeleteSheet()
                .presentationDetents([.large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .onAppear { handleDemo(env.router.demoAction) }
        .onChange(of: env.router.demoAction) { _, action in handleDemo(action) }
    }

    // MARK: Sections

    private var personal: some View {
        AccountSection(title: "Your settings") {
            AccountLinkRow(route: .profile, subtitle: "Name, photo and what customers see")
            AccountDivider()
            AccountLinkRow(route: .notifications, subtitle: "What reaches your email, phone and desktop")
            AccountDivider()
            AccountLinkRow(route: .phone, value: phoneValue)
            AccountDivider()
            AccountLinkRow(route: .security, subtitle: securitySubtitle)
            AccountDivider()
            AccountLinkRow(route: .appearance, value: env.appearance.title)
        }
    }

    private var workspace: some View {
        AccountSection(title: session.workspace.map { "In \($0.workspace.name)" }) {
            AccountLinkRow(route: .savedReplies, value: env.directory.savedReplies.isEmpty ? nil : "\(env.directory.savedReplies.count)")
            AccountDivider()
            AccountLinkRow(route: .views, value: env.directory.views.isEmpty ? nil : "\(env.directory.views.count)")
            AccountDivider()
            AccountLinkRow(route: .workspaces, value: session.memberships.count > 1 ? "\(session.memberships.count)" : nil, subtitle: "Switch or create a workspace")
        }
    }

    private var workspaceSettings: some View {
        AccountSection(
            title: "Workspace settings",
            footer: "These open the web app. It keeps its own sign-in, so it may ask you to sign in once."
        ) {
            ForEach(Array(AccountWebSetting.all(cloud: session.instance.isCloud).enumerated()), id: \.element.path) { index, setting in
                if index > 0 { AccountDivider() }
                Button {
                    Haptics.tap()
                    web = session.instance.webURL(path: setting.path).map(AccountWebPage.init)
                } label: {
                    AccountRow(symbol: setting.symbol, tone: .gray, title: setting.title, subtitle: setting.subtitle) {
                        Image(systemName: "arrow.up.right")
                            .font(.system(size: 12, weight: .semibold))
                            .foregroundStyle(Theme.fg4)
                    }
                }
                .buttonStyle(RowPressStyle())
            }
        }
    }

    private var help: some View {
        AccountSection(title: "Help") {
            Button {
                web = URL(string: "https://docs.opendesk.sh/guides/iphone-app/").map(AccountWebPage.init)
            } label: {
                AccountRow(symbol: "book", tone: .violet, title: "Guide to the iPhone app") {
                    Image(systemName: "arrow.up.right").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.fg4)
                }
            }
            .buttonStyle(RowPressStyle())
            AccountDivider()
            Link(destination: URL(string: "mailto:team@opendesk.sh")!) {
                AccountRow(symbol: "envelope", tone: .blue, title: "Write to us", subtitle: "team@opendesk.sh") {
                    Image(systemName: "arrow.up.right").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.fg4)
                }
            }
            .buttonStyle(RowPressStyle())
            AccountDivider()
            AccountLinkRow(route: .about, value: "Version \(AppInfo.version)")
        }
    }

    private var ending: some View {
        VStack(spacing: 10) {
            if inAppDemo {
                Button {
                    Task { await env.session.signOut() }
                } label: {
                    ButtonLabel(title: "Leave the demo", symbol: "rectangle.portrait.and.arrow.right")
                }
                .buttonStyle(OutlineButtonStyle())
            } else {
                Button {
                    confirm = AccountConfirm(
                        title: "Sign out of opendesk?",
                        message: "This iPhone stops getting notifications for \(session.user?.email ?? "this account") until you sign in again.",
                        confirmTitle: "Sign out"
                    ) {
                        await env.session.signOut()
                    }
                } label: {
                    ButtonLabel(title: "Sign out", symbol: "rectangle.portrait.and.arrow.right")
                }
                .buttonStyle(OutlineButtonStyle(tint: Tone.red.text))
            }
            Button("Delete account") { deleting = true }
                .font(ODFont.sans(14, weight: .medium))
                .foregroundStyle(Theme.fg3)
                .frame(height: 36)
        }
        .padding(.top, 4)
    }

    // MARK: Values

    /// A demo started from the sign-in screen, which signing out leaves.
    private var inAppDemo: Bool {
        #if DEBUG
        DemoMode.isOn && !DemoMode.launchedInDemo
        #else
        false
        #endif
    }

    private var phoneValue: String {
        if env.push.mode == .off { return "Off" }
        switch env.push.authorization {
        case .denied: return "Blocked"
        case .notDetermined: return "Not set up"
        default: return env.push.isEnabledOnDevice ? "On" : "Off"
        }
    }

    private var securitySubtitle: String {
        session.user?.totpEnabled == true ? "Password, two-factor on, sessions" : "Password, two-factor, sessions"
    }

    // MARK: Demo

    private func handleDemo(_ action: String?) {
        #if DEBUG
        guard let action else { return }
        switch action {
        case "web-settings":
            env.router.demoAction = nil
            web = session.instance.webURL(path: "/settings/members").map(AccountWebPage.init)
        case "delete-account":
            env.router.demoAction = nil
            deleting = true
        case "sign-out":
            env.router.demoAction = nil
            confirm = AccountConfirm(title: "Sign out of opendesk?", message: "This iPhone stops getting notifications for \(session.user?.email ?? "this account") until you sign in again.", confirmTitle: "Sign out") {}
        default:
            // Actions of a page open that page; it handles them once it shows.
            if let route = AccountDemo.route(for: action), env.router.accountPath.last != route {
                env.router.tab = .account
                env.router.accountPath = [route]
            }
        }
        #endif
    }
}

/// Which page answers a `/demo/<action>`.
enum AccountDemo {
    static func route(for action: String) -> AccountRoute? {
        switch action {
        case "change-password", "2fa-setup", "2fa-codes", "2fa-off", "reauth", "passkey-rename", "sessions": .security
        case "new-reply", "edit-reply": .savedReplies
        case "new-view", "edit-view": .views
        case "avatar": .profile
        case "new-workspace": .workspaces
        case "pause", "digest": .notifications
        default: nil
        }
    }

    /// Clears `action` and answers whether it was `name`.
    @MainActor
    static func take(_ name: String, from router: Router) -> Bool {
        #if DEBUG
        guard router.demoAction == name else { return false }
        router.demoAction = nil
        return true
        #else
        return false
        #endif
    }
}

/// The workspace settings pages, on the web.
struct AccountWebSetting {
    let title: String
    var subtitle: String?
    let symbol: String
    let path: String

    static func all(cloud: Bool) -> [AccountWebSetting] {
        var list = [
            AccountWebSetting(title: "General", subtitle: "Name, address, time zone", symbol: "building.2", path: "/settings/workspace"),
            AccountWebSetting(title: "Members", subtitle: "Invite people and set roles", symbol: "person.2", path: "/settings/members"),
            AccountWebSetting(title: "Teams", symbol: "person.3", path: "/settings/teams"),
            AccountWebSetting(title: "Mailboxes", subtitle: "Addresses, signatures and routing", symbol: "tray.2", path: "/settings/mailboxes"),
            AccountWebSetting(title: "Automations and SLA", symbol: "bolt", path: "/settings/automations"),
            AccountWebSetting(title: "Ollie and AI", symbol: "sparkles", path: "/settings/ai"),
            AccountWebSetting(title: "Chat widget", symbol: "bubble.left.and.text.bubble.right", path: "/settings/widget"),
        ]
        if cloud { list.append(AccountWebSetting(title: "Plan and billing", symbol: "creditcard", path: "/settings/billing")) }
        return list
    }
}

/// Version, build and server, small at the bottom.
private struct AccountFootnote: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        Text("opendesk \(AppInfo.version) (\(AppInfo.build)) · \(env.session.instance.displayHost)")
            .font(ODFont.mono(11))
            .foregroundStyle(Theme.fg4)
            .frame(maxWidth: .infinity)
            .padding(.top, -6)
    }
}
