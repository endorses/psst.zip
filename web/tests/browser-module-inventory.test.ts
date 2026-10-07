import test from "node:test";
import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { rollup } from "rollup";
import browserModuleInventory from "../scripts/browser-module-inventory.mjs";

const sha = (bytes: string | Buffer) =>
  "sha256:" + createHash("sha256").update(bytes).digest("hex");
const integrity = "sha512-" + Buffer.alloc(64, 1).toString("base64");
const revision = "a".repeat(40);
function fixture() {
  const root = mkdtempSync(join(tmpdir(), "psst-browser-inventory-fixture-"));
  function write(name: string, value: string | object) {
    const file = join(root, name);
    mkdirSync(join(file, ".."), { recursive: true });
    writeFileSync(file, typeof value === "string" ? value : JSON.stringify(value));
    return file;
  }
  const lock: any = { lockfileVersion: 3, packages: { "": { name: "web", version: "0.0.1" } } };
  function pkg(path: string, name: string, version = "1.2.3") {
    lock.packages[path] = {
      version,
      integrity,
      resolved: `https://registry.npmjs.org/${name}/-/${name.split("/").pop()}-${version}.tgz`,
    };
    write(path + "/package.json", { name, version });
    const file = write(path + "/index.js", "export const value = 3;\n");
    write("package-lock.json", lock);
    return file;
  }
  write("package-lock.json", lock);
  const app = write("src/main.js", "export const app = 1;\n");
  return {
    root,
    write,
    pkg,
    lock,
    app,
    cleanup: () => rmSync(root, { recursive: true, force: true }),
  };
}
function harness(f: ReturnType<typeof fixture>, ids: string[], ssr = false) {
  const plugin: any = browserModuleInventory({ root: f.root, version: "v1.2.3", revision });
  plugin.configResolved({ root: f.root, build: { ssr } });
  plugin.buildStart();
  const infos = new Map(ids.map((id) => [id, { id, code: "compiled", isExternal: false }]));
  const emitted: any[] = [];
  const context = {
    getModuleIds: () => infos.keys(),
    getModuleInfo: (id: string) => infos.get(id),
    emitFile: (value: any) => emitted.push(value),
  };
  for (const id of ids) {
    plugin.transform("transform input", id);
    plugin.moduleParsed(infos.get(id));
  }
  function generate(
    modules: Record<string, { renderedLength: number }>,
    reverse = false,
    extras: Record<string, any> = {},
  ) {
    const chunk = { type: "chunk", fileName: "_app/main.js", code: "const bundled=1;", modules };
    const css = { type: "asset", fileName: "_app/style.css", source: new Uint8Array([97, 98]) };
    const bundle = reverse
      ? { "_app/style.css": css, "_app/main.js": chunk }
      : { "_app/main.js": chunk, "_app/style.css": css };
    plugin.generateBundle.handler.call(context, {}, { ...bundle, ...extras });
    return emitted.length ? JSON.parse(emitted.at(-1).source) : undefined;
  }
  return { plugin, infos, emitted, generate };
}

test("fixture records rendered client inputs and original source hashes, excluding tree-shaken modules", () => {
  const f = fixture();
  try {
    const pkg = f.pkg("node_modules/@scope/example", "@scope/example");
    const generated = f.write(".svelte-kit/generated/client.js", "generated app");
    const unused = f.write("src/unused.js", "unused export");
    const zero = f.write("src/zero.js", "empty module");
    const h = harness(f, [f.app, pkg, generated, unused, zero]);
    const value = h.generate({
      [f.app]: { renderedLength: 5 },
      [pkg]: { renderedLength: 8 },
      [generated]: { renderedLength: 3 },
      [zero]: { renderedLength: 0 },
    });
    assert.equal(
      value.source.package_lock.sha256,
      sha(readFileSync(join(f.root, "package-lock.json"))),
    );
    assert.equal(value.source.version, "v1.2.3");
    assert.equal(value.source.revision, revision);
    assert.equal(value.execution, "vite-rollup-client-build");
    assert.equal(value.modules.length, 3);
    const module = value.modules.find((row: any) => row.kind === "package-source");
    assert.deepEqual(module.package, {
      name: "@scope/example",
      version: "1.2.3",
      integrity,
      lock_path: "node_modules/@scope/example",
      package_json_sha256: sha(
        readFileSync(join(f.root, "node_modules/@scope/example/package.json")),
      ),
    });
    assert.equal(module.source_sha256, sha(readFileSync(pkg)));
    assert.equal(module.transform_input_sha256, sha("transform input"));
    assert.equal(module.rollup_input_sha256, sha("compiled"));
    assert.deepEqual(module.rendered_in, [{ file: "_app/main.js", rendered_length: 8 }]);
    assert.equal(
      value.modules.find((row: any) => row.module_path.startsWith(".svelte-kit")).kind,
      "generated-application",
    );
    assert.deepEqual(value.excluded_modules.map((row: any) => row.reason).sort(), [
      "not-in-client-chunks",
      "zero-rendered-length",
    ]);
    assert.equal(value.outputs[0].sha256, sha("const bundled=1;"));
    assert.equal(value.outputs[1].sha256, sha(Buffer.from([97, 98])));
    assert.ok(
      value.outputs.every((row: any) => row.file !== "licenses/browser-module-inventory.json"),
    );
    assert.equal(value.publication_authorized, false);
    assert.equal(value.source_reproduction_verified, false);
    assert.equal(value.browser_module_closure_verified, false);
    assert.ok(!JSON.stringify(value).includes(f.root));
  } finally {
    f.cleanup();
  }
});

