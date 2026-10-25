import Foundation

/// Which `OD-Workspace` a request carries.
enum WorkspaceScope: Sendable {
    /// The workspace the app has open.
    case current
    /// No workspace header (account routes).
    case none
    /// A specific workspace (badge counts across workspaces).
    case id(String)
}

struct APIRequest: Sendable {
    var method: String
    var path: String
    var query: [URLQueryItem] = []
    var body: Data?
    var contentType: String? = "application/json"
    var authorized = true
    var workspace: WorkspaceScope = .current
    var idempotencyKey: String?
    var accept = "application/json"
    var timeout: TimeInterval = 30
}

/// One server-sent event of a streamed answer.
struct SSEEvent: Sendable {
    var event: String
    var data: String

    func json() -> JSONValue? { try? JSONValue.parse(Data(data.utf8)) }
}

/// The API for one instance. Holds the access token in memory and the
/// refresh token in the Keychain; refreshes once for every caller at a time
/// and retries a request once when the access token expired underneath it.
actor APIClient {
    nonisolated let instance: Instance
    private let session: URLSession

    private var accessToken: String?
    private var accessExpiry: Date = .distantPast
    private var workspaceID: String?
    private var refreshing: Task<Void, Error>?
    private var onSignedOut: (@Sendable () async -> Void)?

    init(instance: Instance) {
        self.instance = instance
        let configuration = URLSessionConfiguration.default
        configuration.httpCookieStorage = nil
        configuration.httpShouldSetCookies = false
        configuration.httpCookieAcceptPolicy = .never
        configuration.urlCache = nil
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.waitsForConnectivity = false
        session = URLSession(configuration: configuration)
    }

    // MARK: Session state

    private var refreshAccount: String { "refresh." + instance.storageKey }

    nonisolated var hasRefreshToken: Bool { KeychainStore.get("refresh." + instance.storageKey) != nil }

    func setWorkspace(_ id: String?) { workspaceID = id }
    func currentWorkspace() -> String? { workspaceID }

    func setSignedOutHandler(_ handler: @escaping @Sendable () async -> Void) { onSignedOut = handler }

    /// Stores the tokens of a session-issuing answer.
    func adopt(_ result: AuthResult) {
        if let token = result.accessToken {
            accessToken = token
            accessExpiry = Date.now.addingTimeInterval(TimeInterval(max((result.expiresIn ?? 900) - 30, 30)))
        }
        if let refresh = result.refreshToken, !refresh.isEmpty {
            KeychainStore.set(refresh, for: refreshAccount)
        }
    }

    /// A fresh access token (for the realtime token call's siblings and image loads).
    func validAccessToken() async throws -> String {
        if let accessToken, accessExpiry > .now { return accessToken }
        try await refresh()
        guard let accessToken else { throw APIError.signedOut }
        return accessToken
    }

    /// Ends the session on the server (`refresh_token` in the body), then forgets it here.
    func signOut() async {
        if let refresh = KeychainStore.get(refreshAccount) {
            struct Body: Encodable { let refresh_token: String }
            var request = APIRequest(method: "POST", path: "v1/auth/logout", body: try? APICoding.encoder.encode(Body(refresh_token: refresh)))
            request.workspace = .none
            request.timeout = 10
            _ = try? await perform(request, allowRefresh: false)
        }
        clearTokens()
    }

    func clearTokens() {
        accessToken = nil
        accessExpiry = .distantPast
        refreshing?.cancel()
        refreshing = nil
        KeychainStore.remove(refreshAccount)
    }

    /// Single-flight refresh: every caller waiting on an expired token shares one call.
    func refresh() async throws {
        if let refreshing { return try await refreshing.value }
        guard let token = KeychainStore.get(refreshAccount) else { throw APIError.signedOut }
        let task = Task { try await self.performRefresh(token) }
        refreshing = task
        defer { refreshing = nil }
        try await task.value
    }

    private func performRefresh(_ token: String) async throws {
        struct Body: Encodable { let refresh_token: String }
        var request = APIRequest(method: "POST", path: "v1/auth/refresh", body: try APICoding.encoder.encode(Body(refresh_token: token)))
        request.authorized = false
        request.workspace = .none
        do {
            let (data, _) = try await perform(request, allowRefresh: false)
            let result = try APICoding.decoder.decode(AuthResult.self, from: data)
            guard result.accessToken != nil else { throw APIError.signedOut }
            adopt(result)
        } catch let error as APIError where error.status == 401 || error.status == 403 {
            // The refresh token was revoked, rotated elsewhere or expired.
            clearTokens()
            let handler = onSignedOut
            Task { await handler?() }
            throw APIError.signedOut
        }
    }

    // MARK: Requests

    func send<T: Decodable & Sendable>(_ request: APIRequest, as type: T.Type = T.self) async throws -> T {
        let (data, _) = try await perform(request, allowRefresh: true)
        if T.self == EmptyBody.self { return EmptyBody() as! T }
        if data.isEmpty, let empty = EmptyBody() as? T { return empty }
        do {
            return try APICoding.decoder.decode(T.self, from: data)
        } catch {
            throw APIError.decoding
        }
    }

    func get<T: Decodable & Sendable>(_ path: String, query: [URLQueryItem] = [], workspace: WorkspaceScope = .current, authorized: Bool = true) async throws -> T {
        var request = APIRequest(method: "GET", path: path, query: query, body: nil, contentType: nil)
        request.workspace = workspace
        request.authorized = authorized
        return try await send(request)
    }

    func post<T: Decodable & Sendable>(_ path: String, body: some Encodable & Sendable = [String: String](), workspace: WorkspaceScope = .current, authorized: Bool = true, idempotencyKey: String? = nil) async throws -> T {
        var request = APIRequest(method: "POST", path: path, body: try APICoding.encoder.encode(body))
        request.workspace = workspace
        request.authorized = authorized
        request.idempotencyKey = idempotencyKey
        return try await send(request)
    }

    func patch<T: Decodable & Sendable>(_ path: String, body: some Encodable & Sendable, workspace: WorkspaceScope = .current) async throws -> T {
        var request = APIRequest(method: "PATCH", path: path, body: try APICoding.encoder.encode(body))
        request.workspace = workspace
        return try await send(request)
    }

    func delete(_ path: String, workspace: WorkspaceScope = .current) async throws {
        var request = APIRequest(method: "DELETE", path: path, body: nil, contentType: nil)
        request.workspace = workspace
        let _: EmptyBody = try await send(request)
    }

    func delete(_ path: String, body: some Encodable & Sendable, workspace: WorkspaceScope = .current) async throws {
        var request = APIRequest(method: "DELETE", path: path, body: try APICoding.encoder.encode(body))
        request.workspace = workspace
        let _: EmptyBody = try await send(request)
    }

    /// `multipart/form-data` with one `file` part.
    func upload<T: Decodable & Sendable>(_ path: String, data fileData: Data, filename: String, mimeType: String) async throws -> T {
        let boundary = "od-" + UUID().uuidString
        var body = Data()
        let safeName = filename.replacingOccurrences(of: "\"", with: "")
        body.append(Data("--\(boundary)\r\n".utf8))
        body.append(Data("Content-Disposition: form-data; name=\"file\"; filename=\"\(safeName)\"\r\n".utf8))
        body.append(Data("Content-Type: \(mimeType)\r\n\r\n".utf8))
        body.append(fileData)
        body.append(Data("\r\n--\(boundary)--\r\n".utf8))
        var request = APIRequest(method: "POST", path: path, body: body, contentType: "multipart/form-data; boundary=\(boundary)")
        request.timeout = 120
        return try await send(request)
    }

    /// A streamed run (`text/event-stream`), one event at a time.
    func stream(_ path: String, body: some Encodable & Sendable) async throws -> AsyncThrowingStream<SSEEvent, Error> {
        var request = APIRequest(method: "POST", path: path, body: try APICoding.encoder.encode(body))
        request.accept = "text/event-stream"
        request.timeout = 300
        var urlRequest = try await build(request)
        #if DEBUG
        if DemoMode.isOn { return try await DemoBackend.shared.stream(urlRequest) }
        #endif
        var (bytes, response) = try await session.bytes(for: urlRequest)
        if let http = response as? HTTPURLResponse, http.statusCode == 401 {
            try await refresh()
            urlRequest = try await build(request)
            (bytes, response) = try await session.bytes(for: urlRequest)
        }
        guard let http = response as? HTTPURLResponse else { throw APIError.decoding }
        guard (200 ..< 300).contains(http.statusCode) else {
            var data = Data()
            for try await byte in bytes { data.append(byte) }
            throw APIError.parse(status: http.statusCode, data: data)
        }
        let isJSON = (http.value(forHTTPHeaderField: "Content-Type") ?? "").contains("application/json")
        // The body moves into the reading task and is not touched here again.
        let body = bytes
        return AsyncThrowingStream { continuation in
            let task = Task {
                do {
                    if isJSON {
                        // A server that answers in one piece: hand it over as a single `result` event.
                        var data = Data()
                        for try await byte in body { data.append(byte) }
                        continuation.yield(SSEEvent(event: "result", data: String(decoding: data, as: UTF8.self)))
                    } else {
                        var event = "message"
                        var lines: [String] = []
                        for try await line in body.lines {
                            if line.hasPrefix("event:") {
                                event = line.dropFirst(6).trimmingCharacters(in: .whitespaces)
                            } else if line.hasPrefix("data:") {
                                var value = String(line.dropFirst(5))
                                if value.hasPrefix(" ") { value.removeFirst() }
                                lines.append(value)
                                // `lines` drops the blank separator, so dispatch per data line.
                                continuation.yield(SSEEvent(event: event, data: lines.joined(separator: "\n")))
                                lines.removeAll()
                                event = "message"
                            }
                        }
                    }
                    continuation.finish()
                } catch {
                    continuation.finish(throwing: error)
                }
            }
            continuation.onTermination = { _ in task.cancel() }
        }
    }

    // MARK: Plumbing

    private func build(_ request: APIRequest) async throws -> URLRequest {
        var components = URLComponents(url: instance.apiURL.appending(path: request.path), resolvingAgainstBaseURL: false)
        if !request.query.isEmpty { components?.queryItems = request.query }
        guard let url = components?.url else { throw APIError.decoding }
        var urlRequest = URLRequest(url: url, timeoutInterval: request.timeout)
        urlRequest.httpMethod = request.method
        urlRequest.httpBody = request.body
        urlRequest.httpShouldHandleCookies = false
        urlRequest.setValue(request.accept, forHTTPHeaderField: "Accept")
        urlRequest.setValue(AppInfo.clientHeader, forHTTPHeaderField: "OD-Client")
        if request.body != nil, let contentType = request.contentType {
            urlRequest.setValue(contentType, forHTTPHeaderField: "Content-Type")
        }
        if let key = request.idempotencyKey { urlRequest.setValue(key, forHTTPHeaderField: "Idempotency-Key") }
        switch request.workspace {
        case .current: if let workspaceID { urlRequest.setValue(workspaceID, forHTTPHeaderField: "OD-Workspace") }
        case let .id(id): urlRequest.setValue(id, forHTTPHeaderField: "OD-Workspace")
        case .none: break
        }
        if request.authorized {
            let token = try await validAccessToken()
            urlRequest.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        }
        return urlRequest
    }

    private func perform(_ request: APIRequest, allowRefresh: Bool) async throws -> (Data, HTTPURLResponse) {
        let urlRequest = try await build(request)
        let (data, http) = try await transport(urlRequest)
        if (200 ..< 300).contains(http.statusCode) { return (data, http) }
        let error = APIError.parse(status: http.statusCode, data: data)
        // Only an expired or missing token refreshes; invalid_credentials and friends answer the request itself.
        if allowRefresh, request.authorized, error.isUnauthenticated {
            accessToken = nil
            accessExpiry = .distantPast
            try await refresh()
            let retried = try await build(request)
            let (data, http) = try await transport(retried)
            if (200 ..< 300).contains(http.statusCode) { return (data, http) }
            throw APIError.parse(status: http.statusCode, data: data)
        }
        throw error
    }

    private func transport(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        #if DEBUG
        // The demo answers here, so every store and screen runs its real code path.
        if DemoMode.isOn { return try await DemoBackend.shared.respond(to: request) }
        #endif
        do {
            let (data, response) = try await session.data(for: request)
            guard let http = response as? HTTPURLResponse else { throw APIError.decoding }
            return (data, http)
        } catch let error as URLError {
            if error.code == .cancelled { throw CancellationError() }
            throw APIError.transport(error)
        }
    }
}
