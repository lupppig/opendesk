import SwiftUI

/// From, to, cc, subject and files above an email shown as it goes or went out.
struct EmailHeaderRows: View {
    let from: String
    let to: String
    var cc: [String] = []
    var bcc: [String] = []
    let subject: String
    var files: [String] = []

    var body: some View {
        VStack(alignment: .leading, spacing: 5) {
            if !from.isEmpty { row("From", from) }
            row("To", to)
            if !cc.isEmpty { row("Cc", cc.joined(separator: ", ")) }
            if !bcc.isEmpty { row("Bcc", bcc.joined(separator: ", ")) }
            if !subject.isEmpty { row("Subject", subject) }
            if !files.isEmpty { row("Files", files.joined(separator: ", ")) }
        }
    }

    private func row(_ label: String, _ value: String) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 8) {
            Text(label).font(ODFont.sans(12.5)).foregroundStyle(Theme.fg3).frame(width: 56, alignment: .leading)
            Text(value).font(ODFont.sans(13.5)).foregroundStyle(Theme.fg).fixedSize(horizontal: false, vertical: true)
        }
    }
}

/// A whole email document in its sandboxed frame, on a white card.
struct EmailDocumentFrame: View {
    let html: String
    @Environment(AppEnvironment.self) private var env
    @State private var height: CGFloat = 320

    var body: some View {
        MessageWebView(html: html, baseURL: env.session.instance.apiURL, height: $height)
            .frame(height: height)
            .padding(12)
            .background(Theme.surface, in: RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous))
            .overlay(RoundedRectangle(cornerRadius: Theme.Radius.control, style: .continuous).strokeBorder(Theme.line, lineWidth: 1))
    }
}

/// A sent email as its recipients got it: the copy kept when it went out
/// (`GET /v1/messages/{id}/original`), with the layout, the signature and the quoted conversation.
struct SentEmailSheet: View {
    let store: TicketStore
    let message: Message

    @Environment(\.dismiss) private var dismiss
    @State private var html: String?
    @State private var error: String?

    private var to: String { message.to.isEmpty ? "the customer" : message.to.joined(separator: ", ") }

    private var from: String {
        guard !message.fromEmail.isEmpty else { return "" }
        return message.fromName.isEmpty ? message.fromEmail : "\(message.fromName) <\(message.fromEmail)>"
    }

    var body: some View {
        NavigationStack {
            ScrollView {
                VStack(alignment: .leading, spacing: 14) {
                    Text("The email as \(to) got it, \(Format.fullDate(message.createdAt)).")
                        .font(ODFont.sans(13))
                        .foregroundStyle(Theme.fg3)
                    EmailHeaderRows(
                        from: from,
                        to: to,
                        cc: message.cc,
                        subject: message.subject,
                        files: message.attachments.filter { !$0.inline }.map(\.filename)
                    )
                    if let error {
                        Text(error).font(ODFont.sans(13)).foregroundStyle(Tone.red.text)
                    } else if let html {
                        EmailDocumentFrame(html: html)
                    } else {
                        ProgressView().frame(maxWidth: .infinity, minHeight: 160)
                    }
                }
                .padding(16)
            }
            .background(Theme.canvas)
            .navigationTitle("Sent email")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button("Close") { dismiss() }
                }
            }
        }
        .task { await load() }
    }

    private func load() async {
        do {
            html = try await store.sentEmail(message)
        } catch {
            if !error.isCancellation { self.error = error.userMessage }
        }
    }
}
