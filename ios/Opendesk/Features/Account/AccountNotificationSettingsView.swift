import SwiftUI

/// What reaches your email, phone and desktop: per type and channel for this
/// workspace, the email schedule and the pause for the whole account.
struct AccountNotificationSettingsView: View {
    @Environment(AppEnvironment.self) private var env
    @State private var testing = false
    @State private var resuming = false

    private var store: NotificationsStore { env.notifications }
    private var workspaceName: String { env.session.workspace?.workspace.name ?? "this workspace" }

    var body: some View {
        AccountPage(title: "Notifications") {
            if let prefs = store.preferences {
                holds(prefs)
                cadence(prefs)
                pause(prefs)
                types(prefs)
            } else if let error = store.preferencesError {
                ErrorState(message: error) { Task { await store.loadPreferences() } }
            } else {
                Panel { SkeletonRows(count: 4) }
            }
        }
        .task { await store.loadPreferences() }
        .onChange(of: env.router.demoAction) { _, _ in handleDemo() }
        .onAppear(perform: handleDemo)
    }

    // MARK: Holds

    @ViewBuilder
    private func holds(_ prefs: NotificationPreferences) -> some View {
        if prefs.unsubscribedAt != nil {
            AccountHoldCard(
                symbol: "envelope.badge.shield.half.filled", tone: .amber,
                title: "Notification emails from \(workspaceName) are off",
                message: "You used the unsubscribe link in one of our emails. Turning email back on restores the choices you had before.",
                actionTitle: "Turn email back on", busy: resuming
            ) {
                resuming = true
                Task {
                    do {
                        try await store.resubscribe()
                        env.toasts.show("Notification emails are back on", kind: .success)
                    } catch { env.toasts.error(error) }
                    resuming = false
                }
            }
        }
        if prefs.isPaused(at: AppClock.now), let until = prefs.pausedUntil {
            AccountHoldCard(
                symbol: "moon.zzz", tone: .indigo,
                title: "Notifications are paused until \(Self.when(until))",
                message: "Nothing reaches your email, phone or desktop until then. Everything still shows in Notifications.",
                actionTitle: "Resume now", busy: resuming
            ) { resume() }
        }
    }

    // MARK: Email schedule

