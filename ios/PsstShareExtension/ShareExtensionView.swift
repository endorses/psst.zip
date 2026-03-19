import SwiftUI

/// The SwiftUI view displayed within the share extension.
struct ShareExtensionView: View {
    @Bindable var viewModel: ShareExtensionViewModel
    let onCancel: () -> Void
    let onComplete: () -> Void

    var body: some View {
        NavigationStack {
            VStack(spacing: 24) {
                Spacer()

                statusContent

                Spacer()
            }
            .padding()
            .navigationTitle("psst.zip")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") {
                        onCancel()
                    }
                }
                ToolbarItem(placement: .confirmationAction) {
                    if case .complete = viewModel.state {
                        Button("Done") {
                            onComplete()
                        }
                    }
                }
            }
        }
    }

    @ViewBuilder
    private var statusContent: some View {
        switch viewModel.state {
        case .idle:
            ProgressView("Preparing...")

        case .encrypting:
            VStack(spacing: 12) {
                ProgressView()
                    .controlSize(.large)
                Text("Encrypting \(viewModel.fileCount) file(s)...")
                    .font(.headline)
            }

        case .uploading(let progress):
            VStack(spacing: 12) {
                ProgressView(value: progress)
                    .progressViewStyle(.linear)
                    .frame(maxWidth: 250)
                Text("Uploading...")
                    .font(.headline)
                Text("\(Int(progress * 100))%")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
            }

        case .complete(let shareURL):
            VStack(spacing: 16) {
                Image(systemName: "checkmark.circle.fill")
                    .font(.system(size: 60))
                    .foregroundStyle(.green)

                Text("Upload complete")
                    .font(.headline)

                if let image = QRCodeGenerator.generate(from: shareURL, size: 200) {
                    Image(uiImage: image)
                        .interpolation(.none)
                        .resizable()
                        .scaledToFit()
                        .frame(width: 200, height: 200)
                        .padding()
                        .background(.white)
                        .clipShape(RoundedRectangle(cornerRadius: 12))
                }

                Button {
                    UIPasteboard.general.string = shareURL
                } label: {
                    Label("Copy Link", systemImage: "doc.on.doc")
                }
                .buttonStyle(.borderedProminent)

                Text(shareURL)
                    .font(.caption2)
                    .foregroundStyle(.secondary)
                    .lineLimit(2)
                    .truncationMode(.middle)
                    .padding(.horizontal)
            }

        case .failed(let error):
            VStack(spacing: 12) {
                Image(systemName: "xmark.circle.fill")
                    .font(.system(size: 60))
                    .foregroundStyle(.red)

                Text("Upload failed")
                    .font(.headline)

                Text(error)
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            }
        }
    }
}
