import Foundation
import ServiceManagement
import OCHXPCClient

enum PrivilegedService {
    static let plistName = "io.github.imyangliu.och.helper.plist"

    static func status() async -> ServiceStatus {
        let service = SMAppService.daemon(plistName: plistName)
        let registration = service.status.ochName
        var raw = "Registration: \(registration)\n"

        let reachable: Bool
        do {
            let pong = try await OCHXPCClient().ping()
            reachable = pong == "ok"
            raw += "XPC reachable: yes\n"
        } catch {
            reachable = false
            raw += "XPC reachable: no\n"
            raw += "XPC error: \(error.localizedDescription)\n"
        }

        return ServiceStatus(
            installed: service.status == .enabled || service.status == .requiresApproval,
            running: reachable,
            registrationStatus: registration,
            xpcReachable: reachable,
            rawOutput: raw.trimmingCharacters(in: .whitespacesAndNewlines)
        )
    }

    static func register() throws {
        try SMAppService.daemon(plistName: plistName).register()
    }

    static func unregister() throws {
        try SMAppService.daemon(plistName: plistName).unregister()
    }

    static func openLoginItemsSettings() {
        SMAppService.openSystemSettingsLoginItems()
    }
}

private extension SMAppService.Status {
    var ochName: String {
        switch self {
        case .notRegistered:
            return "notRegistered"
        case .enabled:
            return "enabled"
        case .requiresApproval:
            return "requiresApproval"
        case .notFound:
            return "notFound"
        @unknown default:
            return "unknown"
        }
    }
}

struct PrivilegedServiceRequest: Encodable {
    var action: String
    var config: PrivilegedServiceConfig
    var target: PrivilegedServiceTarget
    var vpn_password: String?

    init(action: String, appConfig: AppConfig, vpnPassword: String?) {
        self.action = action
        self.config = PrivilegedServiceConfig(appConfig)
        self.target = PrivilegedServiceTarget(appConfig)
        self.vpn_password = vpnPassword
    }
}

struct PrivilegedServiceConfig: Encodable {
    var vpn_host: String
    var vpn_user: String
    var vpn_auth_group: String
    var ssh_host: String
    var target_host: String
    var target_user: String
    var target_port: String
    var routes_mode: String
    var routes_extra: [String]
    var dns_mode: String
    var proxy_enabled: Bool
    var proxy_local_host: String
    var proxy_local_port: String
    var proxy_remote_port: String
    var app_language: String

    init(_ config: AppConfig) {
        self.vpn_host = config.vpnHost
        self.vpn_user = config.vpnUser
        self.vpn_auth_group = config.vpnAuthGroup
        self.ssh_host = config.defaultHost
        self.target_host = config.targetHost
        self.target_user = config.targetUser
        self.target_port = config.targetPort
        self.routes_mode = config.routeMode.rawValue
        self.routes_extra = config.extraRoutes
        self.dns_mode = config.dnsMode.rawValue
        self.proxy_enabled = config.proxyEnabled
        self.proxy_local_host = config.proxyLocalHost
        self.proxy_local_port = config.proxyLocalPort
        self.proxy_remote_port = config.proxyRemotePort
        self.app_language = config.appLanguage.rawValue
    }
}

struct PrivilegedServiceTarget: Encodable {
    var host: String?
    var port: String?
    var user: String?

    init(_ config: AppConfig) {
        self.host = config.targetHost.isEmpty ? nil : config.targetHost
        self.port = config.targetPort.isEmpty ? nil : config.targetPort
        self.user = config.targetUser.isEmpty ? nil : config.targetUser
    }
}

struct PrivilegedServiceResponse: Decodable {
    var ok: Bool
    var output: String
    var error: String?
}
