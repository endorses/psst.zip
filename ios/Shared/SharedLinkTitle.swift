import Foundation

/// Explicit descriptive metadata only; never derive this value from a filename.
enum SharedLinkTitle {
    enum Failure: LocalizedError {
        case invalid
        var errorDescription: String? { "Use at most 200 characters without control characters for the shared title." }
    }
    static func normalize(_ value: String?) throws -> String? {
        guard let value else { return nil }
        guard value.utf8.count <= 4096 else { throw Failure.invalid }
        guard value.unicodeScalars.allSatisfy({ !(0...31).contains($0.value) && !(127...159).contains($0.value) }) else { throw Failure.invalid }
        let whitespace = CharacterSet(
            charactersIn:
                " \u{0085}\u{00A0}\u{1680}\u{2000}\u{2001}\u{2002}\u{2003}\u{2004}\u{2005}\u{2006}\u{2007}\u{2008}\u{2009}\u{200A}\u{2028}\u{2029}\u{202F}\u{205F}\u{3000}")
        let title = value.trimmingCharacters(in: whitespace)
        guard !title.isEmpty else { return nil }
        guard title.unicodeScalars.count <= 200, title.utf8.count <= 800 else { throw Failure.invalid }
        return title
    }
}
