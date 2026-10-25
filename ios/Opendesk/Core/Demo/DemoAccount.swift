#if DEBUG
import Foundation

// The account's routes beyond the core ones in DemoBackend.swift: profile, security
// (password, 2FA, passkeys), notification preferences, saved replies and views.

/// The signed-in user's account data that is not per workspace.
struct DemoAccount: Sendable {
    var passkeys: [Passkey] = []
    var totpSecret: String?
    /// The email schedule and the pause are the account's, across workspaces.
    var cadence = "instant"
    var digestHour = 9
    var timezone = DemoMode.timeZone
    var pausedUntil: Date?

    static func seed(user: User) -> DemoAccount {
        var account = DemoAccount()
        account.passkeys = [
            Passkey(id: demoID(.made, 9001), name: "iCloud Keychain", createdAt: DemoSeed.ago(64 * 1440), lastUsedAt: DemoSeed.ago(2 * 1440), backedUp: true),
            Passkey(id: demoID(.made, 9002), name: "YubiKey 5C", createdAt: DemoSeed.ago(210 * 1440), lastUsedAt: DemoSeed.ago(31 * 1440), backedUp: false),
        ]
        return account
    }
}

/// The user's data in one workspace (member profiles, notification choices, teams of replies and views).
struct DemoAccountWorkspace: Sendable {
    struct Channels: Sendable {
        var type: String
        var group: String
        var email: Bool
        var push: Bool
        var desktop: Bool
        var urgent = false
    }

    var profiles: [String: MemberProfile] = [:]
    var types: [Channels] = []
    var unsubscribedAt: Date?
    var replyTeams: [String: String] = [:]
    var viewTeams: [String: String] = [:]

    static func seed(_ ws: DemoWorkspace) -> DemoAccountWorkspace {
        var extras = DemoAccountWorkspace()
        if ws.id == DemoSeed.marginID {
            extras.profiles[DemoPerson.maya.id] = MemberProfile(
                title: "Head of support", phone: "+49 30 5557 0142", pronouns: "she/her",
                signatureHTML: "<p>Maya Chen<br>Head of support, Margin</p>"
            )
            extras.profiles[DemoPerson.jonas.id] = MemberProfile(title: "Support engineer", phone: "", pronouns: "he/him", signatureHTML: "")
            extras.profiles[DemoPerson.priya.id] = MemberProfile(title: "Customer success", phone: "", pronouns: "she/her", signatureHTML: "")
            if let view = ws.views.first(where: { $0.name == "Billing queue" }) { extras.viewTeams[view.id] = DemoSeed.billing }
            if let reply = ws.savedReplies.first(where: { $0.title.localizedCaseInsensitiveContains("refund") }) { extras.replyTeams[reply.id] = DemoSeed.billing }
        }
        let admin = ws.role.isAdmin
        extras.types = [
            Channels(type: "assigned", group: "tickets", email: true, push: true, desktop: true, urgent: true),
            Channels(type: "new_ticket", group: "tickets", email: false, push: true, desktop: true),
            Channels(type: "mentioned", group: "tickets", email: true, push: true, desktop: true, urgent: true),
            Channels(type: "customer_replied", group: "tickets", email: true, push: true, desktop: false),
            Channels(type: "thread_replied", group: "tickets", email: true, push: true, desktop: true),
            Channels(type: "sla_breached", group: "tickets", email: true, push: true, desktop: true, urgent: true),
            Channels(type: "ai_handoff", group: "tickets", email: false, push: true, desktop: true, urgent: true),
            Channels(type: "collab_assigned", group: "mail", email: true, push: true, desktop: true),
            Channels(type: "collab_mention", group: "mail", email: true, push: true, desktop: true),
            Channels(type: "team_joined", group: "team", email: true, push: false, desktop: true),
        ]
        if admin {
            extras.types += [
                Channels(type: "ai_cap_reached", group: "ai_billing", email: true, push: false, desktop: true),
                Channels(type: "ai_key_failed", group: "ai_billing", email: true, push: true, desktop: true, urgent: true),
                Channels(type: "ai_balance_low", group: "ai_billing", email: true, push: false, desktop: true),
                Channels(type: "ai_balance_out", group: "ai_billing", email: true, push: true, desktop: true, urgent: true),
                Channels(type: "auto_recharge_failed", group: "ai_billing", email: true, push: true, desktop: true),
                Channels(type: "import_finished", group: "workspace", email: true, push: false, desktop: true),
            ]
        }
        return extras
    }
}