    private func cadence(_ prefs: NotificationPreferences) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            SectionLabel("Email").padding(.horizontal, 4)
            Panel {
                ForEach(Array(EmailCadence.allCases.enumerated()), id: \.element) { index, value in
                    if index > 0 { AccountDivider() }
                    AccountChoiceRow(symbol: value.symbol, tone: value.tone, title: value.title, subtitle: Self.hint(value, prefs), chosen: prefs.emailCadence == value) {
                        update(NotificationPreferencesPatch(emailCadence: value)) { $0.emailCadence = value }
                    }
                }
                if prefs.emailCadence == .daily {
                    AccountDivider(tiles: false)
                    digestRows(prefs)
                }
            }
            .motion(Motion.snappy, value: prefs.emailCadence)
            Panel {
                AccountRow(symbol: "at", tone: .blue, title: prefs.email, subtitle: "Notification emails go here") {
                    TonePill(text: prefs.emailVerified ? "Confirmed" : "Not confirmed", tone: prefs.emailVerified ? .green : .amber)
                }
                AccountDivider()
                Button(action: test) {
                    AccountRow(symbol: "paperplane", tone: .gray, title: "Send me a test email") {
                        if testing { ProgressView().controlSize(.small) }
                    }
                }
                .buttonStyle(RowPressStyle())
                .disabled(testing || !prefs.emailVerified)
            }
            .padding(.top, 4)
        }
    }

    private func digestRows(_ prefs: NotificationPreferences) -> some View {
        VStack(spacing: 0) {
            AccountRow(title: "Send at") {
                Menu {
                    Picker("Hour", selection: Binding(get: { prefs.digestHour }, set: { hour in
                        update(NotificationPreferencesPatch(digestHour: hour)) { $0.digestHour = hour }
                    })) {
                        ForEach(0 ..< 24, id: \.self) { hour in Text(Self.hourLabel(hour)).tag(hour) }
                    }
                } label: {
                    HStack(spacing: 4) {
                        Text(Self.hourLabel(prefs.digestHour)).font(ODFont.sans(15, weight: .medium)).foregroundStyle(Theme.fg)
                        Image(systemName: "chevron.up.chevron.down").font(.system(size: 10, weight: .semibold)).foregroundStyle(Theme.fg3)
                    }
                    .padding(.horizontal, 10)
                    .frame(height: 30)
                    .background(Theme.sunken, in: RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous))
                    .overlay(RoundedRectangle(cornerRadius: Theme.Radius.chip + 1, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
                }
            }
            AccountDivider(tiles: false)
            let device = TimeZone.current.identifier
            AccountRow(title: "Time zone", subtitle: prefs.timezone.replacingOccurrences(of: "_", with: " ")) {
                if device != prefs.timezone {
                    Button("Use this iPhone's") {
                        update(NotificationPreferencesPatch(timezone: device)) { $0.timezone = device }
                    }
                    .buttonStyle(ChipButtonStyle())
                }
            }
        }
        .transition(.opacity.combined(with: .move(edge: .top)))
    }

    // MARK: Pause

    @ViewBuilder
    private func pause(_ prefs: NotificationPreferences) -> some View {
        if !prefs.isPaused(at: AppClock.now) {
            AccountSection(footer: "Stops email, phone and desktop notifications for a while. Notifications still collect in the app.") {
                Menu {
                    Button("For 1 hour", systemImage: "clock") { pauseFor(AppClock.now.addingTimeInterval(3600)) }
                    Button("Until tomorrow morning", systemImage: "sunrise") { pauseFor(Self.tomorrowMorning()) }
                    Button("For 1 week", systemImage: "calendar") { pauseFor(AppClock.now.addingTimeInterval(7 * 86400)) }
                } label: {
                    AccountRow(symbol: "moon.zzz", tone: .indigo, title: "Pause notifications") {
                        Image(systemName: "chevron.up.chevron.down").font(.system(size: 11, weight: .semibold)).foregroundStyle(Theme.fg4)
                    }
                }
                .buttonStyle(RowPressStyle())
            }
        }
    }

    // MARK: Types

    private func types(_ prefs: NotificationPreferences) -> some View {
        VStack(alignment: .leading, spacing: 20) {
            VStack(alignment: .leading, spacing: 4) {
                Text("What you hear about")
                    .font(ODFont.sans(17, weight: .semibold))
                    .tracking(-0.2)
                    .foregroundStyle(Theme.fg)
                Text("For \(workspaceName) only. Each workspace keeps its own choices. Everything always shows in the app.")
                    .font(ODFont.footnote)
                    .foregroundStyle(Theme.fg3)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .padding(.horizontal, 4)
            .padding(.top, 6)
            ForEach(prefs.groups, id: \.group) { group in
                AccountSection(title: NotificationPreferences.groupTitle(group.group)) {
                    ForEach(Array(group.types.enumerated()), id: \.element.id) { index, item in
                        if index > 0 { AccountDivider() }
                        AccountNotificationTypeRow(item: item, emailOff: prefs.emailCadence == .off) { channel, on in
                            Task {
                                do { try await store.setPreference(item.type, channel: channel, on: on) } catch { env.toasts.error(error) }
                            }
                        }
                    }
                }
            }
            VStack(alignment: .leading, spacing: 6) {
                Text("Phone: the opendesk iPhone app. Each phone can also mute types of its own, in This iPhone.")
                Text("Desktop: the opendesk desktop app, or a browser that allowed notifications.")
            }
            .font(ODFont.footnote)
            .foregroundStyle(Theme.fg3)
            .fixedSize(horizontal: false, vertical: true)
            .padding(.horizontal, 4)
        }
    }

    // MARK: Actions

    private func update(_ patch: NotificationPreferencesPatch, change: (inout NotificationPreferences) -> Void) {
        Haptics.selection()
        var local = store.preferences
        if local != nil { change(&local!) }
        Task {
            do {
                try await store.updatePreferences(patch) { prefs in if let local { prefs = local } }
            } catch {
                env.toasts.error(error)
            }
        }
    }

    private func pauseFor(_ date: Date) {
        let stamp = date.formatted(Date.ISO8601FormatStyle(includingFractionalSeconds: false))
        update(NotificationPreferencesPatch(pausedUntil: stamp)) { $0.pausedUntil = date }
        env.toasts.show("Paused until \(Self.when(date))", kind: .success)
    }

    private func resume() {
        resuming = true
        Task {
            do {
                try await store.updatePreferences(NotificationPreferencesPatch(pausedUntil: "")) { $0.pausedUntil = nil }
                Haptics.success()
            } catch {
                env.toasts.error(error)
            }
            resuming = false
        }
    }

    private func test() {
        testing = true
        Task {
            do {
                try await store.sendTestEmail()
                Haptics.success()
                env.toasts.show("Test email sent to \(store.preferences?.email ?? "you")", kind: .success)
            } catch let failure as APIError where failure.code == "rate_limited" {
                env.toasts.show("A few test emails an hour at most. Try again later.", kind: .error)
            } catch {
                env.toasts.error(error)
            }
            testing = false
        }
    }

    private func handleDemo() {
        #if DEBUG
        if AccountDemo.take("pause", from: env.router) { pauseFor(Self.tomorrowMorning()) }
        if AccountDemo.take("digest", from: env.router) {
            update(NotificationPreferencesPatch(emailCadence: .daily)) { $0.emailCadence = .daily }
        }
        #endif
    }

    // MARK: Words

    static func hint(_ cadence: EmailCadence, _ prefs: NotificationPreferences) -> String {
        switch cadence {
        case .instant: "After \(Format.plural(prefs.delayMinutes, "minute")) unread, paced so it never floods your inbox."
        case .hourly: "One email an hour with what you have not read yet."
        case .daily: "One email a day at the hour you choose, with what you have not read yet."
        case .off: "No notification emails. You still see everything in the app."
        }
    }

    static func hourLabel(_ hour: Int) -> String {
        var components = DateComponents()
        components.hour = hour
        components.minute = 0
        let date = Calendar.current.date(from: components) ?? .now
        return date.formatted(.dateTime.hour().minute())
    }

    static func when(_ date: Date) -> String {
        Calendar.current.isDate(date, inSameDayAs: AppClock.now)
            ? Format.clock(date)
            : date.formatted(.dateTime.weekday(.abbreviated).hour().minute())
    }

    static func tomorrowMorning() -> Date {
        let calendar = Calendar.current
        let tomorrow = calendar.date(byAdding: .day, value: 1, to: AppClock.now) ?? AppClock.now
        return calendar.date(bySettingHour: 8, minute: 0, second: 0, of: tomorrow) ?? tomorrow
    }
}

/// One notification type: its name and hint, and a switch chip per channel.
struct AccountNotificationTypeRow: View {
    let item: NotificationTypePrefs
    var emailOff = false
    let set: (NotificationChannel, Bool) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            HStack(alignment: .top, spacing: 12) {
                AccountIconTile(symbol: item.kind?.symbol ?? "bell", tone: NotificationStyle.tone(for: item.type), size: 30)
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(item.label).font(ODFont.sans(15.5)).foregroundStyle(Theme.fg)
                        if item.urgent {
                            Text("Urgent")
                                .font(ODFont.sans(10.5, weight: .semibold))
                                .foregroundStyle(Tone.red.text)
                                .padding(.horizontal, 5)
                                .frame(height: 17)
                                .background(Tone.red.soft, in: RoundedRectangle(cornerRadius: 4, style: .continuous))
                        }
                    }
                    if !item.hint.isEmpty {
                        Text(item.hint)
                            .font(ODFont.footnote)
                            .foregroundStyle(Theme.fg3)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                }
                Spacer(minLength: 0)
            }
            HStack(spacing: 6) {
                ForEach(NotificationChannel.allCases) { channel in
                    let disabled = channel == .email && emailOff
                    AccountChannelChip(channel: channel, on: item.value(channel) && !disabled, disabled: disabled) {
                        set(channel, !item.value(channel))
                    }
                }
                Spacer(minLength: 0)
            }
            .padding(.leading, 42)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 12)
    }
}

