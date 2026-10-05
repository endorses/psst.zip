import Foundation
import Observation

/// Language is a device preference, shared with the extension, never account or protocol data.
enum AppLanguage: String, CaseIterable {
    case system, en, de
    var title: String {
        switch self { case .system: L10n.text("System"); case .en: "English"; case .de: "Deutsch" }
    }

    static func resolve(_ preference: AppLanguage, preferred: [String]) -> String {
        guard preference == .system else { return preference.rawValue }
        for identifier in preferred {
            let language = identifier.replacingOccurrences(of: "_", with: "-").split(separator: "-").first?.lowercased()
            if language == "en" || language == "de" {
                return language!
            }
        }
        return "en"
    }

    static func effectiveLocale(_ preference: AppLanguage, preferred: [String], current: Locale) -> Locale {
        let selected = resolve(preference, preferred: preferred)
        if current.language.languageCode?.identifier == selected {
            return current
        }
        if let regional = preferred.first(where: { Locale(identifier: $0).language.languageCode?.identifier == selected }) {
            return Locale(identifier: regional)
        }
        return Locale(identifier: selected == "de" ? "de_DE" : "en_US")
    }
}

@Observable final class LanguageSettings {
    static let shared = LanguageSettings()
    var revision = 0
    var preference: AppLanguage {
        didSet { defaults.set(preference.rawValue, forKey: "language") }
    }

    @ObservationIgnored private let defaults: UserDefaults
    init(defaults: UserDefaults = AppConstants.sharedDefaults) {
        self.defaults = defaults
        preference = AppLanguage(rawValue: defaults.string(forKey: "language") ?? "system") ?? .system
    }

    func reload() {
        revision += 1
        let next = AppLanguage(rawValue: defaults.string(forKey: "language") ?? "system") ?? .system
        if next != preference {
            preference = next
        }
    }
}

/// Catalog keys are stable English source identifiers; arguments remain bounded and neutral.
/// Deferred messages let a visible failure change language without restarting its operation.
enum L10n {
    #if SWIFT_PACKAGE
        static let resourceBundle = Bundle.module
    #else
        static let resourceBundle = Bundle.main
    #endif
    struct Message: Codable { let key: String; let arguments: [Argument] }
    enum Argument: Codable {
        case text(String), integer(Int64), number(Double), timestamp(Double), byteCount(Int64, binary: Bool)
        var value: CVarArg {
            switch self { case let .text(value): value; case let .integer(value): value; case let .number(value): value; case let .timestamp(value): value; case let .byteCount(value, binary): L10n.bytes(value, binary: binary) }
        }
    }

    static var language: String {
        _ = LanguageSettings.shared.revision
        return AppLanguage.resolve(LanguageSettings.shared.preference, preferred: Locale.preferredLanguages)
    }

    static var locale: Locale {
        _ = LanguageSettings.shared.revision
        return AppLanguage.effectiveLocale(LanguageSettings.shared.preference, preferred: Locale.preferredLanguages, current: .current)
    }

    static func message(_ key: String) -> String {
        key
    }

    static func format(_ key: String, _ arguments: CVarArg...) -> String {
        let values: [Argument] = arguments.map {
            if let value = $0 as? String {
                return .text(value)
            }
            if let value = $0 as? Int64 {
                return .integer(value)
            }
            if let value = $0 as? Int {
                return .integer(Int64(value))
            }
            if let value = $0 as? Int32 {
                return .integer(Int64(value))
            }
            if let value = $0 as? Double {
                return .number(value)
            }
            return .text(String(describing: $0))
        }
        return encode(key, arguments: values)
    }

    static func typedFormat(_ key: String, _ arguments: Argument...) -> String {
        encode(key, arguments: arguments)
    }

    private static func encode(_ key: String, arguments: [Argument]) -> String {
        guard let data = try? JSONEncoder().encode(Message(key: key, arguments: arguments)), data.count <= 16384 else { return key }
        return "\u{001F}psst-l10n:" + data.base64EncodedString() + "\u{001F}"
    }

