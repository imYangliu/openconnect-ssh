import Darwin
import Foundation

struct SSHHostChoice: Identifiable, Hashable {
    let name: String
    var id: String { name }
}

struct ResolvedSSHHost: Equatable {
    var alias: String
    var hostName: String
    var user: String
    var port: String
}

enum SetupCIDRHelper {
    static func managedAlias(for host: String) -> String {
        host.hasPrefix("och-") ? host : "och-\(host)"
    }

    static func defaultCIDR(for host: String) -> String {
        guard let ip = firstIPv4Address(for: host) else { return "" }
        return "\(ip)/32"
    }

    static func isValidCIDR(_ value: String) -> Bool {
        let parts = value.split(separator: "/", omittingEmptySubsequences: false)
        guard parts.count == 2,
              isValidIPv4(String(parts[0])),
              let prefix = Int(parts[1]),
              (0...32).contains(prefix) else {
            return false
        }
        return true
    }

    static func append(route: String, to routesText: String) -> String {
        let routes = routesText
            .split(whereSeparator: { $0 == "\n" || $0 == " " || $0 == "\t" || $0 == "," })
            .map(String.init)
        guard !route.isEmpty, !routes.contains(route) else {
            return routes.joined(separator: "\n")
        }
        return (routes + [route]).joined(separator: "\n")
    }

    private static func firstIPv4Address(for host: String) -> String? {
        if isValidIPv4(host) {
            return host
        }

        var hints = addrinfo(
            ai_flags: AI_ADDRCONFIG,
            ai_family: AF_INET,
            ai_socktype: SOCK_STREAM,
            ai_protocol: 0,
            ai_addrlen: 0,
            ai_canonname: nil,
            ai_addr: nil,
            ai_next: nil
        )
        var result: UnsafeMutablePointer<addrinfo>?
        guard getaddrinfo(host, nil, &hints, &result) == 0, let result else {
            return nil
        }
        defer { freeaddrinfo(result) }

        var address = result.pointee.ai_addr.withMemoryRebound(to: sockaddr_in.self, capacity: 1) {
            $0.pointee.sin_addr
        }
        var buffer = [CChar](repeating: 0, count: Int(INET_ADDRSTRLEN))
        guard inet_ntop(AF_INET, &address, &buffer, socklen_t(INET_ADDRSTRLEN)) != nil else {
            return nil
        }
        return buffer.withUnsafeBufferPointer { pointer in
            String(cString: pointer.baseAddress!)
        }
    }

    private static func isValidIPv4(_ value: String) -> Bool {
        let parts = value.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 4 else { return false }
        return parts.allSatisfy { part in
            guard let number = Int(part), (0...255).contains(number) else {
                return false
            }
            return String(number) == part || part == "0"
        }
    }
}

enum SetupAuthGroupProbe {
    static func probe(openconnectPath: String, host: String, user: String) throws -> [String] {
        guard !host.isEmpty, !user.isEmpty else { return [] }

        let executable: String
        let arguments: [String]
        if FileManager.default.isExecutableFile(atPath: openconnectPath) {
            executable = openconnectPath
            arguments = [host, "-u", user, "--authenticate", "--non-inter"]
        } else {
            executable = "/usr/bin/env"
            arguments = ["openconnect", host, "-u", user, "--authenticate", "--non-inter"]
        }

        let result = try CommandRunner.run(executable: executable, arguments: arguments)
        return parseGroups(from: result.output)
    }

