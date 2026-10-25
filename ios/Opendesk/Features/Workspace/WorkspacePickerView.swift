import SwiftUI

/// Pick a workspace (or make the first one), on the painted frame sign-in uses.
struct WorkspacePickerView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var creating = false

    private var session: SessionStore { env.session }

    var body: some View {
        NavigationStack {
            AuthScene(painting: .plains) {
                VStack(alignment: .leading, spacing: 16) {
                    ODWordmark(size: 22, color: .white)
                    HeroTitle(
                        title: session.memberships.isEmpty ? "Create a workspace" : "Choose a workspace",
                        subtitle: session.memberships.isEmpty
                            ? "A workspace holds your team's inboxes, tickets and help center."
                            : "Signed in as \(session.user?.email ?? "").",
                        size: 30
                    )
                }
                .padding(.top, 14)
            } panel: {
                if !session.memberships.isEmpty {
                    WorkspaceList()
                }

                if session.memberships.isEmpty {
                    CreateWorkspaceForm()
                } else {
                    Button {
                        creating = true
                    } label: {
                        Label("New workspace", systemImage: "plus")
                    }
                    .buttonStyle(OutlineButtonStyle())
                }

                Button("Sign out") {
                    Task { await session.signOut() }
                }
                .font(ODFont.sans(14, weight: .medium))
                .foregroundStyle(Theme.fg3)
                .frame(maxWidth: .infinity)
                .frame(height: 36)
            }
            .toolbar(.hidden, for: .navigationBar)
            .sheet(isPresented: $creating) {
                NavigationStack {
                    ScrollView { CreateWorkspaceForm().padding(20) }
                        .background(Theme.chrome.ignoresSafeArea())
                        .navigationTitle("New workspace")
                        .navigationBarTitleDisplayMode(.inline)
                        .toolbar {
                            ToolbarItem(placement: .cancellationAction) { Button("Cancel") { creating = false } }
                        }
                }
                .presentationDetents([.medium])
            }
        }
    }
}

/// The workspaces the user belongs to, the open one checked.
struct WorkspaceList: View {
    @Environment(AppEnvironment.self) private var env
    var onPick: () -> Void = {}

    var body: some View {
        Panel {
            ForEach(Array(env.session.memberships.enumerated()), id: \.element.id) { index, membership in
                if index > 0 { Hairline(inset: 58) }
                Button {
                    Haptics.selection()
                    if env.session.phase == .ready {
                        env.switchWorkspace(to: membership)
                    } else {
                        env.session.select(membership)
                    }
                    onPick()
                } label: {
                    HStack(spacing: 12) {
                        WorkspaceAvatar(workspace: membership.workspace, size: 32)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(membership.workspace.name).font(ODFont.sans(15.5, weight: .medium)).foregroundStyle(Theme.fg)
                            Text(membership.role.title).font(ODFont.footnote).foregroundStyle(Theme.fg3)
                        }
                        Spacer()
                        if membership.id == env.session.workspace?.id {
                            Image(systemName: "checkmark").font(.system(size: 14, weight: .semibold)).foregroundStyle(Theme.fg)
                        } else {
                            Chevron()
                        }
                    }
                    .padding(.horizontal, 14)
                    .padding(.vertical, 11)
                    .contentShape(Rectangle())
                }
                .buttonStyle(RowPressStyle())
            }
        }
    }
}

struct WorkspaceAvatar: View {
    let workspace: Workspace
    var size: CGFloat = 28

    var body: some View {
        let tone = Theme.avatarTone(for: workspace.id)
        ZStack {
            RoundedRectangle(cornerRadius: size * 0.26, style: .continuous).fill(tone.soft)
            Text(Format.initials(workspace.name))
                .font(ODFont.sans(size * 0.38, weight: .semibold))
                .foregroundStyle(tone.text)
            if let raw = workspace.avatarURL, let url = URL(string: raw) {
                AsyncImage(url: url) { phase in
                    if case let .success(image) = phase { image.resizable().scaledToFill() }
                }
                .clipShape(RoundedRectangle(cornerRadius: size * 0.26, style: .continuous))
            }
        }
        .frame(width: size, height: size)
        .overlay(RoundedRectangle(cornerRadius: size * 0.26, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}

struct CreateWorkspaceForm: View {
    @Environment(AppEnvironment.self) private var env
    @State private var name = ""
    @State private var busy = false
    @State private var error: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 14) {
            ODTextField(label: "Workspace name", text: $name, prompt: "Acme support", contentType: .organizationName, submitLabel: .done) {
                Task { await create() }
            }
            FormError(text: error)
            Button {
                Task { await create() }
            } label: {
                ButtonLabel(title: "Create workspace", busy: busy)
            }
            .buttonStyle(InkButtonStyle())
            .disabled(name.trimmingCharacters(in: .whitespaces).isEmpty || busy)
        }
    }

    private func create() async {
        guard !busy else { return }
        busy = true
        error = nil
        defer { busy = false }
        do {
            try await env.session.createWorkspace(name: name.trimmingCharacters(in: .whitespaces))
            Haptics.success()
        } catch {
            self.error = error.userMessage
        }
    }
}
