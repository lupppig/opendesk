import SwiftUI

/// Signing in again before a change that needs a recent sign-in (two-factor,
/// passkeys): the password, then the code when two-factor is on, then the
/// change runs. This iPhone gets a fresh session in place of the old one.
struct AccountReauthSheet: View {
    let prompt: AccountStore.ReauthPrompt

    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    @State private var password = ""
    @State private var code = ""
    @State private var mfaToken: String?
    @State private var busy = false
    @State private var error: String?
    @FocusState private var codeFocused: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                HStack(alignment: .top, spacing: 14) {
                    AccountIconTile(symbol: "lock.rotation", tone: .green, size: 44)
                    Spacer()
                    Button { dismiss() } label: {
                        Image(systemName: "xmark")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(Theme.fg2)
                            .frame(width: 32, height: 32)
                            .background(Theme.active, in: Circle())
                    }
                    .buttonStyle(TapScaleStyle(scale: 0.9))
                    .accessibilityLabel("Cancel")
                }
                VStack(alignment: .leading, spacing: 6) {
                    Text("Confirm it's you")
                        .font(ODFont.sans(21, weight: .semibold))
                        .tracking(-0.4)
                        .foregroundStyle(Theme.fg)
                    Text(message)
                        .font(ODFont.callout)
                        .foregroundStyle(Theme.fg2)
                        .fixedSize(horizontal: false, vertical: true)
                }
                if mfaToken == nil {
                    ODTextField(label: "Password for \(env.session.user?.email ?? "your account")", text: $password, secure: true, contentType: .password, submitLabel: .go) { submit() }
                        .transition(.opacity)
                } else {
                    AccountCodeField(code: $code, focused: $codeFocused) { submit() }
                        .transition(.move(edge: .trailing).combined(with: .opacity))
                }
                FormError(text: error)
                Button(action: submit) { ButtonLabel(title: mfaToken == nil ? "Continue" : "Confirm", busy: busy) }
                    .buttonStyle(InkButtonStyle())
                    .disabled(busy || (mfaToken == nil ? password.isEmpty : code.count != 6))
            }
            .padding(22)
            .motion(Motion.snappy, value: error)
        }
        .scrollBounceBehavior(.basedOnSize)
        .background(Theme.surface)
    }

    private var message: String {
        if mfaToken != nil { return "Enter the 6-digit code from your authenticator app." }
        return prompt.needsMFA
            ? "This change needs a sign-in that passed two-factor. Enter your password, then a code."
            : "This change needs a recent sign-in. Enter your password to continue."
    }

    private func submit() {
        guard !busy else { return }
        busy = true
        error = nil
        Task {
            do {
                if let token = mfaToken {
                    try await store.reauthenticate(token: token, code: code)
                } else {
                    switch try await store.reauthenticate(password: password) {
                    case .done: break
                    case let .code(token):
                        withAnimation(reduceMotion ? nil : Motion.smooth) { mfaToken = token }
                        codeFocused = true
                        busy = false
                        return
                    }
                }
                Haptics.success()
                dismiss()
                try await prompt.retry()
            } catch let failure as APIError where failure.code == "invalid_credentials" {
                Haptics.error()
                error = "That password isn't right."
            } catch let failure as APIError where failure.code == "invalid_code" {
                Haptics.error()
                error = "That code didn't work. Try the next one."
                code = ""
            } catch {
                if !error.isCancellation {
                    Haptics.error()
                    env.toasts.error(error)
                }
            }
            busy = false
        }
    }
}
