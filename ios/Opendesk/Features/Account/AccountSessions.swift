import SwiftUI

/// Where you are signed in: this device first, then the rest by last use, each one revocable.
struct AccountSessionsSection: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @State private var confirm: AccountConfirm?
    @State private var revoking: Set<String> = []

    private var others: [SessionInfo] { store.sessions.filter { $0.current != true } }

    var body: some View {
        AccountSection(
            title: "Where you're signed in",
            footer: "Signing a device out ends its session at once, along with its notifications."
        ) {
            if !store.sessionsLoaded {
                SkeletonRows(count: 2)
            } else if let error = store.sessionsError, store.sessions.isEmpty {
                AccountRow(symbol: "wifi.exclamationmark", title: "Couldn't load sessions", subtitle: error)
            } else {
                ForEach(Array(store.sessions.enumerated()), id: \.element.id) { index, info in
                    if index > 0 { AccountDivider() }
                    row(info)
                        .transition(.opacity.combined(with: .move(edge: .leading)))
                }
                if !others.isEmpty {
                    AccountDivider(tiles: false)
                    Button {
                        confirm = AccountConfirm(
                            title: "Sign out other devices?",
                            message: "\(Format.plural(others.count, "other session")) end now. This iPhone stays signed in.",
                            confirmTitle: "Sign out others"
                        ) { await revokeOthers() }
                    } label: {
                        Text("Sign out other devices")
                            .font(ODFont.sans(15, weight: .medium))
                            .foregroundStyle(Tone.red.text)
                            .frame(maxWidth: .infinity)
                            .frame(height: 48)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(RowPressStyle())
                }
            }
        }
        .motion(Motion.snappy, value: store.sessions.map(\.id))
        .accountConfirm($confirm)
        .task { await store.loadSessions() }
    }

    private func row(_ info: SessionInfo) -> some View {
        let current = info.current == true
        return AccountRow(symbol: info.deviceKind.symbol, tone: current ? .green : .gray, title: info.deviceName, subtitle: detail(info)) {
            if current {
                TonePill(text: "This device", tone: .green)
            } else if revoking.contains(info.id) {
                ProgressView().controlSize(.small)
            } else {
                Button("Sign out") {
                    confirm = AccountConfirm(
                        title: "Sign out \(info.deviceName)?",
                        message: "That device needs to sign in again to use opendesk.",
                        confirmTitle: "Sign out"
                    ) { await revoke(info) }
                }
                .buttonStyle(ChipButtonStyle())
            }
        }
    }

    private func detail(_ info: SessionInfo) -> String {
        var parts: [String] = []
        if info.current == true {
            parts.append("Active now")
        } else if let used = info.lastUsedAt {
            parts.append(AppClock.now.timeIntervalSince(used) < 300 ? "Active now" : "Last used \(Format.relLong(used))")
        }
        if let ip = info.ip, !ip.isEmpty { parts.append(ip) }
        return parts.joined(separator: " · ")
    }

    private func revoke(_ info: SessionInfo) async {
        revoking.insert(info.id)
        defer { revoking.remove(info.id) }
        do {
            try await store.revoke(info)
            Haptics.success()
        } catch {
            env.toasts.error(error)
        }
    }

    private func revokeOthers() async {
        do {
            try await store.revokeOthers()
            Haptics.success()
            env.toasts.show("Other devices are signed out", kind: .success)
        } catch {
            env.toasts.error(error)
        }
    }
}
