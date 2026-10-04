import AVFoundation
import ImageIO
import SwiftUI
import UIKit
import Vision

struct PairingScanner: UIViewControllerRepresentable {
    @Environment(\.scenePhase) private var scenePhase
    var allowsPaste = false
    var isActive = true
    let onCode: (String) -> Void
    func makeUIViewController(context _: Context) -> ScannerController {
        ScannerController(allowsPaste: allowsPaste, onCode: onCode)
    }

    func updateUIViewController(_ controller: ScannerController, context _: Context) {
        controller.setActive(isActive && scenePhase == .active)
    }

    static func dismantleUIViewController(_ controller: ScannerController, coordinator _: ()) {
        controller.setActive(false)
    }
}

/// Capture lives inside the calling screen. Session work is serialized off the main thread.
final class ScannerController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    private let capture = AVCaptureSession()
    private let queue = DispatchQueue(label: "zip.psst.ios.camera")
    private var preview: AVCaptureVideoPreviewLayer?
    private let onCode: (String) -> Void
    private let allowsPaste: Bool
    private var delivered = false
    private var active = false
    private var configured = false
    private var permissionPending = false
    private var camera: AVCaptureDevice?
    private let message = UILabel()
    private let torch = UIButton(type: .system)
    private let flip = UIButton(type: .system)
    init(allowsPaste: Bool, onCode: @escaping (String) -> Void) {
        self.allowsPaste = allowsPaste; self.onCode = onCode
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder _: NSCoder) {
        fatalError("init(coder:) is not supported")
    }

    override func viewDidLoad() {
        super.viewDidLoad()
        view.backgroundColor = .black
        let layer = AVCaptureVideoPreviewLayer(session: capture)
        layer.videoGravity = .resizeAspectFill
        view.layer.addSublayer(layer); preview = layer
        message.textColor = .white; message.numberOfLines = 0; message.textAlignment = .center
        message.adjustsFontForContentSizeCategory = true
        view.addSubview(message)
        torch.setImage(UIImage(systemName: "flashlight.off.fill"), for: .normal)
        torch.accessibilityLabel = "Toggle flashlight"
        torch.addTarget(self, action: #selector(toggleTorch), for: .touchUpInside)
        flip.setImage(UIImage(systemName: "camera.rotate"), for: .normal)
        flip.accessibilityLabel = "Switch camera"
        flip.addTarget(self, action: #selector(switchCamera), for: .touchUpInside)
        for button in [torch, flip] {
            button.tintColor = .white; button.backgroundColor = UIColor.black.withAlphaComponent(0.6); button.layer.cornerRadius = 22; button.isHidden = true; view.addSubview(button)
        }
    }

    func setActive(_ value: Bool) {
        loadViewIfNeeded()
        guard value != active else { return }
        active = value
        if !value {
            queue.async { [self] in turnTorchOff(); capture.stopRunning() }
            return
        }
        guard !delivered else { return }
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized: start()
        case .notDetermined:
            guard !permissionPending else { return }
            permissionPending = true
            message.text = "Allow camera access to scan a QR code."
            AVCaptureDevice.requestAccess(for: .video) { [weak self] granted in
                DispatchQueue.main.async {
                    guard let self else { return }
                    self.permissionPending = false
                    guard self.active else { return }
                    if granted {
                        self.start()
                    } else {
                        self.showError()
                    }
                }
            }
        default: showError()
        }
    }

    private func start() {
        queue.async { [self] in
            if !configured {
                guard configure(position: .back) else { DispatchQueue.main.async { self.showError() }; return }
                configured = true
            }
            capture.startRunning()
            DispatchQueue.main.async { self.message.text = nil; self.updateControls() }
        }
    }

    private func configure(position: AVCaptureDevice.Position) -> Bool {
        guard let device = AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: position), let input = try? AVCaptureDeviceInput(device: device) else { return false }
        capture.beginConfiguration()
        defer { capture.commitConfiguration() }
        let old = capture.inputs
        old.forEach { capture.removeInput($0) }
        guard capture.canAddInput(input) else { for item in old {
            if capture.canAddInput(item) {
                capture.addInput(item)
            }
        }; return false }
        capture.addInput(input); camera = device
        if capture.outputs.isEmpty {
            let output = AVCaptureMetadataOutput()
            guard capture.canAddOutput(output) else { return false }
            capture.addOutput(output)
            output.setMetadataObjectsDelegate(self, queue: .main)
            guard output.availableMetadataObjectTypes.contains(.qr) else { return false }
            output.metadataObjectTypes = [.qr]
        }
        return true
    }

    private func updateControls() {
        torch.isHidden = camera?.hasTorch != true
        flip.isHidden = AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .front) == nil || AVCaptureDevice.default(.builtInWideAngleCamera, for: .video, position: .back) == nil
    }

    @objc private func toggleTorch() {
        queue.async { [self] in
            guard let camera, camera.hasTorch, (try? camera.lockForConfiguration()) != nil else { return }
            camera.torchMode = camera.torchMode == .on ? .off : .on
            camera.unlockForConfiguration()
        }
    }

    private func turnTorchOff() {
        guard let camera, camera.hasTorch, (try? camera.lockForConfiguration()) != nil else { return }
        camera.torchMode = .off; camera.unlockForConfiguration()
    }

    @objc private func switchCamera() {
        queue.async { [self] in
            turnTorchOff()
            _ = configure(position: camera?.position == .back ? .front : .back)
            DispatchQueue.main.async { self.updateControls() }
        }
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        preview?.frame = view.bounds
        message.frame = view.bounds.insetBy(dx: 16, dy: 56)
        torch.frame = CGRect(x: 12, y: 12, width: 44, height: 44)
        flip.frame = CGRect(x: view.bounds.width - 56, y: 12, width: 44, height: 44)
        if let connection = preview?.connection, let orientation = view.window?.windowScene?.interfaceOrientation {
            let angle: CGFloat = switch orientation { case .landscapeLeft: 0; case .landscapeRight: 180; case .portraitUpsideDown: 270; default: 90 }
            if connection.isVideoRotationAngleSupported(angle) {
                connection.videoRotationAngle = angle
            }
        }
    }

    private func showError() {
        message.text = allowsPaste ? "Camera unavailable. Enable access in Settings, paste a link, or choose a QR image." : "Camera unavailable. Enable access in Settings, or cancel and sign in manually."
    }

    func metadataOutput(_: AVCaptureMetadataOutput, didOutput objects: [AVMetadataObject], from _: AVCaptureConnection) {
        guard active, !delivered else { return }
        let codes = Set(objects.compactMap { ($0 as? AVMetadataMachineReadableCodeObject)?.stringValue })
        guard codes.count == 1, let value = codes.first else { return }
        delivered = true
        setActive(false)
        queue.async { [weak self] in DispatchQueue.main.async { self?.onCode(value) } }
    }
}

enum QRImageReader {
    static func read(_ url: URL) throws -> String {
        let scoped = url.startAccessingSecurityScopedResource()
        defer {
            if scoped {
                url.stopAccessingSecurityScopedResource()
            }
        }
        guard let size = try url.resourceValues(forKeys: [.fileSizeKey]).fileSize, size <= 25 * 1024 * 1024,
              let source = CGImageSourceCreateWithURL(url as CFURL, nil),
              let image = CGImageSourceCreateThumbnailAtIndex(source, 0, [kCGImageSourceCreateThumbnailFromImageAlways: true, kCGImageSourceCreateThumbnailWithTransform: true, kCGImageSourceThumbnailMaxPixelSize: 2048] as CFDictionary) else { throw QRImageError.unreadable }
        let request = VNDetectBarcodesRequest()
        request.symbologies = [.qr]
        try VNImageRequestHandler(cgImage: image).perform([request])
        let values = Set((request.results ?? []).compactMap(\.payloadStringValue))
        guard values.count == 1, let value = values.first, value.utf8.count <= 8192 else { throw QRImageError.unreadable }
        return value
    }
}

enum QRImageError: Error { case unreadable }
