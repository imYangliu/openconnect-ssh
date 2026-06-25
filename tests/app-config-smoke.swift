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
