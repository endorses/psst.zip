/**
 * Record client output/module facts from Rollup, not source reproduction or approval.
 * Positive chunk renderedLength records JS chunk contribution only. Worker
 * sub-builds and copied JavaScript assets may lack main-chunk attribution; output
 * hashes retain those bytes without claiming complete browser module closure.
 * Hook semantics: https://rollupjs.org/plugin-development/#generatebundle
 * SSR exclusion: https://vite.dev/guide/api-plugin#configresolved
 */
import { createHash } from "node:crypto";
import { readFileSync, realpathSync, statSync } from "node:fs";
import { isAbsolute, relative, resolve, sep } from "node:path";

const INVENTORY = "licenses/browser-module-inventory.json";
const MAX_FILE = 32 * 1024 * 1024;
const MAX_MODULES = 25000;
const BUILD_METADATA = new Set([".vite/manifest.json", ".vite/ssr-manifest.json"]);
/** @typedef {{name:string,version:string,integrity:string,lock_path:string,package_json_sha256:string}} PackageRecord
 * @typedef {{id:string,kind:string,module_path:string|null,source_sha256:string|null,package:PackageRecord|null,transform_input_sha256:string|null,rollup_input_sha256:string|null}} InputRecord
 * @typedef {{version:string,integrity:string,resolved:string,link?:boolean}} LockedPackage
 * @typedef {{lockfileVersion:number,packages:Record<string,LockedPackage>}} LockFile
 */
