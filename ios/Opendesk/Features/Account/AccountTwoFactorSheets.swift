import CoreImage
import CoreImage.CIFilterBuiltins
import SwiftUI
import UIKit

/// A QR code drawn on the device from the `otpauth://` URI; the secret never leaves the app.
struct AccountQRCode: View {
    let text: String
    var size: CGFloat = 188

    var body: some View {
        Group {
            if let image = Self.render(text) {
                Image(uiImage: image)
                    .interpolation(.none)
                    .resizable()
                    .scaledToFit()
            } else {
                Image(systemName: "qrcode").font(.system(size: 60)).foregroundStyle(Theme.fg3)
            }
        }
        .frame(width: size, height: size)
        .padding(12)
        // Black on white in both themes, so every authenticator reads it.
        .background(Color.white, in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
        .accessibilityLabel("QR code for your authenticator app")
    }

    static func render(_ text: String) -> UIImage? {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(text.utf8)
        filter.correctionLevel = "M"
        guard let output = filter.outputImage?.transformed(by: CGAffineTransform(scaleX: 10, y: 10)),
              let cgImage = CIContext().createCGImage(output, from: output.extent) else { return nil }
        return UIImage(cgImage: cgImage)
    }
}

/// Turning two-factor on: scan, confirm a code, save the recovery codes.
struct AccountTwoFactorSetupSheet: View {
    let setup: TOTPSetup
    var initialCodes: [String]?

    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @Environment(\.dismiss) private var dismiss
    @Environment(\.openURL) private var openURL

    enum Step: Int { case scan = 1, confirm, codes }

