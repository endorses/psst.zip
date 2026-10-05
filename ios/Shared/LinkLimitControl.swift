import SwiftUI

struct LinkLimitControl: View {
    let receiving: Bool
    @Binding var enabled: Bool
    @Binding var value: String
    var body: some View {
        DisclosureGroup(L10n.text("Link limits")) {
            VStack(alignment: .leading, spacing: 10) {
                Toggle(L10n.text(receiving ? "Limit files received" : "Limit downloads per file"), isOn: $enabled)
                if enabled {
                    TextField(L10n.text(receiving ? "Maximum number of files" : "Downloads per file"), text: $value)
                        .keyboardType(.numberPad)
                        .textFieldStyle(.roundedBorder)
                        .accessibilityLabel(L10n.text(receiving ? "Maximum number of files" : "Downloads per file"))
                    if LinkLimit.parse(value, enabled: true) == nil {
                        Text(L10n.text("Enter a whole number from 1 to 2147483647.")).foregroundStyle(PsstTheme.error).font(.caption)
                    }
                    Text(L10n.text(receiving ? "Each file allocation uses one slot, including unfinished uploads. Deleting files does not restore slots." : "Each file can be downloaded this many times. Starting a download uses an attempt, even if it is interrupted. Saved copies cannot be recalled."))
                        .font(.caption).foregroundStyle(PsstTheme.secondary)
                } else {
                    Text(L10n.text(receiving ? "No optional file-count limit. Server limits still apply." : "No optional download limit. Expiry and server limits still apply."))
                        .font(.caption).foregroundStyle(PsstTheme.secondary)
                }
            }
        }
    }
}
