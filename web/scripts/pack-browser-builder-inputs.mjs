/** Select regular observed browser inputs from the actual builder; never run package scripts. */
import { closeSync, lstatSync, openSync, readFileSync, readdirSync, writeSync } from "node:fs";
import { dirname } from "node:path";

const root = "/build";
const maximum = 32 * 1024 * 1024;
const fixed = [
  "package.json",
  "package-lock.json",
  "vite.config.ts",
  "svelte.config.js",
  "tsconfig.json",
  "scripts/browser-module-inventory.mjs",
];
function check(condition) {
  if (!condition) throw new Error("Invalid browser builder input");
}
function safe(name) {
  check(typeof name === "string" && name.length <= 4096 && !/[\\\x00-\x1f\x7f?#]/.test(name));
  check(name.split("/").every((part) => part && part !== "." && part !== ".."));
  return name;
}
function regular(name) {
  safe(name);
  let path = root + "/" + name;
  check(lstatSync(path).isFile() && lstatSync(path).size <= maximum);
  for (path = dirname(path); path !== "/"; path = dirname(path))
    check(lstatSync(path).isDirectory());
  const bytes = readFileSync(root + "/" + name);
  check(bytes.length <= maximum);
  return bytes;
}
const inventory = JSON.parse(regular("build/licenses/browser-module-inventory.json"));
const selected = new Set(fixed);
check(Array.isArray(inventory.modules) && Array.isArray(inventory.excluded_modules));
check(inventory.modules.length + inventory.excluded_modules.length <= 25000);
for (const row of [...inventory.modules, ...inventory.excluded_modules]) {
  if (row.module_path === null) continue;
  const name = safe(row.module_path);
  check(
    name.startsWith("src/") ||
      name.startsWith("static/") ||
      name.startsWith("node_modules/") ||
      name.startsWith(".svelte-kit/") ||
      fixed.includes(name),
  );
  check(
    !name
      .split("/")
      .some((part) => part.startsWith(".") && ![".svelte-kit", ".vite"].includes(part)),
  );
  selected.add(name);
  if (row.package) {
    const location = safe(row.package.lock_path);
    check(
      location.startsWith("node_modules/") &&
        !location.split("/").some((part) => part.startsWith(".")),
    );
    selected.add(location + "/package.json");
  }
}
for (const row of inventory.build_metadata_outputs) {
  check([".vite/manifest.json", ".vite/ssr-manifest.json"].includes(row.file));
  selected.add(".svelte-kit/output/client/" + row.file);
}
function walk(name) {
  check(lstatSync(root + "/" + name).isDirectory());
  for (const entry of readdirSync(root + "/" + name, { withFileTypes: true })) {
    const path = safe(name + "/" + entry.name);
    if (entry.isDirectory()) walk(path);
    else {
      check(entry.isFile());
      selected.add(path);
    }
    check(selected.size <= 50000);
  }
}
walk("build");
const descriptor = openSync("/tmp/psst-browser-builder.tar", "wx", 0o600);
let total = 0;
try {
  for (const name of [...selected].sort()) {
    const bytes = regular(name);
    total += bytes.length;
    check(total <= 512 * 1024 * 1024);
    const header = Buffer.alloc(512);
    let file = name,
      prefix = "";
    if (Buffer.byteLength(file) > 100) {
      const index = name.lastIndexOf("/");
      prefix = name.slice(0, index);
      file = name.slice(index + 1);
    }
    check(Buffer.byteLength(file) <= 100 && Buffer.byteLength(prefix) <= 155);
    header.write(file, 0, 100);
    header.write("0000600\0", 100, 8);
    header.write("0000000\0", 108, 8);
    header.write("0000000\0", 116, 8);
    header.write(bytes.length.toString(8).padStart(11, "0") + "\0", 124, 12);
    header.write("00000000000\0", 136, 12);
    header.fill(32, 148, 156);
    header[156] = 48;
    header.write("ustar\0", 257, 6);
    header.write("00", 263, 2);
    header.write(prefix, 345, 155);
    const sum = header.reduce((a, b) => a + b, 0);
    header.write(sum.toString(8).padStart(6, "0") + "\0 ", 148, 8);
    writeSync(descriptor, header);
    writeSync(descriptor, bytes);
    writeSync(descriptor, Buffer.alloc((512 - (bytes.length % 512)) % 512));
  }
  writeSync(descriptor, Buffer.alloc(1024));
} finally {
  closeSync(descriptor);
}