/// A channel switch shaped like a chip: filled ink when on, a hairline when off.
struct AccountChannelChip: View {
    let channel: NotificationChannel
    let on: Bool
    var disabled = false
    let toggle: () -> Void

    var body: some View {
        Button {
            Haptics.selection()
            toggle()
        } label: {
            HStack(spacing: 5) {
                Image(systemName: on ? "checkmark" : channel.symbol)
                    .font(.system(size: 10.5, weight: .bold))
                    .contentTransition(.symbolEffect(.replace))
                Text(channel.title).font(ODFont.sans(12.5, weight: .medium))
            }
            .foregroundStyle(on ? Theme.inverseFg : Theme.fg3)
            .padding(.horizontal, 10)
            .frame(height: 28)
            .background(on ? Theme.inverse : Theme.sunken, in: Capsule())
            .overlay(Capsule().strokeBorder(on ? Color.clear : Theme.line, lineWidth: 1))
            .contentShape(Capsule())
        }
        .buttonStyle(TapScaleStyle(scale: 0.94))
        .disabled(disabled)
        .opacity(disabled ? 0.45 : 1)
        .motion(Motion.snappy, value: on)
        .accessibilityLabel(channel.title)
        .accessibilityValue(on ? "On" : "Off")
        .accessibilityAddTraits(.isToggle)
    }
}

/// A soft card for something holding notifications back, with the way out.
struct AccountHoldCard: View {
    let symbol: String
    let tone: Tone
    let title: String
    let message: String
    let actionTitle: String
    var busy = false
    let action: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .top, spacing: 12) {
                AccountIconTile(symbol: symbol, tone: tone, size: 34)
                VStack(alignment: .leading, spacing: 4) {
                    Text(title).font(ODFont.sans(15, weight: .semibold)).foregroundStyle(Theme.fg).fixedSize(horizontal: false, vertical: true)
                    Text(message).font(ODFont.footnote).foregroundStyle(Theme.fg2).fixedSize(horizontal: false, vertical: true)
                }
            }
            Button(action: action) { ButtonLabel(title: actionTitle, busy: busy) }
                .buttonStyle(InkButtonStyle(height: 40))
                .disabled(busy)
        }
        .padding(16)
        .background(tone.solid.opacity(0.07), in: RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous))
        .overlay(RoundedRectangle(cornerRadius: Theme.Radius.card, style: .continuous).strokeBorder(tone.line, lineWidth: 1))
        .transition(.opacity.combined(with: .scale(scale: 0.98)))
    }
}