extension DemoBackend {
    /// Routes without a workspace. The answer (nil inside for 204), or nil when not this area's.
    func meRoutes(_ call: Call) throws -> Data?? {
        // Two-factor and email confirmation for the signed-in person, ahead of the demo's sign-in routes.
        if call.method == "POST", ["auth/totp/setup", "auth/totp/enable", "auth/totp/disable", "auth/email/resend"].contains(call.path.joined(separator: "/")) {
            return .some(try accountAuthRoute(call.path.joined(separator: "/"), body: call.body))
        }
        // Photo.
        if call.match("PUT", "me/avatar") != nil {
            guard !call.raw.isEmpty else { throw Failure(status: 422, code: "invalid_file", detail: "Choose a PNG, JPEG, GIF or WebP image.") }
            world.user.avatarURL = "https://app.opendesk.sh/demo/avatars/maya.jpg"
            world.user.avatarSource = "upload"
            return .some(encode(world.user))
        }
        if call.match("DELETE", "me/avatar") != nil {
            world.user.avatarURL = nil
            world.user.avatarSource = nil
            return .some(encode(world.user))
        }

        // Password.
        if call.match("POST", "me/password") != nil {
            let current = call.string("current_password") ?? ""
            let next = call.string("new_password") ?? ""
            if current.isEmpty || current == "wrong password" { throw Failure(status: 401, code: "invalid_credentials", detail: "Your current password is not right.") }
            if next.count < 12 { throw Failure(status: 422, code: "weak_password", detail: "Use at least 12 characters.") }
            if next.count > 128 { throw Failure(status: 422, code: "weak_password", detail: "Use at most 128 characters.") }
            world.sessions.removeAll { $0.current != true }
            return .some(nil)
        }

        // Passkeys.
        if call.match("GET", "me/passkeys") != nil { return .some(encode(ListBody(data: world.account.passkeys))) }
        if call.match("POST", "me/passkeys/register/begin") != nil {
            if world.account.passkeys.count >= 25 { throw Failure(status: 409, code: "passkey_limit", detail: "An account can have 25 passkeys. Remove one to add another.") }
            return .some(encode(JSONValue.object([
                "options": .object([
                    "challenge": .string("ZGVtby1jaGFsbGVuZ2U"),
                    "rp": .object(["id": .string("app.opendesk.sh"), "name": .string("opendesk")]),
                    "user": .object(["id": .string("AZegwAAAAAAAAAAAAAAAAQ"), "name": .string(world.user.email), "displayName": .string(world.user.name)]),
                ]),
                "passkey_token": .string("demo-passkey-token"),
            ])))
        }
        if call.match("POST", "me/passkeys/register/finish") != nil {
            let name = call.string("name").flatMap { $0.isEmpty ? nil : $0 } ?? "iPhone"
            let passkey = Passkey(id: made(), name: name, createdAt: now(), lastUsedAt: nil, backedUp: true)
            world.account.passkeys.insert(passkey, at: 0)
            return .some(encode(passkey))
        }
        if let id = call.match("PATCH", "me/passkeys/*")?.first {
            guard let index = world.account.passkeys.firstIndex(where: { $0.id == id }) else { throw notFound("passkey") }
            if let name = call.string("name"), !name.isEmpty { world.account.passkeys[index].name = String(name.prefix(64)) }
            return .some(encode(world.account.passkeys[index]))
        }
        if let id = call.match("DELETE", "me/passkeys/*")?.first {
            world.account.passkeys.removeAll { $0.id == id }
            return .some(nil)
        }

        // Notification preferences (the channels are per workspace).
        if call.path.starts(with: ["me", "notification-preferences"]) {
            guard let wid = call.workspace, let index = world.workspaces.firstIndex(where: { $0.id == wid }) else {
                throw Failure(status: 400, code: "workspace_required", detail: "Pick a workspace first.")
            }
            if call.match("GET", "me/notification-preferences") != nil { return .some(encode(preferences(world.workspaces[index]))) }
            if call.match("PATCH", "me/notification-preferences") != nil {
                try updatePreferences(call.body, workspace: index)
                return .some(encode(preferences(world.workspaces[index])))
            }
            if call.match("POST", "me/notification-preferences/resubscribe") != nil {
                world.workspaces[index].accountExtras.unsubscribedAt = nil
                return .some(encode(preferences(world.workspaces[index])))
            }
            if call.match("POST", "me/notification-preferences/test") != nil {
                guard world.user.emailVerified == true else { throw Failure(status: 422, code: "email_unverified", detail: "Confirm your email address first.") }
                return .some(nil)
            }
        }
        return nil
    }

