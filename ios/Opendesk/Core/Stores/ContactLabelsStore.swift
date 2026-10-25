import Foundation
import Observation

/// The labels that apply to contacts or companies (`GET /v1/tags?applies_to=`),
/// for the directory's label picker and filter. Ticket labels live in `DirectoryStore`.
@MainActor
@Observable
final class ContactLabelsStore {
    let entity: DirectoryEntity
    private(set) var tags: [Tag] = []
    private(set) var loaded = false

    init(entity: DirectoryEntity) {
        self.entity = entity
    }

    func load(api: APIClient) async {
        let page: Page<Tag>? = try? await api.get("v1/tags", query: [
            URLQueryItem(name: "applies_to", value: entity.tagScope),
            URLQueryItem(name: "archived", value: "false"),
        ])
        if let page {
            tags = page.data
                .filter { $0.archived != true && ($0.appliesTo?.contains(entity.tagScope) ?? true) }
                .sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
        }
        loaded = true
    }
}
