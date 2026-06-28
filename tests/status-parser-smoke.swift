import Foundation

@main
struct StatusParserSmoke {
    static func main() {
        let reachable = StatusParsing.parseServiceStatus("""
        Installed: yes
        Running: yes
        Socket: /var/run/och/daemon.sock
        Socket exists: yes
        Socket reachable: yes
        """)

        expect(reachable.installed == true, "installed yes")
        expect(reachable.running == true, "running yes")
        expect(reachable.socketPath == "/var/run/och/daemon.sock", "socket path")
        expect(reachable.socketExists == true, "socket exists yes")
        expect(reachable.socketReachable == true, "socket reachable yes")
        expect(reachable.isAvailable, "available service")

        let unavailable = StatusParsing.parseServiceStatus("""
        Installed: no
        Running: no
        Socket: /var/run/och/daemon.sock
        Socket exists: no
        Socket reachable: no
        """)

        expect(unavailable.installed == false, "installed no")
        expect(unavailable.running == false, "running no")
        expect(unavailable.socketExists == false, "socket exists no")
        expect(unavailable.socketReachable == false, "socket reachable no")
        expect(!unavailable.isAvailable, "unavailable service")

        expect(StatusParsing.parseConnectionRunState("VPN 已连接，PID: 123", isError: false) == .connected, "connected vpn")
        expect(StatusParsing.parseConnectionRunState("VPN 未连接", isError: false) == .disconnected, "disconnected vpn")
        expect(StatusParsing.parseConnectionRunState("VPN 已連線，PID: 123", isError: false) == .connected, "traditional connected vpn")
        expect(StatusParsing.parseConnectionRunState("VPN 未連線", isError: false) == .disconnected, "traditional disconnected vpn")
        expect(StatusParsing.parseConnectionRunState("anything", isError: true) == .error, "errored vpn")
        expect(StatusParsing.connectionStatusIsWarning(.disconnected, isError: false), "disconnected warning")
        expect(!StatusParsing.connectionStatusIsWarning(.connected, isError: false), "connected not warning")
        expect(!StatusParsing.connectionStatusIsWarning(.error, isError: true), "error not warning")

        print("status parser smoke passed")
    }

    private static func expect(_ condition: @autoclosure () -> Bool, _ message: String) {
        if !condition() {
            FileHandle.standardError.write(Data("status parser smoke failed: \(message)\n".utf8))
            exit(1)
        }
    }
}
