import Foundation

/// Ollie's stores for the signed-in member in the open workspace, kept here
/// so the conversation survives closing and reopening the sheet. A different
/// server, member or workspace gets fresh ones; only one set is ever held.
@MainActor
final class AssistantHolder {
    static let shared = AssistantHolder()

    struct Suite {
        let chat: AssistantStore
        let history: AssistantHistoryStore
        let tips: AssistantTipsStore
    }

    private var key: String?
    private var suite: Suite?

    private init() {}

    func suite(api: APIClient, workspaceID: String?, userID: String?) -> Suite {
        let key = [String(describing: ObjectIdentifier(api)), workspaceID ?? "", userID ?? ""].joined(separator: "|")
        if let suite, self.key == key { return suite }
        suite?.chat.newChat()
        let fresh = Suite(chat: AssistantStore(api: api), history: AssistantHistoryStore(api: api), tips: AssistantTipsStore(api: api))
        self.key = key
        suite = fresh
        return fresh
    }
}