    @State private var step: Step = .scan
    @State private var code = ""
    @State private var codes: [String] = []
    @State private var saved = false
    @State private var busy = false
    @State private var error: String?
    @State private var copiedSecret = false
    @State private var closing: AccountConfirm?
    @FocusState private var codeFocused: Bool
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 20) {
                AccountSheetHeader(title: "Turn on two-factor", subtitle: subtitle) { close() }
                AccountStepper(step: step.rawValue, steps: ["Scan the code", "Enter a code", "Save your codes"])
                Group {
                    switch step {
                    case .scan: scan
                    case .confirm: confirm
                    case .codes: recovery
                    }
                }
                .transition(.asymmetric(insertion: .move(edge: .trailing).combined(with: .opacity), removal: .move(edge: .leading).combined(with: .opacity)))
            }
            .padding(22)
        }
        .scrollBounceBehavior(.basedOnSize)
        .background(Theme.surface)
        .interactiveDismissDisabled(step == .codes)
        .onAppear {
            if let initialCodes {
                codes = initialCodes
                step = .codes
            }
        }
        .accountConfirm($closing)
    }

    private var subtitle: String {
        switch step {
        case .scan: "Use an authenticator app such as 1Password, Google Authenticator or the Passwords app."
        case .confirm: "Enter the 6-digit code the app shows for opendesk."
        case .codes: "Each code signs you in once if you lose your authenticator. They are shown only now."
        }
    }

    private func go(_ next: Step) {
        withAnimation(reduceMotion ? nil : Motion.smooth) { step = next }
    }

    private func close() {
        guard step == .codes && !saved else { return dismiss() }
        closing = AccountConfirm(
            title: "Close without saving the codes?",
            message: "Two-factor is on. Without the recovery codes, losing your authenticator can lock you out.",
            confirmTitle: "Close anyway"
        ) { dismiss() }
    }

    // MARK: Steps

    private var scan: some View {
        VStack(alignment: .leading, spacing: 18) {
            AccountQRCode(text: setup.otpauthURL).frame(maxWidth: .infinity)
            VStack(alignment: .leading, spacing: 8) {
                Text("Or enter this key").font(ODFont.sans(13, weight: .medium)).foregroundStyle(Theme.fg2)
                Button {
                    UIPasteboard.general.setItems([[UIPasteboard.typeAutomatic: setup.secret]], options: [.expirationDate: Date().addingTimeInterval(120)])
                    Haptics.success()
                    copiedSecret = true
                } label: {
                    HStack {
                        Text(setup.groupedSecret)
                            .font(ODFont.mono(15, weight: .medium))
                            .foregroundStyle(Theme.fg)
                            .textSelection(.enabled)
                        Spacer()
                        Image(systemName: copiedSecret ? "checkmark" : "doc.on.doc")
                            .font(.system(size: 13, weight: .semibold))
                            .foregroundStyle(copiedSecret ? Tone.green.text : Theme.fg3)
                            .contentTransition(.symbolEffect(.replace))
                    }
                    .padding(.horizontal, 14)
                    .frame(height: 48)
                    .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                }
                .buttonStyle(TapScaleStyle(scale: 0.98))
                .accessibilityLabel("Copy the key")
            }
            if let url = URL(string: setup.otpauthURL) {
                Button {
                    openURL(url)
                } label: {
                    ButtonLabel(title: "Open in an authenticator on this iPhone", symbol: "arrow.up.forward.app")
                }
                .buttonStyle(OutlineButtonStyle(height: 44))
            }
            Button("Next") {
                go(.confirm)
                codeFocused = true
            }
            .buttonStyle(InkButtonStyle())
        }
    }

    private var confirm: some View {
        VStack(alignment: .leading, spacing: 16) {
            AccountCodeField(code: $code, focused: $codeFocused) { enable() }
            FormError(text: error)
            HStack(spacing: 10) {
                Button("Back") { go(.scan) }
                    .buttonStyle(OutlineButtonStyle())
                Button(action: enable) { ButtonLabel(title: "Turn on", busy: busy) }
                    .buttonStyle(InkButtonStyle())
                    .disabled(code.count != 6 || busy)
            }
        }
        .motion(Motion.snappy, value: error)
    }

    private var recovery: some View {
        VStack(alignment: .leading, spacing: 16) {
            AccountRecoveryCodes(codes: codes)
            Button {
                Haptics.selection()
                saved.toggle()
            } label: {
                HStack(spacing: 10) {
                    AccountCheckbox(on: saved)
                    Text("I saved these codes somewhere safe")
                        .font(ODFont.sans(14.5))
                        .foregroundStyle(Theme.fg)
                    Spacer()
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .accessibilityAddTraits(saved ? .isSelected : [])
            Button("Done") { dismiss() }
                .buttonStyle(InkButtonStyle())
                .disabled(!saved)
        }
    }

    private func enable() {
        guard code.count == 6, !busy else { return }
        busy = true
        error = nil
        Task {
            do {
                codes = try await store.enableTOTP(code: code)
                Haptics.success()
                go(.codes)
            } catch let failure as APIError where failure.code == "invalid_code" {
                Haptics.error()
                error = "That code didn't work. Check the time on your phone and try the next one."
                code = ""
            } catch {
                Haptics.error()
                self.error = error.userMessage
            }
            busy = false
        }
    }
}

/// Recovery codes in a grid, with copy and share.
struct AccountRecoveryCodes: View {
    let codes: [String]
    @State private var copied = false

    private var text: String { codes.joined(separator: "\n") }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            LazyVGrid(columns: [GridItem(.flexible(), spacing: 10), GridItem(.flexible(), spacing: 10)], spacing: 10) {
                ForEach(codes, id: \.self) { code in
                    Text(code)
                        .font(ODFont.mono(13.5, weight: .medium))
                        .foregroundStyle(Theme.fg)
                        .frame(maxWidth: .infinity)
                        .lineLimit(1)
                        .minimumScaleFactor(0.8)
                }
            }
            .padding(16)
            .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
            .textSelection(.enabled)
            HStack(spacing: 10) {
                Button {
                    UIPasteboard.general.string = text
                    Haptics.success()
                    copied = true
                } label: {
                    ButtonLabel(title: copied ? "Copied" : "Copy", symbol: copied ? "checkmark" : "doc.on.doc")
                }
                .buttonStyle(OutlineButtonStyle(height: 42))
                ShareLink(item: "opendesk recovery codes\n\n" + text) {
                    ButtonLabel(title: "Share", symbol: "square.and.arrow.up")
                }
                .buttonStyle(OutlineButtonStyle(height: 42))
            }
        }
    }
}

