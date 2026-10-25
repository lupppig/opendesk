import SwiftUI
import UIKit

/// A calm line above the list when this iPhone can't show notifications yet,
/// with the one thing that fixes it. It can be put away.
struct NotificationsPushNotice: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.openURL) private var openURL
    @AppStorage("od.notifications.pushNotice.hidden") private var hiddenFor = ""
    @State private var asking = false

    private var push: PushManager { env.push }

    private enum Kind: String { case denied, ask }

    private var kind: Kind? {
        guard push.mode != .off else { return nil }
        switch push.authorization {
        case .denied: return .denied
        case .notDetermined: return .ask
        default: return nil
        }
    }

    var body: some View {
        if let kind, hiddenFor != kind.rawValue {
            HStack(alignment: .center, spacing: 12) {
                Image(systemName: kind == .denied ? "bell.slash" : "bell.badge")
                    .font(.system(size: 14, weight: .medium))
                    .foregroundStyle(kind == .denied ? Tone.amber.text : Tone.blue.text)
                    .frame(width: 30, height: 30)
                    .background(kind == .denied ? Tone.amber.soft : Tone.blue.soft, in: RoundedRectangle(cornerRadius: 8, style: .continuous))
                VStack(alignment: .leading, spacing: 1) {
                    Text(kind == .denied ? "Notifications are off" : "Hear about it on this iPhone")
                        .font(ODFont.sans(14, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                    Text(kind == .denied ? "Turn them on in Settings to know when you're needed." : "Assignments, mentions and replies, even when the app is closed.")
                        .font(ODFont.sans(12.5))
                        .foregroundStyle(Theme.fg3)
                        .fixedSize(horizontal: false, vertical: true)
                }
                Spacer(minLength: 4)
                Button {
                    fix(kind)
                } label: {
                    ButtonLabel(title: kind == .denied ? "Settings" : "Turn on", busy: asking)
                }
                .buttonStyle(ChipButtonStyle(filled: true))
                .disabled(asking)
                Button {
                    Haptics.tap()
                    withAnimation(Motion.snappy) { hiddenFor = kind.rawValue }
                } label: {
                    Image(systemName: "xmark")
                        .font(.system(size: 11, weight: .semibold))
                        .foregroundStyle(Theme.fg4)
                        .frame(width: 24, height: 30)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Hide")
            }
            .padding(.leading, 12)
            .padding(.trailing, 8)
            .padding(.vertical, 10)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            .transition(.opacity.combined(with: .move(edge: .top)))
        }
    }

    private func fix(_ kind: Kind) {
        Haptics.tap()
        if kind == .denied {
            if let url = URL(string: UIApplication.openNotificationSettingsURLString) { openURL(url) }
            return
        }
        asking = true
        Task {
            await push.requestPermission()
            asking = false
        }
    }
}
