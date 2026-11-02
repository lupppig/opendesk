import Foundation

/// Small, non-secret settings in `UserDefaults`. Tokens never go here (Keychain only).
enum Preferences {
    private static var defaults: UserDefaults {
        #if DEBUG
        if DemoMode.isOn { return DemoMode.defaults }
        #endif
        return .standard
    }

    private enum Key {
        static let instance = "instance"
        static let appearance = "appearance"
        static let askedPush = "push.asked"
        static func workspace(_ server: String) -> String { "workspace." + server }
        static func device(_ server: String) -> String { "push.device." + server }
        static func folder(_ server: String) -> String { "inbox.folder." + server }
        static func sort(_ list: String) -> String { "inbox.sort." + list }
    }

    static var instance: Instance? {
        get {
            guard let data = defaults.data(forKey: Key.instance) else { return nil }
            return try? JSONDecoder().decode(Instance.self, from: data)
        }
        set {
            if let newValue, let data = try? JSONEncoder().encode(newValue) {
                defaults.set(data, forKey: Key.instance)
            } else {
                defaults.removeObject(forKey: Key.instance)
            }
        }
    }

    static func workspaceID(for instance: Instance) -> String? {
        defaults.string(forKey: Key.workspace(instance.storageKey))
    }

    static func setWorkspaceID(_ id: String?, for instance: Instance) {
        defaults.set(id, forKey: Key.workspace(instance.storageKey))
    }

    /// The `Device` id this install registered on a server, for PATCH and DELETE.
    static func deviceID(for instance: Instance) -> String? {
        defaults.string(forKey: Key.device(instance.storageKey))
    }

    static func setDeviceID(_ id: String?, for instance: Instance) {
        defaults.set(id, forKey: Key.device(instance.storageKey))
    }

    static func lastFolder(for instance: Instance) -> String? {
        defaults.string(forKey: Key.folder(instance.storageKey))
    }

    static func setLastFolder(_ id: String, for instance: Instance) {
        defaults.set(id, forKey: Key.folder(instance.storageKey))
    }

    /// The order a member last chose for a ticket list, per server, workspace, member and
    /// list; nil for the default order, which is never kept.
    static func listSort(_ list: String) -> TicketSort? {
        defaults.string(forKey: Key.sort(list)).flatMap(TicketSort.init(saved:))
    }

    static func setListSort(_ sort: TicketSort, _ list: String) {
        if sort.isDefault {
            defaults.removeObject(forKey: Key.sort(list))
        } else {
            defaults.set(sort.saved, forKey: Key.sort(list))
        }
    }

    static var appearance: String {
        get { defaults.string(forKey: Key.appearance) ?? "system" }
        set { defaults.set(newValue, forKey: Key.appearance) }
    }

    /// The app explained notifications and asked once; the system prompt is never forced again.
    static var askedForPush: Bool {
        get { defaults.bool(forKey: Key.askedPush) }
        set { defaults.set(newValue, forKey: Key.askedPush) }
    }
}