    static func datedMessage(_ key: String, date: Date) -> String {
        typedFormat(key, .timestamp(date.timeIntervalSince1970))
    }

    static func text(_ value: String) -> String {
        render(value)
    }

    static func render(_ value: String) -> String {
        let marker = "\u{001F}psst-l10n:"
        if let start = value.range(of: marker), let end = value.range(of: "\u{001F}", range: start.upperBound ..< value.endIndex),
           let data = Data(base64Encoded: String(value[start.upperBound ..< end.lowerBound])), data.count <= 16384,
           let message = try? JSONDecoder().decode(Message.self, from: data)
        {
            let arguments = message.arguments.map { argument -> CVarArg in
                if case let .timestamp(value) = argument {
                    return date(Date(timeIntervalSince1970: value), time: true)
                }
                return argument.value
            }
            let counts = message.arguments.compactMap { argument -> Int64? in
                if case let .integer(value) = argument {
                    return value
                }; return nil
            }
            let output = String(format: lookup(message.key, counts: counts), locale: locale, arguments: arguments)
            return render(String(value[..<start.lowerBound])) + output + render(String(value[end.upperBound...]))
        }
        return lookup(value)
    }

    static func lookup(_ key: String, counts: [Int64] = [], bundle: Bundle = resourceBundle) -> String {
        let english = bundle.path(forResource: "en", ofType: "lproj").flatMap(Bundle.init(path:))
        let selected = bundle.path(forResource: language, ofType: "lproj").flatMap(Bundle.init(path:))
        if !counts.isEmpty {
            for resource in [selected, english].compactMap({ $0 }) {
                if let path = resource.path(forResource: "Localizable", ofType: "stringsdict"),
                   let data = FileManager.default.contents(atPath: path),
                   let table = try? PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any],
                   let entry = table[key] as? [String: Any], let rule = entry["count"] as? [String: String],
                   let format = rule[(counts[max(0, min(entry["PsstPluralArgumentIndex"] as? Int ?? 0, counts.count - 1))] == 1) ? "one" : "other"]
                {
                    return format
                }
            }
        }
        return selected?.localizedString(forKey: key, value: english?.localizedString(forKey: key, value: key, table: nil) ?? key, table: nil) ?? english?.localizedString(forKey: key, value: key, table: nil) ?? key
    }

    static func date(_ value: Date, time: Bool = false) -> String {
        let formatter = DateFormatter()
        formatter.locale = locale
        formatter.dateStyle = .medium
        formatter.timeStyle = time ? .short : .none
        return formatter.string(from: value)
    }

    static func number(_ value: Int64, locale: Locale = locale) -> String {
        let formatter = NumberFormatter(); formatter.locale = locale; formatter.numberStyle = .decimal
        return formatter.string(from: NSNumber(value: value)) ?? String(value)
    }

    static func bytes(_ value: Int64, binary: Bool = false, locale: Locale = locale) -> String {
        let units = binary ? ["B", "KiB", "MiB", "GiB", "TiB"] : ["B", "KB", "MB", "GB", "TB"]
        let base = binary ? 1024.0 : 1000.0
        var amount = Double(value); var index = 0
        while abs(amount) >= base, index < units.count - 1 {
            amount /= base; index += 1
        }
        let formatter = NumberFormatter(); formatter.locale = locale; formatter.numberStyle = .decimal
        formatter.maximumFractionDigits = index == 0 ? 0 : 1
        return (formatter.string(from: NSNumber(value: amount)) ?? String(amount)) + " " + units[index]
    }

    static func percent(_ value: Double, locale: Locale = locale) -> String {
        let formatter = NumberFormatter(); formatter.locale = locale; formatter.numberStyle = .percent; formatter.maximumFractionDigits = 0
        return formatter.string(from: NSNumber(value: value)) ?? ""
    }
}
