import AuthenticationServices
import SwiftUI
import UIKit

/// Creates a passkey on this iPhone for the server's options and returns the
/// `RegistrationResponseJSON` it expects (contracts/api.md "Passkeys").
@MainActor
final class AccountPasskeyRegistrar: NSObject, ASAuthorizationControllerDelegate, ASAuthorizationControllerPresentationContextProviding {
    enum Failure: LocalizedError {
        case cancelled
        case badOptions
        case failed

        var errorDescription: String? {
            switch self {
            case .cancelled: nil
            case .badOptions: "The server sent passkey options the app can't use."
            case .failed: "The passkey wasn't saved. Try again."
            }
        }
    }

    private var continuation: CheckedContinuation<ASAuthorizationPlatformPublicKeyCredentialRegistration, Error>?
    private var controller: ASAuthorizationController?

    func register(options raw: JSONValue, fallbackRP: String) async throws -> JSONValue {
        let options = raw["publicKey"] ?? raw
        guard let challengeText = options["challenge"]?.string, let challenge = PKCE.decodeBase64URL(challengeText),
              let userText = options["user"]?["id"]?.string, let userID = PKCE.decodeBase64URL(userText) else {
            throw Failure.badOptions
        }
        #if DEBUG
        // The demo's relying party isn't this app's, so the system sheet would refuse it.
        if DemoMode.isOn { return Self.demoCredential() }
        #endif
        let rp = options["rp"]?["id"]?.string ?? fallbackRP
        let name = options["user"]?["name"]?.string ?? ""
        let provider = ASAuthorizationPlatformPublicKeyCredentialProvider(relyingPartyIdentifier: rp)
        let request = provider.createCredentialRegistrationRequest(challenge: challenge, name: name, userID: userID)
        switch options["authenticatorSelection"]?["userVerification"]?.string {
        case "required": request.userVerificationPreference = .required
        case "discouraged": request.userVerificationPreference = .discouraged
        default: request.userVerificationPreference = .preferred
        }
        request.attestationPreference = .none
        if let excluded = options["excludeCredentials"]?.array, !excluded.isEmpty {
            request.excludedCredentials = excluded.compactMap { $0["id"]?.string.flatMap(PKCE.decodeBase64URL) }
                .map { ASAuthorizationPlatformPublicKeyCredentialDescriptor(credentialID: $0) }
        }

        let registration = try await withCheckedThrowingContinuation { continuation in
            self.continuation = continuation
            let controller = ASAuthorizationController(authorizationRequests: [request])
            controller.delegate = self
            controller.presentationContextProvider = self
            self.controller = controller
            controller.performRequests()
        }
        controller = nil

        guard let attestation = registration.rawAttestationObject else { throw Failure.failed }
        let id = PKCE.base64URL(registration.credentialID)
        return .object([
            "id": .string(id),
            "rawId": .string(id),
            "type": .string("public-key"),
            "response": .object([
                "clientDataJSON": .string(PKCE.base64URL(registration.rawClientDataJSON)),
                "attestationObject": .string(PKCE.base64URL(attestation)),
                "transports": .array([.string("internal"), .string("hybrid")]),
            ]),
            "clientExtensionResults": .object([:]),
            "authenticatorAttachment": .string("platform"),
        ])
    }

    #if DEBUG
    private static func demoCredential() -> JSONValue {
        let id = PKCE.base64URL(Data("demo-passkey".utf8))
        return .object([
            "id": .string(id), "rawId": .string(id), "type": .string("public-key"),
            "response": .object(["clientDataJSON": .string(""), "attestationObject": .string("")]),
            "clientExtensionResults": .object([:]), "authenticatorAttachment": .string("platform"),
        ])
    }
    #endif

    nonisolated func authorizationController(controller: ASAuthorizationController, didCompleteWithAuthorization authorization: ASAuthorization) {
        MainActor.assumeIsolated {
            if let registration = authorization.credential as? ASAuthorizationPlatformPublicKeyCredentialRegistration {
                continuation?.resume(returning: registration)
            } else {
                continuation?.resume(throwing: Failure.failed)
            }
            continuation = nil
        }
    }

    nonisolated func authorizationController(controller: ASAuthorizationController, didCompleteWithError error: Error) {
        MainActor.assumeIsolated {
            if let auth = error as? ASAuthorizationError, auth.code == .canceled {
                continuation?.resume(throwing: Failure.cancelled)
            } else {
                continuation?.resume(throwing: Failure.failed)
            }
            continuation = nil
        }
    }

    nonisolated func presentationAnchor(for controller: ASAuthorizationController) -> ASPresentationAnchor {
        MainActor.assumeIsolated { PresentationAnchor.window }
    }
}

