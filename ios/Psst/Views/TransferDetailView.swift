import SwiftUI

struct TransferDetailView: View {
    var sendViewModel: SendViewModel?
    var receiveViewModel: ReceiveViewModel?

    var body: some View {
        if let sendViewModel {
            SendTransferDetailContent(viewModel: sendViewModel)
        } else if let receiveViewModel {
            ReceiveTransferDetailContent(viewModel: receiveViewModel)
        }
    }
}

// MARK: - Send Detail

private struct SendTransferDetailContent: View {
    @Bindable var viewModel: SendViewModel

    var body: some View {
        ScrollView {
            VStack(spacing: 24) {
                statusSection
                if let url = viewModel.shareURL {
                    qrCodeSection(url: url)
                    shareLinkSection(url: url)
                }
                fileListSection
                if let expiresAt = viewModel.expiresAt {
                    expirySection(expiresAt: expiresAt)
                }
            }
            .padding()
        }
        .navigationTitle("Share Files")
        .navigationBarTitleDisplayMode(.inline)
    }

    @ViewBuilder
    private var statusSection: some View {
        VStack(spacing: 8) {
            switch viewModel.state {
            case .encrypting:
                ProgressView("Encrypting files...")
            case .uploading(let progress):
                VStack {
                    Text("Uploading...")
                    ProgressView(value: progress)
                        .progressViewStyle(.linear)
                    Text("\(Int(progress * 100))%")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            case .complete:
                Label("Upload complete", systemImage: "checkmark.circle.fill")
                    .font(.headline)
                    .foregroundStyle(.green)
            case .failed(let error):
                Label("Upload failed", systemImage: "xmark.circle.fill")
                    .font(.headline)
                    .foregroundStyle(.red)
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Button("Retry") {
                    Task { await viewModel.startUpload() }
                }
                .buttonStyle(.borderedProminent)
            case .idle:
                EmptyView()
            }
        }
    }

    private var fileListSection: some View {
        VStack(alignment: .leading, spacing: 8) {
            Text("Files")
                .font(.headline)
            ForEach(viewModel.fileNames, id: \.self) { name in
                HStack {
                    Image(systemName: "doc")
                    Text(name)
                        .lineLimit(1)
                        .truncationMode(.middle)
                    Spacer()
                }
                .font(.subheadline)
            }
        }
    }
}

// MARK: - Receive Detail

private struct ReceiveTransferDetailContent: View {
    @Bindable var viewModel: ReceiveViewModel

    var body: some View {
        ScrollView {
            VStack(spacing: 24) {
                statusSection
                if let url = viewModel.uploadURL {
                    qrCodeSection(url: url)
                    shareLinkSection(url: url)
                }
                receivedFilesSection
                if let expiresAt = viewModel.expiresAt {
                    expirySection(expiresAt: expiresAt)
                }
            }
            .padding()
        }
        .navigationTitle("Receive Files")
        .navigationBarTitleDisplayMode(.inline)
    }

    @ViewBuilder
    private var statusSection: some View {
        VStack(spacing: 8) {
            switch viewModel.state {
            case .creating:
                ProgressView("Creating drop slot...")
            case .waiting:
                Label("Waiting for files...", systemImage: "antenna.radiowaves.left.and.right")
                    .font(.headline)
                    .foregroundStyle(.orange)
                Text("Share the QR code or link so someone can upload files to you.")
                    .font(.subheadline)
                    .foregroundStyle(.secondary)
                    .multilineTextAlignment(.center)
            case .downloading(let progress):
                VStack {
                    Text("Downloading received files...")
                    ProgressView(value: progress)
                        .progressViewStyle(.linear)
                    Text("\(Int(progress * 100))%")
                        .font(.caption)
                        .foregroundStyle(.secondary)
                }
            case .decrypting:
                ProgressView("Decrypting files...")
            case .complete:
                Label("Files received", systemImage: "checkmark.circle.fill")
                    .font(.headline)
                    .foregroundStyle(.green)
            case .failed(let error):
                Label("Failed", systemImage: "xmark.circle.fill")
                    .font(.headline)
                    .foregroundStyle(.red)
                Text(error)
                    .font(.caption)
                    .foregroundStyle(.secondary)
                Button("Retry") {
                    Task { await viewModel.createDropSlot() }
                }
                .buttonStyle(.borderedProminent)
            case .idle:
                EmptyView()
            }
        }
    }

    @ViewBuilder
    private var receivedFilesSection: some View {
        if !viewModel.receivedFileURLs.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                Text("Received Files")
                    .font(.headline)
                ForEach(viewModel.receivedFileURLs, id: \.absoluteString) { url in
                    HStack {
                        Image(systemName: "doc.fill")
                        Text(url.lastPathComponent)
                            .lineLimit(1)
                            .truncationMode(.middle)
                        Spacer()
                        ShareLink(item: url) {
                            Image(systemName: "square.and.arrow.up")
                        }
                    }
                    .font(.subheadline)
                }
            }
        }
    }
}

// MARK: - Common Components

private func qrCodeSection(url: String) -> some View {
    VStack(spacing: 12) {
        if let image = QRCodeGenerator.generate(from: url, size: 250) {
            Image(uiImage: image)
                .interpolation(.none)
                .resizable()
                .scaledToFit()
                .frame(width: 250, height: 250)
                .padding()
                .background(.white)
                .clipShape(RoundedRectangle(cornerRadius: 12))
        }
    }
}

private func shareLinkSection(url: String) -> some View {
    HStack {
        Text(url)
            .font(.caption)
            .lineLimit(1)
            .truncationMode(.middle)
            .foregroundStyle(.secondary)
        Spacer()
        ShareLink(item: url) {
            Label("Share Link", systemImage: "link")
        }
        .buttonStyle(.bordered)
    }
}

private func expirySection(expiresAt: Date) -> some View {
    HStack {
        Image(systemName: "clock")
        Text("Expires")
        Spacer()
        Text(expiresAt, style: .relative)
            .foregroundStyle(.secondary)
    }
    .font(.subheadline)
}
