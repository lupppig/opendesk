import Foundation
import Observation

/// What a sign-in step asks for next.
enum SignInOutcome: Sendable, Equatable {
    case signedIn
    case mfa(token: String)
    case link(token: String, email: String)
}

/// The signed-in person, the server they're on, and the open workspace.
@MainActor
@Observable
final class SessionStore {
    enum Phase: Equatable {
        case launching
        case signedOut
        case unreachable(String)
        case needsWorkspace
        case ready
    }

    private(set) var phase: Phase = .launching
    private(set) var instance: Instance = Preferences.instance ?? .cloud
    private(set) var authConfig: AuthConfig?
    private(set) var authConfigError: String?
    private(set) var user: User?
    private(set) var memberships: [Membership] = []
    private(set) var workspace: Membership?

    @ObservationIgnored private(set) var api: APIClient
    @ObservationIgnored private(set) var attest: AppAttestClient

    /// Set by the environment: start and stop everything that belongs to a workspace.
    @ObservationIgnored var onReady: ((Membership) -> Void)?
    @ObservationIgnored var onEnded: ((_ instance: Instance) async -> Void)?

    init() {
        let start = Self.launchInstance()
        instance = start
        api = APIClient(instance: start)
        attest = AppAttestClient(api: api)
        installSignedOutHandler()
    }

    private static func launchInstance() -> Instance {
        #if DEBUG
        if DemoMode.isOn { return DemoMode.instance }
        if let raw = AppInfo.debugValue("OPENDESK_DEBUG_SERVER") {
            if raw == "local" { return .localDev }
            if let origin = try? InstanceResolver.normalize(raw) {
                if origin == Instance.cloudAppOrigin { return .cloud }
                if let api = AppInfo.debugValue("OPENDESK_DEBUG_API").flatMap(URL.init(string:)) {
                    return Instance(appOrigin: origin, apiURL: api, gatewayURL: AppInfo.debugValue("OPENDESK_DEBUG_GATEWAY").flatMap(URL.init(string:)), selfHosted: true)
                }
                return Instance(appOrigin: origin, apiURL: origin, gatewayURL: nil, selfHosted: true)
            }
        }
        #endif
        return Preferences.instance ?? .cloud
    }

    private func installSignedOutHandler() {
        let client = api
        Task { [weak self] in
            await client.setSignedOutHandler { [weak self] in
                await self?.sessionExpired(for: client)
            }
            _ = self
        }
    }

    // MARK: Roles

    var role: Role { workspace?.role ?? .viewer }
    var canWrite: Bool { role.canWrite }
    var isAdmin: Bool { role.isAdmin }
    var userRef: UserRef? { user.map { UserRef(id: $0.id, name: $0.name, email: $0.email, avatarURL: $0.avatarURL) } }

    // MARK: Launch

    func bootstrap() async {
        Task { await loadAuthConfig() }
        guard api.hasRefreshToken else {
            phase = .signedOut
            return
        }
        do {
            try await api.refresh()
            try await loadMe()
            enterWorkspace(preferred: Preferences.workspaceID(for: instance))
        } catch let error as APIError where error.isTransport {
            phase = .unreachable(error.userMessage)
        } catch let error as APIError where error.status >= 500 {
            phase = .unreachable(error.userMessage)
        } catch {
            await api.clearTokens()
            phase = .signedOut
        }
    }

    func retryLaunch() async {
        phase = .launching
        await bootstrap()
    }

    func loadAuthConfig() async {
        do {
            authConfig = try await InstanceResolver().authConfig(for: instance.apiURL)
            authConfigError = nil
        } catch {
            authConfigError = error.userMessage
        }
    }

    // MARK: Server

    /// Moves to another server (only while signed out; the account screen signs out first).
    func useInstance(_ next: Instance, config: AuthConfig?) {
        instance = next
        Preferences.instance = next
        authConfig = config
        authConfigError = nil
        api = APIClient(instance: next)
        attest = AppAttestClient(api: api)
        installSignedOutHandler()
        if config == nil { Task { await loadAuthConfig() } }
    }

    // MARK: Sign-in

    private func humanCheck(_ action: AppAttestClient.Action) async throws -> AppAttestProof? {
        let config: AuthConfig
        if let authConfig {
            config = authConfig
        } else {
            await loadAuthConfig()
            guard let authConfig else { throw APIError.transport(URLError(.cannotConnectToHost)) }
            config = authConfig
        }
        guard config.humanCheckRequired else { return nil }
        guard config.native.appAttest else { throw SignInError.humanCheckUnavailable }
        return try await attest.proof(for: action)
    }

