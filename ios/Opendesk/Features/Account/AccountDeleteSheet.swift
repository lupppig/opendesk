import SwiftUI

/// Deleting your account: what goes with it, then your password (and a code when two-factor is on).
struct AccountDeleteSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var password = ""
    @State private var code = ""
    @State private var busy = false
    @State private var error: String?

    private var needsCode: Bool { env.session.user?.totpEnabled == true }

    private var ready: Bool {
        !password.isEmpty && (!needsCode || code.trimmingCharacters(in: .whitespaces).count >= 6)
    }

    private var owned: [Membership] { env.session.memberships.filter { $0.role == .owner } }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                AccountSheetHeader(title: "Delete your account", subtitle: env.session.user?.email) { dismiss() }
                VStack(alignment: .leading, spacing: 10) {
                    consequence("person.crop.circle.badge.xmark", "Your profile, sign-ins, passkeys and sessions are removed.")
                    consequence("rectangle.portrait.and.arrow.right", "You leave every workspace. Tickets you worked on stay with the workspace.")
                    if !owned.isEmpty {
                        consequence("building.2", ownedText)
                    }
                    consequence("arrow.uturn.backward", "This can't be undone.")
                }
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Tone.red.soft, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
                .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Tone.red.line, lineWidth: 1))

                ODTextField(label: "Password", text: $password, secure: true, contentType: .password, submitLabel: needsCode ? .next : .done)
                if needsCode {
                    ODTextField(label: "Code from your authenticator, or a recovery code", text: $code, prompt: "123456", contentType: .oneTimeCode, keyboard: .asciiCapable, submitLabel: .done)
                }
                FormError(text: error)
                Button(action: delete) { ButtonLabel(title: "Delete my account", busy: busy) }
                    .buttonStyle(AccountDangerButtonStyle())
                    .disabled(!ready || busy)
                    .opacity(ready ? 1 : 0.5)
                Button("Keep my account") { dismiss() }
                    .buttonStyle(OutlineButtonStyle())
            }
            .padding(22)
            .motion(Motion.snappy, value: error)
        }
        .scrollDismissesKeyboard(.interactively)
        .background(Theme.surface)
        .interactiveDismissDisabled(busy)
    }

    private var ownedText: String {
        let names = owned.map(\.workspace.name)
        let list = names.count == 1 ? names[0] : names.dropLast().joined(separator: ", ") + " and " + (names.last ?? "")
        return "You own \(list). A workspace where you're the only member is deleted with its data. One with other members needs a new owner first."
    }

    private func consequence(_ symbol: String, _ text: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 10) {
            Image(systemName: symbol)
                .font(.system(size: 13, weight: .medium))
                .frame(width: 18)
            Text(text)
                .font(ODFont.footnote)
                .fixedSize(horizontal: false, vertical: true)
        }
        .foregroundStyle(Tone.red.text)
    }

    private func delete() {
        guard ready, !busy else { return }
        busy = true
        error = nil
        Task {
            do {
                try await store.deleteAccount(password: password, code: code.trimmingCharacters(in: .whitespaces))
                Haptics.success()
            } catch let failure as APIError {
                Haptics.error()
                switch failure.code {
                case "invalid_credentials": error = "Your password is not right."
                case "invalid_code": error = "That code didn't work. Try the newest one, or a recovery code."
                case "sole_owner": error = "You own a workspace with other members. Make someone else its owner on the web, then delete your account."
                case "mfa_required": error = "Enter a code from your authenticator app."
                default: error = failure.userMessage
                }
            } catch {
                Haptics.error()
                self.error = error.userMessage
            }
            busy = false
        }
    }
}
