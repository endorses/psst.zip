import Foundation

#if canImport(Darwin)
    import Darwin
#else
    import Glibc
#endif

/// Reads one object from a legacy JSON root array at a time. Progress offsets
/// always identify the next object, never a partially parsed record. Parsing is
/// bounded to 16 MiB per object by the database caller, 256 nesting levels and
/// 1 MiB of whitespace between tokens. Exceeding a bound preserves the source.
final class HistoryJSONStream {
    enum Failure: Error { case invalidArray, tooLarge, sourceChanged, invalidSource }
    private struct Identity: Codable, Equatable {
        let path: String
        let device: UInt64
        let inode: UInt64
        let size: Int64
        let modifiedSeconds: Int64
        let modifiedNanoseconds: Int64
        let changedSeconds: Int64
        let changedNanoseconds: Int64
    }

    let identity: String
    private(set) var offset: Int64 = 0
    private let original: Identity
    private let source: URL
    private let file: FileHandle
    private let maximumObjectBytes: Int
    private var buffer = Data()
    private var position = 0
    private var atBeginning = true
    private var finished = false
    private var closed = false

    init(url: URL, maximumObjectBytes: Int) throws {
        guard url.isFileURL, maximumObjectBytes > 0 else { throw Failure.invalidSource }
        source = url
        file = try FileHandle(forReadingFrom: url)
        self.maximumObjectBytes = maximumObjectBytes
        do {
            original = try Self.snapshot(file: file, source: url)
            let encoder = JSONEncoder()
            encoder.outputFormatting = .sortedKeys
            identity = String(decoding: try encoder.encode(original), as: UTF8.self)
            try verifyUnchanged()
        } catch {
            try? file.close()
            throw error
        }
    }

    deinit { close() }

    func close() {
        if !closed {
            try? file.close()
            closed = true
        }
    }

    func verifyUnchanged() throws {
        guard try Self.snapshot(file: file, source: source) == original else { throw Failure.sourceChanged }
        var pathInfo = stat()
        guard source.path.withCString({ lstat($0, &pathInfo) }) == 0,
            UInt64(pathInfo.st_dev) == original.device, UInt64(pathInfo.st_ino) == original.inode,
            UInt32(pathInfo.st_mode) & UInt32(S_IFMT) == UInt32(S_IFREG)
        else { throw Failure.sourceChanged }
    }

    func resume(at offset: Int64) throws {
        guard offset >= 0, offset <= original.size else { throw Failure.invalidArray }
        try file.seek(toOffset: UInt64(offset))
        buffer = Data()
        position = 0
        self.offset = offset
        atBeginning = offset == 0
        finished = false
    }

    func next() throws -> Data? {
        if finished { return nil }
        try skipWhitespace()
        if atBeginning {
            guard try take() == 91 else { throw Failure.invalidArray }  // [
            atBeginning = false
            try skipWhitespace()
            if try peek() == 93 {  // ]: an empty root array
                _ = try take()
                try finish()
                return nil
            }
        }
        guard try take() == 123 else { throw Failure.invalidArray }  // {
        var data = Data([123])
        var expected: [UInt8] = [125]
        var inString = false
        var escaped = false
        while !expected.isEmpty {
            guard let byte = try take() else { throw Failure.invalidArray }
            guard data.count < maximumObjectBytes else { throw Failure.tooLarge }
            data.append(byte)
            if inString {
                if escaped { escaped = false } else if byte == 92 { escaped = true } else if byte == 34 { inString = false }
                continue
            }
            switch byte {
            case 34: inString = true
            case 123, 91:
                guard expected.count < 256 else { throw Failure.tooLarge }
                expected.append(byte == 123 ? 125 : 93)
            case 125, 93:
                guard expected.removeLast() == byte else { throw Failure.invalidArray }
            default: break
            }
        }
        try skipWhitespace()
        switch try take() {
        case 44:  // ,: resumable offset points to the next complete object
            try skipWhitespace()
            guard try peek() == 123 else { throw Failure.invalidArray }
        case 93: try finish()
        default: throw Failure.invalidArray
        }
        return data
    }

    func finishIfAtEnd() throws -> Bool {
        if finished { return true }
        try skipWhitespace()
        guard try peek() == 123 else { throw Failure.invalidArray }
        return false
    }

    private func finish() throws {
        try skipWhitespace()
        guard try peek() == nil else { throw Failure.invalidArray }
        finished = true
    }

    private func skipWhitespace() throws {
        var count = 0
        while let byte = try peek(), byte == 32 || byte == 9 || byte == 10 || byte == 13 {
            guard count < 1_048_576 else { throw Failure.tooLarge }
            _ = try take()
            count += 1
        }
    }

    private func peek() throws -> UInt8? {
        if position == buffer.count {
            buffer = try file.read(upToCount: 65_536) ?? Data()
            position = 0
        }
        guard position < buffer.count else { return nil }
        return buffer[position]
    }

    private func take() throws -> UInt8? {
        guard let byte = try peek() else { return nil }
        position += 1
        offset += 1
        return byte
    }

    private static func snapshot(file: FileHandle, source: URL) throws -> Identity {
        var info = stat()
        guard fstat(file.fileDescriptor, &info) == 0, info.st_size >= 0,
            UInt32(info.st_mode) & UInt32(S_IFMT) == UInt32(S_IFREG)
        else { throw Failure.invalidSource }
        #if canImport(Darwin)
            let modified = info.st_mtimespec
            let changed = info.st_ctimespec
        #else
            let modified = info.st_mtim
            let changed = info.st_ctim
        #endif
        return Identity(
            path: source.standardizedFileURL.path, device: UInt64(info.st_dev), inode: UInt64(info.st_ino), size: Int64(info.st_size),
            modifiedSeconds: Int64(modified.tv_sec), modifiedNanoseconds: Int64(modified.tv_nsec), changedSeconds: Int64(changed.tv_sec), changedNanoseconds: Int64(changed.tv_nsec)
        )
    }
}
