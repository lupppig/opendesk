import PhotosUI
import SwiftUI
import UIKit

/// Your name, photo and email, and the profile customers see in this workspace.
struct AccountProfileView: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store

    @State private var name = ""
    @State private var savingName = false
    @State private var photo: PhotosPickerItem?
    @State private var pickingPhoto = false
    @State private var resending = false
    @State private var resent = false

    var body: some View {
        AccountPage(title: "Profile") {
            photoHeader
            nameSection
            emailSection
            AccountMemberProfileSection()
        }
        .onAppear { name = env.session.user?.name ?? "" }
        .photosPicker(isPresented: $pickingPhoto, selection: $photo, matching: .images, preferredItemEncoding: .compatible)
        .onChange(of: photo) { _, item in
            guard let item else { return }
            upload(item)
        }
        .onAppear(perform: handleDemo)
        .onChange(of: env.router.demoAction) { _, _ in handleDemo() }
    }

    // MARK: Photo

    private var photoHeader: some View {
        VStack(spacing: 12) {
            AccountAvatar(size: 88)
                .overlay {
                    if store.avatarBusy {
                        Circle().fill(.black.opacity(0.35))
                        ProgressView().tint(.white)
                    }
                }
                .overlay(alignment: .bottomTrailing) {
                    photoMenu {
                        Image(systemName: "camera.fill")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(Theme.fg)
                            .frame(width: 32, height: 32)
                            .glassEffect(.regular.interactive(), in: Circle())
                    }
                    .offset(x: 4, y: 4)
                }
                .motion(Motion.fade, value: store.avatarBusy)
            photoMenu {
                Text(env.session.user?.avatarURL == nil ? "Add a photo" : "Change photo")
                    .font(ODFont.sans(14, weight: .medium))
                    .foregroundStyle(Theme.fg2)
            }
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 8)
    }

    private func photoMenu<Label: View>(@ViewBuilder label: () -> Label) -> some View {
        Menu {
            Button("Choose a photo", systemImage: "photo.on.rectangle") { pickingPhoto = true }
            if env.session.user?.avatarURL != nil {
                Button("Remove photo", systemImage: "trash", role: .destructive) { remove() }
            }
        } label: {
            label()
        }
        .disabled(store.avatarBusy)
        .accessibilityLabel("Photo")
    }

    private func upload(_ item: PhotosPickerItem) {
        Task {
            defer { photo = nil }
            do {
                guard let data = try await item.loadTransferable(type: Data.self), let image = UIImage(data: data) else {
                    env.toasts.show("That photo couldn't be read. Try another one.", kind: .error)
                    return
                }
                try await store.uploadAvatar(image)
                Haptics.success()
                env.toasts.show("Photo updated", kind: .success)
            } catch {
                env.toasts.error(error)
            }
        }
    }

    private func remove() {
        Task {
            do {
                try await store.removeAvatar()
                env.toasts.show("Photo removed", kind: .success)
            } catch {
                env.toasts.error(error)
            }
        }
    }

    // MARK: Name

    private var nameChanged: Bool {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        return !trimmed.isEmpty && trimmed != env.session.user?.name
    }

    private var nameSection: some View {
        VStack(alignment: .leading, spacing: 10) {
            ODTextField(label: "Name", text: $name, prompt: "Your name", contentType: .name, submitLabel: .done) { saveName() }
            if nameChanged {
                Button(action: saveName) {
                    ButtonLabel(title: "Save name", busy: savingName)
                }
                .buttonStyle(InkButtonStyle(height: 42))
                .disabled(savingName)
                .transition(.opacity.combined(with: .move(edge: .top)))
            }
        }
        .motion(Motion.snappy, value: nameChanged)
    }

    private func saveName() {
        guard nameChanged else { return }
        savingName = true
        Task {
            do {
                try await store.updateName(name)
                Haptics.success()
                env.toasts.show("Name saved", kind: .success)
            } catch {
                env.toasts.error(error)
            }
            savingName = false
        }
    }

    // MARK: Email

    private var emailSection: some View {
        let user = env.session.user
        let verified = user?.emailVerified == true
        return AccountSection(title: "Email", footer: "Sign-in and notification emails go here. To change it, use the web app.") {
            AccountRow(symbol: "envelope", tone: .blue, title: user?.email ?? "") {
                TonePill(text: verified ? "Confirmed" : "Not confirmed", tone: verified ? .green : .amber) {
                    Image(systemName: verified ? "checkmark.seal.fill" : "exclamationmark.circle").font(.system(size: 10, weight: .semibold))
                }
            }
            if !verified {
                AccountDivider()
                Button(action: resend) {
                    AccountRow(symbol: "paperplane", tone: .gray, title: resent ? "Sent. Check your inbox" : "Send the confirmation email again") {
                        if resending { ProgressView().controlSize(.small) }
                    }
                }
                .buttonStyle(RowPressStyle())
                .disabled(resending || resent)
            }
        }
    }

    private func resend() {
        resending = true
        Task {
            do {
                try await store.resendVerification()
                resent = true
                Haptics.success()
            } catch {
                env.toasts.error(error)
            }
            resending = false
        }
    }

    private func handleDemo() {
        if AccountDemo.take("avatar", from: env.router) { pickingPhoto = true }
    }
}

