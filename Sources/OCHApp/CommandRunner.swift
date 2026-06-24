import Foundation

struct CommandResult {
    let status: Int32
    let output: String
}

enum CommandRunner {
    static func run(
        executable: String,
        arguments: [String],
        environment: [String: String] = [:],
        stdin: String? = nil
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

        if let stdin {
            let inputPipe = Pipe()
            process.standardInput = inputPipe
            try process.run()
            inputPipe.fileHandleForWriting.write(Data(stdin.utf8))
            try inputPipe.fileHandleForWriting.close()
        } else {
            try process.run()
        }

        let data = outputPipe.fileHandleForReading.readDataToEndOfFile()
        process.waitUntilExit()
        return CommandResult(status: process.terminationStatus, output: String(data: data, encoding: .utf8) ?? "")
    }
}
