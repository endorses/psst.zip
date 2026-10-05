import Foundation
import XCTest

@testable import Psst

final class LocalizationTests: XCTestCase {
    private var previousLanguage = AppLanguage.system

    override func setUp() {
        super.setUp()
        previousLanguage = LanguageSettings.shared.preference
    }

    override func tearDown() {
        LanguageSettings.shared.preference = previousLanguage
        super.tearDown()
    }

    func testPreferenceOrderingRegionalVariantsAndFallback() {
        XCTAssertEqual(AppLanguage.resolve(.system, preferred: ["fr", "de-CH", "en-US"]), "de")
        XCTAssertEqual(AppLanguage.resolve(.system, preferred: ["en-GB", "de-AT"]), "en")
        XCTAssertEqual(AppLanguage.resolve(.system, preferred: ["ja", "fr"]), "en")
        XCTAssertEqual(AppLanguage.resolve(.system, preferred: ["de_AT"]), "de")
        XCTAssertEqual(AppLanguage.resolve(.en, preferred: ["de-DE"]), "en")
        XCTAssertEqual(AppLanguage.resolve(.de, preferred: []), "de")
        XCTAssertEqual(
            AppLanguage.effectiveLocale(.system, preferred: ["fr_FR", "de_CH"], current: Locale(identifier: "fr_FR")).identifier, "de_CH")
        XCTAssertEqual(AppLanguage.effectiveLocale(.de, preferred: [], current: Locale(identifier: "de_AT")).identifier, "de_AT")
    }

    func testDevicePreferencePersistenceAndExtensionAgreement() throws {
        let name = "psst-localization-test-" + UUID().uuidString
        let defaults = try XCTUnwrap(UserDefaults(suiteName: name))
        defer { defaults.removePersistentDomain(forName: name) }
        let app = LanguageSettings(defaults: defaults)
        let extensionSettings = LanguageSettings(defaults: defaults)
        XCTAssertEqual(app.preference, .system)
        app.preference = .de
        extensionSettings.reload()
        XCTAssertEqual(extensionSettings.preference, .de)
        XCTAssertEqual(LanguageSettings(defaults: defaults).preference, .de)
        defaults.set("unsupported", forKey: "language")
        app.reload()
        XCTAssertEqual(app.preference, .system)
    }

    func testWholeMessagePluralsAndFormatting() {
        for (language, singular, plural) in [(AppLanguage.en, "file", "files"), (.de, "Datei", "Dateien")] {
            LanguageSettings.shared.preference = language
            XCTAssertEqual(L10n.text(L10n.format("%lld files", Int64(0))), "0 " + plural)
            XCTAssertEqual(L10n.text(L10n.format("%lld files", Int64(1))), "1 " + singular)
            XCTAssertEqual(L10n.text(L10n.format("%lld files", Int64(2))), "2 " + plural)
        }
        LanguageSettings.shared.preference = .de
        XCTAssertEqual(L10n.bytes(1536, binary: true, locale: Locale(identifier: "de_DE")), "1,5 KiB")
        XCTAssertEqual(L10n.bytes(1_500_000, locale: Locale(identifier: "de_DE")), "1,5 MB")
        XCTAssertEqual(L10n.number(1_234_567, locale: Locale(identifier: "de_DE")), "1.234.567")
        XCTAssertEqual(L10n.percent(0.5, locale: Locale(identifier: "de_DE")), "50 %")
        XCTAssertEqual(L10n.text(L10n.format("%lld of %lld file allowances used", Int64(0), Int64(1))), "0 von 1 Dateiannahme genutzt")
        XCTAssertEqual(L10n.text(L10n.format("%lld of %lld file allowances used", Int64(1), Int64(2))), "1 von 2 Dateiannahmen genutzt")
        XCTAssertEqual(L10n.text("Sign in"), "Anmelden")
        XCTAssertEqual(L10n.text("psst.zip"), "psst.zip")
    }

    func testTransferProgressUsesLocalizedSizesForBothValues() {
        for (identifier, expected) in [("en_US", "1.5 KB / 2.5 MB"), ("de_DE", "1,5 KB / 2,5 MB")] {
            let locale = Locale(identifier: identifier)
            XCTAssertEqual(L10n.bytes(1_500, locale: locale) + " / " + L10n.bytes(2_500_000, locale: locale), expected)
        }
        XCTAssertEqual(
            L10n.bytes(0, locale: Locale(identifier: "de_DE")) + " / " + L10n.bytes(5_000_000_000, locale: Locale(identifier: "de_DE")),
            "0 B / 5 GB")
        LanguageSettings.shared.preference = .de
        XCTAssertEqual(L10n.bytes(1_500_000) + " / " + L10n.bytes(2_500_000), "1,5 MB / 2,5 MB")
        LanguageSettings.shared.preference = .en
        XCTAssertEqual(L10n.bytes(1_500_000) + " / " + L10n.bytes(2_500_000), "1.5 MB / 2.5 MB")
    }

    func testDeferredErrorChangesLanguageWithoutChangingIdentityOrUserData() throws {
        LanguageSettings.shared.preference = .en
        let userValue = "Settings"
        let draft = ["filename": "Receive link", "title": userValue, "server": "https://example.test", "checkpoint": "stable"]
        let serializedDraft = try JSONEncoder().encode(draft)
        let error = L10n.format("Open %@", userValue)
        let markerLike = L10n.format("%lld files", Int64(99))
        let markerArgument = L10n.format("Open %@", markerLike)
        XCTAssertEqual(L10n.text(error), "Open Settings")
        LanguageSettings.shared.preference = .de
        XCTAssertEqual(L10n.text(error), "Settings öffnen")
        XCTAssertEqual(L10n.text(markerArgument), markerLike + " öffnen")
        XCTAssertEqual(draft["filename"], "Receive link")
        XCTAssertEqual(draft["title"], "Settings")
        XCTAssertEqual(draft["checkpoint"], "stable")
        XCTAssertEqual(try JSONDecoder().decode([String: String].self, from: serializedDraft), draft)
        XCTAssertFalse(L10n.text(error).contains("psst-l10n:"))
    }

