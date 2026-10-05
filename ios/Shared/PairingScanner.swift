import AVFoundation
import ImageIO
import SwiftUI
import UIKit
import Vision

struct PairingScanner: UIViewControllerRepresentable {
    @Environment(\.scenePhase) private var scenePhase
    @Environment(\.locale) private var locale
    var allowsPaste = false
    var isActive = true
    let onCode: (String) -> Void
    func makeUIViewController(context _: Context) -> ScannerController {
        ScannerController(allowsPaste: allowsPaste, onCode: onCode)
    }

    func updateUIViewController(_ controller: ScannerController, context _: Context) {
        _ = locale
        controller.refreshLanguage()
        controller.setActive(isActive && scenePhase == .active)
    }

    static func dismantleUIViewController(_ controller: ScannerController, coordinator _: ()) {
        controller.setActive(false)
    }
}

/// Capture lives inside the calling screen. Session work is serialized off the main thread.
enum ScannerCameraSelection {
    /// Deterministic rear-first ordering, with front/unspecified fallbacks.
    static func ordered<T>(_ devices: [T], position: (T) -> AVCaptureDevice.Position) -> [T] {
        devices.filter { position($0) == .back } + devices.filter { position($0) != .back }
    }
}

final class ScannerController: UIViewController, AVCaptureMetadataOutputObjectsDelegate {
    private let capture = AVCaptureSession()
    private let queue = DispatchQueue(label: "zip.psst.ios.camera")
    private var preview: AVCaptureVideoPreviewLayer?
    private let onCode: (String) -> Void
    private let allowsPaste: Bool
    private var delivered = false
    private var active = false
    private var permissionPending = false
    // Capture state is confined to queue; UI ownership remains on main.
    private var shouldRun = false
    private var configured = false
    private var camera: AVCaptureDevice?
    private let message = UILabel()
    private var messageKey: String?
    private var torchEnabled = false
    private var retryKey = "Retry camera"
    private let torch = UIButton(type: .system)
    private let retry = UIButton(type: .system)
    private var observers: [NSObjectProtocol] = []
    init(allowsPaste: Bool, onCode: @escaping (String) -> Void) {
        self.allowsPaste = allowsPaste; self.onCode = onCode
        super.init(nibName: nil, bundle: nil)
    }

    @available(*, unavailable)
    required init?(coder _: NSCoder) {
        fatalError("init(coder:) is not supported")
    }