    func signIn(email: String, password: String) async throws -> SignInOutcome {
        struct Body: Encodable, Sendable {
            let email: String
            let password: String
            let app_attest: AppAttestProof?
        }
        let proof = try await humanCheck(.login)
        let result: AuthResult = try await withAttestRetry {
            try await self.api.post("v1/auth/login", body: Body(email: email, password: password, app_attest: proof), workspace: .none, authorized: false)
        }
        return try await complete(result)
    }

    func verifyMFA(token: String, code: String) async throws {
        struct Body: Encodable, Sendable {
            let mfa_token: String
            let code: String
        }
        let cleaned = code.trimmingCharacters(in: .whitespacesAndNewlines)
        let result: AuthResult = try await api.post("v1/auth/mfa", body: Body(mfa_token: token, code: cleaned), workspace: .none, authorized: false)
        _ = try await complete(result)
    }

    func register(name: String, email: String, password: String) async throws -> SignInOutcome {
        struct Body: Encodable, Sendable {
            let name: String
            let email: String
            let password: String
            let app_attest: AppAttestProof?
        }
        let proof = try await humanCheck(.register)
        let result: AuthResult = try await withAttestRetry {
            try await self.api.post("v1/auth/register", body: Body(name: name, email: email, password: password, app_attest: proof), workspace: .none, authorized: false)
        }
        return try await complete(result)
    }

    func forgotPassword(email: String) async throws {
        struct Body: Encodable, Sendable {
            let email: String
            let app_attest: AppAttestProof?
        }
        let proof = try await humanCheck(.passwordForgot)
        let _: EmptyBody = try await withAttestRetry {
            try await self.api.post("v1/auth/password/forgot", body: Body(email: email, app_attest: proof), workspace: .none, authorized: false)
        }
    }

    /// A refused proof can mean the server lost this install's key: forget it so the next try registers a new one.
    private func withAttestRetry<T>(_ body: () async throws -> T) async throws -> T {
        do {
            return try await body()
        } catch let error as APIError where error.code == "captcha_failed" {
            await attest.forgetKey()
            throw error
        }
    }

    /// `{apiUrl}/v1/auth/oauth/{provider}/start?client=native&code_challenge=…`
    func oauthStartURL(provider: String, challenge: String) -> URL? {
        var components = URLComponents(url: instance.apiURL.appending(path: "v1/auth/oauth/\(provider)/start"), resolvingAgainstBaseURL: false)
        components?.queryItems = [
            URLQueryItem(name: "client", value: "native"),
            URLQueryItem(name: "code_challenge", value: challenge),
            URLQueryItem(name: "redirect_to", value: "/inbox"),
        ]
        return components?.url
    }

    func exchangeOAuth(code: String, verifier: String) async throws -> SignInOutcome {
        struct Body: Encodable, Sendable {
            let code: String
            let code_verifier: String
        }
        let result: AuthResult = try await api.post("v1/auth/oauth/exchange", body: Body(code: code, code_verifier: verifier), workspace: .none, authorized: false)
        return try await complete(result)
    }

    func linkAccount(token: String, password: String) async throws -> SignInOutcome {
        struct Body: Encodable, Sendable {
            let link_token: String
            let password: String
        }
        let result: AuthResult = try await api.post("v1/auth/oauth/link", body: Body(link_token: token, password: password), workspace: .none, authorized: false)
        return try await complete(result)
    }

    func beginPasskey() async throws -> PasskeyBegin {
        try await api.post("v1/auth/passkey/login/begin", workspace: .none, authorized: false)
    }

    func finishPasskey(token: String, credential: JSONValue) async throws -> SignInOutcome {
        struct Body: Encodable, Sendable {
            let passkey_token: String
            let credential: JSONValue
        }
        let result: AuthResult = try await api.post("v1/auth/passkey/login/finish", body: Body(passkey_token: token, credential: credential), workspace: .none, authorized: false)
        return try await complete(result)
    }

    private func complete(_ result: AuthResult) async throws -> SignInOutcome {
        if result.linkRequired, let token = result.linkToken {
            return .link(token: token, email: result.email ?? "")
        }
        if result.mfaRequired, let token = result.mfaToken {
            return .mfa(token: token)
        }
        guard result.accessToken != nil else { throw APIError.decoding }
        await api.adopt(result)
        try await loadMe()
        enterWorkspace(preferred: Preferences.workspaceID(for: instance))
        return .signedIn
    }

