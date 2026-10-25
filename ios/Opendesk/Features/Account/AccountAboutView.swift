import SwiftUI

/// The app's version, the server it talks to, and where to read more.
struct AccountAboutView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var web: AccountWebPage?

    private var instance: Instance { env.session.instance }

    var body: some View {
        AccountPage(title: "About") {
            VStack(spacing: 12) {
                ODLogoMark()
                    .fill(Theme.fg)
                    .frame(width: 34, height: 34)
                    .frame(width: 68, height: 68)
                    .background(Theme.surface, in: RoundedRectangle(cornerRadius: 17, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: 17, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                    .accessibilityHidden(true)
                VStack(spacing: 3) {
                    Text("opendesk for iPhone").font(ODFont.sans(18, weight: .semibold)).foregroundStyle(Theme.fg)
                    Text("Version \(AppInfo.version) (\(AppInfo.build))").font(ODFont.mono(12.5)).foregroundStyle(Theme.fg3)
                }
            }
            .frame(maxWidth: .infinity)
            .padding(.top, 12)

            AccountSection(title: "Server") {
                AccountRow(symbol: "server.rack", tone: .cyan, title: "Address") { value(instance.displayHost) }
                AccountDivider()
                AccountRow(symbol: instance.isCloud ? "cloud" : "house", tone: instance.isCloud ? .blue : .green, title: "Kind") {
                    value(instance.isCloud ? "opendesk Cloud" : "Self-hosted")
                }
                AccountDivider()
                AccountRow(symbol: "bell.badge", tone: .rose, title: "Push") { value(pushValue) }
            }

            AccountSection(title: "More") {
                link("Documentation", symbol: "book", tone: .violet, url: "https://docs.opendesk.sh")
                AccountDivider()
                link("Source code", symbol: "chevron.left.forwardslash.chevron.right", tone: .gray, url: "https://github.com/useopendesk/opendesk")
                AccountDivider()
                link("Privacy", symbol: "hand.raised", tone: .green, url: "https://opendesk.sh/privacy")
                AccountDivider()
                link("Terms", symbol: "doc.text", tone: .amber, url: "https://opendesk.sh/terms")
            }

            Text("opendesk is open source. Questions or a problem to report: team@opendesk.sh.")
                .font(ODFont.footnote)
                .foregroundStyle(Theme.fg3)
                .multilineTextAlignment(.center)
                .frame(maxWidth: .infinity)
        }
        .accountWebSheet($web)
    }

    private var pushValue: String {
        switch env.push.mode {
        case .off: "Not set up"
        case .relay: "Through the relay"
        case .direct: "Direct to Apple"
        }
    }

    private func value(_ text: String) -> some View {
        Text(text).font(ODFont.sans(14.5)).foregroundStyle(Theme.fg3).lineLimit(1)
    }

    private func link(_ title: String, symbol: String, tone: Tone, url: String) -> some View {
        Button {
            Haptics.tap()
            web = URL(string: url).map(AccountWebPage.init)
        } label: {
            AccountRow(symbol: symbol, tone: tone, title: title) {
                Image(systemName: "arrow.up.right").font(.system(size: 12, weight: .semibold)).foregroundStyle(Theme.fg4)
            }
        }
        .buttonStyle(RowPressStyle())
    }
}
