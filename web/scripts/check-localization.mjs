import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "svelte/compiler";
import ts from "typescript";
export function checkLocalization(root) {
  const failures = [];
  function catalog(name) {
    const file = path.join(root, `lib/i18n/${name}.json`);
    const source = fs.readFileSync(file, "utf8");
    const tree = ts.parseJsonText(file, source);
    function visit(node) {
      if (ts.isObjectLiteralExpression(node)) {
        const names = new Set();
        for (const property of node.properties) {
          const name = property.name?.text;
          if (names.has(name)) failures.push(`Duplicate catalog key ${file}: ${name}`);
          names.add(name);
        }
      }
      ts.forEachChild(node, visit);
    }
    visit(tree);
    return JSON.parse(source);
  }
  const en = catalog("en"),
    de = catalog("de");
  const exceptions = JSON.parse(
    fs.readFileSync(path.join(root, "lib/i18n/nontranslatable.json"), "utf8"),
  );
  const inventory = [];
  const slots = (text) =>
    [...text.matchAll(/\{([A-Za-z][A-Za-z0-9_]*)\}/g)]
      .map((m) => m[1])
      .sort()
      .join(",");
  for (const key of new Set([...Object.keys(en), ...Object.keys(de)])) {
    if (!Object.hasOwn(en, key) || !Object.hasOwn(de, key)) {
      failures.push(`Missing catalog key ${key}`);
      continue;
    }
    for (const value of [en[key], de[key]]) {
      if (
        typeof value !== "string" &&
        (!value ||
          typeof value !== "object" ||
          Object.keys(value).sort().join() !== "one,other" ||
          typeof value.one !== "string" ||
          typeof value.other !== "string")
      )
        failures.push(`Invalid plural variants ${key}`);
    }
    const variants = (value) => (typeof value === "string" ? { text: value } : value);
    const e = variants(en[key]),
      d = variants(de[key]);
    if (Object.keys(e).sort().join() !== Object.keys(d).sort().join())
      failures.push(`Plural shape ${key}`);
    for (const variant of Object.keys(e))
      if (!d[variant] || slots(e[variant]) !== slots(d[variant]))
        failures.push(`Placeholder mismatch ${key}.${variant}`);
    if (JSON.stringify(en[key]) === JSON.stringify(de[key]) && !exceptions.identical.includes(key))
      failures.push(`Untranslated catalog key ${key}`);
  }
  const attrs = new Set(["alt", "title", "placeholder", "aria-label", "label", "description"]);
  const exempt = (file, value) =>
    exceptions.source.some((entry) => entry.file === file && entry.value === value);
  function record(file, value, kind) {
    if (!value.trim() || !/[A-Za-z]/.test(value)) return;
    inventory.push({ file, value, kind });
    if (!exempt(file, value)) failures.push(`${file}: unextracted ${kind}: ${value}`);
  }
  for (const relative of fs
    .readdirSync(root, { recursive: true })
    .filter((p) => p.endsWith(".svelte"))) {
    const source = fs.readFileSync(path.join(root, relative), "utf8");
    const ast = parse(source);
    function walk(node, attribute, parent) {
      if (!node || typeof node !== "object") return;
      if (node.type === "Attribute") attribute = node.name;
      if (
        node.type === "Literal" &&
        typeof node.value === "string" &&
        (/[A-Za-z][a-z]+[ .][A-Za-z]/.test(node.value) ||
          (parent?.type === "CallExpression" && parent.callee?.name === "$t") ||
          (attrs.has(attribute) &&
            /[A-Za-z]/.test(node.value) &&
            (parent?.type === "MustacheTag" ||
              (parent?.type === "ConditionalExpression" &&
                (parent.consequent === node || parent.alternate === node)) ||
              (parent?.type === "CallExpression" && parent.callee?.name === "$t"))))
      )
        record(relative, node.value, "expression prose");
      if (node.type === "Text" && (!attribute || attrs.has(attribute)))
        record(relative, node.data.replace(/\s+/g, " ").trim(), "text");
      for (const [key, value] of Object.entries(node)) {
        if (["loc", "metadata", "comments"].includes(key)) continue;
        if (Array.isArray(value)) value.forEach((v) => walk(v, attribute, node));
        else if (value && typeof value === "object") walk(value, attribute, node);
      }
    }
    walk(ast.html);
  }
  // Display assignments/error prose are checked as source; machine values are explicitly documented per file.
  for (const relative of fs
    .readdirSync(root, { recursive: true })
    .filter((p) => (p.endsWith(".ts") || p.endsWith(".svelte")) && !p.startsWith("lib/i18n/"))) {
    let source = fs.readFileSync(path.join(root, relative), "utf8");
    if (relative.endsWith(".svelte"))
      source = source.match(/<script[^>]*>([\s\S]*?)<\/script>/)?.[1] ?? "";
    const ast = ts.createSourceFile(relative, source, ts.ScriptTarget.Latest, true);
    function visit(node) {
      if (ts.isImportDeclaration(node) || ts.isTypeNode(node)) return;
      if (
        ts.isStringLiteral(node) &&
        /[A-Za-z][a-z]+[ .][A-Za-z]/.test(node.text) &&
        !node.text.startsWith("$") &&
        !node.text.startsWith("./") &&
        !node.text.startsWith("../")
      )
        record(relative, node.text, "script prose");
      if (ts.isTemplateExpression(node)) {
        const value =
          node.head.text +
          node.templateSpans.map((span, i) => `{arg${i}}` + span.literal.text).join("");
        if (/[A-Za-z][a-z]+[ .][A-Za-z]/.test(value)) record(relative, value, "template prose");
      }
      ts.forEachChild(node, visit);
    }
    visit(ast);
  }
  return { failures, inventory, catalogCount: Object.keys(en).length };
}

// Keep the command-line interface separate so fixture checks can run in process.
if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const root = process.env.PSST_LOCALIZATION_FIXTURE_ROOT
    ? path.resolve(process.env.PSST_LOCALIZATION_FIXTURE_ROOT)
    : path.resolve(import.meta.dirname, "../src");
  const { failures, inventory, catalogCount } = checkLocalization(root);
  if (process.argv.includes("--inventory")) console.log(JSON.stringify(inventory, null, 2));
  if (failures.length) {
    console.error(failures.join("\n"));
    process.exitCode = 1;
  } else {
    console.log(
      `Localization checked: ${catalogCount} bilingual keys, ${inventory.length} documented source exceptions.`,
    );
  }
}