    func testHistoryAndReportLocalizeWithoutChangingCapabilitiesOrUserTitles() throws {
        var record = TransferRecord(
            id: "stable", direction: .sent, state: .complete, createdAt: Date(), fileCount: 1, totalSize: 1536,
            shareURL: "https://example.test/d/stable#secret", sharedTitle: "Settings")
        LanguageSettings.shared.preference = .de
        XCTAssertEqual(record.displayTitle, "Settings")
        XCTAssertEqual(record.statusText, "Bereit zum Herunterladen")
        XCTAssertEqual(record.summary, "1 Datei · " + L10n.bytes(1536))
        record.sharedTitle = nil
        record.title = "Receive link"
        XCTAssertEqual(record.displayTitle, "Receive link")
        XCTAssertEqual(record.shareURL, "https://example.test/d/stable#secret")
        record.isSlot = true
        record.title = nil
        XCTAssertEqual(record.displayTitle, "Empfangslink")
        let context = try XCTUnwrap(
            AbuseReportContext(origin: "https://example.test", resourceType: "slot", resourceID: "12345678-1234-1234-1234-123456789abc"))
        XCTAssertTrue(context.text.contains("Ressourcentyp: slot"))
        XCTAssertTrue(context.text.contains("12345678-1234-1234-1234-123456789abc"))
        let mail = try XCTUnwrap(context.mailURL(contact: "abuse@example.test"))
        let fields = try XCTUnwrap(URLComponents(url: mail, resolvingAgainstBaseURL: false)).queryItems
        XCTAssertEqual(fields?.first(where: { $0.name == "subject" })?.value, "psst.zip Missbrauchsmeldung")
        XCTAssertFalse(mail.absoluteString.contains("psst-l10n"))
        XCTAssertFalse(context.text.contains("#secret"))
    }

    func testDateAndUnknownFailureUseCurrentLocaleWithoutServerProse() throws {
        let date = Date(timeIntervalSince1970: 1_791_158_400)
        let error = try XCTUnwrap(TransferIncident.budget(retryAt: date).errorDescription)
        LanguageSettings.shared.preference = .en
        let english = L10n.text(error)
        LanguageSettings.shared.preference = .de
        let german = L10n.text(error)
        XCTAssertNotEqual(english, german)
        XCTAssertTrue(german.contains("Datenverkehrsbudget"))
        XCTAssertTrue(german.contains(L10n.date(date, time: true)))
        XCTAssertFalse(german.contains("psst-l10n:"))
        XCTAssertEqual(L10n.failureCode("unknown_secret_URL"), "Could not connect. Check your network and server address, then retry.")
        XCTAssertFalse(L10n.text(L10n.failureCode("unknown_secret_URL")).contains("unknown_secret"))
        XCTAssertEqual(L10n.failureCode("download_limit"), TransferIncident.revoked.errorDescription)
    }

    func testKnownLocalUploadLimitRemainsNumericAcrossLanguageChanges() throws {
        let limit: Int64 = 1_572_864
        try LocalUploadFailure.validateSize(limit, limitBytes: limit)
        var knownFailure: Error?
        XCTAssertThrowsError(try LocalUploadFailure.validateSize(limit + 1, limitBytes: limit)) { knownFailure = $0 }
        XCTAssertEqual(knownFailure as? LocalUploadFailure, .fileTooLarge(limitBytes: limit))
        let fallback = "Could not connect. Check your network and server address, then retry."
        let message = try L10n.failure(XCTUnwrap(knownFailure), fallback: fallback)
        LanguageSettings.shared.preference = .en
        XCTAssertEqual(L10n.text(message), "Each file must be no larger than " + L10n.bytes(limit, binary: true) + ".")
        LanguageSettings.shared.preference = .de
        XCTAssertEqual(L10n.text(message), "Jede Datei darf höchstens " + L10n.bytes(limit, binary: true) + " groß sein.")
        XCTAssertThrowsError(try LocalUploadFailure.validateSize(1, limitBytes: 0)) {
            XCTAssertEqual($0 as? LocalUploadFailure, .invalidSelection)
        }
        XCTAssertEqual(L10n.failure(LocalUploadFailure.fileTooLarge(limitBytes: Int64.max), fallback: fallback), fallback)
        let arbitrary = NSError(
            domain: "Psst", code: 1, userInfo: [NSLocalizedDescriptionKey: "secret remote prose https://example.test/#secret"])
        XCTAssertEqual(L10n.failure(arbitrary, fallback: fallback), fallback)
    }

    func testRetainedCapacityFormatsRawByteCountInCurrentLocale() {
        let size: Int64 = 1_572_864
        let message = L10n.typedFormat("Up to %lld files · %@ per file", .integer(2), .byteCount(size, binary: true))
        LanguageSettings.shared.preference = .en
        XCTAssertEqual(L10n.text(message), "Up to 2 files · " + L10n.bytes(size, binary: true) + " per file")
        LanguageSettings.shared.preference = .de
        XCTAssertEqual(L10n.text(message), "Bis zu 2 Dateien · " + L10n.bytes(size, binary: true) + " pro Datei")
    }
}