    /// Workspace routes. The answer (nil inside for 204), or nil when not this area's.
    func accountRoutes(_ call: Call, in ws: inout DemoWorkspace) throws -> Data?? {
        // Members with their workspace profile.
        if call.match("GET", "members") != nil {
            let members = ws.members.map { member -> JSONValue in
                var member = member
                member.openTickets = ws.tickets.filter { !$0.spam && !$0.status.isDone && $0.assignee?.id == member.id }.count
                var object = DemoSeed.json(member).object ?? [:]
                object["profile"] = DemoSeed.json(ws.accountExtras.profiles[member.id] ?? .empty)
                return .object(object)
            }
            return .some(encode(ListBody(data: members)))
        }
        if call.match("PATCH", "members/me/profile") != nil {
            guard let member = ws.members.first(where: { $0.id == world.user.id }) else { throw notFound("member") }
            var profile = ws.accountExtras.profiles[member.id] ?? .empty
            if let value = call.string("title") { profile.title = String(value.prefix(80)) }
            if let value = call.string("phone") { profile.phone = String(value.prefix(40)) }
            if let value = call.string("pronouns") { profile.pronouns = String(value.prefix(40)) }
            if let value = call.string("signature_html") { profile.signatureHTML = value }
            ws.accountExtras.profiles[member.id] = profile
            var object = DemoSeed.json(member).object ?? [:]
            object["profile"] = DemoSeed.json(profile)
            publish("member.updated", ["member": DemoSeed.json(member)], in: &ws)
            return .some(encode(JSONValue.object(object)))
        }

        // Saved replies, with their team.
        if call.match("GET", "saved-replies") != nil {
            return .some(encode(ListBody(data: ws.savedReplies.map { reply(json: $0, in: ws) })))
        }
        if call.match("POST", "saved-replies") != nil {
            let shortcut = try cleanShortcut(call.string("shortcut"), except: nil, in: ws)
            let saved = SavedReply(id: made(), title: call.string("title") ?? "Saved reply", shortcut: shortcut, bodyHTML: call.string("body_html") ?? "")
            ws.savedReplies.append(saved)
            if let team = call.string("team_id"), !team.isEmpty { ws.accountExtras.replyTeams[saved.id] = team }
            let json = reply(json: saved, in: ws)
            publish("saved_reply.created", ["saved_reply": json], in: &ws)
            return .some(encode(json))
        }
        if let id = call.match("PATCH", "saved-replies/*")?.first {
            guard let index = ws.savedReplies.firstIndex(where: { $0.id == id }) else { throw notFound("saved reply") }
            if let title = call.string("title"), !title.isEmpty { ws.savedReplies[index].title = title }
            if call.body["shortcut"] != nil { ws.savedReplies[index].shortcut = try cleanShortcut(call.string("shortcut"), except: id, in: ws) }
            if let body = call.string("body_html"), !body.isEmpty { ws.savedReplies[index].bodyHTML = body }
            if let team = call.string("team_id") { ws.accountExtras.replyTeams[id] = team.isEmpty ? nil : team }
            let json = reply(json: ws.savedReplies[index], in: ws)
            publish("saved_reply.updated", ["saved_reply": json], in: &ws)
            return .some(encode(json))
        }
        if let id = call.match("DELETE", "saved-replies/*")?.first {
            ws.savedReplies.removeAll { $0.id == id }
            ws.accountExtras.replyTeams[id] = nil
            publish("saved_reply.deleted", ["saved_reply_id": .string(id)], in: &ws)
            return .some(nil)
        }

        // Views, with their team.
        if call.match("GET", "views") != nil {
            return .some(encode(ListBody(data: ws.views.sorted { ($0.position ?? 0) < ($1.position ?? 0) }.map { view(json: $0, in: ws) })))
        }
        if call.match("POST", "views") != nil {
            let shared = call.body["shared"]?.bool ?? false
            if shared, !ws.role.isAdmin { throw Failure(status: 403, code: "forbidden", detail: "Only admins share views.") }
            let filters = call.body["filters"].flatMap { try? APICoding.decoder.decode(TicketFilters.self, from: encode($0)) } ?? TicketFilters()
            let saved = SavedView(id: made(), name: call.string("name") ?? "View", shared: shared, filters: filters, position: (ws.views.compactMap(\.position).max() ?? -1) + 1)
            ws.views.append(saved)
            if let team = call.string("team_id"), !team.isEmpty { ws.accountExtras.viewTeams[saved.id] = team }
            let json = view(json: saved, in: ws)
            publish("view.created", ["view": json], in: &ws)
            return .some(encode(json))
        }
        if let id = call.match("PATCH", "views/*")?.first {
            guard let index = ws.views.firstIndex(where: { $0.id == id }) else { throw notFound("view") }
            if let name = call.string("name"), !name.isEmpty { ws.views[index].name = name }
            if let shared = call.body["shared"]?.bool { ws.views[index].shared = shared }
            if let position = call.body["position"]?.int { ws.views[index].position = position }
            if let filters = call.body["filters"], let decoded = try? APICoding.decoder.decode(TicketFilters.self, from: encode(filters)) { ws.views[index].filters = decoded }
            if let team = call.string("team_id") { ws.accountExtras.viewTeams[id] = team.isEmpty ? nil : team }
            let json = view(json: ws.views[index], in: ws)
            publish("view.updated", ["view": json], in: &ws)
            return .some(encode(json))
        }
        if let id = call.match("DELETE", "views/*")?.first {
            ws.views.removeAll { $0.id == id }
            ws.accountExtras.viewTeams[id] = nil
            publish("view.deleted", ["view_id": .string(id)], in: &ws)
            return .some(nil)
        }
        return nil
    }

