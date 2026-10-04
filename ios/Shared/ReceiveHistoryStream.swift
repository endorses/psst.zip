import Foundation

/// Resumable JSON token reader. Aggregate checkpoint collections never become a
/// Data/map/array allocation. Only bounded scalars and one checkpoint are decoded.
final class ReceiveHistoryStream {
    enum Failure: Error { case malformed, scalarTooLarge, invalidCheckpoint, sourceChanged }
    struct Entry: Codable {
        let key: String?
        let value: String
    }
    struct State: Codable {
        var offset: Int64 = 0
        var objectStart: Int64 = 0
        var objectEnd: Int64 = 0
        var phase = "root"
        var fields: [String: Data] = [:]
        var seen: [String] = []
        var staged: Int64 = 0
        var promoted: Int64 = 0
        var parentID: String?
        var processed: Int64 = 0
        var inserted: Int64 = 0
        var metadataBytes = 0
    }
    private struct JSONValue: Decodable {
        init(from decoder: Decoder) throws {
            if let array = try? decoder.unkeyedContainer() {
                var array = array
                while !array.isAtEnd { _ = try array.decode(JSONValue.self) }
            } else if let object = try? decoder.container(keyedBy: Key.self) {
                for key in object.allKeys { _ = try object.decode(JSONValue.self, forKey: key) }
            } else {
                let value = try decoder.singleValueContainer()
                if value.decodeNil() { return }
                if (try? value.decode(String.self)) != nil || (try? value.decode(Bool.self)) != nil || (try? value.decode(Double.self)) != nil { return }
                throw Failure.malformed
            }
        }
        struct Key: CodingKey {
            var stringValue: String
            var intValue: Int? { nil }
            init?(stringValue: String) { self.stringValue = stringValue }
            init?(intValue: Int) { return nil }
        }
    }
    static func stageEntry(_ db: HistoryRecordDatabase, entry: Entry, scope: String, index: Int64) throws {
        // A map key may occur only once. Completion arrays may repeat IDs and
        // promotion remains idempotent. Entry identity is source/range/index.
        if let key = entry.key {
            let keyID = scope + "|key|" + key
            guard try db.importIfAbsent(.init(id: keyID, scope: scope, kind: "stage-key", created: 0, body: try JSONEncoder().encode(entry))) else { throw Failure.malformed }
        }
        guard try db.importIfAbsent(.init(id: scope + "|entry|" + String(index), scope: scope, kind: "stage-entry", created: 0, body: try JSONEncoder().encode(entry))) else {
            throw Failure.malformed
        }
    }
    static func stagedEntry(_ db: HistoryRecordDatabase, scope: String, index: Int64) throws -> Entry {
        guard let row = try db.read(scope + "|entry|" + String(index)) else { throw Failure.malformed }
        return try JSONDecoder().decode(Entry.self, from: row.body)
    }
    static func retainRange(_ db: HistoryRecordDatabase, scope: String, identity: String, url: URL, start: Int64, end: Int64) throws {
        let fields = ["identity": identity, "url": url.path, "start": String(start), "end": String(end), "savedFiles": "retained in immutable original JSON range"]
        try db.importIfAbsent(.init(id: scope + "|source", scope: "receive-checkpoint-migration", kind: "source", created: 0, body: try JSONEncoder().encode(fields)))
    }
    var state: State
    private let read: (Int64, Int) throws -> Data
    private let objectOnly: Bool
    private var buffer = Data()
    private var position = 0
    init(state: State = State(), objectOnly: Bool = false, read: @escaping (Int64, Int) throws -> Data) {
        self.state = state
        self.objectOnly = objectOnly
        self.read = read
    }
    private func peek() throws -> UInt8? {
        if position == buffer.count {
            buffer = try read(state.offset, 16384)
            position = 0
        }
        return position < buffer.count ? buffer[position] : nil
    }
    @discardableResult private func take() throws -> UInt8 {
        guard let value = try peek() else { throw Failure.malformed }
        position += 1
        state.offset += 1
        return value
    }
    private func whitespace() throws {
        var count = 0
        while let value = try peek(), [9, 10, 13, 32].contains(value) {
            guard count < 8192 else { throw Failure.scalarTooLarge }
            try take()
            count += 1
        }
    }
    private func expect(_ byte: UInt8) throws {
        try whitespace()
        guard try take() == byte else { throw Failure.malformed }
    }
    private func value() throws -> Data {
        try whitespace()
        var result = Data()
        var stack: [UInt8] = []
        var string = false
        var escaped = false
        while let byte = try peek() {
            if !string, stack.isEmpty, !result.isEmpty, [9, 10, 13, 32, 44, 93, 125].contains(byte) { break }
            guard result.count < 65536 else { throw Failure.scalarTooLarge }
            try take()
            result.append(byte)
            if string {
                if escaped { escaped = false } else if byte == 92 { escaped = true } else if byte == 34 { string = false }
            } else {
                switch byte {
                case 34: string = true
                case 123, 91:
                    guard stack.count < 64 else { throw Failure.scalarTooLarge }
                    stack.append(byte == 123 ? 125 : 93)
                case 125, 93: guard stack.popLast() == byte else { throw Failure.malformed }
                default: break
                }
            }
            if !string, stack.isEmpty, result.first == 34 || result.first == 123 || result.first == 91 { break }
        }
        guard !result.isEmpty, !string, stack.isEmpty else { throw Failure.malformed }
        _ = try JSONDecoder().decode(JSONValue.self, from: result)
        return result
    }
    private func string() throws -> String { try JSONDecoder().decode(String.self, from: value()) }
    private func memberEnd() throws {
        try whitespace()
        switch try take() {
        case 44: state.phase = "member"
        case 125:
            state.objectEnd = state.offset
            state.phase = "ready"
        default: throw Failure.malformed
        }
    }
    private func collectionEnd(_ closing: UInt8) throws {
        try whitespace()
        switch try take() {
        case 44: break
        case closing: try memberEnd()
        default: throw Failure.malformed
        }
    }
    /// Returns at most one checkpoint, stopping at a complete entry boundary.
    /// nil means bounded metadata is ready for validation or the source ended.
    func next() throws -> Entry? {
        guard state.offset >= 0, state.objectStart >= 0, state.objectStart <= state.offset, state.staged >= 0, state.promoted >= 0, state.promoted <= state.staged,
            state.processed >= 0, state.inserted >= 0, state.inserted <= state.processed, state.fields.count <= 128, state.seen.count <= 128,
            Set(state.seen).count == state.seen.count, (0...1_048_576).contains(state.metadataBytes), state.fields.values.reduce(0, { $0 + $1.count }) <= 1_048_576
        else { throw Failure.malformed }
        var work = 0
        let beginning = state.offset
        while work < 128, state.offset - beginning < 262144 {
            work += 1
            switch state.phase {
            case "root":
                if !objectOnly { try expect(91) }
                state.phase = "object"
                try whitespace()
                if !objectOnly, try peek() == 93 {
                    try take()
                    try finish()
                    return nil
                }
            case "object":
                try whitespace()
                state.objectStart = state.offset
                try expect(123)
                state.phase = "firstMember"
            case "firstMember", "member":
                try whitespace()
                if state.phase == "firstMember", try peek() == 125 {
                    try take()
                    state.objectEnd = state.offset
                    state.phase = "ready"
                    return nil
                }
                let key = try string()
                guard key.utf8.count <= 128, !state.seen.contains(key), state.seen.count < 128 else { throw Failure.malformed }
                state.seen.append(key)
                try expect(58)
                if key == "savedFiles" || key == "savedTransfers" {
                    try whitespace()
                    let opening: UInt8 = key == "savedFiles" ? 123 : 91
                    if try peek() == opening {
                        try take()
                        state.phase = key == "savedFiles" ? "firstFile" : "firstTransfer"
                    } else {
                        guard try value() == Data("null".utf8) else { throw Failure.malformed }
                        try memberEnd()
                    }
                } else {
                    let data = try value()
                    guard state.metadataBytes <= 1_048_576 - data.count else { throw Failure.scalarTooLarge }
                    state.metadataBytes += data.count
                    state.fields[key] = data
                    try memberEnd()
                }
            case "firstFile", "file", "firstTransfer", "transfer":
                let files = state.phase == "firstFile" || state.phase == "file"
                let closing: UInt8 = files ? 125 : 93
                try whitespace()
                if state.phase.hasPrefix("first"), try peek() == closing {
                    try take()
                    try memberEnd()
                    continue
                }
                let key = files ? try string() : nil
                if files { try expect(58) }
                let entry = Entry(key: key, value: try string())
                state.phase = files ? "file" : "transfer"
                try collectionEnd(closing)
                guard state.staged < Int64.max else { throw Failure.scalarTooLarge }
                state.staged += 1
                return entry
            case "ready", "done": return nil
            default: throw Failure.malformed
            }
        }
        return nil
    }
    func metadata() throws -> Data {
        guard state.phase == "ready", state.objectEnd >= state.objectStart, state.objectEnd <= state.offset, state.staged >= 0, state.promoted >= 0, state.promoted <= state.staged,
            state.fields.count <= 128, state.fields.keys.allSatisfy({ $0.utf8.count <= 128 }), state.fields.values.reduce(0, { $0 + $1.count }) <= 1_048_576
        else { throw Failure.malformed }
        var data = Data([123])
        for (index, key) in state.fields.keys.sorted().enumerated() {
            if index > 0 { data.append(44) }
            data.append(try JSONEncoder().encode(key))
            data.append(58)
            data.append(state.fields[key]!)
        }
        data.append(125)
        return data
    }
    func advance() throws {
        guard state.phase == "ready", state.promoted == state.staged else { throw Failure.malformed }
        let processed = state.processed
        let inserted = state.inserted
        if objectOnly {
            try finish()
            return
        }
        try whitespace()
        let delimiter = try take()
        guard delimiter == 44 || delimiter == 93 else { throw Failure.malformed }
        let offset = state.offset
        state = State()
        state.offset = offset
        state.processed = processed
        state.inserted = inserted
        if delimiter == 93 { try finish() } else { state.phase = "object" }
    }
    private func finish() throws {
        try whitespace()
        guard try peek() == nil else { throw Failure.malformed }
        state.phase = "done"
    }
}