/// Job title, phone, pronouns and the personal signature, as customers see them here.
struct AccountMemberProfileSection: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store

    @State private var draft = MemberProfile.empty
    @State private var signature = ""
    @State private var loaded = false
    @State private var saving = false

    private var workspaceName: String { env.session.workspace?.workspace.name ?? "this workspace" }

    private var dirty: Bool {
        guard let saved = store.profile else { return false }
        return draft.title != saved.title || draft.phone != saved.phone || draft.pronouns != saved.pronouns
            || signature != HTMLText.plain(from: saved.signatureHTML)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            VStack(alignment: .leading, spacing: 4) {
                SectionLabel("Profile for customers")
                Text("What customers see of you in \(workspaceName). It fills the sender details in signatures and replies.")
                    .font(ODFont.footnote)
                    .foregroundStyle(Theme.fg3)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.horizontal, 4)
            if store.profile == nil {
                Panel { SkeletonRows(count: 2) }
            } else {
                VStack(alignment: .leading, spacing: 14) {
                    ODTextField(label: "Job title", text: $draft.title, prompt: "Support specialist", contentType: .jobTitle)
                    HStack(spacing: 10) {
                        ODTextField(label: "Phone", text: $draft.phone, prompt: "+1 555 010 0199", contentType: .telephoneNumber, keyboard: .phonePad)
                        ODTextField(label: "Pronouns", text: $draft.pronouns, prompt: "she/her")
                            .frame(maxWidth: 130)
                    }
                    AccountTextEditor(label: "Personal signature", text: $signature, prompt: "Jane Cooper, Support", minHeight: 96)
                    Text("Added to your email replies. When the team or address has a signature of its own, yours shows only where it asks for it.")
                        .font(ODFont.footnote)
                        .foregroundStyle(Theme.fg3)
                        .fixedSize(horizontal: false, vertical: true)
                    if dirty {
                        HStack(spacing: 10) {
                            Button("Discard") { reset() }
                                .buttonStyle(OutlineButtonStyle(height: 42))
                            Button(action: save) { ButtonLabel(title: "Save profile", busy: saving) }
                                .buttonStyle(InkButtonStyle(height: 42))
                                .disabled(saving)
                        }
                        .transition(.opacity.combined(with: .move(edge: .top)))
                    }
                }
                .motion(Motion.snappy, value: dirty)
            }
        }
        .task {
            if store.profile == nil { await store.loadProfile() }
            reset()
        }
    }

    private func reset() {
        guard let saved = store.profile else { return }
        draft = saved
        signature = HTMLText.plain(from: saved.signatureHTML)
    }

    private func save() {
        guard let saved = store.profile else { return }
        var next = draft
        next.title = next.title.trimmingCharacters(in: .whitespacesAndNewlines)
        next.phone = next.phone.trimmingCharacters(in: .whitespacesAndNewlines)
        next.pronouns = next.pronouns.trimmingCharacters(in: .whitespacesAndNewlines)
        // A signature written on the web keeps its formatting unless it changed here.
        next.signatureHTML = signature == HTMLText.plain(from: saved.signatureHTML)
            ? saved.signatureHTML
            : (signature.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "" : HTMLText.html(fromPlain: signature))
        saving = true
        Task {
            do {
                try await store.saveProfile(next)
                reset()
                Haptics.success()
                env.toasts.show("Profile saved", kind: .success)
            } catch {
                env.toasts.error(error)
            }
            saving = false
        }
    }
}
