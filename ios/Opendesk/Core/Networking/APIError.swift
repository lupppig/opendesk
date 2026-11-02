import Foundation

/// An RFC 9457 problem from the API, or a transport failure.
struct APIError: LocalizedError, Sendable, Equatable {
    var status: Int
    var code: String?
    var title: String?
    var detail: String?
    var fieldErrors: [FieldError]

    struct FieldError: Decodable, Sendable, Equatable {
        var message: String?
        var location: String?
    }

    /// No answer at all (offline, DNS, TLS, timeout).
    static func transport(_ error: URLError) -> APIError {
        let message: String
        switch error.code {
        case .notConnectedToInternet, .networkConnectionLost, .dataNotAllowed:
            message = "You're offline. Check your connection and try again."
        case .timedOut:
            message = "The server took too long to answer."
        case .cannotFindHost, .cannotConnectToHost, .dnsLookupFailed:
            message = "Couldn't reach the server."
        case .secureConnectionFailed, .serverCertificateUntrusted, .serverCertificateHasBadDate, .serverCertificateNotYetValid:
            message = "The server's certificate isn't trusted."
        default:
            message = "Something went wrong with the connection."
        }
        return APIError(status: 0, code: "transport", title: nil, detail: message, fieldErrors: [])
    }

    static let decoding = APIError(status: 0, code: "decoding", title: nil, detail: "The server sent an answer this app doesn't understand. Update the app or try again.", fieldErrors: [])
    static let signedOut = APIError(status: 401, code: "unauthenticated", title: nil, detail: "Your session ended. Sign in again.", fieldErrors: [])

    static func parse(status: Int, data: Data) -> APIError {
        struct Problem: Decodable {
            var title: String?
            var detail: String?
            var code: String?
            var errors: [FieldError]?
        }
        let problem = try? JSONDecoder().decode(Problem.self, from: data)
        return APIError(status: status, code: problem?.code, title: problem?.title, detail: problem?.detail, fieldErrors: problem?.errors ?? [])
    }

    var isTransport: Bool { status == 0 && code == "transport" }
    var isUnauthenticated: Bool { status == 401 && (code == nil || code == "unauthenticated") }

    /// What to show a person. Stable codes get plain words; otherwise the server's detail.
    var errorDescription: String? {
        switch code {
        case "invalid_credentials": return "That email and password don't match."
        case "invalid_code": return "That code didn't work. Try a fresh one."
        case "mfa_required": return "Sign in again with your two-factor code to do this."
        case "reauth_required": return "This needs a sign-in from the last 12 hours. Sign in again to confirm it's you."
        case "captcha_failed": return "This device couldn't be verified. Try again, or sign in another way."
        case "rate_limited": return "Too many tries. Wait a moment and try again."
        case "email_taken": return "An account with that email already exists."
        case "signup_disabled": return "This server only accepts invited people."
        case "weak_password", "password_breached": return detail ?? "Choose a different password."
        case "workspace_required": return "Pick a workspace first."
        case "forbidden": return "You don't have access to that."
        case "not_found": return "That isn't here any more."
        case "sole_owner": return "Transfer or delete the workspaces you own with other members first."
        case "ai_balance_empty": return "The AI balance is used up. An owner can add a top-up on the web."
        case "ai_off", "ai_disabled", "assistant_off": return "Ollie is turned off for this workspace."
        case "assistant_busy": return "Ollie is still working on the last message."
        case "suggestion_not_pending": return "That suggestion was already handled."
        case "approval_not_pending": return "That action was already decided or expired."
        case "field_required": return detail ?? "Fill in the required fields on the web before solving."
        default: break
        }
        if let detail, !detail.isEmpty { return detail }
        if let first = fieldErrors.first?.message, !first.isEmpty { return first }
        switch status {
        case 401: return "Your session ended. Sign in again."
        case 403: return "You don't have access to that."
        case 404: return "That isn't here any more."
        case 409: return "Someone changed this at the same time. Refresh and try again."
        case 429: return "Too many requests. Wait a moment."
        case 500...: return "The server had a problem. Try again in a moment."
        default: return title ?? "Something went wrong."
        }
    }
}

extension Error {
    /// A person-facing line for any error.
    var userMessage: String {
        if let api = self as? APIError { return api.errorDescription ?? "Something went wrong." }
        if let url = self as? URLError { return APIError.transport(url).errorDescription ?? "" }
        if let localized = self as? LocalizedError, let text = localized.errorDescription { return text }
        return "Something went wrong."
    }

    var isCancellation: Bool {
        if self is CancellationError { return true }
        if let url = self as? URLError, url.code == .cancelled { return true }
        return false
    }
}
