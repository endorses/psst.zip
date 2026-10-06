import test from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { checkLocalization } from "../scripts/check-localization.mjs";
function check(en: string, de: string, component = "") {
  const directory = mkdtempSync(join(tmpdir(), "psst-localization-fixture-"));
  try {
    mkdirSync(join(directory, "lib/i18n"), { recursive: true });
    writeFileSync(join(directory, "lib/i18n/en.json"), en);
    writeFileSync(join(directory, "lib/i18n/de.json"), de);
    writeFileSync(join(directory, "lib/i18n/nontranslatable.json"), '{"identical":[],"source":[]}');
    if (component) writeFileSync(join(directory, "Example.svelte"), component);
    return checkLocalization(directory);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}
test("catalog gate rejects duplicates, missing plural variants, placeholder drift and untranslated keys", () => {
  for (const [en, de, expected] of [
    ['{"hello":"Hello","hello":"Hi"}', '{"hello":"Hallo"}', "Duplicate"],
    ['{"files":{"one":"One"}}', '{"files":{"one":"Eine"}}', "Invalid plural"],
    [
      '{"files":{"one":"One","other":"Many","other":"Several"}}',
      '{"files":{"one":"Eine","other":"Viele"}}',
      "Duplicate",
    ],
    ['{"hello":"Hello {name}"}', '{"hello":"Hallo {other}"}', "Placeholder"],
    ['{"hello":"Hello"}', '{"hello":"Hello"}', "Untranslated"],
  ] as const) {
    const result = check(en, de);
    assert.ok(result.failures.length > 0);
    assert.match(result.failures.join("\n"), new RegExp(expected));
  }
});
test("source gate rejects single word visible attributes, prose expressions and raw text", () => {
  for (const source of [
    '<button aria-label={"Save"}></button>',
    "<h1>Hello</h1>",
    '<h1>{$t("Settings")}</h1>',
    '<p>{"Try again please"}</p>',
  ]) {
    const result = check('{"hello":"Hello"}', '{"hello":"Hallo"}', source);
    assert.ok(result.failures.length > 0);
    assert.match(result.failures.join("\n"), /unextracted/);
  }
  const valid = check('{"hello":"Hello {name}"}', '{"hello":"Hallo {name}"}');
  assert.deepEqual(valid.failures, []);
  assert.equal(valid.catalogCount, 1);
});
