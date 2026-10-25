#if DEBUG
import Foundation
import Synchronization

/// A signed-in run against an in-memory backend, for screenshots and screen
/// recordings (the QA harness in `qa/`) and for trying a debug build on a phone
/// with no server in reach. Debug builds only, switched on with `OPENDESK_DEMO=1`
/// in the launch environment:
///
///     SIMCTL_CHILD_OPENDESK_DEMO=1 xcrun simctl launch <udid> sh.opendesk.app
///
/// or from the sign-in screen ("Explore the demo"), which `enter()`s it until
/// sign-out `leave()`s it; that choice is kept across launches.
///
/// The API answers from `DemoBackend` (the world in `DemoSeed`), realtime
/// stays on the device, push and App Attest are skipped, and the clock,
/// time zone and stored preferences start the same on every launch, so the
/// same build draws the same screens. Nothing goes over the network.
enum DemoMode {
    /// Every seam asks this first, so the first ask (building the session, before any screen) sets the run up.
    static var isOn: Bool {
        state.withLock { state in
            if let on = state { return on }
            let on = launchedInDemo || UserDefaults.standard.bool(forKey: chosenKey)
            if on { start() }
            state = on
            return on
        }
    }

    /// The launch environment asked for the demo, so leaving it is not offered.
    static var launchedInDemo: Bool { AppInfo.debugValue("OPENDESK_DEMO") == "1" }

    /// Switches the demo on from inside the app; the session then moves to `instance`.
    static func enter() {
        UserDefaults.standard.set(true, forKey: chosenKey)
        state.withLock { state in
            if state != true { start() }
            state = true
        }
    }

    /// Switches a demo entered from the app off again, back to the device's clock and Keychain.
    static func leave() {
        guard !launchedInDemo else { return }
        UserDefaults.standard.removeObject(forKey: chosenKey)
        state.withLock { state in
            state = false
            keychain.withLock { $0 = [:] }
            unsetenv("TZ")
            tzset()
            NSTimeZone.resetSystemTimeZone()
        }
    }

    private static let state = Mutex<Bool?>(nil)
    private static let chosenKey = "opendesk.demo"

    /// The instant every demo screen is drawn at: Tuesday 16 June 2026, 09:30 UTC (11:30 in Berlin).
    static let anchor = Date(timeIntervalSince1970: 1_781_602_200)

    /// Margin's time zone, so clock times and "Today" read the same on any Mac.
    static let timeZone = "Europe/Berlin"

    /// The app origin is Cloud's (links on `app.opendesk.sh` open in the app); the API host never resolves.
    static let instance = Instance(
        appOrigin: Instance.cloudAppOrigin,
        apiURL: URL(string: "https://api.demo.opendesk.invalid")!,
        gatewayURL: nil,
        selfHosted: false
    )

    /// Email and password only: no human check, no OAuth or passkeys (they would leave the device).
    static let authConfig: AuthConfig = {
        let json = #"{"providers":[],"password_login":true,"signup_enabled":true,"passkeys":false,"native":{"app_attest":false,"push":"direct","relay_url":null}}"#
        return try! APICoding.decoder.decode(AuthConfig.self, from: Data(json.utf8))
    }()

    /// Preferences live in a suite of their own, emptied at launch.
    static let defaultsSuite = "sh.opendesk.app.demo"

    static var defaults: UserDefaults { UserDefaults(suiteName: defaultsSuite) ?? .standard }

    /// The Keychain stand-in: a demo session never touches the real one.
    private static let keychain = Mutex<[String: String]>([:])

    static func keychainValue(_ account: String) -> String? { keychain.withLock { $0[account] } }
    static func setKeychainValue(_ value: String?, for account: String) { keychain.withLock { $0[account] = value } }

    /// Runs once, before anything reads the clock, the time zone or preferences.
    private static func start() {
        setenv("TZ", timeZone, 1)
        tzset()
        NSTimeZone.resetSystemTimeZone()
        UserDefaults.standard.removePersistentDomain(forName: defaultsSuite)
        // Signed in from the first frame: the launch refresh finds this token.
        keychain.withLock { $0 = ["refresh." + instance.storageKey: DemoBackend.refreshToken] }
    }
}

/// Paths the QA harness hands a running demo without a system prompt: it writes
/// one to `tmp/opendesk-demo-open` in the app's data container, then posts
/// `sh.opendesk.demo.open` (`xcrun simctl spawn <udid> notifyutil -p sh.opendesk.demo.open`).
@MainActor
enum DemoLinks {
    static let notification = "sh.opendesk.demo.open"
    private static var handler: ((String) -> Void)?

    static var file: URL { FileManager.default.temporaryDirectory.appending(path: "opendesk-demo-open") }

    static func watch(_ handler: @escaping (String) -> Void) {
        self.handler = handler
        CFNotificationCenterAddObserver(CFNotificationCenterGetDarwinNotifyCenter(), nil, { _, _, _, _, _ in
            Task { @MainActor in DemoLinks.deliver() }
        }, notification as CFString, nil, .deliverImmediately)
    }

    private static func deliver() {
        guard let path = try? String(contentsOf: file, encoding: .utf8) else { return }
        try? FileManager.default.removeItem(at: file)
        handler?(path)
    }
}
#endif
