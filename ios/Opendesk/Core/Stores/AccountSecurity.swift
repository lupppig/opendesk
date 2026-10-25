import Foundation

/// Signing in and staying safe: password, two-factor, passkeys, sessions, and
/// signing in again for the changes that need a recent sign-in.
extension AccountStore {
    // MARK: Password

    /// `POST /v1/me/password`. The server signs out every other session.
    func changePassword(current: String, new: String) async throws {
        guard let api else { return }
        struct Body: Encodable, Sendable {
            let current_password: String
            let new_password: String
        }
        let _: EmptyBody = try await api.post("v1/me/password", body: Body(current_password: current, new_password: new), workspace: .none)
        await loadSessions()
    }

    // MARK: Two-factor

    /// `POST /v1/auth/totp/setup`: a new secret, not active until a code confirms it.
    func beginTOTP() async throws -> TOTPSetup {
        try await authCall("v1/auth/totp/setup", body: [String: String]())
    }

    /// `POST /v1/auth/totp/enable`: the recovery codes, shown once.
    func enableTOTP(code: String) async throws -> [String] {
        struct Body: Encodable, Sendable { let code: String }
        let answer: RecoveryCodes = try await authCall("v1/auth/totp/enable", body: Body(code: Self.cleanCode(code)))
        try? await session?.loadMe()
        return answer.recoveryCodes
    }

    /// `POST /v1/auth/totp/disable` with a current code or a recovery code.
    func disableTOTP(code: String) async throws {
        struct Body: Encodable, Sendable { let code: String }
        let _: EmptyBody = try await authCall("v1/auth/totp/disable", body: Body(code: Self.cleanCode(code)))
        try? await session?.loadMe()
    }

    /// Six digits lose their spaces; a recovery code keeps its dashes.
    static func cleanCode(_ code: String) -> String {
        let trimmed = code.trimmingCharacters(in: .whitespacesAndNewlines)
        let digits = trimmed.filter(\.isNumber)
        return digits.count == trimmed.filter { !$0.isWhitespace }.count ? digits : trimmed.lowercased()
    }

    // MARK: Passkeys

    func loadPasskeys() async {
        guard let api else { return }
        do {
            let page: Page<Passkey> = try await api.get("v1/me/passkeys", query: [URLQueryItem(name: "limit", value: "100")], workspace: .none)
            passkeys = page.data
            passkeysError = nil
        } catch {
            if !error.isCancellation { passkeysError = error.userMessage }
        }
        passkeysLoaded = true
    }

    func beginPasskey() async throws -> PasskeyBegin {
        guard let api else { throw APIError.signedOut }
        return try await api.post("v1/me/passkeys/register/begin", body: [String: String](), workspace: .none)
    }

    func finishPasskey(token: String, credential: JSONValue, name: String?) async throws {
        guard let api else { return }
        struct Body: Encodable, Sendable {
            let passkey_token: String
            let credential: JSONValue
            let name: String?
        }
        let added: Passkey = try await api.post("v1/me/passkeys/register/finish", body: Body(passkey_token: token, credential: credential, name: name), workspace: .none)
        passkeys.removeAll { $0.id == added.id }
        passkeys.insert(added, at: 0)
    }

    func renamePasskey(_ passkey: Passkey, to name: String) async throws {
        guard let api else { return }
        struct Body: Encodable, Sendable { let name: String }
        let saved: Passkey = try await api.patch("v1/me/passkeys/\(passkey.id)", body: Body(name: name.trimmingCharacters(in: .whitespacesAndNewlines)), workspace: .none)
        if let index = passkeys.firstIndex(where: { $0.id == saved.id }) { passkeys[index] = saved }
    }

    func deletePasskey(_ passkey: Passkey) async throws {
        guard let api else { return }
        try await api.delete("v1/me/passkeys/\(passkey.id)", workspace: .none)
        passkeys.removeAll { $0.id == passkey.id }
    }

    // MARK: Sessions

    func loadSessions() async {
        guard let api else { return }
        do {
            let page: Page<SessionInfo> = try await api.get("v1/me/sessions", query: [URLQueryItem(name: "limit", value: "100")], workspace: .none)
            sessions = page.data.sorted { lhs, rhs in
                if (lhs.current ?? false) != (rhs.current ?? false) { return lhs.current ?? false }
                return (lhs.lastUsedAt ?? .distantPast) > (rhs.lastUsedAt ?? .distantPast)
            }
            sessionsError = nil
        } catch {
            if !error.isCancellation { sessionsError = error.userMessage }
        }
        sessionsLoaded = true
    }

    /// Signs one device out. Its sockets close and its push devices go with it.
    func revoke(_ info: SessionInfo) async throws {
        guard let api else { return }
        try await api.delete("v1/me/sessions/\(info.id)", workspace: .none)
        sessions.removeAll { $0.id == info.id }
    }

    /// Signs out every session but this one.
    func revokeOthers() async throws {
        let others = sessions.filter { $0.current != true }
        for info in others { try await revoke(info) }
    }

    // MARK: Signing in again

    /// Signs in again with the password, on this device, then ends the old session.
    /// Answers `.code` when the account has two-factor on.
    func reauthenticate(password: String) async throws -> ReauthStep {
        guard let session, let api, let email = session.user?.email else { throw APIError.signedOut }
        struct Body: Encodable, Sendable {
            let email: String
            let password: String
            let app_attest: AppAttestProof?
        }
        var proof: AppAttestProof?
        if let config = session.authConfig, config.humanCheckRequired {
            guard config.native.appAttest else { throw SignInError.humanCheckUnavailable }
            proof = try await session.attest.proof(for: .login)
        }
        let previous = await currentSessionID()
        let result: AuthResult = try await api.post("v1/auth/login", body: Body(email: email, password: password, app_attest: proof), workspace: .none, authorized: false)
        if result.mfaRequired, let token = result.mfaToken {
            pendingPreviousSession = previous
            return .code(token: token)
        }
        try await adopt(result, replacing: previous)
        return .done
    }

    func reauthenticate(token: String, code: String) async throws {
        guard let api else { throw APIError.signedOut }
        struct Body: Encodable, Sendable {
            let mfa_token: String
            let code: String
        }
        let result: AuthResult = try await api.post("v1/auth/mfa", body: Body(mfa_token: token, code: Self.cleanCode(code)), workspace: .none, authorized: false)
        try await adopt(result, replacing: pendingPreviousSession)
        pendingPreviousSession = nil
    }

    private func adopt(_ result: AuthResult, replacing previous: String?) async throws {
        guard let api else { return }
        guard result.accessToken != nil else { throw APIError.decoding }
        await api.adopt(result)
        // The session this device used before is ended, so one device keeps one session.
        if let previous { try? await api.delete("v1/me/sessions/\(previous)", workspace: .none) }
        try? await session?.loadMe()
        await loadSessions()
    }

    private func currentSessionID() async -> String? {
        if let current = sessions.first(where: { $0.current == true }) { return current.id }
        guard let api, let page: Page<SessionInfo> = try? await api.get("v1/me/sessions", query: [URLQueryItem(name: "limit", value: "100")], workspace: .none) else { return nil }
        return page.data.first(where: { $0.current == true })?.id
    }
}