/// Turning two-factor off with a current code or a recovery code.
struct AccountTwoFactorOffSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    @State private var code = ""
    @State private var useRecovery = false
    @State private var busy = false
    @State private var error: String?
    @State private var reauth: AccountStore.ReauthPrompt?
    @FocusState private var focused: Bool

    private var ready: Bool {
        useRecovery ? code.trimmingCharacters(in: .whitespaces).count >= 16 : code.count == 6
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            AccountSheetHeader(
                title: "Turn off two-factor",
                subtitle: "Signing in will need only your password or a passkey. Enter a code to confirm it's you."
            ) { dismiss() }
            if useRecovery {
                ODTextField(label: "Recovery code", text: $code, prompt: "xxxx-xxxx-xxxx-xxxx", contentType: .oneTimeCode, keyboard: .asciiCapable, submitLabel: .done) { disable() }
            } else {
                AccountCodeField(code: $code, focused: $focused) { disable() }
            }
            Button(useRecovery ? "Use a code from the app" : "Use a recovery code") {
                code = ""
                error = nil
                withAnimation(Motion.snappy) { useRecovery.toggle() }
            }
            .font(ODFont.sans(13.5, weight: .medium))
            .foregroundStyle(Theme.fg2)
            FormError(text: error)
            Spacer(minLength: 0)
            Button(action: disable) { ButtonLabel(title: "Turn off two-factor", busy: busy) }
                .buttonStyle(AccountDangerButtonStyle())
                .disabled(!ready || busy)
        }
        .padding(22)
        .background(Theme.surface)
        .motion(Motion.snappy, value: error)
        .onAppear { focused = true }
        .sheet(item: $reauth) { prompt in
            AccountReauthSheet(prompt: prompt)
                .presentationDetents([.medium])
                .presentationCornerRadius(Theme.Radius.sheet)
        }
    }

    private func disable() {
        guard ready, !busy else { return }
        busy = true
        error = nil
        Task {
            do {
                try await store.disableTOTP(code: code)
                Haptics.success()
                env.toasts.show("Two-factor is off", kind: .success)
                dismiss()
            } catch let failure as APIError where failure.code == "reauth_required" || failure.code == "mfa_required" {
                reauth = AccountStore.ReauthPrompt(needsMFA: failure.code == "mfa_required") {
                    try await store.disableTOTP(code: code)
                    env.toasts.show("Two-factor is off", kind: .success)
                    dismiss()
                }
            } catch let failure as APIError where failure.code == "invalid_code" {
                Haptics.error()
                error = useRecovery ? "That recovery code didn't work, or it was used already." : "That code didn't work. Try the next one."
            } catch {
                Haptics.error()
                self.error = error.userMessage
            }
            busy = false
        }
    }
}

/// Six boxes for a one-time code over one hidden field (paste and autofill work).
struct AccountCodeField: View {
    @Binding var code: String
    var focused: FocusState<Bool>.Binding
    var onComplete: () -> Void = {}

    var body: some View {
        ZStack {
            TextField("", text: $code)
                .keyboardType(.numberPad)
                .textContentType(.oneTimeCode)
                .focused(focused)
                .opacity(0.02)
                .onChange(of: code) { _, value in
                    let digits = String(value.filter(\.isNumber).prefix(6))
                    if digits != value { code = digits }
                    if digits.count == 6 { onComplete() }
                }
                .accessibilityLabel("Six-digit code")
            HStack(spacing: 8) {
                ForEach(0 ..< 6, id: \.self) { index in
                    let characters = Array(code)
                    let active = focused.wrappedValue && index == min(characters.count, 5)
                    Text(index < characters.count ? String(characters[index]) : "")
                        .font(ODFont.mono(22, weight: .semibold))
                        .foregroundStyle(Theme.fg)
                        .frame(maxWidth: .infinity)
                        .frame(height: 54)
                        .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
                        .overlay(
                            RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous)
                                .strokeBorder(active ? Theme.fg : Theme.line, lineWidth: active ? 1.5 : 1)
                        )
                        .motion(Motion.fade, value: active)
                }
            }
            .allowsHitTesting(false)
            .accessibilityHidden(true)
        }
        .contentShape(Rectangle())
        .onTapGesture { focused.wrappedValue = true }
    }
}

/// A round check in the app's style (never the system checkbox).
struct AccountCheckbox: View {
    let on: Bool

    var body: some View {
        ZStack {
            RoundedRectangle(cornerRadius: 6, style: .continuous)
                .fill(on ? Theme.inverse : Theme.surface)
            RoundedRectangle(cornerRadius: 6, style: .continuous)
                .strokeBorder(on ? Theme.inverse : Theme.lineStrong, lineWidth: 1.5)
            Image(systemName: "checkmark")
                .font(.system(size: 11, weight: .bold))
                .foregroundStyle(Theme.inverseFg)
                .scaleEffect(on ? 1 : 0.4)
                .opacity(on ? 1 : 0)
        }
        .frame(width: 22, height: 22)
        .motion(Motion.snappy, value: on)
    }
}

/// Numbered steps with a line that fills as you go.
struct AccountStepper: View {
    let step: Int
    let steps: [String]

    var body: some View {
        HStack(spacing: 6) {
            ForEach(Array(steps.enumerated()), id: \.offset) { index, title in
                let number = index + 1
                VStack(alignment: .leading, spacing: 6) {
                    Capsule()
                        .fill(number <= step ? Theme.fg : Theme.line)
                        .frame(height: 3)
                    Text(title)
                        .font(ODFont.sans(11.5, weight: number == step ? .semibold : .medium))
                        .foregroundStyle(number == step ? Theme.fg : Theme.fg3)
                        .lineLimit(1)
                        .minimumScaleFactor(0.85)
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .motion(Motion.smooth, value: step)
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Step \(step) of \(steps.count): \(steps[max(0, min(step - 1, steps.count - 1))])")
    }
}
