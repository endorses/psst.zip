import Foundation

/// Finite account cooldowns shared by reconstructed History views in this process.
/// If all slots hold live server deadlines, overflow uses one conservative deadline
/// for untracked scopes instead of evicting an active restriction or growing the map.
struct HistoryCooldowns {
    let capacity: Int
    private(set) var deadlines: [String: Date] = [:]
    private(set) var overflow: Date?

    init(capacity: Int = 500) { self.capacity = max(1, capacity) }

    mutating func deadline(for scope: String, now: Date) -> Date? {
        prune(now: now)
        return deadlines[scope] ?? overflow
    }

    mutating func record(scope: String, until deadline: Date, now: Date) {
        prune(now: now)
        guard deadline > now else { return }
        if let previous = deadlines[scope] {
            deadlines[scope] = max(previous, deadline)
        } else if deadlines.count < capacity {
            deadlines[scope] = deadline
        } else {
            overflow = max(overflow ?? deadline, deadline)
        }
    }

    private mutating func prune(now: Date) {
        deadlines = deadlines.filter { $0.value > now }
        if let overflow, overflow <= now { self.overflow = nil }
    }
}
