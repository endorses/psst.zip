import fs from "node:fs";
const directory = new URL("../src/lib/i18n/", import.meta.url);
const catalog = JSON.parse(fs.readFileSync(new URL("en.json", directory), "utf8"));
let source =
  '// Generated from en.json by scripts/generate-message-types.mjs.\nimport type { MessageArgument } from "./messages.ts";\nexport interface MessageArguments {\n';
for (const [key, value] of Object.entries(catalog)) {
  const values = typeof value === "string" ? [value] : Object.values(value);
  const args = [
    ...new Set([...values.join("").matchAll(/\{([A-Za-z][A-Za-z0-9_]*)\}/g)].map((m) => m[1])),
  ].sort();
  source += `  ${key}: ${args.length ? "{" + args.map((a) => a + ": MessageArgument").join("; ") + "}" : "never"};\n`;
}
fs.writeFileSync(new URL("arguments.ts", directory), source + "}\n");
