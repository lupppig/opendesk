import Foundation
import Observation
import UIKit

/// The signed-in person's own settings: profile and photo, availability, the
/// workspace profile customers see, security (password, two-factor, passkeys,
/// sessions), and their saved replies and views. The Account tab owns one and
/// hands it down; every call it makes is the account's own (contracts/api.md
/// "Auth and account").
@MainActor
@Observable
final class AccountStore {
    /// An action that needs a fresh sign-in (`reauth_required`) or one that passed
    /// two-factor (`mfa_required`): the screen asks for the password, then retries.
    struct ReauthPrompt: Identifiable {
        let id = UUID()
        var needsMFA: Bool
        var retry: @MainActor () async throws -> Void
    }

    enum ReauthStep: Equatable {
        case done
        case code(token: String)
    }

    // Profile
    private(set) var profile: MemberProfile?
    private(set) var profileLoading = false
    /// The photo just uploaded, shown at once while the server's copy loads.
    private(set) var avatarPreview: UIImage?
    private(set) var avatarBusy = false

    // Security (set by AccountSecurity.swift)
    var sessions: [SessionInfo] = []
    var sessionsLoaded = false
    var sessionsError: String?
    var passkeys: [Passkey] = []
    var passkeysLoaded = false
    var passkeysError: String?
    var reauthPrompt: ReauthPrompt?

    // Library (set by AccountLibrary.swift)
    var replies: [AccountSavedReply] = []
    var repliesLoaded = false
    var repliesError: String?
    var views: [AccountTicketView] = []
    var viewsLoaded = false
    var viewsError: String?

    @ObservationIgnored private(set) weak var session: SessionStore?
    @ObservationIgnored private(set) weak var directory: DirectoryStore?
    @ObservationIgnored private var workspaceID: String?
    /// The session to end once a second sign-in with two-factor completes.
    @ObservationIgnored var pendingPreviousSession: String?

    var api: APIClient? { session?.api }

    func attach(session: SessionStore, directory: DirectoryStore) {
        self.session = session
        self.directory = directory
    }

    /// The open workspace changed: forget what belongs to the last one.
    func workspaceChanged(_ id: String?) {
        guard id != workspaceID else { return }
        workspaceID = id
        profile = nil
        replies = []
        repliesLoaded = false
        repliesError = nil
        views = []
        viewsLoaded = false
        viewsError = nil
    }

    var me: User? { session?.user }

    // MARK: Profile

    func updateName(_ name: String) async throws {
        guard let session else { return }
        try await session.updateName(name.trimmingCharacters(in: .whitespacesAndNewlines))
    }

    /// `PUT /v1/me/avatar`: a square JPEG of the picked photo.
    func uploadAvatar(_ image: UIImage) async throws {
        guard let api, let session else { return }
        let square = Self.squared(image, side: 512)
        guard let data = square.jpegData(compressionQuality: 0.86) else { throw APIError.decoding }
        avatarBusy = true
        defer { avatarBusy = false }
        let boundary = "od-" + UUID().uuidString
        var body = Data()
        body.append(Data("--\(boundary)\r\n".utf8))
        body.append(Data("Content-Disposition: form-data; name=\"file\"; filename=\"avatar.jpg\"\r\n".utf8))
        body.append(Data("Content-Type: image/jpeg\r\n\r\n".utf8))
        body.append(data)
        body.append(Data("\r\n--\(boundary)--\r\n".utf8))
        var request = APIRequest(method: "PUT", path: "v1/me/avatar", body: body, contentType: "multipart/form-data; boundary=\(boundary)")
        request.workspace = .none
        request.timeout = 120
        let _: User = try await api.send(request)
        avatarPreview = square
        try? await session.loadMe()
    }

    func removeAvatar() async throws {
        guard let api, let session else { return }
        avatarBusy = true
        defer { avatarBusy = false }
        var request = APIRequest(method: "DELETE", path: "v1/me/avatar", body: nil, contentType: nil)
        request.workspace = .none
        let _: User = try await api.send(request)
        avatarPreview = nil
        try? await session.loadMe()
    }

    /// Sends the confirmation email again (`POST /v1/auth/email/resend`).
    func resendVerification() async throws {
        let _: EmptyBody = try await authCall("v1/auth/email/resend", body: [String: String]())
    }

    func setAvailability(_ value: Availability) async throws {
        guard let session else { return }
        let member = try await session.setAvailability(value)
        directory?.setMember(member)
    }

    /// The workspace profile from the member list (it is not on `/v1/me`).
    func loadProfile() async {
        guard let api, let me = session?.user?.id else { return }
        profileLoading = true
        defer { profileLoading = false }
        guard let page: Page<AccountMemberAnswer> = try? await api.get("v1/members", query: [URLQueryItem(name: "limit", value: "100")]) else { return }
        profile = page.data.first(where: { $0.member.id == me })?.profile ?? .empty
    }

    func saveProfile(_ next: MemberProfile) async throws {
        guard let api else { return }
        struct Body: Encodable, Sendable {
            let title: String
            let phone: String
            let pronouns: String
            let signature_html: String
        }
        let body = Body(title: next.title, phone: next.phone, pronouns: next.pronouns, signature_html: next.signatureHTML)
        let answer: AccountMemberAnswer = try await api.patch("v1/members/me/profile", body: body)
        profile = answer.profile ?? next
        directory?.setMember(answer.member)
    }

    // MARK: Account

    func deleteAccount(password: String, code: String) async throws {
        guard let session else { return }
        try await session.deleteAccount(password: password, code: code)
    }

    // MARK: Plumbing

    /// Runs an action; a `reauth_required` or `mfa_required` answer asks for the
    /// password first and runs it again. Other failures are thrown.
    func guarded(_ action: @escaping @MainActor () async throws -> Void) async throws {
        do {
            try await action()
        } catch let error as APIError where error.code == "reauth_required" || error.code == "mfa_required" {
            reauthPrompt = ReauthPrompt(needsMFA: error.code == "mfa_required", retry: action)
        }
    }

    /// Calls under `/v1/auth/` that need a signed-in person.
    func authCall<T: Decodable & Sendable>(_ path: String, body: some Encodable & Sendable) async throws -> T {
        guard let api else { throw APIError.signedOut }
        return try await api.post(path, body: body, workspace: .none)
    }

    private static func squared(_ image: UIImage, side: CGFloat) -> UIImage {
        let size = image.size
        let crop = min(size.width, size.height)
        let target = min(side, crop)
        let format = UIGraphicsImageRendererFormat()
        format.scale = 1
        return UIGraphicsImageRenderer(size: CGSize(width: target, height: target), format: format).image { _ in
            let scale = target / crop
            let drawn = CGSize(width: size.width * scale, height: size.height * scale)
            image.draw(in: CGRect(x: (target - drawn.width) / 2, y: (target - drawn.height) / 2, width: drawn.width, height: drawn.height))
        }
    }
}