test("nested scoped package lookup uses the closest node_modules lock entry, not inner type-only manifests", () => {
  const f = fixture();
  try {
    f.pkg("node_modules/parent/node_modules/@scope/child", "@scope/child", "2.0.0");
    f.write("node_modules/parent/node_modules/@scope/child/src/package.json", { type: "module" });
    const child = f.write("node_modules/parent/node_modules/@scope/child/src/child.js", "child");
    const value = harness(f, [child]).generate({ [child]: { renderedLength: 4 } });
    assert.equal(
      value.modules[0].package.lock_path,
      "node_modules/parent/node_modules/@scope/child",
    );
    assert.equal(value.modules[0].package.version, "2.0.0");
  } finally {
    f.cleanup();
  }
});

test("opaque virtual and query module identities do not publish private descriptions and are root relocatable", () => {
  const left = fixture(),
    right = fixture();
  try {
    const run = (f: ReturnType<typeof fixture>, reverse: boolean) => {
      const virtual = "\0" + f.root + "/generated/private?token=operator-value";
      const query = f.app + "?svelte&type=script&token=operator-value";
      return harness(f, [virtual, query]).generate(
        { [virtual]: { renderedLength: 2 }, [query]: { renderedLength: 1 } },
        reverse,
      );
    };
    const first = run(left, false),
      second = run(right, true);
    assert.deepEqual(first, second);
    assert.ok(!JSON.stringify(first).includes("operator-value"));
    assert.ok(!JSON.stringify(first).includes("token"));
    assert.equal(first.modules.find((row: any) => row.kind === "virtual").module_path, null);
    assert.ok(
      first.modules.find((row: any) => row.kind === "application-source").id.includes(":variant:"),
    );
  } finally {
    left.cleanup();
    right.cleanup();
  }
});

test("SSR builds emit nothing and never read browser lock or module inputs", () => {
  const f = fixture();
  try {
    rmSync(join(f.root, "package-lock.json"));
    const h = harness(f, ["/outside/ssr/private.js"], true);
    assert.equal(h.generate({ "/outside/ssr/private.js": { renderedLength: 50 } }), undefined);
    assert.deepEqual(h.emitted, []);
  } finally {
    f.cleanup();
  }
});

test("missing locked package, stale name/version, foreign registry, absent integrity and linked packages fail closed", () => {
  for (const mode of ["missing", "version", "name", "registry", "integrity", "link"] as const) {
    const f = fixture();
    try {
      const pkg = f.pkg("node_modules/example", "example");
      if (mode === "missing") delete f.lock.packages["node_modules/example"];
      if (mode === "version")
        f.write("node_modules/example/package.json", { name: "example", version: "9.0.0" });
      if (mode === "name")
        f.write("node_modules/example/package.json", { name: "other", version: "1.2.3" });
      if (mode === "registry")
        f.lock.packages["node_modules/example"].resolved = "https://foreign.example/example.tgz";
      if (mode === "integrity") delete f.lock.packages["node_modules/example"].integrity;
      if (mode === "link") f.lock.packages["node_modules/example"].link = true;
      f.write("package-lock.json", f.lock);
      assert.throws(() => harness(f, [pkg]), /Browser inventory:/, mode);
    } finally {
      f.cleanup();
    }
  }
});

test("physical outside-root paths, traversal, null-byte misuse and symlink escapes are rejected", () => {
  const f = fixture(),
    outside = fixture();
  try {
    for (const id of [
      outside.app,
      f.root + "/src/../src/main.js",
      f.app + "\0malformed",
      "relative.js",
    ])
      assert.throws(() => harness(f, [id]), /Browser inventory:/);
    const link = join(f.root, "src/linked.js");
    symlinkSync(outside.app, link);
    assert.throws(() => harness(f, [link]), /Browser inventory:/);
    for (const version of ["bad/version", "v1.2.3\nsecret"])
      assert.throws(() => browserModuleInventory({ version }), /invalid source version/);
    assert.throws(
      () => browserModuleInventory({ revision: "private/path" }),
      /invalid source revision/,
    );
  } finally {
    f.cleanup();
    outside.cleanup();
  }
});

