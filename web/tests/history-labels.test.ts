import test from "node:test";
import assert from "node:assert/strict";
import { labelFor, labelKey, compactTitle } from "../src/lib/history-labels.ts";
test("labels retain legacy fallback names, separate resource types and preserve filename extensions", () => {
  const legacy = { same: { title: "Holiday.jpg", size: 12 } };
  assert.equal(labelFor(legacy, "transfers", "same").title, "Holiday.jpg");
  const changed = { ...legacy, [labelKey("slots", "same")]: { custom: "結婚式 💕" } };
  assert.equal(labelFor(changed, "slots", "same").custom, "結婚式 💕");
  assert.equal(labelFor(changed, "transfers", "same").title, "Holiday.jpg");
  const name = "😀".repeat(100) + ".jpg + 3 files";
  assert.ok(compactTitle(name).endsWith(".jpg + 3 files"));
  assert.equal(Array.from(compactTitle(name)).length, 72);
});