    // MARK: Account and workspaces

    func loadMe() async throws {
        let me: Me = try await api.get("v1/me", workspace: .none)
        user = me.user
        memberships = me.memberships
        if let current = workspace, let fresh = me.memberships.first(where: { $0.id == current.id }) {
            workspace = fresh
        }
    }

    /// Reloads the account after a membership event; leaves the workspace if access is gone.
    func membershipChanged() async {
        guard (try? await loadMe()) != nil else { return }
        if let workspace, !memberships.contains(where: { $0.id == workspace.id }) {
            self.workspace = nil
            enterWorkspace(preferred: nil)
        }
    }

    private func enterWorkspace(preferred: String?) {
        var preferred = AppInfo.debugValue("OPENDESK_DEBUG_WORKSPACE") ?? preferred
        #if DEBUG
        if DemoMode.isOn, preferred == nil { preferred = DemoSeed.marginID }
        #endif
        if let preferred,
           let match = memberships.first(where: { $0.id == preferred || $0.workspace.slug == preferred }) {
            select(match)
        } else if memberships.count == 1, let only = memberships.first {
            select(only)
        } else {
            workspace = nil
            phase = .needsWorkspace
        }
    }

    func select(_ membership: Membership) {
        workspace = membership
        Preferences.setWorkspaceID(membership.id, for: instance)
        let client = api
        Task {
            await client.setWorkspace(membership.id)
            self.phase = .ready
            self.onReady?(membership)
        }
    }

    /// Back to the picker (switching workspaces).
    func chooseWorkspace() {
        phase = .needsWorkspace
    }

    func createWorkspace(name: String) async throws {
        struct Body: Encodable, Sendable { let name: String }
        let created: Workspace = try await api.post("v1/workspaces", body: Body(name: name), workspace: .none)
        try await loadMe()
        let membership = memberships.first(where: { $0.id == created.id }) ?? Membership(workspace: created, role: .owner)
        select(membership)
    }

    func updateName(_ name: String) async throws {
        struct Body: Encodable, Sendable { let name: String }
        let updated: User = try await api.patch("v1/me", body: Body(name: name), workspace: .none)
        user = updated
    }

    func setAvailability(_ availability: Availability) async throws -> Member {
        struct Body: Encodable, Sendable { let availability: Availability }
        return try await api.patch("v1/members/me/availability", body: Body(availability: availability))
    }

    // MARK: Ending

    func signOut() async {
        let ended = instance
        await onEnded?(ended)
        await api.signOut()
        reset()
        #if DEBUG
        if DemoMode.isOn, !DemoMode.launchedInDemo { leaveDemo() }
        #endif
    }

    #if DEBUG
    /// Signs in to the demo's in-memory workspace (`DemoMode`), with nothing leaving the device.
    func enterDemo() async {
        DemoMode.enter()
        await DemoBackend.shared.restart()
        phase = .launching
        useInstance(DemoMode.instance, config: DemoMode.authConfig)
        await bootstrap()
    }

    /// Back to the server chosen before the demo, signed out.
    private func leaveDemo() {
        DemoMode.leave()
        useInstance(Preferences.instance ?? .cloud, config: nil)
    }
    #endif

    func deleteAccount(password: String?, code: String?) async throws {
        struct Body: Encodable, Sendable {
            let password: String?
            let code: String?
        }
        try await api.delete("v1/me", body: Body(password: password?.isEmpty == true ? nil : password, code: code?.isEmpty == true ? nil : code), workspace: .none)
        await onEnded?(instance)
        await api.clearTokens()
        reset()
    }

    private func sessionExpired(for client: APIClient) async {
        guard client === api, phase != .signedOut else { return }
        await onEnded?(instance)
        reset()
    }

    private func reset() {
        user = nil
        memberships = []
        workspace = nil
        phase = .signedOut
        let client = api
        Task { await client.setWorkspace(nil) }
    }
}

enum SignInError: LocalizedError {
    case humanCheckUnavailable

    var errorDescription: String? {
        switch self {
        case .humanCheckUnavailable:
            "This server checks sign-ins with a captcha the app can't show. Use another sign-in option, or sign in on the web."
        }
    }
}
