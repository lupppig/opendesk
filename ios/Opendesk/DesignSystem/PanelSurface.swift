import SwiftUI

extension View {
    /// The white panel under a screen's glass header, with rounded top corners. It runs on
    /// under the tab bar and the home indicator to the bottom edge, so a list keeps scrolling
    /// behind them instead of stopping short of the screen's foot.
    func panelSurface() -> some View {
        let shape = UnevenRoundedRectangle(topLeadingRadius: Theme.Radius.card, topTrailingRadius: Theme.Radius.card, style: .continuous)
        return background { shape.fill(Theme.surface).ignoresSafeArea(.container, edges: .bottom) }
            .mask { shape.ignoresSafeArea(.container, edges: .bottom) }
    }
}
