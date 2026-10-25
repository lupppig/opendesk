import SwiftUI

/// One notification: who or what it's about, the line the web shows, the
/// ticket or conversation, and when. Unread rows carry a blue dot.
struct NotificationRow: View {
    let notification: AppNotification

    private var unread: Bool { notification.isUnread }
    private var tone: Tone { NotificationStyle.tone(for: notification) }

    private var preview: String? {
        guard let text = notification.data["preview"]?.string?.trimmingCharacters(in: .whitespacesAndNewlines), !text.isEmpty else { return nil }
        return text
    }

    var body: some View {
        HStack(alignment: .top, spacing: 12) {
            leading
            VStack(alignment: .leading, spacing: 3) {
                HStack(alignment: .firstTextBaseline, spacing: 8) {
                    Text(notification.title)
                        .font(ODFont.sans(15, weight: unread ? .semibold : .regular))
                        .foregroundStyle(unread ? Theme.fg : Theme.fg2)
                        .lineLimit(2)
                        .fixedSize(horizontal: false, vertical: true)
                    Spacer(minLength: 4)
                    NotificationTime(date: notification.createdAt, unread: unread)
                }
                if let context = NotificationStyle.context(of: notification) {
                    Text(context)
                        .font(ODFont.sans(13.5, weight: .medium))
                        .foregroundStyle(unread ? Theme.fg2 : Theme.fg3)
                        .lineLimit(1)
                }
                if let preview {
                    Text(preview)
                        .font(ODFont.sans(13.5))
                        .foregroundStyle(Theme.fg3)
                        .lineLimit(2)
                }
            }
        }
        .padding(.leading, 22)
        .padding(.trailing, 16)
        .padding(.vertical, 12)
        .frame(maxWidth: .infinity, alignment: .leading)
        .overlay(alignment: .topLeading) {
            if unread {
                Circle()
                    .fill(Tone.blue.solid)
                    .frame(width: 7, height: 7)
                    .padding(.leading, 8)
                    .padding(.top, 25)
                    .transition(.scale.combined(with: .opacity))
            }
        }
        .contentShape(Rectangle())
        .motion(Motion.snappy, value: unread)
        .accessibilityElement(children: .combine)
        .accessibilityLabel(accessibility)
    }

    /// The actor's avatar with the kind as a small tinted badge, or the kind alone.
    @ViewBuilder
    private var leading: some View {
        if let actor = NotificationStyle.actor(of: notification) {
            Avatar(name: actor.name, seed: actor.id, size: 36)
                .overlay(alignment: .bottomTrailing) {
                    Image(systemName: NotificationStyle.symbol(for: notification))
                        .font(.system(size: 8.5, weight: .bold))
                        .foregroundStyle(.white)
                        .frame(width: 17, height: 17)
                        .background(tone.solid, in: Circle())
                        .overlay(Circle().strokeBorder(Theme.surface, lineWidth: 2))
                        .offset(x: 4, y: 4)
                }
                .padding(.bottom, 4)
        } else {
            Image(systemName: NotificationStyle.symbol(for: notification))
                .font(.system(size: 15, weight: .medium))
                .foregroundStyle(tone.text)
                .frame(width: 36, height: 36)
                .background(tone.soft, in: RoundedRectangle(cornerRadius: 10, style: .continuous))
        }
    }

    private var accessibility: String {
        var parts = [notification.title]
        if let context = NotificationStyle.context(of: notification) { parts.append(context) }
        if let preview { parts.append(preview) }
        parts.append(Format.relLong(notification.createdAt))
        if unread { parts.insert("Unread", at: 0) }
        return parts.joined(separator: ", ")
    }
}

/// "5m", kept current by a clock that lives only here.
private struct NotificationTime: View {
    let date: Date
    let unread: Bool

    var body: some View {
        TimelineView(.periodic(from: .now, by: 60)) { context in
            Text(Format.relShort(date, now: AppClock.now(at: context.date)))
                .font(ODFont.sans(12.5))
                .monospacedDigit()
                .foregroundStyle(unread ? Tone.blue.text : Theme.fg4)
        }
    }
}
