import CoreImage
import CoreImage.CIFilterBuiltins
import SwiftUI

enum QRCodeGenerator {
    /// Render whole pixels per module, including exactly four white modules on each edge.
    static func generate(from string: String, size: CGFloat) -> UIImage? {
        let context = CIContext()
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(string.utf8)
        filter.correctionLevel = "M"

        guard let output = filter.outputImage,
              let native = context.createCGImage(output, from: output.extent),
              let bounds = moduleBounds(native),
              let modules = native.cropping(to: bounds) else { return nil }

        // Core Image supplies its own border. Crop it before adding a single standard quiet zone.
        let quietZone = 4
        let width = modules.width + quietZone * 2
        let scale = max(1, Int(size / CGFloat(width)))
        let canvasSize = width * scale
        guard let canvas = CGContext(data: nil, width: canvasSize, height: canvasSize,
                                     bitsPerComponent: 8, bytesPerRow: canvasSize,
                                     space: CGColorSpaceCreateDeviceGray(), bitmapInfo: CGImageAlphaInfo.none.rawValue) else { return nil }
        canvas.setFillColor(gray: 1, alpha: 1)
        canvas.fill(CGRect(x: 0, y: 0, width: canvasSize, height: canvasSize))
        canvas.interpolationQuality = .none
        canvas.draw(modules, in: CGRect(x: quietZone * scale, y: quietZone * scale,
                                        width: modules.width * scale, height: modules.height * scale))
        guard let image = canvas.makeImage() else { return nil }
        return UIImage(cgImage: image)
    }

    /// Finder patterns reach all four matrix edges, so the dark-pixel bounds exclude only whitespace.
    private static func moduleBounds(_ image: CGImage) -> CGRect? {
        let width = image.width
        let height = image.height
        var pixels = [UInt8](repeating: 255, count: width * height)
        let rendered = pixels.withUnsafeMutableBytes { bytes -> Bool in
            guard let canvas = CGContext(data: bytes.baseAddress, width: width, height: height,
                                         bitsPerComponent: 8, bytesPerRow: width,
                                         space: CGColorSpaceCreateDeviceGray(), bitmapInfo: CGImageAlphaInfo.none.rawValue) else { return false }
            canvas.draw(image, in: CGRect(x: 0, y: 0, width: width, height: height))
            return true
        }
        guard rendered else { return nil }
        var left = width
        var top = height
        var right = -1
        var bottom = -1
        for y in 0 ..< height {
            for x in 0 ..< width where pixels[y * width + x] < 128 {
                left = min(left, x)
                top = min(top, y)
                right = max(right, x)
                bottom = max(bottom, y)
            }
        }
        guard right >= left, bottom >= top else { return nil }
        return CGRect(x: left, y: top, width: right - left + 1, height: bottom - top + 1)
    }
}
