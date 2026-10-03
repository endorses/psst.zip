import Shared
import SwiftUI
import UIKit
import UniformTypeIdentifiers

/// The share extension's root view controller.
/// Presents a SwiftUI-based progress UI and coordinates the encrypt-and-upload flow.
class ShareViewController: UIViewController, UIAdaptivePresentationControllerDelegate {
    private var hostingController: UIHostingController<ShareExtensionView>?
    private let viewModel = ShareExtensionViewModel()
    private var processing: Task<Void, Never>?
    private var copiedDirectories: [URL] = []

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
        presentationController?.delegate = self
        processing = Task { await processSharedItems() }
    }

    private func processSharedItems() async {
        guard let extensionItems = extensionContext?.inputItems as? [NSExtensionItem] else {
            viewModel.error = String(localized: "No items to share")
            return
        }

        do {
            let providers = extensionItems.flatMap { $0.attachments ?? [] }
            let fileURLs = try await ShareSelection.loadAll(providers, load: { try await self.loadFileURL(from: $0) })
            copiedDirectories = fileURLs.map { $0.deletingLastPathComponent() }
            viewModel.prepare(fileURLs)
        } catch is CancellationError {
            // loadAll removes every completed copy, including copies returned after cancellation.
        } catch {
            viewModel.files = []
            viewModel.error = ((error as? ShareSelectionError) ?? .unreadable).message
        }
    }

    private func loadFileURL(from provider: NSItemProvider) async throws -> URL {
        let fileTypes: [UTType] = [.item, .data, .image, .movie, .audio, .pdf]
        for type in fileTypes where provider.hasItemConformingToTypeIdentifier(type.identifier) {
            try Task.checkCancellation()
            do {
                return try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<URL, Error>) in
                    provider.loadFileRepresentation(forTypeIdentifier: type.identifier) { url, error in
                        if let error {
                            continuation.resume(throwing: error)
                            return
                        }
                        guard let url else {
                            continuation.resume(throwing: ShareSelectionError.unreadable)
                            return
                        }
                        do {
                            try continuation.resume(returning: ShareSelection.copyProviderFile(url))
                        } catch {
                            continuation.resume(throwing: error)
                        }
                    }
                }
            } catch ShareSelectionError.tooLarge {
                // Do not replace an oversized original with a different/smaller representation.
                throw ShareSelectionError.tooLarge
            } catch is CancellationError {
                throw CancellationError()
            } catch {
                // Another representation of this same attachment may still be readable.
                continue
            }
        }
        throw ShareSelectionError.unreadable
    }

    func presentationControllerShouldDismiss(_: UIPresentationController) -> Bool {
        !viewModel.active
    }

    func presentationControllerDidAttemptToDismiss(_: UIPresentationController) {
        viewModel.cancelRequested = true
    }

    override func viewDidDisappear(_ animated: Bool) {
        super.viewDidDisappear(animated)
        if isBeingDismissed || navigationController?.isBeingDismissed == true {
            cleanup()
        }
    }

    private func cleanup() {
        processing?.cancel()
        viewModel.cancel()
        for directory in copiedDirectories {
            try? FileManager.default.removeItem(at: directory)
        }
        copiedDirectories = []
    }

    private func cancelShare() {
        cleanup()
        extensionContext?.cancelRequest(withError: NSError(
            domain: "zip.psst.ios.share-extension",
            code: 0,
            userInfo: [NSLocalizedDescriptionKey: "User cancelled"]
        ))
    }

    private func completeShare() {
        cleanup()
        extensionContext?.completeRequest(returningItems: nil)
    }
}
