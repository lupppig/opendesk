import SwiftUI

/// Inbox, Notifications and Account, under the app's own glass tab bar with
/// Ollie's round button beside it.
struct MainTabView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var tabBar = TabBarState()
    @State private var showPushPrimer = false

    private var items: [TabItem] {
        [
            TabItem(tab: .inbox, title: "Inbox", symbol: "tray"),
            TabItem(tab: .notifications, title: "Notifications", symbol: "bell", badge: env.notifications.unread),
            TabItem(tab: .account, title: "Account", symbol: "person.crop.circle"),
        ]
    }

    private var ollieAvailable: Bool { env.directory.ai.assistantOn }

    var body: some View {
        @Bindable var router = env.router
        TabView(selection: $router.tab) {
            Tab("Inbox", systemImage: "tray", value: AppTab.inbox) {
                InboxRootView().toolbarVisibility(.hidden, for: .tabBar)
            }
            Tab("Notifications", systemImage: "bell", value: AppTab.notifications) {
                NotificationsView().toolbarVisibility(.hidden, for: .tabBar)
            }
            Tab("Account", systemImage: "person.crop.circle", value: AppTab.account) {
                AccountView().toolbarVisibility(.hidden, for: .tabBar)
            }
        }
        .safeAreaInset(edge: .bottom, spacing: 0) {
            if !tabBar.isHidden {
                ODTabBar(
                    items: items,
                    selection: $router.tab,
                    ollie: ollieAvailable ? { router.showOllie = true } : nil,
                    onReselect: { tab in
                        if tab == .inbox { router.inboxPath = [] }
                        if tab == .notifications { router.notificationsPath = [] }
                        if tab == .account { router.accountPath = [] }
                    }
                )
                .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        .motion(Motion.snappy, value: tabBar.isHidden)
        .environment(tabBar)
        .sheet(isPresented: $router.showOllie) {
            AssistantView(context: router.assistantContext)
                .presentationDetents([.large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(isPresented: $showPushPrimer) {
            PushPrimerSheet()
                .presentationDetents([.height(380)])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .task {
            // A calm moment, after the inbox is up, to explain notifications once.
            try? await Task.sleep(for: .seconds(1.5))
            if env.push.shouldOfferPermission { showPushPrimer = true }
        }
        .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillShowNotification)) { _ in
            tabBar.setKeyboard(true)
        }
        .onReceive(NotificationCenter.default.publisher(for: UIResponder.keyboardWillHideNotification)) { _ in
            tabBar.setKeyboard(false)
        }
    }
}

/// Why notifications help, before the system asks.
struct PushPrimerSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss
    @State private var asking = false

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            Image(systemName: "bell.badge")
                .font(.system(size: 26))
                .foregroundStyle(Theme.fg)
                .frame(width: 52, height: 52)
                .background(Theme.sunken, in: RoundedRectangle(cornerRadius: 14, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: 14, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            VStack(alignment: .leading, spacing: 8) {
                Text("Know when a customer needs you")
                    .font(ODFont.sans(21, weight: .semibold))
                    .tracking(-0.4)
                    .foregroundStyle(Theme.fg)
                Text("Get a notification when a ticket is assigned to you, someone mentions you, a customer replies, or an SLA is about to slip. You choose which ones in Account.")
                    .font(ODFont.callout)
                    .foregroundStyle(Theme.fg2)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
            Button {
                asking = true
                Task {
                    await env.push.requestPermission()
                    asking = false
                    dismiss()
                }
            } label: {
                ButtonLabel(title: "Turn on notifications", busy: asking)
            }
            .buttonStyle(InkButtonStyle())
            Button("Not now") {
                env.push.declinePermission()
                dismiss()
            }
            .buttonStyle(OutlineButtonStyle())
        }
        .padding(24)
        .background(Theme.surface)
    }
}
