import Darwin
import Foundation

struct CommandResult {
    let status: Int32
    let output: String
}

/// Holds output collected on a background queue. Access is serialized by the
/// DispatchGroup join below (write happens-before the read), so the unchecked
/// Sendable conformance is safe.
private final class OutputBox: @unchecked Sendable {
    var data = Data()
}

enum CommandRunnerError: Error, Equatable {
    case timedOut(executable: String, timeout: TimeInterval)
}

enum CommandRunner {
    static func run(
        executable: String,
        arguments: [String],
        environment: [String: String] = [:],
        stdin: String? = nil,
        timeout: TimeInterval = 30
    ) throws -> CommandResult {
        let process = Process()
        process.executableURL = URL(fileURLWithPath: executable)
        process.arguments = arguments

        var env = ProcessInfo.processInfo.environment
        environment.forEach { env[$0.key] = $0.value }
        process.environment = env

        let outputPipe = Pipe()
        process.standardOutput = outputPipe
        process.standardError = outputPipe

        let inputPipe: Pipe?
        if stdin != nil {
            inputPipe = Pipe()
            process.standardInput = inputPipe
        } else {
            inputPipe = nil
        }

        // Drain stdout/stderr on a background queue so a child that fills the
        // pipe buffer (~64KB) cannot deadlock against us writing stdin or
        // waiting for exit. Collect the data, then join below.
        let box = OutputBox()
        let readQueue = DispatchQueue(label: "och.command-runner.read")
        let readGroup = DispatchGroup()
        readGroup.enter()
        readQueue.async {
            box.data = outputPipe.fileHandleForReading.readDataToEndOfFile()
            readGroup.leave()
        }

        try process.run()

        if let stdin, let inputPipe {
            inputPipe.fileHandleForWriting.write(Data(stdin.utf8))
            try inputPipe.fileHandleForWriting.close()
        }

        let waitGroup = DispatchGroup()
        waitGroup.enter()
        DispatchQueue.global(qos: .utility).async {
            process.waitUntilExit()
            waitGroup.leave()
        }

        if waitGroup.wait(timeout: .now() + timeout) == .timedOut {
            process.terminate()
            if waitGroup.wait(timeout: .now() + 1) == .timedOut {
                kill(process.processIdentifier, SIGKILL)
                waitGroup.wait()
            }
            readGroup.wait()
            throw CommandRunnerError.timedOut(executable: executable, timeout: timeout)
        }

        readGroup.wait()
        return CommandResult(status: process.terminationStatus, output: String(data: box.data, encoding: .utf8) ?? "")
    }
}
