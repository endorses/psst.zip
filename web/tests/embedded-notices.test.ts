import assert from "node:assert/strict";
import test from "node:test";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, readFileSync, writeFileSync, rmSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { generateNotices } from "../scripts/generate-third-party-notices.mjs";

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const hash = (value: Buffer | string) => createHash("sha256").update(value).digest("hex");
function fixture() {
  const root = mkdtempSync(path.join(tmpdir(), "psst-embedded-notices-"));
  function write(file: string, value: Buffer | string | object) {
    const target = path.join(root, file);
    mkdirSync(path.dirname(target), { recursive: true });
    writeFileSync(
      target,
      typeof value === "string" || Buffer.isBuffer(value) ? value : JSON.stringify(value),
    );
  }
  const catalog = JSON.parse(
    readFileSync(path.join(web, "licenses/embedded-components.json"), "utf8"),
  );
  const component = catalog.components[0];
  const parent = component.parent;
  const lock = {
    packages: {
      [parent.path]: { version: parent.version, integrity: parent.integrity, license: "MIT" },
    },
  };
  write("package-lock.json", lock);
  write("licenses/embedded-components.json", catalog);
  for (const file of [
    component.license_file,
    ...[
      "package.json",
      "LICENSE",
      component.attribution_source.file,
      ...Object.keys(component.compiled_inputs),
    ].map((file) => `${parent.path}/${file}`),
  ])
    write(file, readFileSync(path.join(web, file)));
  mkdirSync(path.join(root, "static/licenses"), { recursive: true });
  return {
    root,
    write,
    catalog,
    component,
    lock,
    cleanup: () => rmSync(root, { recursive: true, force: true }),
  };
}

test("embedded decoder notice preserves original and normalized license hashes separately", () => {
  const f = fixture();
  try {
    assert.deepEqual(generateNotices(f.root), { packages: 1, embedded_components: 1 });
    assert.deepEqual(generateNotices(f.root, { check: true }), {
      packages: 1,
      embedded_components: 1,
    });
    const inventory = JSON.parse(
      readFileSync(path.join(f.root, "static/licenses/dependency-inventory.json"), "utf8"),
    );
    assert.equal(inventory.packages.length, 1);
    const embedded = inventory.embedded_components[0];
    assert.equal(embedded.name, "jsqr-es6");
    assert.equal(embedded.parent.name, "qr-scanner");
    assert.equal(embedded.source_reproduction_verified, false);
    assert.equal(embedded.publication_authorized, false);
    const original = readFileSync(path.join(f.root, f.component.license_file));
    assert.equal(
      hash(original),
      "c6596eb7be8581c18be736c846fb9173b69eccf6ef94c5135893ec56bd92ba08",
    );
    const normalized = original
      .toString("utf8")
      .replace(/\r\n?/g, "\n")
      .split("\n")
      .map((line) => line.replace(/[ \t]+$/g, ""))
      .join("\n")
      .replace(/\n*$/, "\n");
    assert.equal(embedded.notices[0].upstream_sha256, hash(original));
    assert.equal(embedded.notices[0].sha256, hash(normalized));
    const notices = readFileSync(
      path.join(f.root, "static/licenses/THIRD_PARTY_NOTICES.txt"),
      "utf8",
    );
    assert.ok(notices.includes("embedded within qr-scanner@1.4.2"));
    assert.ok(notices.includes(normalized));
    f.write("static/licenses/THIRD_PARTY_NOTICES.txt", notices + "stale\n");
    assert.throws(() => generateNotices(f.root, { check: true }), /Stale notices/);
  } finally {
    f.cleanup();
  }
});

test("reviewed parent lock, installed version, compiled bytes and original license changes fail closed", () => {
  const mutations = [
    (f: ReturnType<typeof fixture>) => {
      f.lock.packages[f.component.parent.path].version = "1.4.3";
      f.write("package-lock.json", f.lock);
      f.write(`${f.component.parent.path}/package.json`, { name: "qr-scanner", version: "1.4.3" });
    },
    (f: ReturnType<typeof fixture>) => {
      f.lock.packages[f.component.parent.path].integrity =
        "sha512-" + Buffer.alloc(64, 2).toString("base64");
      f.write("package-lock.json", f.lock);
    },
    (f: ReturnType<typeof fixture>) =>
      f.write(`${f.component.parent.path}/package.json`, { name: "qr-scanner", version: "1.4.3" }),
    (f: ReturnType<typeof fixture>) =>
      f.write(`${f.component.parent.path}/qr-scanner-worker.min.js`, "changed compiled decoder"),
    (f: ReturnType<typeof fixture>) =>
      f.write(f.component.license_file, "replaced original license"),
    (f: ReturnType<typeof fixture>) =>
      f.write(`${f.component.parent.path}/README.md`, "changed attribution"),
  ];
  for (const mutate of mutations) {
    const f = fixture();
    try {
      mutate(f);
      assert.throws(() => generateNotices(f.root), /Embedded component|Installed version/);
      assert.throws(
        () => readFileSync(path.join(f.root, "static/licenses/dependency-inventory.json")),
        /ENOENT/,
      );
    } finally {
      f.cleanup();
    }
  }
});

test("embedded license paths cannot escape or follow symlinks", () => {
  for (const mode of ["escape", "symlink"]) {
    const f = fixture();
    try {
      if (mode === "escape") f.component.license_file = "../outside/LICENSE";
      else {
        const original = path.join(f.root, f.component.license_file);
        rmSync(original);
        symlinkSync(path.join(web, f.component.license_file), original);
      }
      f.write("licenses/embedded-components.json", f.catalog);
      assert.throws(() => generateNotices(f.root), /Unsafe embedded|regular file/);
    } finally {
      f.cleanup();
    }
  }
});
