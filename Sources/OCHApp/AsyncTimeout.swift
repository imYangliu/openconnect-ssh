import Foundation

enum AsyncTimeoutError: Error, Equatable {
    case timedOut
}

func withTimeout<T: Sendable>(
    seconds: TimeInterval,
    operation: @escaping @Sendable () async throws -> T
) async throws -> T {
    try await withThrowingTaskGroup(of: T.self) { group in
        group.addTask {
            try await operation()
        }
        group.addTask {
            let nanoseconds = UInt64(max(0, seconds) * 1_000_000_000)
            try await Task.sleep(nanoseconds: nanoseconds)
            throw AsyncTimeoutError.timedOut
        }

        let result = try await group.next()
        group.cancelAll()
        guard let result else {
            throw AsyncTimeoutError.timedOut
        }
        return result
    }
}
