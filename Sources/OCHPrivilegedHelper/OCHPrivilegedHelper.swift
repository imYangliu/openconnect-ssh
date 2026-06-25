import Foundation
@preconcurrency import XPC
import OCHXPCRequirement

private let allowedActions = Set(["connect", "disconnect", "status", "logs"])

@main
enum OCHPrivilegedHelper {
    static func main() {
        xpc_main { peer in
            if OCHSetTeamPeerRequirement(peer, "io.github.imyangliu.och") != 0 {
                xpc_connection_cancel(peer)
                return
            }

            let peerBox = OCHHelperConnectionBox(peer)
            xpc_connection_set_event_handler(peerBox.connection) { event in
                if event === XPC_ERROR_CONNECTION_INTERRUPTED || event === XPC_ERROR_CONNECTION_INVALID {
                    return
                }
                handle(event, peer: peerBox.connection)
            }
            xpc_connection_resume(peerBox.connection)
        }
    }
}

private func handle(_ message: xpc_object_t, peer: xpc_connection_t) {
    guard let rawCommand = xpc_dictionary_get_string(message, "command") else {
        sendError("missing XPC command", to: message, peer: peer)
        return
    }

    switch String(cString: rawCommand) {
    case "ping":
        sendResponse(Data("ok".utf8), to: message, peer: peer)
    case "perform":
        perform(message, peer: peer)
    default:
        sendError("unknown XPC command", to: message, peer: peer)
    }
}

private func perform(_ message: xpc_object_t, peer: xpc_connection_t) {
    var length = 0
    guard let bytes = xpc_dictionary_get_data(message, "request", &length) else {
        sendError("missing service request", to: message, peer: peer)
        return
    }

    let request = Data(bytes: bytes, count: length)
    guard actionIsAllowed(in: request) else {
        sendError("unknown or disallowed service action", to: message, peer: peer)
        return
    }

    do {
        let response = try runServiceExec(stdin: request)
        sendResponse(response, to: message, peer: peer)
    } catch {
        sendError(error.localizedDescription, to: message, peer: peer)
    }
}

private func actionIsAllowed(in request: Data) -> Bool {
    guard
        let object = try? JSONSerialization.jsonObject(with: request),
        let dictionary = object as? [String: Any],
        let action = dictionary["action"] as? String
    else {
        return false
    }
    return allowedActions.contains(action)
}

private func runServiceExec(stdin: Data) throws -> Data {
    let ochURL = try bundledOCHURL()
    let process = Process()
    process.executableURL = ochURL
    process.arguments = ["service", "exec"]

    let inputPipe = Pipe()
    let outputPipe = Pipe()
    process.standardInput = inputPipe
    process.standardOutput = outputPipe
    process.standardError = outputPipe

    try process.run()
    inputPipe.fileHandleForWriting.write(stdin)
    try inputPipe.fileHandleForWriting.close()
    process.waitUntilExit()

    let output = outputPipe.fileHandleForReading.readDataToEndOfFile()
    if process.terminationStatus != 0 {
        let message = String(data: output, encoding: .utf8) ?? "och service exec failed"
        throw HelperError(message.trimmingCharacters(in: .whitespacesAndNewlines))
    }
    return output
}

private func bundledOCHURL() throws -> URL {
    let executableURL = URL(fileURLWithPath: CommandLine.arguments[0])
    let contentsURL = executableURL
        .deletingLastPathComponent()
        .deletingLastPathComponent()
        .deletingLastPathComponent()
    let candidate = contentsURL.appendingPathComponent("Resources/bin/och")
    guard FileManager.default.isExecutableFile(atPath: candidate.path) else {
        throw HelperError("Cannot find bundled OCH CLI at \(candidate.path)")
    }
    return candidate
}

private func sendResponse(_ data: Data, to message: xpc_object_t, peer: xpc_connection_t) {
    let reply = xpc_dictionary_create_reply(message)
    guard let reply else { return }
    data.withUnsafeBytes { buffer in
        xpc_dictionary_set_data(reply, "response", buffer.baseAddress, data.count)
    }
    xpc_connection_send_message(peer, reply)
}

private func sendError(_ error: String, to message: xpc_object_t, peer: xpc_connection_t) {
    let reply = xpc_dictionary_create_reply(message)
    guard let reply else { return }
    xpc_dictionary_set_string(reply, "error", error)
    xpc_connection_send_message(peer, reply)
}

private struct HelperError: LocalizedError {
    let message: String

    init(_ message: String) {
        self.message = message
    }

    var errorDescription: String? {
        message
    }
}

private final class OCHHelperConnectionBox: @unchecked Sendable {
    let connection: xpc_connection_t

    init(_ connection: xpc_connection_t) {
        self.connection = connection
    }
}
