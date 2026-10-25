import SafariServices
import SwiftUI
import UIKit

/// A page of the web app opened in the app (workspace settings live on the web).
struct AccountWebPage: Identifiable, Hashable {
    let url: URL
    var id: URL { url }
}

/// `SFSafariViewController` in a sheet: the web keeps its own sign-in, so it may ask once.
struct AccountSafariView: UIViewControllerRepresentable {
    let url: URL

    func makeUIViewController(context: Context) -> SFSafariViewController {
        let configuration = SFSafariViewController.Configuration()
        configuration.barCollapsingEnabled = true
        let controller = SFSafariViewController(url: url, configuration: configuration)
        controller.dismissButtonStyle = .done
        return controller
    }

    func updateUIViewController(_ controller: SFSafariViewController, context: Context) {}
}

extension View {
    /// Opens `page` in the in-app browser.
    func accountWebSheet(_ page: Binding<AccountWebPage?>) -> some View {
        sheet(item: page) { page in
            AccountSafariView(url: page.url).ignoresSafeArea()
        }
    }
}
