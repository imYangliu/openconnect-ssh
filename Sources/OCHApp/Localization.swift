import Foundation

enum AppLanguage: String, CaseIterable, Identifiable {
    case system
    case english = "en"
    case zhHans = "zh-Hans"
    case zhHant = "zh-Hant"

    private static let supportedCodes = [english.rawValue, zhHans.rawValue, zhHant.rawValue]

    var id: String { rawValue }

    var titleKey: String {
        switch self {
        case .system:
            return "language.system"
        case .english:
            return "language.english"
        case .zhHans:
            return "language.zh_hans"
        case .zhHant:
            return "language.zh_hant"
        }
    }

    var resolvedCode: String {
        switch self {
        case .system:
            return Bundle.preferredLocalizations(from: Self.supportedCodes).first ?? Self.english.rawValue
        case .english, .zhHans, .zhHant:
            return rawValue
        }
    }

    var locale: Locale {
        Locale(identifier: resolvedCode)
    }

    static func parse(_ rawValue: String) -> AppLanguage {
        AppLanguage(rawValue: rawValue) ?? .system
    }
}

enum L10n {
    static func tr(_ key: String, language: AppLanguage = .system, _ arguments: CVarArg...) -> String {
        let format = NSLocalizedString(key, bundle: bundle(for: language), comment: "")
        return String(format: format, locale: language.locale, arguments: arguments)
    }

    private static func bundle(for language: AppLanguage) -> Bundle {
        let code = language.resolvedCode
        for candidate in [code, code.lowercased()] {
            if let path = Bundle.module.path(forResource: candidate, ofType: "lproj"),
               let localizedBundle = Bundle(path: path) {
                return localizedBundle
            }
        }
        return Bundle.module
    }
}
