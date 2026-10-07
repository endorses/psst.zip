export interface ReleaseSource {
  version: string;
  revision: string;
  source: string;
  sourceArchive: string;
  noticeFiles: string[];
}
const MAX_METADATA_BYTES = 16 * 1024;
export function safeSourceURL(value: unknown): string | null {
  if (typeof value !== "string" || value.length > 2048 || /[\u0000-\u0020\u007f]/.test(value))
    return null;
  try {
    const url = new URL(value);
    if (url.protocol !== "https:" || url.username || url.password || url.search || url.hash)
      return null;
    return url.href.replace(/\/$/, "");
  } catch {
    return null;
  }
}
export function parseReleaseSource(value: unknown): ReleaseSource | null {
  if (!value || typeof value !== "object") return null;
  const data = value as Record<string, unknown>;
  const source = safeSourceURL(data.source),
    sourceArchive = safeSourceURL(data.source_archive);
  if (
    data.name !== "psst.zip" ||
    data.license !== "AGPL-3.0-only" ||
    typeof data.version !== "string" ||
    /[\u0000-\u0020\u007f]/.test(data.version) ||
    !/^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$/.test(data.version) ||
    typeof data.revision !== "string" ||
    data.revision.length !== 40 ||
    !/^[a-f0-9]{40}$/.test(data.revision) ||
    !source ||
    !sourceArchive
  )
    return null;
  const allowedNotices = new Set([
    "/licenses/backend/THIRD_PARTY_NOTICES.txt",
    "/licenses/runtime/THIRD_PARTY_NOTICES.txt",
  ]);
  const noticeFiles = data.notice_files ?? [];
  if (
    !Array.isArray(noticeFiles) ||
    noticeFiles.length > allowedNotices.size ||
    noticeFiles.some((value) => typeof value !== "string" || !allowedNotices.has(value)) ||
    new Set(noticeFiles).size !== noticeFiles.length
  )
    return null;
  return { version: data.version, revision: data.revision, source, sourceArchive, noticeFiles };
}
export async function loadReleaseSource(
  fetcher: typeof fetch,
  signal?: AbortSignal,
): Promise<ReleaseSource | null> {
  const response = await fetcher("/licenses/release.json", {
    credentials: "omit",
    redirect: "error",
    cache: "no-store",
    signal,
  });
  if (!response.ok || !response.body) return null;
  const reader = response.body.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  try {
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      size += value.length;
      if (size > MAX_METADATA_BYTES) return null;
      chunks.push(value);
    }
    const bytes = new Uint8Array(size);
    let offset = 0;
    for (const chunk of chunks) {
      bytes.set(chunk, offset);
      offset += chunk.length;
    }
    return parseReleaseSource(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(bytes)));
  } finally {
    await reader.cancel();
  }
}
