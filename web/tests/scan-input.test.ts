import { test } from "node:test";
import assert from "node:assert/strict";
import {
  classifyScanInput,
  normalizeScanOrigin,
  qrImageDimensions,
} from "../src/lib/scan-input.ts";
const id = "01234567-89ab-cdef-0123-456789abcdef",
  key = "A".repeat(43);
test("shared contract: download/upload, canonical origin and 32-byte fragment", () => {
  for (const path of ["d", "u"]) {
    const result = classifyScanInput(
      ` HTTPS://EXAMPLE.COM.:443/${path}/${id.toUpperCase()}#${path === "u" ? "v2." : ""}${key}= `,
    );
    assert.deepEqual(result, {
      kind: path === "d" ? "download" : "upload",
      origin: "https://example.com",
      url: `https://example.com/${path}/${id}#${path === "u" ? "v2." : ""}${key}`,
    });
  }
  assert.equal(normalizeScanOrigin("http://[::1]:8080"), "http://[::1]:8080");
  assert.equal(normalizeScanOrigin("http://192.168.178.29"), "http://192.168.178.29");
});
test("reject hostile or malformed inputs before any navigation", () => {
  const valid = `https://example.com/d/${id}#${key}`;
  for (const value of [
    valid.replace("https:", "javascript:"),
    valid.replace("example.com", "user:pass@example.com"),
    valid.replace("/d/", "/x/"),
    valid.replace("/d/", "/D/"),
    valid.replace("/d/", "/a/../d/"),
    valid.replace("/d/", "\\d/"),
    valid.replace("#", "?x=1#"),
    valid + "A",
    valid.slice(0, -1) + "B",
    valid.replace(id, "abc"),
    valid.replace("example.com", "127.1"),
    valid.replace("example.com", "0177.0.0.1"),
    valid.replace("example.com", "example.com:0"),
    valid.replace("example.com", "evil%2ecom"),
    valid.replace("example.com", "example.com\n"),
    "x".repeat(4097),
  ])
    assert.equal(classifyScanInput(value), null);
});
test("pairing is recognized without retaining or redeeming its secret", () => {
  const value = {
    type: "psst-pairing",
    version: 1,
    server_url: "https://example.com",
    code: "A".repeat(32),
  };
  assert.deepEqual(classifyScanInput(JSON.stringify(value)), { kind: "pairing" });
  assert.equal(classifyScanInput(JSON.stringify({ ...value, code: "short" })), null);
  assert.equal(classifyScanInput(JSON.stringify({ ...value, extra: true })), null);
});
test("reject image dimensions before raster allocation", () => {
  const png = new Uint8Array(24),
    view = new DataView(png.buffer);
  view.setUint32(0, 0x89504e47);
  view.setUint32(4, 0x0d0a1a0a);
  view.setUint32(12, 0x49484452);
  view.setUint32(16, 1024);
  view.setUint32(20, 1024);
  assert.deepEqual(qrImageDimensions(png), { width: 1024, height: 1024 });
  view.setUint32(16, 50000);
  assert.equal(qrImageDimensions(png), null);
  assert.equal(qrImageDimensions(new Uint8Array([255, 216, 255, 192, 0, 1])), null);
  assert.equal(qrImageDimensions(new Uint8Array()), null);
});