    static func parseGroups(from output: String) -> [String] {
        var groups: [String] = []

        func add(_ value: String) {
            let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
            guard !trimmed.isEmpty, !groups.contains(trimmed) else { return }
            groups.append(trimmed)
        }

        let groupPattern = try? NSRegularExpression(pattern: #"GROUP:\s*\[([^\]]+)\]"#)
        let choicePattern = try? NSRegularExpression(pattern: #"^\s*[0-9]+[).]\s+(.+)$"#)
        let optionPattern = try? NSRegularExpression(pattern: #"<option[^>]*>([^<]+)</option>"#)

        for line in output.components(separatedBy: .newlines) {
            let nsLine = line as NSString
            let range = NSRange(location: 0, length: nsLine.length)
            if let match = groupPattern?.firstMatch(in: line, range: range), match.numberOfRanges > 1 {
                nsLine.substring(with: match.range(at: 1))
                    .split(separator: "|")
                    .forEach { add(String($0)) }
            }
            if let match = choicePattern?.firstMatch(in: line, range: range), match.numberOfRanges > 1 {
                add(nsLine.substring(with: match.range(at: 1)))
            }
            if let match = optionPattern?.firstMatch(in: line, range: range), match.numberOfRanges > 1 {
                add(nsLine.substring(with: match.range(at: 1)))
            }
        }
        return groups
    }
}

enum SetupSSHDiscovery {
    static func discoverHosts() -> [SSHHostChoice] {
        var visited = Set<String>()
        var names: [String] = []
        collectHosts(from: ConfigPaths.sshConfig, depth: 0, visited: &visited, names: &names)
        return names.map(SSHHostChoice.init(name:))
    }

    static func resolve(host: String) throws -> ResolvedSSHHost {
        let arguments = FileManager.default.isReadableFile(atPath: ConfigPaths.sshConfig.path)
            ? ["-F", ConfigPaths.sshConfig.path, "-G", host]
            : ["-G", host]
        let result = try CommandRunner.run(executable: "/usr/bin/ssh", arguments: arguments)
        let values = parseSSHConfigDump(result.output)
        return ResolvedSSHHost(
            alias: SetupCIDRHelper.managedAlias(for: host),
            hostName: values["hostname"] ?? host,
            user: values["user"] ?? NSUserName(),
            port: values["port"] ?? "22"
        )
    }

    private static func collectHosts(
        from url: URL,
        depth: Int,
        visited: inout Set<String>,
        names: inout [String]
    ) {
        guard depth < 5,
              url.path != ConfigPaths.managedSSHConfig.path,
              !visited.contains(url.path),
              let contents = try? String(contentsOf: url, encoding: .utf8) else {
            return
        }
        visited.insert(url.path)

        let base = url.deletingLastPathComponent()
        for rawLine in contents.components(separatedBy: .newlines) {
            let line = stripSSHComment(rawLine).trimmingCharacters(in: .whitespaces)
            guard !line.isEmpty else { continue }
            let parts = line.split(whereSeparator: \.isWhitespace).map(String.init)
            guard let keyword = parts.first?.lowercased() else { continue }
            let values = Array(parts.dropFirst())
            if keyword == "host" {
                for value in values where isUsableHostPattern(value) && !names.contains(value) {
                    names.append(value)
                }
            } else if keyword == "include" {
                for value in values {
                    for includeURL in expandInclude(value, relativeTo: base) {
                        collectHosts(from: includeURL, depth: depth + 1, visited: &visited, names: &names)
                    }
                }
            }
        }
    }

    private static func parseSSHConfigDump(_ output: String) -> [String: String] {
        var values: [String: String] = [:]
        for line in output.components(separatedBy: .newlines) {
            let parts = line.split(separator: " ", maxSplits: 1).map(String.init)
            guard parts.count == 2 else { continue }
            values[parts[0]] = parts[1]
        }
        return values
    }

    private static func isUsableHostPattern(_ value: String) -> Bool {
        !value.hasPrefix("!")
            && !value.hasPrefix("och-")
            && !value.contains("*")
            && !value.contains("?")
    }

    private static func expandInclude(_ value: String, relativeTo base: URL) -> [URL] {
        let expanded: String
        if value == "~" {
            expanded = FileManager.default.homeDirectoryForCurrentUser.path
        } else if value.hasPrefix("~/") {
            expanded = FileManager.default.homeDirectoryForCurrentUser
                .appendingPathComponent(String(value.dropFirst(2)))
                .path
        } else if value.hasPrefix("/") {
            expanded = value
        } else {
            expanded = base.appendingPathComponent(value).path
        }

        guard expanded.contains("*") || expanded.contains("?") || expanded.contains("[") else {
            return FileManager.default.isReadableFile(atPath: expanded) ? [URL(fileURLWithPath: expanded)] : []
        }

        let matches = (try? FileManager.default.contentsOfDirectory(
            at: URL(fileURLWithPath: (expanded as NSString).deletingLastPathComponent),
            includingPropertiesForKeys: nil
        )) ?? []
        let pattern = (expanded as NSString).lastPathComponent
        return matches.filter { url in
            fnmatch(pattern, url.lastPathComponent, 0) == 0
        }
    }

    private static func stripSSHComment(_ line: String) -> String {
        var result = ""
        var inDoubleQuote = false
        var inSingleQuote = false
        var escaping = false

        for character in line {
            if escaping {
                result.append(character)
                escaping = false
                continue
            }
            if character == "\\" {
                result.append(character)
                escaping = inDoubleQuote
                continue
            }
            if character == "\"", !inSingleQuote {
                inDoubleQuote.toggle()
            } else if character == "'", !inDoubleQuote {
                inSingleQuote.toggle()
            } else if character == "#", !inDoubleQuote, !inSingleQuote {
                break
            }
            result.append(character)
        }
        return result
    }
}