    private func accountAuthRoute(_ route: String, body: JSONValue) throws -> Data? {
        switch route {
        case "auth/totp/setup":
            let secret = "JBSWY3DPEHPK3PXPMZXW6YTB"
            world.account.totpSecret = secret
            let label = "opendesk:\(world.user.email)".addingPercentEncoding(withAllowedCharacters: .urlPathAllowed) ?? world.user.email
            return encode(JSONValue.object([
                "secret": .string(secret),
                "otpauth_url": .string("otpauth://totp/\(label)?secret=\(secret)&issuer=opendesk&algorithm=SHA1&digits=6&period=30"),
            ]))
        case "auth/totp/enable":
            let code = body["code"]?.string ?? ""
            guard world.account.totpSecret != nil else { throw Failure(status: 409, code: "totp_not_started", detail: "Start two-factor setup again.") }
            guard code.count == 6, code.allSatisfy(\.isNumber), code != "000000" else { throw Failure(status: 400, code: "invalid_code", detail: "That code didn't work.") }
            world.user.totpEnabled = true
            world.account.totpSecret = nil
            let alphabet = Array("abcdefghjkmnpqrstuvwxyz23456789")
            let codes = (1 ... 10).map { index in
                var state = UInt64(index) &* 0x9E37_79B9_7F4A_7C15
                let letters = (0 ..< 16).map { _ -> Character in
                    state = state &* 6_364_136_223_846_793_005 &+ 1_442_695_040_888_963_407
                    return alphabet[Int((state >> 33) % UInt64(alphabet.count))]
                }
                return stride(from: 0, to: 16, by: 4).map { String(letters[$0 ..< $0 + 4]) }.joined(separator: "-")
            }
            return encode(JSONValue.object(["recovery_codes": .array(codes.map(JSONValue.string))]))
        case "auth/totp/disable":
            let code = body["code"]?.string ?? ""
            let digits = code.count == 6 && code.allSatisfy(\.isNumber) && code != "000000"
            let recovery = code.count == 19 && code.split(separator: "-").count == 4
            guard digits || recovery else { throw Failure(status: 400, code: "invalid_code", detail: "That code didn't work.") }
            world.user.totpEnabled = false
            return nil
        case "auth/email/resend":
            return nil
        default:
            throw Failure(status: 404, code: "not_found", detail: "The demo has no answer for /v1/\(route).")
        }
    }

    // MARK: Helpers

