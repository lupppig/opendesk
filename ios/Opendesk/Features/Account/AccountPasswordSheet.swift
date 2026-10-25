import SwiftUI

/// How hard a password is to guess, 0 (too weak) to 4 (strong), like the web's
/// meter. The server has the last word (`auth.CheckPassword`); this only guides.
enum AccountPasswordStrength {
    static let minimum = 12
    static let maximum = 128

    private static let common: Set<String> = [
        "password", "passw0rd", "letmein", "welcome", "qwerty", "qwertyuiop", "asdfgh", "zxcvbn", "iloveyou", "admin",
        "monkey", "dragon", "football", "baseball", "sunshine", "princess", "starwars", "whatever", "trustno1", "opendesk",
        "123456", "1234567890", "abc123", "111111", "000000", "master", "shadow", "superman", "michael", "secret",
    ]

    struct Result {
        var score: Int
        var warning: String?
    }

    static func evaluate(_ password: String, userInputs: [String] = []) -> Result {
        let length = password.count
        guard length > 0 else { return Result(score: 0) }
        let lower = password.lowercased()
        var warning: String?

        var classes = 0
        if password.contains(where: \.isLowercase) { classes += 1 }
        if password.contains(where: \.isUppercase) { classes += 1 }
        if password.contains(where: \.isNumber) { classes += 1 }
        if password.contains(where: { !$0.isLetter && !$0.isNumber }) { classes += 1 }
        let unique = Set(password).count
        let words = password.split(whereSeparator: { $0 == " " || $0 == "-" || $0 == "_" || $0 == "." }).filter { $0.count >= 3 }.count

        // Rough entropy: length times the alphabet a guesser would try.
        let alphabet: Double = [26, 52, 62, 94][max(0, classes - 1)]
        var bits = Double(length) * log2(alphabet) * min(1, Double(unique) / Double(max(4, length / 2)))
        if words >= 3 { bits += 12 }

        if common.contains(where: { lower.contains($0) }) {
            bits *= 0.45
            warning = "This is similar to a commonly used password."
        }
        for input in userInputs.map({ $0.lowercased() }) where input.count >= 3 && lower.contains(input) {
            bits *= 0.6
            warning = "There should not be any personal or page related data."
        }
        if hasRun(password, length: 3) {
            bits *= 0.75
            warning = warning ?? "Repeated characters like \"aaa\" are easy to guess."
        }
        if hasSequence(lower) {
            bits *= 0.8
            warning = warning ?? "Common character sequences like \"abc\" are easy to guess."
        }
        if unique <= 2 { bits = min(bits, 10) }

        let score: Int
        switch bits {
        case ..<28: score = 0
        case ..<40: score = 1
        case ..<56: score = 2
        case ..<72: score = 3
        default: score = 4
        }
        return Result(score: score, warning: score < 3 ? warning : nil)
    }

    private static func hasRun(_ text: String, length: Int) -> Bool {
        var count = 1
        var last: Character?
        for character in text {
            count = character == last ? count + 1 : 1
            if count >= length { return true }
            last = character
        }
        return false
    }

    private static func hasSequence(_ text: String) -> Bool {
        let scalars = text.unicodeScalars.map(\.value)
        guard scalars.count >= 4 else { return false }
        for index in 0 ..< scalars.count - 3 {
            let a = scalars[index], b = scalars[index + 1], c = scalars[index + 2], d = scalars[index + 3]
            if b == a + 1 && c == b + 1 && d == c + 1 { return true }
            if b + 1 == a && c + 1 == b && d + 1 == c { return true }
        }
        return false
    }
}

/// Four bars that warm up as the password gets harder to guess, and a word for it.
struct AccountStrengthMeter: View {
    let password: String
    var userInputs: [String] = []

    private static let words = ["Too weak", "Weak", "Fair", "Good", "Strong"]

    var body: some View {
        let result = AccountPasswordStrength.evaluate(password, userInputs: userInputs)
        let length = password.count
        let short = length > 0 && length < AccountPasswordStrength.minimum
        let label = password.isEmpty
            ? "12 characters minimum"
            : short ? Format.plural(AccountPasswordStrength.minimum - length, "more character") : Self.words[result.score]
        let filled = password.isEmpty ? 0 : max(1, result.score)
        let color = short ? Tone.gray.solid : [Tone.red, .red, .amber, .green, .green][result.score].solid

        VStack(alignment: .leading, spacing: 6) {
            HStack(spacing: 10) {
                HStack(spacing: 4) {
                    ForEach(1 ... 4, id: \.self) { bar in
                        Capsule()
                            .fill(Theme.line)
                            .overlay(alignment: .leading) {
                                Capsule().fill(color).scaleEffect(x: bar <= filled ? 1 : 0, anchor: .leading)
                            }
                            .frame(height: 3)
                    }
                }
                .motion(Motion.smooth, value: filled)
                .accessibilityHidden(true)
                Text(label)
                    .font(ODFont.mono(11))
                    .foregroundStyle(Theme.fg3)
                    .contentTransition(.opacity)
            }
            if let warning = result.warning, !short {
                Text(warning)
                    .font(ODFont.sans(11.5))
                    .foregroundStyle(Theme.fg3)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .accessibilityElement(children: .combine)
        .accessibilityLabel("Password strength: \(label)")
    }
}

/// Current password, then a new one. The server signs every other device out.
struct AccountPasswordSheet: View {
    @Environment(AppEnvironment.self) private var env
    @Environment(AccountStore.self) private var store
    @Environment(\.dismiss) private var dismiss

    @State private var current = ""
    @State private var next = ""
    @State private var saving = false
    @State private var error: String?

    private var valid: Bool {
        !current.isEmpty && next.count >= AccountPasswordStrength.minimum && next.count <= AccountPasswordStrength.maximum
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 18) {
                AccountSheetHeader(title: "Change password", subtitle: "Other devices are signed out. This iPhone stays signed in.") { dismiss() }
                ODTextField(label: "Current password", text: $current, secure: true, contentType: .password)
                VStack(alignment: .leading, spacing: 8) {
                    ODTextField(label: "New password", text: $next, secure: true, contentType: .newPassword, submitLabel: .done) { save() }
                    AccountStrengthMeter(password: next, userInputs: [env.session.user?.email ?? "", env.session.user?.name ?? ""])
                    if next.count > AccountPasswordStrength.maximum {
                        FormError(text: "Use at most 128 characters.")
                    }
                }
                Text("12 to 128 characters. Passphrases work well.")
                    .font(ODFont.footnote)
                    .foregroundStyle(Theme.fg3)
                FormError(text: error)
                Button(action: save) { ButtonLabel(title: "Change password", busy: saving) }
                    .buttonStyle(InkButtonStyle())
                    .disabled(!valid || saving)
            }
            .padding(22)
            .motion(Motion.snappy, value: error)
        }
        .scrollBounceBehavior(.basedOnSize)
        .background(Theme.surface)
    }

    private func save() {
        guard valid else { return }
        saving = true
        error = nil
        Task {
            do {
                try await store.changePassword(current: current, new: next)
                Haptics.success()
                env.toasts.show("Password changed. Other devices are signed out.", kind: .success)
                dismiss()
            } catch let failure as APIError where failure.code == "invalid_credentials" {
                Haptics.error()
                error = "Your current password is not right."
            } catch {
                Haptics.error()
                self.error = error.userMessage
            }
            saving = false
        }
    }
}
