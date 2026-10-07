import SwiftUI

private final class SourceMetadataRedirectPolicy: NSObject, URLSessionTaskDelegate {
  func urlSession(
    _: URLSession, task _: URLSessionTask, willPerformHTTPRedirection _: HTTPURLResponse,
    newRequest _: URLRequest, completionHandler: @escaping (URLRequest?) -> Void
  ) {
    completionHandler(nil)
  }
}

/// Metadata is public and intentionally independent of the authenticated API client.
private func loadReleaseSource(server: String) async -> ReleaseSource? {
  guard let url = ReleaseSource.metadataURL(server: server) else { return nil }
  let configuration = URLSessionConfiguration.ephemeral
  configuration.httpShouldSetCookies = false
  configuration.timeoutIntervalForRequest = 10
  configuration.timeoutIntervalForResource = 10
  let session = URLSession(
    configuration: configuration, delegate: SourceMetadataRedirectPolicy(), delegateQueue: nil)
  defer { session.invalidateAndCancel() }
  do {
    var request = URLRequest(url: url)
    request.cachePolicy = .reloadIgnoringLocalCacheData
    let (bytes, response) = try await session.bytes(for: request)
    guard (response as? HTTPURLResponse)?.statusCode == 200 else { return nil }
    var data = Data()
    for try await byte in bytes {
      if Task.isCancelled || data.count >= 16 * 1024 { return nil }
      data.append(byte)
    }
    return ReleaseSource.parse(data)
  } catch { return nil }
}

struct SourceLicensesView: View {
  let serverURL: String
  @State private var release: ReleaseSource?
  @State private var loading = true
  private var clientRevision: String? {
    guard let url = Bundle.main.url(forResource: "ClientSource", withExtension: "json"),
      let data = try? Data(contentsOf: url),
      let object = try? JSONSerialization.jsonObject(with: data) as? [String: String],
      let revision = object["revision"],
      revision.range(of: #"^[a-f0-9]{40}$"#, options: .regularExpression) != nil
    else { return nil }
    return revision
  }
  var body: some View {
    Form {
      Section(L10n.text("Native client")) {
        Text(verbatim: Bundle.main.infoDictionary?["CFBundleShortVersionString"] as? String ?? "")
        Link(
          L10n.text("Project source repository"),
          destination: URL(string: "https://github.com/endorses/psst.zip")!)
        if let revision = clientRevision {
          Text(verbatim: revision).font(.caption).textSelection(.enabled)
          if let archive = URL(
            string: "https://github.com/endorses/psst.zip/archive/\(revision).tar.gz")
          {
            Link(L10n.text("Download this client’s source"), destination: archive)
          }
        }
        NavigationLink(L10n.text("Project license (AGPL v3)")) {
          LegalTextView(resource: "AGPL-3.0-only", title: "Project license (AGPL v3)")
        }
        NavigationLink(L10n.text("Third-party notices")) {
          LegalTextView(resource: "THIRD_PARTY_NOTICES", title: "Third-party notices")
        }
      }
      Section(L10n.text("Hosted server & web client")) {
        if loading {
          ProgressView()
        } else if let release {
          Text(verbatim: release.version)
          Text(verbatim: release.revision).font(.caption).textSelection(.enabled)
          Link(L10n.text("Download source for this version"), destination: release.sourceArchive)
        } else {
          Text(
            L10n.text(
              "Exact source metadata is unavailable. Ask the server operator for the source of this deployed version."
            ))
        }
        if let url = ReleaseSource.metadataURL(server: serverURL)?.deletingLastPathComponent()
          .deletingLastPathComponent().appendingPathComponent("legal")
        {
          Link(L10n.text("Server source & licenses"), destination: url)
        }
      }
    }
    .navigationTitle(L10n.text("Source & licenses"))
    .task(id: serverURL) {
      loading = true
      release = await loadReleaseSource(server: serverURL)
      loading = false
    }
  }
}

private struct LegalTextView: View {
  let resource: String
  let title: String
  private var text: String {
    guard let url = Bundle.main.url(forResource: resource, withExtension: "txt"),
      let text = try? String(contentsOf: url, encoding: .utf8)
    else {
      return L10n.text("Notices are unavailable in this build.")
    }
    return text
  }
  var body: some View {
    ScrollView {
      Text(verbatim: text).textSelection(.enabled).frame(maxWidth: .infinity, alignment: .leading)
        .padding()
    }
    .navigationTitle(L10n.text(title))
  }
}
