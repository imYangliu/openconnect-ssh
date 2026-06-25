import Foundation
@preconcurrency import XPC
import OCHXPCRequirement

public enum OCHXPCClientError: LocalizedError {
    case invalidReply
    case serviceError(String)
    case connectionError(String)

    public var errorDescription: String? {
        switch self {
        case .invalidReply:
            return "Invalid response from OCH privileged helper"
        case .serviceError(let message), .connectionError(let message):
            return message
        }
    }
}

public struct OCHXPCClient {
    public static let machServiceName = "io.github.imyangliu.och.helper"
    public static let clientSigningIdentifier = "io.github.imyangliu.och"

    public init() {}

    public func ping() async throws -> String {
        let reply = try await send(command: "ping", requestJSON: nil)
        return String(data: reply, encoding: .utf8) ?? "ok"
    }

    public func perform(requestJSON: Data) async throws -> Data {
        try await send(command: "perform", requestJSON: requestJSON)
    }

    private func send(command: String, requestJSON: Data?) async throws -> Data {
        try await withCheckedThrowingContinuation { continuation in
            let connection = xpc_connection_create_mach_service(
                Self.machServiceName,
                nil,
                UInt64(XPC_CONNECTION_MACH_SERVICE_PRIVILEGED)
            )
            let box = OCHXPCConnectionBox(connection)

            if OCHSetTeamPeerRequirement(box.connection, Self.machServiceName) != 0 {
                xpc_connection_cancel(box.connection)
                continuation.resume(throwing: OCHXPCClientError.connectionError("Cannot configure XPC peer requirement"))
                return
            }

            xpc_connection_set_event_handler(box.connection) { event in
                if event === XPC_ERROR_CONNECTION_INTERRUPTED || event === XPC_ERROR_CONNECTION_INVALID {
                    return
                }
            }
            xpc_connection_resume(box.connection)

            let message = xpc_dictionary_create_empty()
            xpc_dictionary_set_string(message, "command", command)
            if let requestJSON {
                requestJSON.withUnsafeBytes { buffer in
                    xpc_dictionary_set_data(message, "request", buffer.baseAddress, requestJSON.count)
                }
            }

            xpc_connection_send_message_with_reply(box.connection, message, nil) { reply in
                defer { xpc_connection_cancel(box.connection) }

                if reply === XPC_ERROR_CONNECTION_INTERRUPTED || reply === XPC_ERROR_CONNECTION_INVALID {
                    continuation.resume(throwing: OCHXPCClientError.connectionError("OCH privileged helper is unavailable"))
                    return
                }

                if let error = xpc_dictionary_get_string(reply, "error") {
                    continuation.resume(throwing: OCHXPCClientError.serviceError(String(cString: error)))
                    return
                }

                var length = 0
                guard let bytes = xpc_dictionary_get_data(reply, "response", &length) else {
                    continuation.resume(throwing: OCHXPCClientError.invalidReply)
                    return
                }
                continuation.resume(returning: Data(bytes: bytes, count: length))
            }
        }
    }
}

private final class OCHXPCConnectionBox: @unchecked Sendable {
    let connection: xpc_connection_t

    init(_ connection: xpc_connection_t) {
        self.connection = connection
    }
}
