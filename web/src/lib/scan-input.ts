/** Kept in step with shared ScanInputClassifier / UrlHelper. Never report raw input. */
export type ScanInput =
  | { kind: "pairing" }
  | { kind: "download" | "upload"; origin: string; url: string };
export function normalizeScanOrigin(raw: string): string | null {
  if (raw.length > 2048) return null;
  const match = /^(https?):\/\/(\[[0-9a-f:.]+\]|[a-z0-9.-]+)(?::([0-9]{1,5}))?\/?$/i.exec(raw);
  if (!match) return null;
  const host = match[2].toLowerCase().replace(/\.$/, "");
  if (!host.startsWith("[")) {
    if (
      host.length > 253 ||
      host.split(".").some((p) => !/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(p))
    )
      return null;
    if (
      /^[0-9.]+$/.test(host) &&
      (host.split(".").length !== 4 ||
        host.split(".").some((p) => !/^(0|[1-9][0-9]{0,2})$/.test(p) || Number(p) > 255))
    )
      return null;
  }
  if (match[3] && (Number(match[3]) < 1 || Number(match[3]) > 65535)) return null;
  try {
    return new URL(`${match[1]}://${host}${match[3] ? `:${match[3]}` : ""}`).origin;
  } catch {
    return null;
  }
}
export function classifyScanInput(raw: string): ScanInput | null {
  if (raw.length > 4096) return null;
  const input = raw.trim();
  if (input.startsWith("{")) {
    try {
      const value = JSON.parse(input);
      if (
        !value ||
        Object.keys(value).sort().join(",") !== "code,server_url,type,version" ||
        value.type !== "psst-pairing" ||
        value.version !== 1 ||
        typeof value.code !== "string" ||
        !/^[A-Za-z0-9_-]{32,128}$/.test(value.code) ||
        typeof value.server_url !== "string" ||
        !normalizeScanOrigin(value.server_url)
      )
        return null;
      return { kind: "pairing" };
    } catch {
      return null;
    }
  }
  const match =
    /^(https?:\/\/[^/?#]+)\/([du])\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})#([A-Za-z0-9_-]{43})=?$/i.exec(
      input,
    );
  if (!match || !["d", "u"].includes(match[2])) return null;
  const origin = normalizeScanOrigin(match[1]);
  // A canonical 32-byte base64url key has two zero padding bits.
  if (!origin || !"AEIMQUYcgkosw048".includes(match[4].at(-1)!)) return null;
  return {
    kind: match[2] === "d" ? "download" : "upload",
    origin,
    url: `${origin}/${match[2]}/${match[3].toLowerCase()}#${match[4]}`,
  };
}

/** Read dimensions before asking the browser to allocate a decoded raster. */
export function qrImageDimensions(bytes: Uint8Array): { width: number; height: number } | null {
  const view = new DataView(bytes.buffer, bytes.byteOffset, bytes.byteLength);
  let width = 0,
    height = 0;
  if (
    bytes.length >= 24 &&
    view.getUint32(0) === 0x89504e47 &&
    view.getUint32(4) === 0x0d0a1a0a &&
    view.getUint32(12) === 0x49484452
  ) {
    width = view.getUint32(16);
    height = view.getUint32(20);
  } else if (bytes[0] === 0xff && bytes[1] === 0xd8) {
    let pos = 2;
    while (pos + 4 <= bytes.length) {
      if (bytes[pos++] !== 0xff) return null;
      while (bytes[pos] === 0xff) pos++;
      const marker = bytes[pos++];
      if (marker === 0xda || marker === 0xd9 || pos + 2 > bytes.length) break;
      const size = view.getUint16(pos);
      if (size < 2 || pos + size > bytes.length) return null;
      if (
        [0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf].includes(
          marker,
        ) &&
        size >= 7
      ) {
        height = view.getUint16(pos + 3);
        width = view.getUint16(pos + 5);
        break;
      }
      pos += size;
    }
  }
  return width > 0 && height > 0 && width <= 8192 && height <= 8192 && width * height <= 16_000_000
    ? { width, height }
    : null;
}
