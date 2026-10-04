import { test } from "node:test";
import assert from "node:assert/strict";
import { legacyHistoryEntries, LEGACY_ENTRY_CHARS } from "../src/lib/legacy-history.ts";
test("legacy parser yields each entry without building a whole account map", () => {
  const values = {
    'quoted\\"key': "https://example.test/d/id#key",
    braces: { custom: '} , [ " \\ 😀', title: "title", size: 4 },
    unicode: { custom: "結婚式" },
  };
  assert.deepEqual(Object.fromEntries(legacyHistoryEntries(JSON.stringify(values))), values);
  const parser = legacyHistoryEntries('{"valid":"first", "broken":');
  assert.deepEqual(parser.next().value, ["valid", "first"]);
  assert.throws(() => parser.next());
  const count = 5000,
    raw = "{" + Array.from({ length: count }, (_, i) => `"id${i}":"link${i}"`).join(",") + "}";
  let processed = 0;
  for (const [key, value] of legacyHistoryEntries(raw)) {
    assert.equal(key, `id${processed}`);
    assert.equal(value, `link${processed}`);
    processed++;
  }
  assert.equal(processed, count);
});
test("legacy parser rejects malformed and oversized entries without silently dropping them", () => {
  for (const raw of [
    "[]",
    '{"x":1,}',
    '{"x" 1}',
    '{"x":',
    '{"x":1} trailing',
    '{"x":"unterminated}',
    '{"x":[}',
    '{"x":' + "[".repeat(17) + "0" + "]".repeat(17) + "}",
    '{"x":"' + "a".repeat(LEGACY_ENTRY_CHARS + 1) + '"}',
  ])
    assert.throws(() => [...legacyHistoryEntries(raw)]);
  assert.deepEqual([...legacyHistoryEntries(" { } \n")], []);
});
