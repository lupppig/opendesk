import SwiftUI

/// Password, two-factor, passkeys and sessions.
struct AccountSecurityView: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store

    private enum Sheet: Identifiable {
        case password
        case setup(TOTPSetup, codes: [String]?)
        case off

        var id: String {
            switch self {
            case .password: "password"
            case .setup: "setup"
            case .off: "off"
            }
        }
    }

    @State private var sheet: Sheet?
    @State private var starting = false

    private var totpOn: Bool { env.session.user?.totpEnabled == true }

    var body: some View {
        @Bindable var store = store
        AccountPage(title: "Security") {
            overview
            AccountSection(title: "Password") {
                Button { sheet = .password } label: {
                    AccountRow(symbol: "key", tone: .amber, title: "Change password", subtitle: "Signs out your other devices") {
                        Chevron()
                    }
                }
                .buttonStyle(RowPressStyle())
            }
            twoFactor
            if env.session.instance.passkeysSupported {
                AccountPasskeysSection()
            }
            AccountSessionsSection()
        }
        .sheet(item: $sheet) { sheet in
            Group {
                switch sheet {
                case .password:
                    AccountPasswordSheet()
                        .presentationDetents([.large])
                case let .setup(setup, codes):
                    AccountTwoFactorSetupSheet(setup: setup, initialCodes: codes)
                        .presentationDetents([.large])
                case .off:
                    AccountTwoFactorOffSheet()
                        .presentationDetents([.medium, .large])
                }
            }
            .presentationCornerRadius(Theme.Radius.sheet)
        }
        .sheet(item: $store.reauthPrompt) { prompt in
            AccountReauthSheet(prompt: prompt)
                .presentationDetents([.medium, .large])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .onAppear(perform: handleDemo)
        .onChange(of: env.router.demoAction) { _, _ in handleDemo() }
    }

    // MARK: Overview

    /// A calm summary card: how protected the account is right now.
    private var overview: some View {
        let passkeys = store.passkeys.count
        let level = (totpOn ? 1 : 0) + (passkeys > 0 ? 1 : 0)
        let tone: Tone = level == 0 ? .amber : .green
        return HStack(alignment: .top, spacing: 14) {
            AccountIconTile(symbol: level == 0 ? "exclamationmark.shield" : "checkmark.shield", tone: tone, size: 42)
            VStack(alignment: .leading, spacing: 4) {
                Text(level == 0 ? "Your account uses a password only" : totpOn ? "Two-factor protects your account" : "Passkeys protect your account")
                    .font(ODFont.sans(15.5, weight: .semibold))
                    .foregroundStyle(Theme.fg)
                    .fixedSize(horizontal: false, vertical: true)
                Text(level == 0
                    ? "Turn on two-factor so a stolen password alone can't open it."
                    : "Signed in on \(Format.plural(max(1, store.sessions.count), "device"))\(passkeys > 0 ? ", \(Format.plural(passkeys, "passkey"))" : "").")
                    .font(ODFont.footnote)
                    .foregroundStyle(Theme.fg3)
                    .fixedSize(horizontal: false, vertical: true)
            }
            Spacer(minLength: 0)
        }
        .padding(16)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(tone.solid.opacity(0.07), in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(tone.line, lineWidth: 1))
        .motion(Motion.smooth, value: level)
    }

    // MARK: Two-factor

    private var twoFactor: some View {
        AccountSection(
            title: "Two-factor authentication",
            footer: "A 6-digit code from an authenticator app each time you sign in, so a password alone isn't enough."
        ) {
            AccountRow(symbol: "lock.shield", tone: totpOn ? .green : .gray, title: "Authenticator app", subtitle: totpOn ? "On. You're asked for a code when you sign in." : "Off") {
                TonePill(text: totpOn ? "On" : "Off", tone: totpOn ? .green : .gray)
            }
            AccountDivider(tiles: false)
            if totpOn {
                Button { sheet = .off } label: {
                    Text("Turn off two-factor")
                        .font(ODFont.sans(15, weight: .medium))
                        .foregroundStyle(Tone.red.text)
                        .frame(maxWidth: .infinity)
                        .frame(height: 48)
                        .contentShape(Rectangle())
                }
                .buttonStyle(RowPressStyle())
            } else {
                Button(action: startSetup) {
                    ButtonLabel(title: "Turn on two-factor", busy: starting, symbol: "plus")
                }
                .buttonStyle(InkButtonStyle(height: 42))
                .disabled(starting)
                .padding(12)
            }
        }
        .motion(Motion.snappy, value: totpOn)
    }

    private func startSetup() {
        starting = true
        Task {
            do {
                try await store.guarded {
                    let setup = try await store.beginTOTP()
                    sheet = .setup(setup, codes: nil)
                }
            } catch {
                env.toasts.error(error)
            }
            starting = false
        }
    }

    // MARK: Demo

    private func handleDemo() {
        #if DEBUG
        let router = env.router
        if AccountDemo.take("change-password", from: router) { sheet = .password }
        if AccountDemo.take("2fa-off", from: router) { sheet = .off }
        if AccountDemo.take("2fa-setup", from: router) { startSetup() }
        if AccountDemo.take("2fa-codes", from: router) {
            Task {
                guard let setup = try? await store.beginTOTP() else { return }
                let codes = (try? await store.enableTOTP(code: "123456")) ?? []
                sheet = .setup(setup, codes: codes)
            }
        }
        if AccountDemo.take("reauth", from: router) {
            store.reauthPrompt = AccountStore.ReauthPrompt(needsMFA: false) {}
        }
        #endif
    }
}
