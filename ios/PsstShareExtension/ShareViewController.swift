import UIKit
import SwiftUI
import UniformTypeIdentifiers
import Shared

/// The share extension's root view controller.
/// Presents a SwiftUI-based progress UI and coordinates the encrypt-and-upload flow.
class ShareViewController: UIViewController {
    private var hostingController: UIHostingController<ShareExtensionView>?
    private let viewModel = ShareExtensionViewModel()

    override func viewDidLoad() {
        super.viewDidLoad()

        let shareView = ShareExtensionView(
            viewModel: viewModel,
            onCancel: { [weak self] in
                self?.cancelShare()
            },
            onComplete: { [weak self] in
                self?.completeShare()
            }
        )

        let hosting = UIHostingController(rootView: shareView)
        hostingController = hosting

        addChild(hosting)
        view.addSubview(hosting.view)
        hosting.view.translatesAutoresizingMaskIntoConstraints = false
        NSLayoutConstraint.activate([
            hosting.view.topAnchor.constraint(equalTo: view.topAnchor),
            hosting.view.bottomAnchor.constraint(equalTo: view.bottomAnchor),
            hosting.view.leadingAnchor.constraint(equalTo: view.leadingAnchor),
            hosting.view.trailingAnchor.constraint(equalTo: view.trailingAnchor),
        ])
        hosting.didMove(toParent: self)

        // Start processing shared items
        Task {
            await processSharedItems()
        }
    }

    private func processSharedItems() async {
        guard let extensionItems = extensionContext?.inputItems as? [NSExtensionItem] else {
            viewModel.state = .failed("No items to share")
            return
        }

        var fileURLs: [URL] = []

        for item in extensionItems {
            guard let attachments = item.attachments else { continue }
            for provider in attachments {
                if let url = await loadFileURL(from: provider) {
                    fileURLs.append(url)
                }
            }
        }

        guard !fileURLs.isEmpty else {
            viewModel.state = .failed("No files could be loaded")
            return
        }

        await viewModel.uploadFiles(fileURLs: fileURLs)
    }

    private func loadFileURL(from provider: NSItemProvider) async -> URL? {
        // Try to load as a file URL first.
        let fileTypes: [UTType] = [.item, .data, .image, .movie, .audio, .pdf]
        for type in fileTypes {
            if provider.hasItemConformingToTypeIdentifier(type.identifier) {
                do {
                    let url = try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<URL, Error>) in
                        provider.loadFileRepresentation(forTypeIdentifier: type.identifier) { url, error in
                            if let error {
                                continuation.resume(throwing: error)
                                return
                            }
                            guard let url else {
                                continuation.resume(throwing: ShareError.noURL)
                                return
                            }
                            // Copy to a temporary location since the original may be cleaned up.
                            let tempDir = FileManager.default.temporaryDirectory
                            let tempURL = tempDir.appendingPathComponent(url.lastPathComponent)
                            try? FileManager.default.removeItem(at: tempURL)
                            do {
                                try FileManager.default.copyItem(at: url, to: tempURL)
                                continuation.resume(returning: tempURL)
                            } catch {
                                continuation.resume(throwing: error)
                            }
                        }
                    }
                    return url
                } catch {
                    continue
                }
            }
        }
        return nil
    }

    private func cancelShare() {
        extensionContext?.cancelRequest(withError: NSError(
            domain: "zip.psst.ios.share-extension",
            code: 0,
            userInfo: [NSLocalizedDescriptionKey: "User cancelled"]
        ))
    }

    private func completeShare() {
        extensionContext?.completeRequest(returningItems: nil)
    }
}

enum ShareError: Error {
    case noURL
}
