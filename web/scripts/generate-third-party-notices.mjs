// Refresh notices using the exact installed versions in package-lock.json.
// Run after npm ci. No network requests. --check detects stale notices.
import { createHash } from "node:crypto";
import { readFileSync, readdirSync, writeFileSync, lstatSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

/**
 * @typedef {{file: string, sha256: string, upstream_sha256: string}} Notice
 * @typedef {{version: string, license?: string, integrity?: string, dev?: boolean, optional?: boolean}} LockEntry
 * @typedef {{packages: Record<string, LockEntry>}} PackageLock
 * @typedef {{name: string, version: string}} InstalledPackage
 * @typedef {{sha256: string, size: number}} FileDigest
 * @typedef {{path: string, name: string, version: string, integrity: string, source_repository: string, source_commit: string}} EmbeddedParent
 * @typedef {object} EmbeddedComponent
 * @property {string} name
 * @property {string} version
 * @property {string} license
 * @property {string} relationship
 * @property {EmbeddedParent} parent
 * @property {string} source_repository
 * @property {string} source_commit
 * @property {Record<string, FileDigest>} compiled_inputs
 * @property {string} license_file
 * @property {string} license_upstream_sha256
 * @property {string} attribution
 * @property {{file: string, sha256: string}} attribution_source
 * @property {boolean} source_reproduction_verified
 * @property {boolean} publication_authorized
 */
/** @typedef {{schema_version: number, components: EmbeddedComponent[]}} EmbeddedCatalog */
/**
 * @typedef {object} PackageNotice
 * @property {string} path
 * @property {string} name
 * @property {string} version
 * @property {string | null} license
 * @property {boolean} development
 * @property {boolean} optional
 * @property {string | null} integrity
 * @property {Notice[]} notices
 * @property {string} [omitted_text_reason]
 */

/** @param {string | Buffer} value */
const hash = (value) => createHash("sha256").update(value).digest("hex");

// Change only line endings and trailing horizontal whitespace/EOF blank lines.
/** @param {string | Buffer} content */
const normalize = (content) =>
  (typeof content === "string" ? content : content.toString("utf8"))
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

// The checked-in catalog is a reviewed input, not a discovery or approval API.
// Embedded code can be absent from the lockfile while present in a parent bundle.
/**
 * @param {string} root
 * @param {string} relative
 * @param {number} [maximum]
 * @returns {Buffer}
 */
function regularBytes(root, relative, maximum = 1024 * 1024) {
  if (
    typeof relative !== "string" ||
    !relative ||
    relative.includes("\\") ||
    relative.split("/").some((part) => !part || part === "." || part === "..") ||
    path.isAbsolute(relative)
  )
    throw new Error("Unsafe embedded component input path");
  let current = root;
  const parts = relative.split("/");
  for (const [index, part] of parts.entries()) {
    current = path.join(current, part);
    const stat = lstatSync(current);
    if (
      stat.isSymbolicLink() ||
      (index === parts.length - 1 ? !stat.isFile() : !stat.isDirectory())
    )
      throw new Error("Embedded component input must be a regular file");
    if (index === parts.length - 1 && stat.size > maximum)
      throw new Error("Embedded component input exceeds bounds");
  }
  const bytes = readFileSync(current);
  if (bytes.length > maximum) throw new Error("Embedded component input exceeds bounds");
  return bytes;
}

/**
 * @param {string} web
 * @param {PackageLock} lock
 * @param {string[]} notices
 * @returns {(EmbeddedComponent & {notices: Notice[]})[]}
 */
function embeddedNotices(web, lock, notices) {
  /** @type {EmbeddedCatalog} */
  const catalog = JSON.parse(
    regularBytes(web, "licenses/embedded-components.json").toString("utf8"),
  );
  if (
    catalog.schema_version !== 1 ||
    !Array.isArray(catalog.components) ||
    !catalog.components.length ||
    catalog.components.length > 32
  )
    throw new Error("Invalid embedded component catalog");
  const identities = new Set();
  return catalog.components.map((component) => {
    const { parent } = component,
      identity = `${component.name}@${component.version}`;
    if (
      typeof component.name !== "string" ||
      !component.name ||
      typeof component.version !== "string" ||
      !component.version ||
      identities.has(identity) ||
      component.license !== "Apache-2.0" ||
      component.relationship !== "bundled-within" ||
      component.source_reproduction_verified !== false ||
      component.publication_authorized !== false ||
      !/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(component.source_repository) ||
      !/^[a-f0-9]{40}$/.test(component.source_commit) ||
      !parent ||
      parent.path !== `node_modules/${parent.name}` ||
      typeof component.attribution !== "string" ||
      !component.attribution ||
      component.attribution.length > 4096
    )
      throw new Error("Invalid reviewed embedded component identity");
    identities.add(identity);
    const entry = lock.packages[parent.path];
    /** @type {InstalledPackage} */
    const installed = JSON.parse(regularBytes(web, `${parent.path}/package.json`).toString("utf8"));
    if (
      !entry ||
      entry.version !== parent.version ||
      entry.integrity !== parent.integrity ||
      !/^sha512-[A-Za-z0-9+/]{86}==$/.test(parent.integrity) ||
      installed.name !== parent.name ||
      installed.version !== parent.version
    )
      throw new Error(`Embedded component parent differs from reviewed lock: ${identity}`);
    const inputs = Object.entries(component.compiled_inputs ?? {});
    if (!inputs.length || inputs.length > 16) throw new Error("Missing embedded compiled inputs");
    for (const [file, expected] of inputs) {
      const bytes = regularBytes(web, `${parent.path}/${file}`);
      if (bytes.length !== expected.size || hash(bytes) !== expected.sha256)
        throw new Error(`Embedded component compiled input changed: ${identity}/${file}`);
    }
    const attribution = component.attribution_source;
    if (
      !attribution ||
      hash(regularBytes(web, `${parent.path}/${attribution.file}`)) !== attribution.sha256
    )
      throw new Error(`Embedded component attribution input changed: ${identity}`);
    const upstream = regularBytes(web, component.license_file),
      content = normalize(upstream);
    if (
      !/^[a-f0-9]{64}$/.test(component.license_upstream_sha256) ||
      hash(upstream) !== component.license_upstream_sha256
    )
      throw new Error(`Embedded component original license changed: ${identity}`);
    notices.push(
      `=== ${identity}: embedded within ${parent.name}@${parent.version} (${component.relationship}) ===`,
      `Source: https://github.com/${component.source_repository}/tree/${component.source_commit}`,
      component.attribution,
      `Original license: ${component.license_file}`,
      content,
      "",
    );
    return {
      ...component,
      notices: [
        { file: component.license_file, sha256: hash(content), upstream_sha256: hash(upstream) },
      ],
    };
  });
}
/**
 * @param {string} web Explicit web root for deterministic offline fixtures.
 * @param {{check?: boolean}} [options]
 * @returns {{packages: number, embedded_components: number}}
 */
export function generateNotices(web, { check = false } = {}) {
  const lockBytes = readFileSync(path.join(web, "package-lock.json")),
    output = path.join(web, "static", "licenses");
  /** @type {PackageLock} */
  const lock = JSON.parse(lockBytes.toString("utf8"));
  /** @type {PackageNotice[]} */
  const packages = [];
  const notices = [
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
    const name = relative.split("node_modules/").at(-1) ?? "";
    /** @type {PackageNotice} */
    const record = {
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
        .filter(
          (item) => item.isFile() && /^(licen[cs]e|copying|copyright|notice)/i.test(item.name),
        )
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
  const embedded_components = embeddedNotices(web, lock, notices);
  const files = new Map([
    [
      "dependency-inventory.json",
      JSON.stringify(
        { package_lock_sha256: hash(lockBytes), packages, embedded_components },
        null,
        2,
      ) + "\n",
    ],
    ["THIRD_PARTY_NOTICES.txt", normalize(notices.join("\n"))],
  ]);
  for (const [name, content] of files) {
    const file = path.join(output, name);
    if (check) {
      if (readFileSync(file, "utf8") !== content)
        throw new Error(
          `Stale notices: ${file}; run node scripts/generate-third-party-notices.mjs`,
        );
    } else writeFileSync(file, content);
  }
  return { packages: packages.length, embedded_components: embedded_components.length };
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), ".."),
    check = process.argv.includes("--check"),
    result = generateNotices(web, { check });
  console.log(
    `${check ? "Checked" : "Generated"} notices for ${result.packages} locked npm packages and ${result.embedded_components} embedded components.`,
  );
}
