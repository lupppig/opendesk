import Foundation

/// How a workspace shows its ticket statuses (contracts/api.md `WorkspaceSettings`): the
/// names it gives them, the ones it hides from the menus agents pick from, and the words
/// for finishing a ticket. Finishing always sets `solved`; it is named after solved's own
/// label, so a workspace that labels it Closed reads "Close" and "Send and close".
struct StatusDisplay {
    /// How finishing is named.
    enum Verb: Equatable {
        case solve, close
        /// A label of the workspace's own ("Done"): "Mark as Done".
        case mark(String)
    }

    var labels: [String: DisplayOverride]
    /// Sending and finishing on a segment of its own before Send.
    var finishButton: Bool

    init(_ settings: WorkspaceSettings?) {
        labels = settings?.statusLabels ?? [:]
        finishButton = settings?.sendAndFinishButton ?? false
    }

    /// What finishing sets, whatever it is called.
    let finish = TicketStatus.solved

    func title(_ status: TicketStatus) -> String {
        let label = labels[status.rawValue]?.label?.trimmingCharacters(in: .whitespaces) ?? ""
        return label.isEmpty ? status.title : label
    }

    /// Left out of the status menus; solved never is, since it is how a ticket finishes.
    func hidden(_ status: TicketStatus) -> Bool {
        status != .solved && status != .open && labels[status.rawValue]?.hidden == true
    }

    /// The statuses an agent may pick, in menu order, keeping the ticket's current one.
    func offered(current: TicketStatus? = nil) -> [TicketStatus] {
        TicketStatus.allCases.filter { $0 == current || !hidden($0) }
    }

    var verb: Verb {
        let name = labels[TicketStatus.solved.rawValue]?.label?.trimmingCharacters(in: .whitespaces) ?? ""
        switch name.lowercased() {
        case "", TicketStatus.solved.title.lowercased(): return .solve
        case TicketStatus.closed.title.lowercased(): return .close
        default: return .mark(name)
        }
    }

    /// The ticket header's button.
    var finishTitle: String {
        switch verb {
        case .solve: "Solve"
        case .close: "Close"
        case let .mark(status): "Mark as \(status)"
        }
    }

    /// A menu item that finishes the ticket.
    var finishCommand: String {
        switch verb {
        case .solve: "Solve ticket"
        case .close: "Close ticket"
        case let .mark(status): "Mark ticket as \(status)"
        }
    }

    func finishToast(_ number: Int) -> String {
        switch verb {
        case .solve: "Solved #\(number)"
        case .close: "Closed #\(number)"
        case let .mark(status): "Marked #\(number) as \(status)"
        }
    }

    /// The composer's finish segment: "Send and solve", "Add note and close".
    func sendAndFinish(note: Bool) -> String {
        let lead = note ? "Add note and" : "Send and"
        switch verb {
        case .solve: return "\(lead) solve"
        case .close: return "\(lead) close"
        case let .mark(status): return "\(lead) mark as \(status)"
        }
    }

    /// The toast after setting a status by hand.
    func statusToast(_ status: TicketStatus, number: Int) -> String {
        switch status {
        case finish: finishToast(number)
        case .open: "Reopened #\(number)"
        default: "Marked #\(number) \(title(status).lowercased())"
        }
    }
}

extension AppEnvironment {
    /// The open workspace's status names, hidden statuses and finish words.
    var statusDisplay: StatusDisplay { StatusDisplay(session.workspace?.workspace.settings) }
}
