import SwiftUI

/// The person's clock: 12 or 24 hour times in the app and in their notification
/// emails. Saved to the account as soon as it changes, so it follows them to the
/// web and every other device (`PATCH /v1/me {time_format}`).
struct AccountClockSection: View {
    @Environment(AppEnvironment.self) private var env
    @State private var saving = false

    private var chosen: TimeFormat { TimeFormat(env.session.user?.timeFormat) }

    var body: some View {
        AccountSection(title: "Time format", footer: "For times in the app and in your notification emails, on every device. Automatic follows your iPhone's settings here and uses 24-hour in emails.") {
            ForEach(Array(TimeFormat.allCases.enumerated()), id: \.element) { index, option in
                if index > 0 { AccountDivider(tiles: false) }
                AccountChoiceRow(title: option.title, subtitle: option.example, chosen: chosen == option) {
                    choose(option)
                }
                .disabled(saving)
            }
        }
    }

    private func choose(_ option: TimeFormat) {
        guard option != chosen, !saving else { return }
        saving = true
        Task {
            defer { saving = false }
            do {
                try await env.session.setTimeFormat(option)
                env.toasts.show("Time format saved", kind: .success)
            } catch {
                env.toasts.error(error)
            }
        }
    }
}