    private func preferences(_ ws: DemoWorkspace) -> JSONValue {
        let account = world.account
        let types = ws.accountExtras.types.map { item -> JSONValue in
            .object([
                "type": .string(item.type), "group": .string(item.group),
                "email": .bool(item.email), "push": .bool(item.push), "desktop": .bool(item.desktop),
                "defaults": .object(["email": .bool(true), "push": .bool(true), "desktop": .bool(true)]),
                "urgent": .bool(item.urgent),
            ])
        }
        return .object([
            "email_cadence": .string(account.cadence),
            "digest_hour": .number(Double(account.digestHour)),
            "timezone": .string(account.timezone),
            "paused_until": account.pausedUntil.map { .string(DemoSeed.stamp($0)) } ?? .null,
            "email": .string(world.user.email),
            "email_verified": .bool(world.user.emailVerified ?? false),
            "delay_minutes": .number(5),
            "pacing": .object([
                "delay_minutes": .number(5), "cooldown_minutes": .array([.number(15), .number(60)]),
                "quiet_reset_minutes": .number(180), "urgent_gap_minutes": .number(5), "urgent_per_day": .number(24),
                "per_hour": .number(12), "per_day": .number(60), "active_minutes": .number(10),
            ]),
            "unsubscribed_at": ws.accountExtras.unsubscribedAt.map { .string(DemoSeed.stamp($0)) } ?? .null,
            "types": .array(types),
        ])
    }

    private func updatePreferences(_ body: JSONValue, workspace index: Int) throws {
        if let cadence = body["email_cadence"]?.string {
            guard ["instant", "hourly", "daily", "off"].contains(cadence) else { throw Failure(status: 422, code: "invalid_cadence", detail: "Choose right away, hourly, daily or off.") }
            world.account.cadence = cadence
        }
        if let hour = body["digest_hour"]?.int { world.account.digestHour = max(0, min(23, hour)) }
        if let zone = body["timezone"]?.string {
            guard TimeZone(identifier: zone) != nil else { throw Failure(status: 422, code: "invalid_timezone", detail: "That time zone isn't known.") }
            world.account.timezone = zone
        }
        if let paused = body["paused_until"]?.string {
            if paused.isEmpty {
                world.account.pausedUntil = nil
            } else if let date = try? Date(paused, strategy: .iso8601) {
                world.account.pausedUntil = min(date, DemoMode.anchor.addingTimeInterval(30 * 86400))
            }
        }
        for change in body["types"]?.array ?? [] {
            guard let type = change["type"]?.string,
                  let row = world.workspaces[index].accountExtras.types.firstIndex(where: { $0.type == type }) else {
                throw Failure(status: 422, code: "invalid_type", detail: "That notification type isn't known.")
            }
            if let on = change["email"]?.bool {
                world.workspaces[index].accountExtras.types[row].email = on
                if on { world.workspaces[index].accountExtras.unsubscribedAt = nil }
            }
            if let on = change["push"]?.bool { world.workspaces[index].accountExtras.types[row].push = on }
            if let on = change["desktop"]?.bool { world.workspaces[index].accountExtras.types[row].desktop = on }
        }
    }

    private func reply(json reply: SavedReply, in ws: DemoWorkspace) -> JSONValue {
        var object = DemoSeed.json(reply).object ?? [:]
        object["team_id"] = ws.accountExtras.replyTeams[reply.id].map(JSONValue.string) ?? .null
        object["shortcut"] = .string(reply.shortcut ?? "")
        return .object(object)
    }

    private func view(json view: SavedView, in ws: DemoWorkspace) -> JSONValue {
        var object = DemoSeed.json(view).object ?? [:]
        object["team_id"] = ws.accountExtras.viewTeams[view.id].map(JSONValue.string) ?? .null
        return .object(object)
    }

    private func cleanShortcut(_ raw: String?, except id: String?, in ws: DemoWorkspace) throws -> String? {
        var value = (raw ?? "").trimmingCharacters(in: .whitespaces).lowercased()
        while value.hasPrefix("/") { value.removeFirst() }
        guard !value.isEmpty else { return nil }
        if ws.savedReplies.contains(where: { $0.id != id && $0.shortcut?.lowercased() == value }) {
            throw Failure(status: 409, code: "shortcut_taken", detail: "Another saved reply uses /\(value).")
        }
        return value
    }
}
#endif
