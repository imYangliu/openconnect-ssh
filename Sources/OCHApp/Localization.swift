import Foundation
import SwiftUI

enum AppLanguage: String, CaseIterable, Identifiable {
    case system
    case english = "en"
    case zhHans = "zh-Hans"

    static let storageKey = "appLanguage"
    private static let supportedCodes = [english.rawValue, zhHans.rawValue]

    var id: String { rawValue }

    var titleKey: LocalizedStringKey {
        switch self {
        case .system:
            return "language.system"
        case .english:
            return "language.english"
        case .zhHans:
            return "language.zh_hans"
        }
    }

    var resolvedCode: String {
        switch self {
        case .system:
            return Bundle.preferredLocalizations(from: Self.supportedCodes).first ?? Self.english.rawValue
        case .english, .zhHans:
            return rawValue
        }
    }

    var locale: Locale {
        Locale(identifier: resolvedCode)
    }

    static var stored: AppLanguage {
        guard let rawValue = UserDefaults.standard.string(forKey: storageKey),
              let language = AppLanguage(rawValue: rawValue) else {
            return .system
        }
        return language
    }
}

enum L10n {
    static func tr(_ key: String, _ arguments: CVarArg...) -> String {
        let format = NSLocalizedString(key, bundle: bundle, comment: "")
        return String(format: format, locale: AppLanguage.stored.locale, arguments: arguments)
    }

    private static var bundle: Bundle {
        let code = AppLanguage.stored.resolvedCode
        guard let path = Bundle.module.path(forResource: code, ofType: "lproj"),
              let localizedBundle = Bundle(path: path) else {
            return Bundle.module
        }
        return localizedBundle
    }
}
