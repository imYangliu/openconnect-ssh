import Foundation

@main
struct AsyncTimeoutSmoke {
    static func main() async {
        do {
            _ = try await withTimeout(seconds: 0.05) {
                try await Task.sleep(nanoseconds: 5_000_000_000)
                return "late"
            }
            fail("expected timeout")
        } catch let error as AsyncTimeoutError {
            expect(error == .timedOut, "timed out")
        } catch {
            fail("unexpected timeout error: \(error)")
        }

        do {
            let value = try await withTimeout(seconds: 1) {
                "ok"
            }
            expect(value == "ok", "successful value")
        } catch {
            fail("unexpected success-path error: \(error)")
        }

        print("async timeout smoke passed")
    }

    private static func expect(_ condition: @autoclosure () -> Bool, _ message: String) {
        if !condition() {
            fail(message)
        }
    }

    private static func fail(_ message: String) -> Never {
        FileHandle.standardError.write(Data("async timeout smoke failed: \(message)\n".utf8))
        exit(1)
    }
}