/** @param {string|Uint8Array} bytes */
const digest = (bytes) => "sha256:" + createHash("sha256").update(bytes).digest("hex");
/** @param {unknown} condition @param {string} message @returns {asserts condition} */
function requireFact(condition, message) {
  if (!condition) throw new Error("Browser inventory: " + message);
}
/** @param {string} value */
function safePath(value) {
  requireFact(
    typeof value === "string" &&
      value.length > 0 &&
      value.length <= 4096 &&
      !/[\\\x00-\x1f\x7f?#]/.test(value) &&
      !isAbsolute(value) &&
      value.split("/").every((part) => part && part !== "." && part !== ".."),
    "unsafe relative path",
  );
  return value;
}
/** @param {string} root @param {string} file */
function localPath(root, file) {
  const path = relative(root, file).split(sep).join("/");
  return safePath(path);
}
/** @param {string} root @param {string} file */
function readLocal(root, file) {
  localPath(root, file);
  localPath(root, realpathSync(file));
  const metadata = statSync(file);
  requireFact(metadata.isFile() && metadata.size <= MAX_FILE, "invalid or oversized input file");
  const bytes = readFileSync(file);
  requireFact(bytes.length <= MAX_FILE, "input grew beyond bounds");
  return bytes;
}
/** @param {string} root @param {string} file */
function recordJson(root, file) {
  const bytes = readLocal(root, file);
  const value = JSON.parse(bytes.toString("utf8"));
  requireFact(value && typeof value === "object" && !Array.isArray(value), "invalid JSON record");
  return { bytes, value };
}
/** @param {string} path */
function packageRoot(path) {
  const parts = path.split("/");
  const index = parts.lastIndexOf("node_modules");
  if (index < 0) return null;
  const scope = parts[index + 1];
  requireFact(scope && !scope.startsWith("."), "invalid installed package path");
  const end = index + (scope.startsWith("@") ? 3 : 2);
  requireFact(parts.length > end, "missing package module path");
  const name = parts.slice(index + 1, end).join("/");
  requireFact(/^(@[a-zA-Z0-9_.-]+\/)?[a-zA-Z0-9_.-]+$/.test(name), "invalid package name");
  return { name, path: parts.slice(0, end).join("/") };
}

/** @param {{root?: string, version?: string, revision?: string}} [options]
 * @returns {import('vite').Plugin}
 */
export default function browserModuleInventory({
  root: requestedRoot,
  version = "dev",
  revision = "main",
} = {}) {
  requireFact(
    version === "dev" || /^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version),
    "invalid source version",
  );
  requireFact(revision === "main" || /^[0-9a-f]{40}$/.test(revision), "invalid source revision");
  let enabled = false;
  let root = "";
  /** @type {LockFile} */
  let lock;
  /** @type {Buffer} */
  let lockBytes;
  /** @type {Map<string,InputRecord>} */
  const inputs = new Map();
  /** @type {Map<string,PackageRecord>} */
  const packages = new Map();

  /** @param {string} path @returns {PackageRecord|null} */
  function installedPackage(path) {
    const location = packageRoot(path);
    if (!location) return null;
    const cached = packages.get(location.path);
    if (cached) return cached;
    const item = lock.packages[location.path];
    requireFact(
      item &&
        typeof item === "object" &&
        !item.link &&
        typeof item.version === "string" &&
        /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$/.test(
          item.version,
        ),
      "installed package is absent from the lockfile",
    );
    requireFact(
      /^sha512-[A-Za-z0-9+/]{86}==$/.test(item.integrity ?? ""),
      "missing or invalid locked package integrity",
    );
    const integrityBytes = Buffer.from(item.integrity.slice(7), "base64");
    requireFact(
      integrityBytes.length === 64 &&
        "sha512-" + integrityBytes.toString("base64") === item.integrity,
      "noncanonical locked integrity",
    );
    const registry = new URL(item.resolved);
    const expectedTarball =
      "/" + location.name + "/-/" + location.name.split("/").at(-1) + "-" + item.version + ".tgz";
    requireFact(
      registry.protocol === "https:" &&
        registry.hostname === "registry.npmjs.org" &&
        !registry.port &&
        !registry.username &&
        !registry.password &&
        !registry.search &&
        !registry.hash &&
        registry.pathname === expectedTarball,
      "unsupported locked package registry",
    );
    const installed = recordJson(root, resolve(root, location.path, "package.json"));
    requireFact(
      installed.value.name === location.name && installed.value.version === item.version,
      "installed package name/version differs from lockfile",
    );
    const result = {
      name: location.name,
      version: item.version,
      integrity: item.integrity,
      lock_path: location.path,
      package_json_sha256: digest(installed.bytes),
    };
    packages.set(location.path, result);
    return result;
  }

  /** @param {string} id @returns {InputRecord} */
  function input(id) {
    const cached = inputs.get(id);
    if (cached) return cached;
    requireFact(
      typeof id === "string" && id.length > 0 && id.length <= 16384,
      "invalid module identifier",
    );
    /** @type {InputRecord} */
    let result;
    if (id.startsWith("\0") || id.startsWith("virtual:")) {
      // The description stays opaque. Normalize the build root before hashing;
      // never publish virtual IDs, query strings, environment contents or code.
      const canonical = id.split(root).join("<root>");
      result = {
        id: "virtual:" + digest(canonical).slice(7),
        kind: "virtual",
        module_path: null,
        source_sha256: null,
        package: null,
        transform_input_sha256: null,
        rollup_input_sha256: null,
      };
    } else {
      requireFact(
        !id.includes("\0") && !/[\\\x00-\x1f\x7f]/.test(id),
        "invalid physical module identifier",
      );
      const separator = id.search(/[?#]/);
      const file = separator < 0 ? id : id.slice(0, separator);
      const variant = separator < 0 ? "" : ":variant:" + digest(id.slice(separator)).slice(7);
      requireFact(isAbsolute(file), "unresolved physical module identifier");
      // Reject traversal even when normalization would happen to stay in root.
      requireFact(!file.split("/").includes(".."), "module path traversal");
      const path = localPath(root, file);
      const bytes = readLocal(root, file);
      const pkg = installedPackage(path);
      result = {
        id: "file:" + path + variant,
        kind: pkg
          ? "package-source"
          : path.startsWith(".svelte-kit/")
            ? "generated-application"
            : "application-source",
        module_path: path,
        source_sha256: digest(bytes),
        package: pkg,
        transform_input_sha256: null,
        rollup_input_sha256: null,
      };
    }
    inputs.set(id, result);
    requireFact(inputs.size <= MAX_MODULES, "module count exceeds bounds");
    return result;
  }

  return {
    name: "psst-browser-module-inventory",
    apply: "build",
    enforce: "pre",
    configResolved(config) {
      enabled = !config.build.ssr;
      root = realpathSync(resolve(requestedRoot ?? config.root));
    },
    buildStart() {
      inputs.clear();
      packages.clear();
      if (!enabled) return;
      const committed = recordJson(root, resolve(root, "package-lock.json"));
      lock = committed.value;
      lockBytes = committed.bytes;
      requireFact(
        lock.lockfileVersion === 3 &&
          lock.packages &&
          typeof lock.packages === "object" &&
          !Array.isArray(lock.packages),
        "unsupported package lock",
      );
    },
    transform(code, id) {
      if (enabled) input(id).transform_input_sha256 = digest(code);
      return null;
    },
    moduleParsed(info) {
      if (enabled && !info.isExternal) {
        const row = input(info.id);
        if (typeof info.code === "string") row.rollup_input_sha256 = digest(info.code);
      }
    },
    generateBundle: {
      order: "post",
      handler(_options, bundle) {
        if (!enabled) return;
        requireFact(!Object.hasOwn(bundle, INVENTORY), "inventory output already exists");
        /** @type {Map<string,Array<{file:string,rendered_length:number}>>} */
        const included = new Map();
        const mentioned = new Set();
        const outputs = [];
        const buildMetadataOutputs = [];
        for (const [key, output] of Object.entries(bundle).sort(([a], [b]) =>
          a < b ? -1 : a > b ? 1 : 0,
        )) {
          safePath(key);
          requireFact(output.fileName === key, "output key/filename differs");
          const bytes =
            output.type === "chunk"
              ? Buffer.from(output.code)
              : typeof output.source === "string"
                ? Buffer.from(output.source)
                : Buffer.from(output.source);
          requireFact(bytes.length <= MAX_FILE, "oversized client output");
          const outputFact = {
            file: key,
            type: output.type,
            sha256: digest(bytes),
            size: bytes.length,
          };
          if (BUILD_METADATA.has(key)) {
            requireFact(output.type === "asset", "build metadata must be an asset");
            buildMetadataOutputs.push(outputFact);
          } else outputs.push(outputFact);
          if (output.type !== "chunk") continue;
          for (const [id, module] of Object.entries(output.modules)) {
            requireFact(
              Number.isSafeInteger(module.renderedLength) && module.renderedLength >= 0,
              "invalid rendered module length",
            );
            input(id);
            mentioned.add(id);
            if (module.renderedLength > 0) {
              const contributions = included.get(id) ?? [];
              included.set(id, contributions);
              contributions.push({ file: key, rendered_length: module.renderedLength });
            }
          }
        }
        // getModuleIds also contains parsed modules completely tree-shaken out
        // of every chunk. External imports are not bundled source inputs.
        for (const id of this.getModuleIds()) {
          if (!this.getModuleInfo(id)?.isExternal) input(id);
        }
        requireFact(
          digest(readLocal(root, resolve(root, "package-lock.json"))) === digest(lockBytes),
          "package lock changed during build",
        );
        for (const item of packages.values()) {
          requireFact(
            digest(readLocal(root, resolve(root, item.lock_path, "package.json"))) ===
              item.package_json_sha256,
            "installed package manifest changed during build",
          );
        }
        const modules = [],
          excluded = [];
        const unique = new Set();
        for (const [id, item] of inputs) {
          requireFact(!unique.has(item.id), "module identifier collision");
          unique.add(item.id);
          if (item.module_path !== null) {
            requireFact(
              digest(readLocal(root, resolve(root, item.module_path))) === item.source_sha256,
              "module source changed during build",
            );
          }
          const rendered = included.get(id) ?? [];
          const row = { ...item, rendered_in: rendered };
          if (rendered.length) modules.push(row);
          else
            excluded.push({
              ...row,
              reason: mentioned.has(id) ? "zero-rendered-length" : "not-in-client-chunks",
            });
        }
        /** @param {{id:string}} a @param {{id:string}} b */
        const byId = (a, b) => (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
        const inventory = {
          schema_version: 1,
          kind: "browser-module-inventory",
          source: {
            version,
            revision,
            package_lock: { file: "package-lock.json", sha256: digest(lockBytes) },
          },
          execution: "vite-rollup-client-build",
          outputs,
          build_metadata_outputs: buildMetadataOutputs,
          modules: modules.sort(byId),
          excluded_modules: excluded.sort(byId),
          browser_module_closure_verified: false,
          source_reproduction_verified: false,
          publication_authorized: false,
        };
        const serialized = JSON.stringify(inventory, null, 2) + "\n";
        requireFact(Buffer.byteLength(serialized) <= MAX_FILE, "inventory exceeds bounds");
        this.emitFile({ type: "asset", fileName: INVENTORY, source: serialized });
      },
    },
  };
}
