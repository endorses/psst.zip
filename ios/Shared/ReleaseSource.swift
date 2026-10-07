import Foundation

struct ReleaseSource: Equatable {
  let version: String
  let revision: String
  let source: URL
  let sourceArchive: URL

  static func safeSourceURL(_ value: String) -> URL? {
    guard value.utf8.count <= 2048,
      !value.unicodeScalars.contains(where: { $0.value <= 32 || $0.value == 127 }),
      let components = URLComponents(string: value), components.scheme == "https",
      let host = components.host, !host.isEmpty,
      components.user == nil, components.password == nil,
      components.query == nil, components.fragment == nil,
      components.port == nil || (1...65535).contains(components.port!),
      let url = components.url
    else { return nil }
    return url
  }

  static func metadataURL(server: String) -> URL? {
    guard let origin = safeSourceURL(server), origin.path.isEmpty || origin.path == "/" else {
      return nil
    }
    return origin.appendingPathComponent("licenses/release.json")
  }

  static func parse(_ data: Data) -> ReleaseSource? {
    guard data.count <= 16 * 1024,
      let object = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
      object["name"] as? String == "psst.zip", object["license"] as? String == "AGPL-3.0-only",
      let version = object["version"] as? String,
      !version.unicodeScalars.contains(where: { $0.value <= 32 || $0.value == 127 }),
      version.range(of: #"^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$"#, options: .regularExpression)
        != nil,
      let revision = object["revision"] as? String, revision.utf8.count == 40,
      revision.range(of: #"^[a-f0-9]{40}$"#, options: .regularExpression) != nil,
      let sourceText = object["source"] as? String, let source = safeSourceURL(sourceText),
      let archiveText = object["source_archive"] as? String,
      let archive = safeSourceURL(archiveText)
    else { return nil }
    return ReleaseSource(
      version: version, revision: revision, source: source, sourceArchive: archive)
  }
}
