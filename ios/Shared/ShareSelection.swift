import Foundation

enum ShareSelectionError: Error, Equatable {
    case tooLarge, unreadable

    var message: String {
        switch self {
        case .tooLarge:
            L10n.message("Files are encrypted in chunks. The server sets the maximum file size.")
        case .unreadable:
            L10n.message("Some selected files could not be loaded. Nothing was sent. Share the files again.")
        }
    }
}

/// A selection is all-or-nothing. A provider failure must never silently remove an attachment.
enum ShareSelection {
    @MainActor
    static func loadAll<Provider>(
        _ providers: [Provider],
        load: (Provider) async throws -> URL,
        removeDirectory: (URL) -> Void = { try? FileManager.default.removeItem(at: $0) }
    ) async throws -> [URL] {
        var files: [URL] = []
        do {
            guard !providers.isEmpty else { throw ShareSelectionError.unreadable }
            for provider in providers {
                try Task.checkCancellation()
                let file = try await load(provider)
                // Track the copy before checking cancellation so late provider callbacks are cleaned up too.
                files.append(file)
                try Task.checkCancellation()
            }
            return files
        } catch {
            for file in files {
                removeDirectory(file.deletingLastPathComponent())
            }
            if error is CancellationError {
                throw error
            }
            throw (error as? ShareSelectionError) ?? .unreadable
        }
    }

    static func copyProviderFile(_ url: URL) throws -> URL {
        let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        do {
            guard let size = try url.resourceValues(forKeys: [.fileSizeKey]).fileSize else {
                throw ShareSelectionError.unreadable
            }
            guard size <= BufferedUpload.maxFileBytes else { throw ShareSelectionError.tooLarge }
            try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
            let copy = directory.appendingPathComponent(url.lastPathComponent)
            try FileManager.default.copyItem(at: url, to: copy)
            return copy
        } catch {
            try? FileManager.default.removeItem(at: directory)
            throw error
        }
    }
}