    deinit { observers.forEach { NotificationCenter.default.removeObserver($0) } }

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
        torch.accessibilityLabel = L10n.text("Turn flashlight on")
        torch.addTarget(self, action: #selector(toggleTorch), for: .touchUpInside)
        retry.setTitle(L10n.text("Retry camera"), for: .normal)
        retry.addTarget(self, action: #selector(retryCamera), for: .touchUpInside)
        for button in [torch, retry] {
            button.tintColor = .white; button.backgroundColor = UIColor.black.withAlphaComponent(0.6)
            button.layer.cornerRadius = 22; button.isHidden = true; view.addSubview(button)
        }
        for name in [AVCaptureSession.runtimeErrorNotification, AVCaptureSession.wasInterruptedNotification] {
            observers.append(NotificationCenter.default.addObserver(forName: name, object: capture, queue: .main) { [weak self] _ in
                guard let self, active, !self.delivered else { return }
                queue.async { self.turnTorchOff(); self.capture.stopRunning(); self.configured = false }
                showError()
            })
        }
    }

    func setActive(_ value: Bool) {
        loadViewIfNeeded()
        guard value != active else { return }
        active = value
        queue.async { [self] in
            shouldRun = value
            if !value {
                turnTorchOff(); capture.stopRunning()
            }
        }
        if !value {
            torch.isHidden = true; retry.isHidden = true; return
        }
        requestCamera()
    }

    private func requestCamera() {
        guard active, !delivered else { return }
        switch AVCaptureDevice.authorizationStatus(for: .video) {
        case .authorized: start()
        case .notDetermined:
            guard !permissionPending else { return }
            permissionPending = true
            showMessage("Allow camera access to scan a QR code.")
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
        retry.isHidden = true
        queue.async { [self] in
            guard shouldRun else { return }
            if !configured {
                guard configure() else { DispatchQueue.main.async { self.showError() }; return }
                configured = true
            }
            capture.startRunning()
            let running = capture.isRunning
            let hasTorch = camera?.hasTorch == true
            DispatchQueue.main.async {
                guard self.active, !self.delivered else { return }
                if running {
                    self.showMessage(nil); self.torch.isHidden = !hasTorch; self.updateTorch(false)
                } else {
                    self.showError()
                }
            }
        }
    }

    private func configure() -> Bool {
        capture.beginConfiguration()
        defer { capture.commitConfiguration() }
        capture.inputs.forEach { capture.removeInput($0) }
        capture.outputs.forEach { capture.removeOutput($0) }
        camera = nil
        var devices = AVCaptureDevice.DiscoverySession(deviceTypes: [.builtInWideAngleCamera, .builtInUltraWideCamera, .builtInTelephotoCamera, .builtInTrueDepthCamera], mediaType: .video, position: .unspecified).devices
        if let fallback = AVCaptureDevice.default(for: .video), !devices.contains(where: { $0.uniqueID == fallback.uniqueID }) {
            devices.append(fallback)
        }
        for device in ScannerCameraSelection.ordered(devices, position: { $0.position }) {
            guard let input = try? AVCaptureDeviceInput(device: device), capture.canAddInput(input) else { continue }
            capture.addInput(input); camera = device; break
        }
        guard camera != nil else { return false }
        let output = AVCaptureMetadataOutput()
        guard capture.canAddOutput(output) else { return false }
        capture.addOutput(output)
        output.setMetadataObjectsDelegate(self, queue: .main)
        guard output.availableMetadataObjectTypes.contains(.qr) else { return false }
        output.metadataObjectTypes = [.qr]
        return true
    }

    private func updateTorch(_ enabled: Bool) {
        torch.setImage(UIImage(systemName: enabled ? "flashlight.on.fill" : "flashlight.off.fill"), for: .normal)
        torchEnabled = enabled
        torch.accessibilityLabel = L10n.text(enabled ? "Turn flashlight off" : "Turn flashlight on")
    }

    @objc private func toggleTorch() {
        queue.async { [self] in
            guard shouldRun, capture.isRunning, let camera, camera.hasTorch, camera.isTorchAvailable,
                  (try? camera.lockForConfiguration()) != nil else { return }
            camera.torchMode = camera.torchMode == .on ? .off : .on
            let enabled = camera.torchMode == .on
            camera.unlockForConfiguration()
            DispatchQueue.main.async { self.updateTorch(enabled) }
        }
    }

    private func turnTorchOff() {
        guard let camera, camera.hasTorch, (try? camera.lockForConfiguration()) != nil else { return }
        camera.torchMode = .off; camera.unlockForConfiguration()
        DispatchQueue.main.async { self.updateTorch(false) }
    }

    @objc private func retryCamera() {
        guard active, !delivered else { return }
        if AVCaptureDevice.authorizationStatus(for: .video) == .denied || AVCaptureDevice.authorizationStatus(for: .video) == .restricted {
            showMessage("Enable camera access for psst.zip in system Settings, then return here. You can also paste a link or choose a QR image.")
            return
        }
        queue.async { [self] in turnTorchOff(); capture.stopRunning(); configured = false }
        requestCamera()
    }

    override func viewDidLayoutSubviews() {
        super.viewDidLayoutSubviews()
        preview?.frame = view.bounds
        message.frame = view.bounds.insetBy(dx: 16, dy: 64)
        torch.frame = CGRect(x: 12, y: 12, width: 44, height: 44)
        retry.frame = CGRect(x: max(12, (view.bounds.width - 180) / 2), y: view.bounds.height - 56, width: 180, height: 44)
        if let connection = preview?.connection, let orientation = view.window?.windowScene?.interfaceOrientation {
            let angle: CGFloat = switch orientation { case .landscapeLeft: 0; case .landscapeRight: 180; case .portraitUpsideDown: 270; default: 90 }
            if connection.isVideoRotationAngleSupported(angle) {
                connection.videoRotationAngle = angle
            }
        }
    }

    private func showError() {
        guard active, !delivered else { return }
        showMessage(allowsPaste ? "Camera unavailable. Retry, paste a link, or choose a QR image." : "Camera unavailable. Retry, or cancel and sign in manually.")
        torch.isHidden = true
        let denied = AVCaptureDevice.authorizationStatus(for: .video) == .denied || AVCaptureDevice.authorizationStatus(for: .video) == .restricted
        retryKey = denied ? "Camera access help" : "Retry camera"
        retry.setTitle(L10n.text(retryKey), for: .normal)
        retry.isHidden = false
    }

    private func showMessage(_ key: String?) {
        messageKey = key
        message.text = key.map(L10n.text)
    }

    func refreshLanguage() {
        message.text = messageKey.map(L10n.text)
        retry.setTitle(L10n.text(retryKey), for: .normal)
        updateTorch(torchEnabled)
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
