import AVFoundation
import SwiftUI
import UIKit

struct PairingScanner: UIViewControllerRepresentable {
    var allowsPaste = false
    let onCode: (String) -> Void
    func makeUIViewController(context _: Context) -> ScannerController {
        ScannerController(allowsPaste: allowsPaste, onCode: onCode)
    }

    func updateUIViewController(_: ScannerController, context _: Context) {}
    static func dismantleUIViewController(_ uiViewController: ScannerController, coordinator _: ()) {
        uiViewController.stop()
    }
}

final class ScannerController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    private let capture = AVCaptureSession()
    private let queue = DispatchQueue(label: "zip.psst.ios.camera")
    private var preview: AVCaptureVideoPreviewLayer?
    private let onCode: (String) -> Void
    private let allowsPaste: Bool
    private var delivered = false
    private var active = true
    init(allowsPaste: Bool, onCode: @escaping (String) -> Void) {
        self.allowsPaste = allowsPaste
        self.onCode = onCode
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder _: NSCoder) {
        fatalError("init(coder:) is not supported")
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        AVCaptureDevice.requestAccess(for: .video) { [weak self] granted in
            DispatchQueue.main.async {
                guard let self, self.active else { return }
                if granted {
                    self.configure()
                } else {
                    self.showError()
                }
            }
        }
    }

    private func configure() {
        guard let device = AVCaptureDevice.default(for: .video), let input = try? AVCaptureDeviceInput(device: device), capture.canAddInput(input) else { showError()
            return
        }
        capture.addInput(input)
        let output = AVCaptureMetadataOutput()
        guard capture.canAddOutput(output) else { showError()
            return
        }
        capture.addOutput(output)
        output.setMetadataObjectsDelegate(self, queue: .main)
        output.metadataObjectTypes = [.qr]
        let layer = AVCaptureVideoPreviewLayer(session: capture)
        layer.videoGravity = .resizeAspectFill
        view.layer.addSublayer(layer)
        preview = layer
        layer.frame = view.bounds
        queue.async { [capture] in capture.startRunning() }
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        preview?.frame = view.bounds
    }

    private func showError() {
        let label = UILabel(frame: view.bounds)
        label.text = allowsPaste ? String(localized: "Camera unavailable. Enable camera access in iOS Settings, or go back to paste a link.") : String(localized: "Camera unavailable. Enable camera access in iOS Settings, or cancel and sign in manually.")
        label.numberOfLines = 0
        label.textAlignment = .center
        label.adjustsFontForContentSizeCategory = true
        label.autoresizingMask = [.flexibleWidth, .flexibleHeight]
        view.addSubview(label)
    }

    func metadataOutput(_: AVCaptureMetadataOutput, didOutput objects: [AVMetadataObject], from _: AVCaptureConnection) {
        guard !delivered, let value = (objects.first as? AVMetadataMachineReadableCodeObject)?.stringValue else { return }
        delivered = true
        stop()
        onCode(value)
    }

    func stop() {
        active = false
        queue.async { [capture] in capture.stopRunning() }
    }
}
