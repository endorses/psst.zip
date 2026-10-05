import { test } from "node:test";
import assert from "node:assert/strict";
import { rememberInboxPosition, restoreInboxPosition } from "../src/lib/inbox-navigation.ts";
import { historyID, historyCursor } from "./history-page-fixture.ts";
const position = { cursor: historyCursor(1), previous: [""], page: 2, scroll: 400 };
function store() {
  const values = new Map<string, string>();
  return {
    values,
    getItem: (key: string) => values.get(key) ?? null,
    setItem: (key: string, value: string) => {
      values.set(key, value);
    },
    removeItem: (key: string) => {
      values.delete(key);
    },
  };
}
test("owner inbox return restores one bounded account-scoped view without keys or file metadata", () => {
  const storage = store();
  rememberInboxPosition(storage, historyID(9), historyID(2), position, 100);
  assert.equal(restoreInboxPosition(storage, historyID(8), historyID(2), 101), null);
  assert.equal(restoreInboxPosition(storage, historyID(9), historyID(3), 101), null);
  assert.deepEqual(restoreInboxPosition(storage, historyID(9), historyID(2), 101), position);
  assert.equal(storage.values.size, 0);
  assert.equal(restoreInboxPosition(storage, historyID(9), historyID(2), 101), null);
});
test("stale, oversized or malformed positions cannot become navigation authority", () => {
  const storage = store();
  for (const invalid of [
    { ...position, previous: Array(101).fill("") },
    { ...position, cursor: "secret+value" },
    { ...position, scroll: -1 },
  ]) {
    rememberInboxPosition(storage, historyID(9), historyID(2), invalid, 100);
    assert.equal(storage.values.size, 0);
  }
  rememberInboxPosition(storage, historyID(9), historyID(2), position, 100);
  assert.equal(restoreInboxPosition(storage, historyID(9), historyID(2), 100 + 86400001), null);
  assert.equal(storage.values.size, 0);
  storage.setItem("psst.inbox-return", "x".repeat(65000));
  assert.equal(restoreInboxPosition(storage, historyID(9), historyID(2)), null);
  assert.equal(storage.values.size, 0);
});
