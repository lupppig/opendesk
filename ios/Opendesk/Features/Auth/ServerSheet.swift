import SwiftUI

/// Choose the server: opendesk Cloud, a self-hosted address, or (debug
/// builds) this Mac's dev stack or the demo workspace. Checks `/config.json` and `/v1/auth/config`
/// and says plainly when push or App Attest isn't available there.
struct ServerSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(\.dismiss) private var dismiss

    enum Choice: Hashable { case cloud, selfHosted, local, demo }

    @State private var choice: Choice = .cloud
    @State private var address = ""
    @State private var checking = false
    @State private var error: String?
    @State private var found: (Instance, AuthConfig)?

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 18) {
                    Text("Use opendesk Cloud, or the address your team's server runs at.")
                        .font(ODFont.callout)
                        .foregroundStyle(Theme.fg3)

                    Panel {
                        option(.cloud, title: "opendesk Cloud", subtitle: "opendesk.sh", symbol: "cloud")
                        Hairline(inset: 50)
                        option(.selfHosted, title: "Self-hosted", subtitle: "Your own opendesk server", symbol: "server.rack")
                        #if DEBUG
                        Hairline(inset: 50)
                        option(.local, title: "Local development", subtitle: "localhost:8080 on this Mac", symbol: "hammer")
                        Hairline(inset: 50)
                        option(.demo, title: "Demo workspace", subtitle: "Sample data on this phone, nothing sent", symbol: "play.rectangle")
                        #endif
                    }

                    if choice == .selfHosted {
                        ODTextField(label: "Server address", text: $address, prompt: "desk.example.com", contentType: .URL, keyboard: .URL, submitLabel: .go) {
                            Task { await check() }
                        }
                        .transition(.opacity.combined(with: .move(edge: .top)))
                    }

                    FormError(text: error)

                    if let found {
                        ServerReport(instance: found.0, config: found.1)
                            .transition(.opacity)
                    }

                    Button {
                        Task {
                            #if DEBUG
                            if choice == .demo {
                                dismiss()
                                await env.session.enterDemo()
                                return
                            }
                            #endif
                            if found == nil { await check() }
                            if let found { use(found) }
                        }
                    } label: {
                        ButtonLabel(title: choice == .demo ? "Explore the demo" : found == nil ? "Connect" : "Use this server", busy: checking)
                    }
                    .buttonStyle(InkButtonStyle())
                    .disabled(checking || (choice == .selfHosted && address.trimmingCharacters(in: .whitespaces).isEmpty))
                }
                .padding(20)
                .motion(Motion.snappy, value: choice)
                .motion(Motion.fade, value: found?.0)
            }
            .background(Theme.chrome.ignoresSafeArea())
            .navigationTitle("Server")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { dismiss() }
                }
            }
        }
        .onAppear {
            let current = env.session.instance
            #if DEBUG
            if current == Instance.localDev {
                choice = .local
                return
            }
            #endif
            if current.isCloud {
                choice = .cloud
            } else {
                choice = .selfHosted
                address = current.displayHost
            }
        }
        .onChange(of: choice) { _, _ in
            found = nil
            error = nil
        }
        .onChange(of: address) { _, _ in found = nil }
    }

    private func option(_ value: Choice, title: String, subtitle: String, symbol: String) -> some View {
        Button {
            choice = value
            Haptics.selection()
        } label: {
            PanelRow(title: title, subtitle: subtitle, symbol: symbol) {
                Image(systemName: choice == value ? "largecircle.fill.circle" : "circle")
                    .font(.system(size: 18))
                    .foregroundStyle(choice == value ? Theme.fg : Theme.fg4)
            }
        }
        .buttonStyle(RowPressStyle())
        .accessibilityAddTraits(choice == value ? .isSelected : [])
    }

    private func check() async {
        checking = true
        error = nil
        defer { checking = false }
        do {
            switch choice {
            case .cloud:
                found = (Instance.cloud, try await InstanceResolver().authConfig(for: Instance.cloud.apiURL))
            case .selfHosted:
                found = try await InstanceResolver().resolve(address)
            case .local:
                #if DEBUG
                found = (Instance.localDev, try await InstanceResolver().authConfig(for: Instance.localDev.apiURL))
                #endif
            case .demo:
                break
            }
        } catch {
            Haptics.error()
            self.error = error.userMessage
        }
    }

    private func use(_ result: (Instance, AuthConfig)) {
        env.session.useInstance(result.0, config: result.1)
        Haptics.success()
        dismiss()
    }
}

/// What this server supports in the app, in plain words.
struct ServerReport: View {
    let instance: Instance
    let config: AuthConfig

    var body: some View {
        Panel(padding: 14) {
            VStack(alignment: .leading, spacing: 12) {
                HStack(spacing: 8) {
                    Image(systemName: "checkmark.circle.fill").foregroundStyle(Tone.green.solid)
                    Text("Found opendesk at \(instance.displayHost)").font(ODFont.sans(14.5, weight: .medium)).foregroundStyle(Theme.fg)
                }
                line(signIn, ok: config.emailSignInAvailable || !config.providers.isEmpty)
                line(push, ok: config.native.push != .off)
                if config.humanCheckRequired, config.native.appAttest, !AppAttestClient.isSupported {
                    line("This device can't use App Attest (the simulator never can), so email sign-in won't work from it.", ok: false)
                }
                if instance.passkeysSupported, config.passkeys {
                    line("Passkeys work here.", ok: true)
                }
            }
        }
    }

    private var signIn: String {
        let providers = config.providers.map(\.name)
        if config.emailSignInAvailable {
            return providers.isEmpty ? "Sign in with email and password." : "Sign in with email or \(providers.joined(separator: ", "))."
        }
        if config.passwordLogin, config.humanCheckRequired {
            return providers.isEmpty
                ? "App Attest is off on this server, so email sign-in only works on the web."
                : "App Attest is off on this server: sign in with \(providers.joined(separator: ", ")), or on the web."
        }
        return providers.isEmpty ? "No sign-in method is open to the app." : "Sign in with \(providers.joined(separator: ", "))."
    }

    private var push: String {
        switch config.native.push {
        case .direct: "Push notifications are on."
        case .relay: "Push notifications go through the opendesk relay."
        case .off: "Push notifications are off on this server. The app still updates live while it's open."
        }
    }

    private func line(_ text: String, ok: Bool) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Image(systemName: ok ? "checkmark" : "exclamationmark.triangle")
                .font(.system(size: 11.5, weight: .semibold))
                .foregroundStyle(ok ? Theme.fg3 : Tone.amber.solid)
                .frame(width: 14)
            Text(text).font(ODFont.footnote).foregroundStyle(Theme.fg2).fixedSize(horizontal: false, vertical: true)
        }
    }
}
