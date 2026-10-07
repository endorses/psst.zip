import assert from "node:assert/strict";
import { test } from "node:test";
import { loadReleaseSource, parseReleaseSource, safeSourceURL } from "../src/lib/release-source.ts";
const good = {
  name: "psst.zip",
  license: "AGPL-3.0-only",
  version: "v1.2.3",
  revision: "a".repeat(40),
  source: "https://example.org/fork",
  source_archive: "https://example.org/source/a.tar.gz",
};
test("release metadata keeps operator source and exact commit", () => {
  assert.equal(parseReleaseSource(good)?.sourceArchive, good.source_archive);
  assert.equal(parseReleaseSource({ ...good, revision: "main" }), null);
  assert.equal(parseReleaseSource({ ...good, revision: good.revision + "\n" }), null);
  assert.equal(parseReleaseSource({ ...good, version: good.version + "\n" }), null);
  for (const source of [
    "javascript:alert(1)",
    "http://example.org/source",
    "https://user:secret@example.org/source",
    "https://example.org/source?token=x",
    "https://example.org/source#secret",
    "https://example.org/\nsource",
  ])
    assert.equal(safeSourceURL(source), null);
});
test("only notices actually declared by the deployment are linked", () => {
  assert.deepEqual(parseReleaseSource(good)?.noticeFiles, []);
  const path = "/licenses/backend/THIRD_PARTY_NOTICES.txt";
  assert.deepEqual(parseReleaseSource({ ...good, notice_files: [path] })?.noticeFiles, [path]);
  for (const notice_files of [
    ["https://other.example/notice"],
    ["/missing"],
    [path, path],
    "invalid",
  ])
    assert.equal(parseReleaseSource({ ...good, notice_files }), null);
});
test("source fetch rejects oversized metadata and never sends session credentials", async () => {
  let cancelled = false;
  const fetcher = (async (url, options) => {
    assert.equal(url, "/licenses/release.json");
    assert.equal(options?.credentials, "omit");
    assert.equal(options?.redirect, "error");
    return new Response(
      new ReadableStream({
        start(controller) {
          controller.enqueue(new Uint8Array(16 * 1024 + 1));
        },
        cancel() {
          cancelled = true;
        },
      }),
    );
  }) as typeof fetch;
  assert.equal(await loadReleaseSource(fetcher), null);
  assert.ok(cancelled);
});
test("source fetch parses bounded operator metadata", async () => {
  assert.equal(
    (await loadReleaseSource((async () => new Response(JSON.stringify(good))) as typeof fetch))
      ?.revision,
    good.revision,
  );
});
