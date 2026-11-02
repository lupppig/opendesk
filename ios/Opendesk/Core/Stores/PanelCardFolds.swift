import Foundation
import Observation

/// The cards an agent folded or opened in a ticket's panels, while the app runs
/// (contracts/integrations.md "The http panel"): a refresh or a newer answer keeps
/// their choice, and a card they did not touch follows each answer's `collapsed`.
/// Kept in memory only, so a new launch starts from the answers again.
@MainActor @Observable
final class PanelCardFolds {
    static let shared = PanelCardFolds()

    private var open: [String: Bool] = [:]

    private init() {}

    func isOpen(_ card: PanelCard, in scope: String) -> Bool {
        open[key(card, scope)] ?? !card.collapsed
    }

    func set(_ isOpen: Bool, for card: PanelCard, in scope: String) {
        open[key(card, scope)] = isOpen
    }

    /// A card is known by its id, or by its title without one.
    private func key(_ card: PanelCard, _ scope: String) -> String {
        "\(scope)\u{1F}\(card.id.map { "id:\($0)" } ?? "title:\(card.title)")"
    }
}
