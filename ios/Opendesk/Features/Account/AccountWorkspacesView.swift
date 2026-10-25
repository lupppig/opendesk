import SwiftUI

/// The workspaces you belong to: switch between them, or start a new one.
struct AccountWorkspacesView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var creating = false

    var body: some View {
        AccountPage(title: "Workspaces") {
            VStack(alignment: .leading, spacing: 8) {
                SectionLabel(env.session.memberships.count == 1 ? "Your workspace" : "Your \(env.session.memberships.count) workspaces")
                    .padding(.horizontal, 4)
                WorkspaceList()
                Text("Switching keeps you signed in. Notifications and the inbox follow the workspace you open.")
                    .font(ODFont.footnote)
                    .foregroundStyle(Theme.fg3)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.horizontal, 4)
                    .padding(.top, 2)
            }
            AccountSection {
                Button {
                    Haptics.tap()
                    creating = true
                } label: {
                    AccountRow(symbol: "plus", tone: .orange, title: "Create a workspace", subtitle: "A separate helpdesk with its own mailboxes and people")
                }
                .buttonStyle(RowPressStyle())
            }
        }
        .sheet(isPresented: $creating) {
            AccountCreateWorkspaceSheet()
                .presentationDetents([.height(330)])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
        .onAppear(perform: handleDemo)
        .onChange(of: env.router.demoAction) { _, _ in handleDemo() }
    }

    private func handleDemo() {
        if AccountDemo.take("new-workspace", from: env.router) { creating = true }
    }
}

private struct AccountCreateWorkspaceSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            AccountSheetHeader(title: "New workspace", subtitle: "You'll be its owner. You can invite people from the web app.") { dismiss() }
            CreateWorkspaceForm()
            Spacer(minLength: 0)
        }
        .padding(22)
        .background(Theme.surface)
        .onChange(of: env.session.workspace?.id) { _, _ in
            env.toasts.show("Workspace created", kind: .success)
            dismiss()
        }
    }
}