test("mid-build source, lockfile and installed manifest mutations cannot receive stale hashes", () => {
  for (const mode of ["source", "lock", "package"] as const) {
    const f = fixture();
    try {
      const pkg = f.pkg("node_modules/example", "example");
      const h = harness(f, [f.app, pkg]);
      if (mode === "source") f.write("src/main.js", "changed");
      if (mode === "lock") f.write("package-lock.json", { ...f.lock, changed: true });
      if (mode === "package")
        f.write("node_modules/example/package.json", {
          name: "example",
          version: "1.2.3",
          changed: true,
        });
      assert.throws(
        () => h.generate({ [f.app]: { renderedLength: 1 }, [pkg]: { renderedLength: 1 } }),
        /changed during build/,
      );
    } finally {
      f.cleanup();
    }
  }
});

test("invalid output paths and rendered-length claims fail, external modules are not falsely recorded", () => {
  const f = fixture();
  try {
    assert.throws(
      () => harness(f, [f.app]).generate({ [f.app]: { renderedLength: -1 } }),
      /invalid rendered module length/,
    );
    const h = harness(f, [f.app]);
    h.infos.set("node:fs", { id: "node:fs", code: null as any, isExternal: true });
    assert.equal(h.generate({ [f.app]: { renderedLength: 2 } }).modules.length, 1);
    assert.throws(
      () =>
        h.plugin.generateBundle.handler.call(
          { getModuleIds: () => [], emitFile() {} },
          {},
          { "../escaped.js": { type: "chunk", fileName: "../escaped.js", code: "", modules: {} } },
        ),
      /unsafe relative path/,
    );
  } finally {
    f.cleanup();
  }
});

test("actual local Rollup build binds real rendered module and output bytes without browser execution", async () => {
  const f = fixture();
  let build;
  try {
    f.pkg("node_modules/@scope/example", "@scope/example");
    f.write(
      "src/main.js",
      'import {value} from "../node_modules/@scope/example/index.js"; export const answer=value+1;\n',
    );
    const plugin: any = browserModuleInventory({ root: f.root });
    plugin.configResolved({ root: f.root, build: { ssr: false } });
    build = await rollup({ input: f.app, plugins: [plugin] });
    const { output } = await build.generate({ format: "es", entryFileNames: "_app/main.js" });
    const asset: any = output.find(
      (row) => row.fileName === "licenses/browser-module-inventory.json",
    );
    const inventory = JSON.parse(asset.source);
    assert.equal(inventory.modules.length, 2);
    assert.equal(inventory.modules.filter((row: any) => row.kind === "package-source").length, 1);
    const chunk: any = output.find((row) => row.type === "chunk");
    assert.equal(inventory.outputs[0].sha256, sha(chunk.code));
    assert.equal(inventory.outputs[0].size, Buffer.byteLength(chunk.code));
    assert.ok(
      inventory.modules.every(
        (row: any) => row.rollup_input_sha256 && row.rendered_in[0].rendered_length > 0,
      ),
    );
  } finally {
    if (build) await build.close();
    f.cleanup();
  }
});

test("only exact adapter-dropped Vite manifests become build metadata; other Vite assets retain final hashes", () => {
  const f = fixture();
  try {
    const value = harness(f, [f.app]).generate({ [f.app]: { renderedLength: 1 } }, false, {
      ".vite/manifest.json": {
        type: "asset",
        fileName: ".vite/manifest.json",
        source: "manifest bytes",
      },
      ".vite/ssr-manifest.json": {
        type: "asset",
        fileName: ".vite/ssr-manifest.json",
        source: "SSR mapping bytes",
      },
      ".vite/another.js": { type: "asset", fileName: ".vite/another.js", source: "runtime bytes" },
    });
    assert.deepEqual(
      value.build_metadata_outputs.map((row: any) => row.file),
      [".vite/manifest.json", ".vite/ssr-manifest.json"],
    );
    assert.equal(value.build_metadata_outputs[0].sha256, sha("manifest bytes"));
    assert.ok(!value.outputs.some((row: any) => row.file === ".vite/manifest.json"));
    assert.equal(
      value.outputs.find((row: any) => row.file === ".vite/another.js").sha256,
      sha("runtime bytes"),
    );
    assert.throws(
      () =>
        harness(f, [f.app]).generate({ [f.app]: { renderedLength: 1 } }, false, {
          ".vite/manifest.json": {
            type: "chunk",
            fileName: ".vite/manifest.json",
            code: "wrong type",
            modules: {},
          },
        }),
      /build metadata must be an asset/,
    );
  } finally {
    f.cleanup();
  }
});