/// The account's passkeys: list, add one on this iPhone, rename, remove.
struct AccountPasskeysSection: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @State private var adding = false
    @State private var registrar = AccountPasskeyRegistrar()
    @State private var renaming: Passkey?
    @State private var confirm: AccountConfirm?

    var body: some View {
        AccountSection(
            title: "Passkeys",
            footer: "Sign in with Face ID or your device's lock instead of a password. Passkeys in iCloud Keychain work on your other Apple devices too."
        ) {
            if !store.passkeysLoaded {
                SkeletonRows(count: 1)
            } else {
                ForEach(Array(store.passkeys.enumerated()), id: \.element.id) { index, passkey in
                    if index > 0 { AccountDivider() }
                    row(passkey)
                }
                if !store.passkeys.isEmpty { AccountDivider() }
                Button(action: add) {
                    AccountRow(symbol: "plus", tone: .green, title: store.passkeys.isEmpty ? "Add a passkey" : "Add another passkey", subtitle: store.passkeys.isEmpty ? "Saved on this iPhone and in iCloud Keychain" : nil) {
                        if adding { ProgressView().controlSize(.small) }
                    }
                }
                .buttonStyle(RowPressStyle())
                .disabled(adding)
            }
        }
        .task { if !store.passkeysLoaded { await store.loadPasskeys() } }
        .sheet(item: $renaming) { passkey in
            AccountRenamePasskeySheet(passkey: passkey)
                .presentationDetents([.height(250)])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .accountConfirm($confirm)
        .onChange(of: env.router.demoAction) { _, _ in handleDemo() }
        .onAppear(perform: handleDemo)
        .onChange(of: store.passkeysLoaded) { _, _ in handleDemo() }
    }

    private func row(_ passkey: Passkey) -> some View {
        AccountRow(symbol: "person.badge.key", tone: .gray, title: passkey.name, subtitle: detail(passkey)) {
            Menu {
                Button("Rename", systemImage: "pencil") { renaming = passkey }
                Button("Remove", systemImage: "trash", role: .destructive) {
                    confirm = AccountConfirm(
                        title: "Remove \u{201C}\(passkey.name)\u{201D}?",
                        message: "You can't sign in with this passkey any more. Your password and other passkeys still work.",
                        confirmTitle: "Remove passkey"
                    ) { await remove(passkey) }
                }
            } label: {
                Image(systemName: "ellipsis")
                    .font(.system(size: 15, weight: .semibold))
                    .foregroundStyle(Theme.fg3)
                    .frame(width: 36, height: 36)
                    .contentShape(Rectangle())
            }
            .accessibilityLabel("Actions for \(passkey.name)")
        }
    }

    private func detail(_ passkey: Passkey) -> String {
        var parts: [String] = []
        if let created = passkey.createdAt { parts.append("Added \(Format.shortDate(created))") }
        parts.append(passkey.lastUsedAt.map { "used \(Format.relLong($0))" } ?? "never used")
        if passkey.backedUp { parts.append("synced") }
        return parts.joined(separator: " · ")
    }

    private func add() {
        adding = true
        Task {
            defer { adding = false }
            do {
                try await store.guarded { try await registerPasskey() }
            } catch let failure as AccountPasskeyRegistrar.Failure where failure == .cancelled {
                return
            } catch let error as APIError where error.code == "passkey_exists" {
                env.toasts.show("This iPhone already has a passkey for your account.", kind: .info)
            } catch let error as APIError where error.code == "passkey_limit" {
                env.toasts.show("An account can have 25 passkeys. Remove one to add another.", kind: .error)
            } catch {
                env.toasts.error(error)
            }
        }
    }

    private func registerPasskey() async throws {
        let begin = try await store.beginPasskey()
        let rp = env.session.instance.appOrigin.host() ?? "app.opendesk.sh"
        let credential = try await registrar.register(options: begin.options, fallbackRP: rp)
        try await store.finishPasskey(token: begin.passkeyToken, credential: credential, name: UIDevice.current.name)
        Haptics.success()
        env.toasts.show("Passkey added", kind: .success)
    }

    private func remove(_ passkey: Passkey) async {
        do {
            try await store.guarded {
                try await store.deletePasskey(passkey)
                env.toasts.show("Passkey removed", kind: .success)
            }
        } catch {
            env.toasts.error(error)
        }
    }

    private func handleDemo() {
        guard store.passkeysLoaded else { return }
        if AccountDemo.take("passkey-rename", from: env.router) { renaming = store.passkeys.first }
    }
}

/// A new name for a passkey.
private struct AccountRenamePasskeySheet: View {
    let passkey: Passkey
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @State private var name = ""
    @State private var saving = false

    private var valid: Bool {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return !trimmed.isEmpty && trimmed.count <= 64 && trimmed != passkey.name
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            AccountSheetHeader(title: "Rename passkey") { dismiss() }
            ODTextField(label: "Name", text: $name, prompt: "Work laptop", submitLabel: .done) { save() }
            Button(action: save) { ButtonLabel(title: "Save", busy: saving) }
                .buttonStyle(InkButtonStyle())
                .disabled(!valid || saving)
        }
        .padding(22)
        .frame(maxHeight: .infinity, alignment: .top)
        .background(Theme.surface)
        .onAppear { name = passkey.name }
    }

    private func save() {
        guard valid else { return }
        saving = true
        Task {
            do {
                try await store.renamePasskey(passkey, to: name)
                dismiss()
            } catch {
                env.toasts.error(error)
            }
            saving = false
        }
    }
}
