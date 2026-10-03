import SwiftUI
import UIKit

enum PsstTheme {
    static let primary = adaptive(0x0F766E, 0x5EEAD4)
    static let onPrimary = adaptive(0xFFFFFF, 0x172B2A)
    static let text = adaptive(0x172B2A, 0xE5F4EF)
    static let secondary = adaptive(0x526561, 0xB0C5BF)
    static let background = adaptive(0xF6F8F7, 0x0B1715)
    static let surface = adaptive(0xFFFFFF, 0x122321)
    static let subtle = adaptive(0xDDF4EC, 0x203B36)
    static let error = adaptive(0xB42318, 0xFFB4AB)
    static let warning = adaptive(0x805500, 0xF5CF73)
    static let success = adaptive(0x256344, 0x8DD9AA)
    static let border = adaptive(0x68817B, 0x829B95)
    private static func adaptive(_ light: UInt32, _ dark: UInt32) -> Color {
        Color(UIColor { traits in
            let value = traits.userInterfaceStyle == .dark ? dark : light
            return UIColor(red: CGFloat((value >> 16) & 255) / 255, green: CGFloat((value >> 8) & 255) / 255,
                           blue: CGFloat(value & 255) / 255, alpha: 1)
        })
    }
}

struct PsstStyle: ViewModifier {
    func body(content: Content) -> some View {
        content.tint(PsstTheme.primary).foregroundStyle(PsstTheme.text)
            .background(PsstTheme.background).scrollContentBackground(.hidden)
    }
}

struct PrimaryAction: ButtonStyle {
    @Environment(\.isEnabled) private var enabled
    func makeBody(configuration: Configuration) -> some View {
        configuration.label.font(.headline).padding(.horizontal, 18).frame(minHeight: 48)
            .foregroundStyle(enabled ? PsstTheme.onPrimary : PsstTheme.secondary)
            .background(enabled ? PsstTheme.primary : PsstTheme.subtle, in: RoundedRectangle(cornerRadius: 12))
            .overlay {
                if configuration.isPressed {
                    RoundedRectangle(cornerRadius: 12).fill(.black.opacity(0.12))
                }
            }
    }
}

/// Shared between the app and extension. The QR always retains dark modules and a white quiet zone.
struct LinkCard: View {
    let url: String
    @State private var copied = false
    var body: some View {
        VStack(spacing: 12) {
            if let image = QRCodeGenerator.generate(from: url, size: 200) {
                Image(uiImage: image).interpolation(.none).resizable().scaledToFit()
                    .frame(width: 200, height: 200).padding(16).background(Color.white)
                    .clipShape(RoundedRectangle(cornerRadius: 12))
                    .accessibilityLabel("Shareable link QR code")
            }
            ViewThatFits(in: .horizontal) {
                HStack { copy
                    share
                }
                VStack { copy
                    share
                }
            }
            DisclosureGroup("Link and details") {
                Text(verbatim: url).font(.caption).textSelection(.enabled)
                    .fixedSize(horizontal: false, vertical: true).padding(.top, 8)
            }
        }
    }

    private var copy: some View {
        Button {
            UIPasteboard.general.string = url
            copied = true
            UIAccessibility.post(notification: .announcement, argument: String(localized: "Link copied"))
        } label: { Label(LocalizedStringKey(copied ? "Link copied" : "Copy link"), systemImage: copied ? "checkmark" : "doc.on.doc").frame(maxWidth: .infinity) }
            .buttonStyle(PrimaryAction())
    }

    private var share: some View {
        ShareLink(item: url) { Label("Share", systemImage: "square.and.arrow.up").frame(maxWidth: .infinity).frame(minHeight: 48) }
            .buttonStyle(.bordered)
    }
}
