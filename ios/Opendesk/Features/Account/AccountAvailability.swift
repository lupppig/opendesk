import SwiftUI
import UIKit

extension Availability {
    var accountTone: Tone {
        switch self {
        case .online: .green
        case .away: .amber
        case .offline: .gray
        }
    }

    var accountHint: String {
        switch self {
        case .online: "You get new tickets and chats"
        case .away: "Assignment skips you for now"
        case .offline: "Assignment skips you until you're back"
        }
    }
}

/// The signed-in person's photo, or the photo they just picked while the server's copy loads.
struct AccountAvatar: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    var size: CGFloat = 58

    var body: some View {
        let user = env.session.user
        ZStack {
            if let preview = store.avatarPreview, user?.avatarURL != nil {
                Image(uiImage: preview)
                    .resizable()
                    .scaledToFill()
                    .frame(width: size, height: size)
                    .clipShape(Circle())
                    .transition(.opacity)
            } else {
                Avatar(name: user?.displayName ?? "", url: user?.avatarURL, seed: user?.id ?? "", size: size)
            }
        }
        .overlay(Circle().strokeBorder(Theme.line, lineWidth: 1))
    }
}

/// Who you are here: photo, name, email, the workspace and your role, and your availability.
struct AccountProfileCard: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let session = env.session
        Panel {
            NavigationLink(value: AccountRoute.profile) {
                HStack(spacing: 14) {
                    AccountAvatar(size: 58)
                        .overlay(alignment: .bottomTrailing) { AccountPresenceDot() }
                    VStack(alignment: .leading, spacing: 3) {
                        Text(session.user?.displayName ?? "")
                            .font(ODFont.sans(18, weight: .semibold))
                            .tracking(-0.3)
                            .foregroundStyle(Theme.fg)
                            .lineLimit(1)
                        Text(session.user?.email ?? "")
                            .font(ODFont.sans(14))
                            .foregroundStyle(Theme.fg3)
                            .lineLimit(1)
                        if let membership = session.workspace {
                            HStack(spacing: 6) {
                                TonePill(text: membership.workspace.name, tone: .gray, height: 21) {
                                    WorkspaceAvatar(workspace: membership.workspace, size: 13)
                                }
                                TonePill(text: membership.role.title, tone: membership.role.accountTone, height: 21)
                            }
                            .padding(.top, 5)
                        }
                    }
                    Spacer(minLength: 4)
                    Chevron()
                }
                .padding(16)
                .contentShape(Rectangle())
            }
            .buttonStyle(RowPressStyle())
            Hairline()
            AccountAvailabilityPicker()
                .padding(.horizontal, 16)
                .padding(.vertical, 14)
        }
    }
}

/// A dot on the avatar in the availability's tone.
private struct AccountPresenceDot: View {
    @Environment(AppEnvironment.self) private var env

    var body: some View {
        let value = env.directory.member(env.session.user?.id)?.availability ?? .online
        Circle()
            .fill(value == .offline ? Theme.fg4 : value.accountTone.solid)
            .frame(width: 14, height: 14)
            .overlay(Circle().strokeBorder(Theme.surface, lineWidth: 2.5))
            .offset(x: 1, y: 1)
            .motion(Motion.snappy, value: value)
            .accessibilityHidden(true)
    }
}

/// Online, away or offline, saved at once and shown to the team live.
struct AccountAvailabilityPicker: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @State private var pending: Availability?

    private var current: Availability {
        pending ?? env.directory.member(env.session.user?.id)?.availability ?? .online
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack {
                Text("Availability")
                    .font(ODFont.sans(13.5, weight: .medium))
                    .foregroundStyle(Theme.fg2)
                Spacer()
                Text(current.accountHint)
                    .font(ODFont.footnote)
                    .foregroundStyle(Theme.fg3)
                    .contentTransition(.opacity)
                    .motion(Motion.fade, value: current)
            }
            AccountSegmented(
                options: Availability.allCases,
                selection: Binding(get: { current }, set: { set($0) }),
                label: \.title,
                dot: { $0 == .offline ? Theme.fg4 : $0.accountTone.solid }
            )
        }
    }

    private func set(_ value: Availability) {
        pending = value
        Task {
            do {
                try await store.setAvailability(value)
            } catch {
                env.toasts.error(error)
            }
            pending = nil
        }
    }
}

extension Role {
    var accountTone: Tone {
        switch self {
        case .owner: .violet
        case .admin: .indigo
        case .agent: .blue
        case .viewer: .gray
        }
    }

    var accountSummary: String {
        switch self {
        case .owner: "Owns the workspace and its billing"
        case .admin: "Manages settings, members and teams"
        case .agent: "Works tickets and conversations"
        case .viewer: "Reads tickets, can't change them"
        }
    }
}
