import Foundation

/// Owns scanner preflight work; leaving never waits for a server to acknowledge cancellation.
@MainActor
final class ScannerRequestLifetime {
    private var current: UUID?
    private var task: Task<Void, Never>?
    private var closeTransport: (() -> Void)?

    func begin() -> UUID {
        cancel()
        let request = UUID()
        current = request
        return request
    }

    func isCurrent(_ request: UUID) -> Bool {
        current == request && !Task.isCancelled
    }

    func attach(_ task: Task<Void, Never>, request: UUID) {
        guard current == request else { task.cancel(); return }
        self.task = task
    }

    func attachTransport(request: UUID, close: @escaping () -> Void) {
        guard current == request else { close(); return }
        closeTransport = close
    }

    func finish(_ request: UUID) {
        guard current == request else { return }
        current = nil
        task = nil
        closeTransport = nil
    }

    func cancel() {
        current = nil
        let pending = task
        let close = closeTransport
        task = nil
        closeTransport = nil
        pending?.cancel()
        close?()
    }
}
