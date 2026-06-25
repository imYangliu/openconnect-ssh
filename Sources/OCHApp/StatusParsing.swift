import Foundation

enum ConnectionRunState: Equatable {
    case unknown
    case connected
    case disconnected
    case error
}

struct ServiceStatus: Equatable {
    var installed: Bool?
    var running: Bool?
    var registrationStatus: String = ""
    var xpcReachable: Bool?
    var socketPath: String = ""
    var socketExists: Bool?
    var socketReachable: Bool?
    var rawOutput: String = ""
    var lastError: String = ""

    var isAvailable: Bool {
        if !registrationStatus.isEmpty {
            return registrationStatus == "enabled" && xpcReachable == true
        }
        return installed == true && running == true && socketReachable == true
    }
}

enum StatusParsing {
    static func parseServiceStatus(_ output: String) -> ServiceStatus {
        var status = ServiceStatus(rawOutput: output)
        for line in output.split(separator: "\n") {
            let parts = line.split(separator: ":", maxSplits: 1)
            guard parts.count == 2 else { continue }
            let key = parts[0].trimmingCharacters(in: .whitespacesAndNewlines).lowercased()
            let value = parts[1].trimmingCharacters(in: .whitespacesAndNewlines)
            switch key {
            case "installed":
                status.installed = parseBool(value)
            case "running":
                status.running = parseBool(value)
            case "socket":
                status.socketPath = value
            case "socket exists":
                status.socketExists = parseBool(value)
            case "socket reachable":
                status.socketReachable = parseBool(value)
            default:
                continue
            }
        }
        return status
    }

    static func parseConnectionRunState(_ output: String, isError: Bool) -> ConnectionRunState {
        if isError {
            return .error
        }
        if output.contains("VPN 已连接") || output.localizedCaseInsensitiveContains("VPN connected") {
            return .connected
        }
        if output.contains("VPN 未连接") || output.localizedCaseInsensitiveContains("VPN disconnected") {
            return .disconnected
        }
        return .unknown
    }

    private static func parseBool(_ value: String) -> Bool? {
        switch value.trimmingCharacters(in: .whitespacesAndNewlines).lowercased() {
        case "yes", "true", "1":
            return true
        case "no", "false", "0":
            return false
        default:
            return nil
        }
    }
}
