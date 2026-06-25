import Foundation

enum AppLanguage: String {
    case system
    case english = "en"
    case zhHans = "zh-Hans"
    case zhHant = "zh-Hant"

    var locale: Locale {
        Locale(identifier: rawValue)
    }
}

enum L10n {
    static func tr(_ key: String, language: AppLanguage = .system, _ arguments: CVarArg...) -> String {
        let formats = [
            "error.toml.invalid_line": "Invalid TOML at line %d: %@",
            "error.toml.invalid_section": "Invalid TOML section at line %d: %@",
            "error.toml.unknown_section": "Unknown TOML section at line %d: %@",
            "error.toml.paths_key": "[paths] is fixed by the installed runtime layout; remove %@ at line %d",
            "error.toml.unknown_key": "Unknown TOML key at line %d: [%@].%@",
            "error.toml.invalid_value": "Invalid value at line %d for %@: %@",
            "error.toml.missing_required": "Missing required TOML key: %@"
        ]
        return String(format: formats[key] ?? key, locale: language.locale, arguments: arguments)
    }
}
