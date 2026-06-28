import Foundation

@main
struct AppConfigSmoke {
    static func main() {
        let valid = try! TOMLConfigFile.parse("""
        [vpn]
        host = "vpn.example.com"
        user = "alice"

        [dns]
        mode = "ignore"

        [paths]
        # empty section is accepted because helper paths are runtime-owned

        [app]
        language = "zh-Hans"
        """)
        expect(valid.vpnHost == "vpn.example.com", "vpn host")
        expect(valid.dnsMode == .ignore, "dns mode")
        expect(valid.appLanguage == .zhHans, "language")
        let rendered = TOMLConfigFile.render(valid)
        expect(rendered.contains("[dns]"), "rendered dns section")
        expect(rendered.contains("mode = \"ignore\""), "rendered dns mode")

        let blockedOverviewIssues = OverviewIssue.issues(
            for: AppConfig(),
            includeInstalled: false,
            serviceIsAvailable: false,
            serviceNeedsAttention: true,
            sshConfigured: false,
            hasUnsavedConfigTextChanges: true
        )
        expect(
            blockedOverviewIssues == [
                .missingVPNConfig,
                .serviceFallback,
                .serviceNeedsAttention,
                .unsavedConfig
            ],
            "overview blocked issue ordering"
        )

        let vpnOnly = try! TOMLConfigFile.parse("""
        [vpn]
        host = "vpn.example.com"
        user = "alice"
        """)
        let vpnOnlyOverviewIssues = OverviewIssue.issues(
            for: vpnOnly,
            includeInstalled: false,
            serviceIsAvailable: false,
            serviceNeedsAttention: false,
            sshConfigured: false,
            hasUnsavedConfigTextChanges: false
        )
        expect(vpnOnlyOverviewIssues == [.serviceFallback], "vpn-only service fallback issue")

        var sshConfigured = valid
        sshConfigured.defaultHost = "och-target"
        sshConfigured.targetHost = "10.2.3.4"
        let missingIncludeIssues = OverviewIssue.issues(
            for: sshConfigured,
            includeInstalled: false,
            serviceIsAvailable: true,
            serviceNeedsAttention: false,
            sshConfigured: true,
            hasUnsavedConfigTextChanges: false
        )
        expect(missingIncludeIssues == [.sshIncludeMissing], "managed SSH include issue")

        let readyOverviewIssues = OverviewIssue.issues(
            for: valid,
            includeInstalled: true,
            serviceIsAvailable: true,
            serviceNeedsAttention: false,
            sshConfigured: false,
            hasUnsavedConfigTextChanges: false
        )
        expect(readyOverviewIssues.isEmpty, "overview ready issue list")

        do {
            var injected = valid
            injected.defaultHost = "och-target"
            injected.targetHost = "10.2.3.4\n  ProxyCommand /tmp/injected %h %p"
            try SSHConfigManager.writeManagedHost(config: injected, ochPath: "/usr/local/bin/och")
            fail("expected SSH config field validation to reject newline injection")
        } catch let error as SSHConfigError {
            guard case .invalidField(let field, _) = error else {
                fail("unexpected SSH config error: \(error)")
            }
            expect(field == "HostName", "invalid SSH field name")
        } catch {
            fail("unexpected SSH validation error type: \(error)")
        }

        do {
            _ = try CommandRunner.run(
                executable: "/bin/sh",
                arguments: ["-c", "sleep 2"],
                timeout: 0.1
            )
            fail("expected CommandRunner timeout")
        } catch let error as CommandRunnerError {
            guard case .timedOut(let executable, let timeout) = error else {
                fail("unexpected CommandRunner error: \(error)")
            }
            expect(executable == "/bin/sh", "timeout executable")
            expect(timeout == 0.1, "timeout value")
        } catch {
            fail("unexpected CommandRunner timeout error: \(error)")
        }

        do {
            _ = try TOMLConfigFile.parse("""
            [vpn]
            host = "vpn.example.com"
            user = "alice"

            [paths]
            och = "/tmp/och"
            """)
            fail("expected [paths] helper key rejection")
        } catch let error as TOMLConfigError {
            let message = error.localizedDescription(language: .english)
            expect(message.contains("[paths] is fixed"), "paths-specific error")
            expect(message.contains("och"), "paths key name")
        } catch {
            fail("unexpected error type: \(error)")
        }

        print("app config smoke passed")
    }

    private static func expect(_ condition: @autoclosure () -> Bool, _ message: String) {
        if !condition() {
            fail(message)
        }
    }

    private static func fail(_ message: String) -> Never {
        FileHandle.standardError.write(Data("app config smoke failed: \(message)\n".utf8))
        exit(1)
    }
}
