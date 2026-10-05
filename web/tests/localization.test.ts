import assert from "node:assert/strict";
import test from "node:test";
import { get } from "svelte/store";
import { normalizePreference, resolveLanguage, regionalLocale } from "../src/lib/i18n/locale.ts";
import { message, renderMessage, dateArgument, errorText } from "../src/lib/i18n/messages.ts";
import { preference, t } from "../src/lib/i18n/index.ts";
import { ApiError, hasStatus } from "../src/lib/api-error.ts";
import { TrafficLimitError } from "../src/lib/traffic-policy.ts";

test("system preference resolves supported regional language in browser priority order", () => {
  assert.equal(resolveLanguage("system", ["fr-FR", "de-AT", "en-US"]), "de");
  assert.equal(resolveLanguage("system", ["en-GB", "de-DE"]), "en");
  assert.equal(resolveLanguage("system", ["fr-FR"]), "en");
  assert.equal(resolveLanguage("de", ["en-US"]), "de");
  assert.equal(normalizePreference("DE"), "system");
  assert.equal(regionalLocale("de", ["en-US", "de-CH"]), "de-CH");
  assert.equal(regionalLocale("en", ["invalid_???", "de-DE"]), "en-US");
});
test("catalog plural messages use complete sentences for zero one and multiple", () => {
  assert.equal(renderMessage(message("fileCount", { count: 0 }), "de"), "0 Dateien");
  assert.equal(renderMessage(message("fileCount", { count: 1 }), "de"), "1 Datei");
  assert.equal(renderMessage(message("fileCount", { count: 2 }), "en"), "2 files");
  assert.equal(
    renderMessage(message("receivedFileCount", { count: 1 }), "de"),
    "1 Datei empfangen",
  );
  assert.equal(renderMessage(message("valueHours", { arg0: 1 }), "en"), "1 hour");
  assert.equal(renderMessage(message("valueHours", { arg0: 2 }), "de"), "2 Stunden");
  assert.equal(
    renderMessage(message("fileCount", { count: 1234 }), "de", "de-DE"),
    "1.234 Dateien",
  );
});
test("existing error descriptors react without changing classification or user content", () => {
  const failure = new ApiError(401, "invalid_credentials");
  const snapshot = errorText(failure);
  preference.set("en");
  assert.match(get(t)(snapshot), /Incorrect username/);
  preference.set("de");
  assert.match(get(t)(snapshot), /Benutzername/);
  assert.equal(hasStatus(failure, 401), true);
  assert.equal(get(t)("<script>my file</script>"), "<script>my file</script>");
  assert.equal(get(t)("Send"), "Send"); // user-owned text never used as a catalog key
  assert.match(get(t)(errorText(new Error("secret https://private/#key"))), /schiefgelaufen/);
  assert.doesNotMatch(get(t)(errorText(new ApiError(503, "secret.server.path"))), /secret/);
  preference.set("en");
});
test("dates and nested retry messages follow display locale after creation", () => {
  const failure = new TrafficLimitError("traffic_budget_exhausted", "2026-11-01T00:00:00Z");
  assert.match(renderMessage(failure.presentation, "en", "en-US"), /Nov 1, 2026/);
  assert.match(renderMessage(failure.presentation, "de", "de-DE"), /01.11.2026/);
  assert.match(
    renderMessage(
      message("nextTrafficCycle", { date: dateArgument("2026-11-01T00:00:00Z", true) }),
      "de",
    ),
    /UTC/,
  );
});

test("stored file-limit errors keep raw sizes for current-locale formatting", async () => {
  const { assertFileSize } = await import("../src/lib/limits.ts");
  let failure: unknown;
  try {
    assertFileSize(30 * 1024 * 1024, 25.5 * 1024 * 1024);
  } catch (error) {
    failure = error;
  }
  assert.match(renderMessage(errorText(failure), "de"), /25,5 MiB/);
  assert.match(renderMessage(errorText(failure), "en"), /25.5 MiB/);
});

test("link policy summaries pluralize the total limit independently of used allowances", () => {
  for (const [used, count, english, german] of [
    [0, 1, "0 of 1 file allowance used", "0 von 1 Dateiplatz verbraucht"],
    [1, 1, "1 of 1 file allowance used", "1 von 1 Dateiplatz verbraucht"],
    [1, 2, "1 of 2 file allowances used", "1 von 2 Dateiplätzen verbraucht"],
  ] as const) {
    const summary = message("fileAllowancesUsed", { used, count });
    assert.equal(renderMessage(summary, "en"), english);
    assert.equal(renderMessage(summary, "de"), german);
  }
  for (const count of [1, 2]) {
    const english = `${count} download ${count === 1 ? "attempt" : "attempts"} per file`;
    const german = `${count} ${count === 1 ? "Download-Versuch" : "Download-Versuche"} pro Datei`;
    assert.equal(renderMessage(message("downloadAttemptsPerFile", { count }), "en"), english);
    assert.equal(renderMessage(message("downloadAttemptsPerFile", { count }), "de"), german);
    const complete = message("downloadAttemptsPerFileIncludingInterruptedDownloads", { count });
    assert.equal(renderMessage(complete, "en"), `${english}, including interrupted downloads.`);
    assert.equal(
      renderMessage(complete, "de"),
      `${german}, einschließlich unterbrochener Downloads.`,
    );
  }
});
