import Foundation

extension APIClient {
    /// Every page of a cursor-paged list (`{data, next_cursor}`), 100 at a time, as the web's
    /// `allPages`: a list the app needs whole (the workspace's members) never stops at the first page.
    func all<T: Decodable & Sendable>(_ path: String, query: [URLQueryItem] = []) async throws -> [T] {
        var out: [T] = []
        var cursor: String?
        var seen: Set<String> = []
        repeat {
            var items = query + [URLQueryItem(name: "limit", value: "100")]
            if let cursor { items.append(URLQueryItem(name: "cursor", value: cursor)) }
            let page: Page<T> = try await get(path, query: items)
            out += page.data
            cursor = page.nextCursor.flatMap { $0.isEmpty ? nil : $0 }
            // A cursor that comes back again would never end.
            if let next = cursor, !seen.insert(next).inserted { break }
        } while cursor != nil
        return out
    }
}
