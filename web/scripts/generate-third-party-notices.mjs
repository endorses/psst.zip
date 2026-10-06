// Refresh notices using the exact installed versions in package-lock.json.
// Run after npm ci. No network requests. --check detects stale notices.
import { createHash } from "node:crypto";
import { readFileSync, readdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), ".."),
  check = process.argv.includes("--check"),
  hash = (value) => createHash("sha256").update(value).digest("hex"),
  lockBytes = readFileSync(path.join(web, "package-lock.json")),
  lock = JSON.parse(lockBytes),
  output = path.join(web, "static", "licenses");

// Change only line endings and trailing horizontal whitespace/EOF blank lines.
const normalize = (content) =>
  content
    .toString("utf8")
    .replace(/\r\n?/g, "\n")
    .split("\n")
    .map((line) => line.replace(/[ \t]+$/g, ""))
    .join("\n")
    .replace(/\n*$/, "\n");

// These exact versions omit full license texts from their npm tarballs and are
// build-only or Node-only dependencies, absent from the served browser code.
// Preserve their declared metadata without presenting it as a full license text.
// A changed version requires reviewing this exception again.
const omitted = new Map([
  ["combine-errors@3.0.3", "Node-only tus URL storage; browser uses lib/browser/urlStorage.js"],
  ["custom-error-instance@2.1.1", "Node-only dependency of combine-errors"],
  ["is-reference@3.0.3", "Svelte compiler only"],
  ["locate-character@3.0.0", "Svelte compiler only"],
  ["@polka/url@1.0.0-next.29", "Node-only URL parser used by the development/preview server"],
  ["sirv@3.0.2", "Node-only static file server used for development/preview; releases use Caddy"],
]);
const packages = [],
  notices = [
    "psst.zip web dependency notices",
    "Generated from exact package-lock.json package versions after npm ci.",
    "License formatting uses LF, no trailing spaces/tabs, and one final newline.",
    "The inventory retains both original upstream and distributed text hashes.",
    "Includes available license/notice texts for all nonoptional locked packages,",
    "including development dependencies whose runtimes can enter the browser bundle.",
    "This is a conservative application/compiler/build dependency superset.",
    "Optional development platform packages are inventoried without host-specific",
    "license extraction, so generated notices do not depend on the build host.",
    "Build-only/Node-only missing-text exceptions are identified in the inventory.",
    "The Caddy/base-image distribution has separate upstream license obligations.",
    "",
  ];

for (const [relative, entry] of Object.entries(lock.packages).sort(([a], [b]) =>
  a.localeCompare(b),
)) {
  if (!relative) continue;
  const name = relative.split("node_modules/").at(-1),
    record = {
      path: relative,
      name,
      version: entry.version,
      license: entry.license ?? null,
      development: Boolean(entry.dev),
      optional: Boolean(entry.optional),
      integrity: entry.integrity ?? null,
      notices: [],
    };
  if (entry.optional) {
    if (!entry.dev)
      throw new Error(`Review optional runtime dependency before distributing: ${relative}`);
    record.omitted_text_reason = "Optional development/platform package; metadata only";
  } else {
    const directory = path.join(web, relative),
      installed = JSON.parse(readFileSync(path.join(directory, "package.json"), "utf8"));
    if (installed.version !== entry.version)
      throw new Error(`Installed version differs from lockfile: ${relative}`);
    const documents = readdirSync(directory, { withFileTypes: true })
      .filter((item) => item.isFile() && /^(licen[cs]e|copying|copyright|notice)/i.test(item.name))
      .map((item) => item.name)
      .sort();
    if (!documents.some((name) => /^(licen[cs]e|copying)/i.test(name))) {
      const reason = omitted.get(`${name}@${entry.version}`);
      if (!reason)
        throw new Error(`No complete upstream license text: ${relative}@${entry.version}`);
      record.omitted_text_reason = reason;
    }
    for (const document of documents) {
      const upstream = readFileSync(path.join(directory, document)),
        content = normalize(upstream);
      record.notices.push({
        file: document,
        sha256: hash(content),
        upstream_sha256: hash(upstream),
      });
      notices.push(`=== ${name}@${entry.version}: ${document} ===`, content, "");
    }
  }
  packages.push(record);
}
const files = new Map([
  [
    "dependency-inventory.json",
    JSON.stringify({ package_lock_sha256: hash(lockBytes), packages }, null, 2) + "\n",
  ],
  ["THIRD_PARTY_NOTICES.txt", normalize(notices.join("\n"))],
]);
for (const [name, content] of files) {
  const file = path.join(output, name);
  if (check) {
    if (readFileSync(file, "utf8") !== content)
      throw new Error(`Stale notices: ${file}; run node scripts/generate-third-party-notices.mjs`);
  } else writeFileSync(file, content);
}
console.log(
  `${check ? "Checked" : "Generated"} notices for ${packages.length} locked npm packages.`,
);
