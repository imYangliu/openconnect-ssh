import Foundation

enum HelperKind: String, CaseIterable {
    case och
    case ochVpn
    case askpass

    var displayName: String {
        switch self {
        case .och:
            return "och"
        case .ochVpn:
            return "och-vpn"
        case .askpass:
            return "och-sudo-askpass.sh"
        }
    }

    var bundleRelativePath: String {
        switch self {
        case .och:
            return "bin/och"
        case .ochVpn:
            return "libexec/och/och-vpn.sh"
        case .askpass:
            return "libexec/och/och-sudo-askpass.sh"
        }
    }

}

struct HelperPathResolution {
    let kind: HelperKind
    let path: String
}

struct ResolvedHelperPaths {
    let och: HelperPathResolution
    let ochVpn: HelperPathResolution
    let askpass: HelperPathResolution

    var all: [HelperPathResolution] {
        [och, ochVpn, askpass]
    }
}

struct HelperPathError: LocalizedError {
    let kind: HelperKind

    var errorDescription: String? {
        "Cannot find OCH helper \(kind.displayName). Rebuild or reinstall the app."
    }
}

enum HelperPathResolver {
    static func resolveAll() throws -> ResolvedHelperPaths {
        ResolvedHelperPaths(
            och: try resolve(.och),
            ochVpn: try resolve(.ochVpn),
            askpass: try resolve(.askpass)
        )
    }

    static func resolve(_ kind: HelperKind) throws -> HelperPathResolution {
        for candidate in bundleCandidates(for: kind) {
            if isExecutable(candidate) {
                return HelperPathResolution(kind: kind, path: candidate)
            }
        }

        throw HelperPathError(kind: kind)
    }

    private static func isExecutable(_ path: String) -> Bool {
        !path.isEmpty && FileManager.default.isExecutableFile(atPath: path)
    }

    private static func bundleCandidates(for kind: HelperKind) -> [String] {
        var candidates: [String] = []
        if let resourceURL = Bundle.main.resourceURL {
            candidates.append(resourceURL.appendingPathComponent(kind.bundleRelativePath).path)
        }
        candidates.append(Bundle.main.bundleURL.appendingPathComponent("Contents/Resources/\(kind.bundleRelativePath)").path)
        return unique(candidates)
    }

    private static func unique(_ paths: [String]) -> [String] {
        var seen = Set<String>()
        return paths.filter { seen.insert($0).inserted }
    }
}
