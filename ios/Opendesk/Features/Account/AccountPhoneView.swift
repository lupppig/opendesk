import SwiftUI
import UIKit
import UserNotifications

/// Push on this iPhone: whether it's allowed, how this server sends it, and
/// the types this phone mutes on its own.
struct AccountPhoneView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var asking = false

    private var push: PushManager { env.push }

    var body: some View {
        AccountPage(title: "This iPhone") {
            AccountPushStatusCard(asking: asking, ask: ask)
            if push.mode != .off, let device = push.device {
                mutes(device)
            }
            AccountSection {
                AccountLinkRow(route: .notifications, subtitle: "Email, phone and desktop for every device")
            }
        }
        .task {
            await push.refreshAuthorization()
            await push.loadDevice()
        }
    }

    private var kinds: [NotificationKind] {
        let admin = env.session.isAdmin
        let order: [NotificationKind] = [
            .assigned, .newTicket, .mentioned, .customerReplied, .threadReplied, .slaBreached, .aiHandoff,
            .collabAssigned, .collabMention, .teamJoined,
            .aiCapReached, .aiKeyFailed, .aiBalanceLow, .aiBalanceOut, .autoRechargeFailed, .importFinished,
        ]
        return order.filter { !$0.isAdminAlert || admin }
    }

    private func mutes(_ device: PushDevice) -> some View {
        AccountSection(
            title: "On this iPhone",
            footer: "Turning a type off here mutes it on this iPhone only. Your other devices and email follow Notifications."
        ) {
            ForEach(Array(kinds.enumerated()), id: \.element) { index, kind in
                if index > 0 { AccountDivider() }
                AccountToggleRow(
                    symbol: kind.symbol,
                    tone: NotificationStyle.tone(for: kind.rawValue),
                    title: kind.settingTitle,
                    isOn: Binding(
                        get: { !device.mutedTypes.contains(kind.rawValue) },
                        set: { on in Task { await push.setMuted(kind, muted: !on) } }
                    )
                )
            }
        }
    }

    private func ask() {
        asking = true
        Task {
            await push.requestPermission()
            asking = false
        }
    }
}

/// Where push stands on this iPhone, with the one thing to do about it.
struct AccountPushStatusCard: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.openURL) private var openURL
    var asking = false
    let ask: () -> Void

    private var push: PushManager { env.push }

    private struct Status {
        var symbol: String
        var tone: Tone
        var title: String
        var message: String
        var action: String?
    }

    private var state: Status {
        let host = env.session.instance.displayHost
        if push.mode == .off {
            return Status(symbol: "bell.slash", tone: .gray, title: "This server doesn't send push notifications",
                         message: "\(host) isn't set up to reach phones. You still see everything in Notifications while the app is open.")
        }
        switch push.authorization {
        case .denied:
            return Status(symbol: "bell.slash", tone: .amber, title: "Notifications are off for opendesk",
                         message: "Allow them in Settings to hear about assignments, mentions and replies.", action: "Open Settings")
        case .notDetermined:
            return Status(symbol: "bell.badge", tone: .blue, title: "Get notified on this iPhone",
                         message: "Know when a ticket is assigned to you, someone mentions you, or a customer replies.", action: "Turn on notifications")
        default:
            let via = push.mode == .relay
                ? "\(host) sends them through the opendesk push relay to Apple."
                : "\(host) sends them to Apple directly."
            let device = push.device.map { " This iPhone is registered as \u{201C}\($0.name ?? "iPhone")\u{201D}." } ?? ""
            return Status(symbol: "bell.badge.fill", tone: .green, title: "Notifications are on", message: via + device)
        }
    }

    var body: some View {
        let state = state
        VStack(alignment: .leading, spacing: 14) {
            HStack(alignment: .top, spacing: 14) {
                AccountIconTile(symbol: state.symbol, tone: state.tone, size: 44)
                VStack(alignment: .leading, spacing: 4) {
                    Text(state.title)
                        .font(ODFont.sans(16, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                        .fixedSize(horizontal: false, vertical: true)
                    Text(state.message)
                        .font(ODFont.footnote)
                        .foregroundStyle(Theme.fg2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 0)
            }
            if let error = push.lastError, push.mode != .off {
                Notice(text: error, tone: .amber, symbol: "exclamationmark.triangle")
            }
            if let action = state.action {
                Button {
                    if push.authorization == .denied {
                        if let url = URL(string: UIApplication.openNotificationSettingsURLString) { openURL(url) }
                    } else {
                        ask()
                    }
                } label: {
                    ButtonLabel(title: action, busy: asking)
                }
                .buttonStyle(InkButtonStyle(height: 42))
                .disabled(asking)
            }
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}
