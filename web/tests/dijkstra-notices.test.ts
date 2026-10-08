import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import {
  cpSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { generateNotices } from "../scripts/generate-third-party-notices.mjs";

const web = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const digest = (bytes: Buffer) => createHash("sha256").update(bytes).digest("hex");

function fixture() {
  const root = mkdtempSync(path.join(tmpdir(), "psst-dijkstra-notices-"));
  const reviewed = JSON.parse(
    readFileSync(path.join(web, "licenses/dijkstrajs-1.0.3-supplement.json"), "utf8"),
  );
  const embedded = JSON.parse(
    readFileSync(path.join(web, "licenses/embedded-components.json"), "utf8"),
  ).components[0];
  const lock = {
    packages: {
      "node_modules/dijkstrajs": {
        version: reviewed.version as string,
        integrity: reviewed.integrity as string,
        license: "MIT",
      },
      [embedded.parent.path]: {
        version: embedded.parent.version as string,
        integrity: embedded.parent.integrity as string,
        license: "MIT",
      },
    },
  };
  function write(file: string, value: string | object) {
    writeFileSync(path.join(root, file), typeof value === "string" ? value : JSON.stringify(value));
  }
  for (const file of [
    "licenses/embedded-components.json",
    embedded.license_file,
    "licenses/dijkstrajs-1.0.3-supplement.json",
    reviewed.supplement.file,
    "node_modules/dijkstrajs/package.json",
    "node_modules/dijkstrajs/LICENSE.md",
    ...[
      "package.json",
      "LICENSE",
      embedded.attribution_source.file,
      ...Object.keys(embedded.compiled_inputs),
    ].map((file) => `${embedded.parent.path}/${file}`),
  ]) {
    mkdirSync(path.dirname(path.join(root, file)), { recursive: true });
    cpSync(path.join(web, file), path.join(root, file));
  }
  mkdirSync(path.join(root, "static/licenses"), { recursive: true });
  write("package-lock.json", lock);
  return {
    root,
    lock,
    reviewed,
    write,
    cleanup: () => rmSync(root, { recursive: true, force: true }),
  };
}

test("dijkstra notice retains Wyatt's upstream text and adds identified full MIT terms", () => {
  const f = fixture();
  try {
    generateNotices(f.root);
    generateNotices(f.root, { check: true });
    const notices = readFileSync(
      path.join(f.root, "static/licenses/THIRD_PARTY_NOTICES.txt"),
      "utf8",
    );
    const upstream = readFileSync(path.join(f.root, "node_modules/dijkstrajs/LICENSE.md"));
    const supplement = readFileSync(path.join(f.root, f.reviewed.supplement.file));
    assert.ok(notices.includes(upstream.toString()));
    assert.ok(notices.includes("supplementary full MIT terms"));
    assert.ok(notices.includes("Permission is hereby granted, free of charge"));
    assert.ok(notices.includes("This supplement was not present in the npm upstream notice."));
    const record = JSON.parse(
      readFileSync(path.join(f.root, "static/licenses/dependency-inventory.json"), "utf8"),
    ).packages.find((item: { name: string }) => item.name === "dijkstrajs");
    assert.equal(record.notices[0].upstream_sha256, digest(upstream));
    assert.equal(record.supplementary_license.supplement.sha256, digest(supplement));
    assert.equal(
      record.supplementary_license.integrity,
      f.lock.packages["node_modules/dijkstrajs"].integrity,
    );
  } finally {
    f.cleanup();
  }
});

test("changed dijkstra version, SRI, original notice or supplement fails before overwriting outputs", () => {
  const mutations = [
    (f: ReturnType<typeof fixture>) => {
      f.lock.packages["node_modules/dijkstrajs"].version = "1.0.4";
      f.write("package-lock.json", f.lock);
      f.write("node_modules/dijkstrajs/package.json", { name: "dijkstrajs", version: "1.0.4" });
    },
    (f: ReturnType<typeof fixture>) => {
      f.lock.packages["node_modules/dijkstrajs"].integrity =
        "sha512-" + Buffer.alloc(64, 1).toString("base64");
      f.write("package-lock.json", f.lock);
    },
    (f: ReturnType<typeof fixture>) =>
      f.write("node_modules/dijkstrajs/LICENSE.md", "changed original notice"),
    (f: ReturnType<typeof fixture>) =>
      f.write(f.reviewed.supplement.file, "changed supplementary terms"),
  ];
  for (const mutate of mutations) {
    const f = fixture();
    try {
      for (const file of ["dependency-inventory.json", "THIRD_PARTY_NOTICES.txt"])
        f.write(`static/licenses/${file}`, "existing output");
      mutate(f);
      assert.throws(() => generateNotices(f.root), /dijkstrajs/);
      for (const file of ["dependency-inventory.json", "THIRD_PARTY_NOTICES.txt"])
        assert.equal(
          readFileSync(path.join(f.root, "static/licenses", file), "utf8"),
          "existing output",
        );
    } finally {
      f.cleanup();
    }
  }
});

test("supplement paths reject traversal and upstream, catalog or terms symlinks", () => {
  for (const file of [
    "../outside/MIT-terms.txt",
    "node_modules/dijkstrajs/LICENSE.md",
    "licenses/dijkstrajs-1.0.3-supplement.json",
    "licenses/dijkstrajs-1.0.3/MIT-terms.txt",
  ]) {
    const f = fixture();
    try {
      if (file.startsWith("../")) {
        f.reviewed.supplement.file = file;
        f.write("licenses/dijkstrajs-1.0.3-supplement.json", f.reviewed);
      } else {
        rmSync(path.join(f.root, file));
        symlinkSync(path.join(web, file), path.join(f.root, file));
      }
      assert.throws(
        () => generateNotices(f.root),
        /Invalid reviewed dijkstrajs|regular file|No complete upstream license text/,
      );
      assert.throws(
        () => readFileSync(path.join(f.root, "static/licenses/dependency-inventory.json")),
        /ENOENT/,
      );
    } finally {
      f.cleanup();
    }
  }
});
