/** Generate disposable native interoperability fixtures using the web encryption implementation.
 * Usage: PSST_TEST_BASE_URL=http://127.0.0.1:18481 PSST_TEST_USERNAME=... \
 * PSST_TEST_PASSWORD=... node web/tests/native-fixture.mjs /tmp/fixture-output
 * Optional PSST_NATIVE_BASE_URL changes only the links (e.g. Android 10.0.2.2).
 * Use a disposable server: this creates transfers and a receive slot.
 */
import { mkdir, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { createHash } from "node:crypto";
import QRCode from "qrcode";
import { encrypt, encryptManifest, generateKey, exportKey } from "../src/lib/crypto.ts";

const base = process.env.PSST_TEST_BASE_URL?.replace(/\/$/, "");
const nativeBase = process.env.PSST_NATIVE_BASE_URL?.replace(/\/$/, "") ?? base;
const username = process.env.PSST_TEST_USERNAME;
const password = process.env.PSST_TEST_PASSWORD;
if (!base || !username || !password || !process.argv[2]) {
  throw new Error(
    "Set PSST_TEST_BASE_URL, PSST_TEST_USERNAME, PSST_TEST_PASSWORD and an output directory",
  );
}
const output = resolve(process.argv[2]);
await mkdir(output, { recursive: true, mode: 0o700 });
async function request(path, init = {}) {
  const response = await fetch(`${base}/api/v1${path}`, { redirect: "error", ...init });
  if (!response.ok) throw new Error(`Fixture request failed: ${response.status} ${path}`);
  return response;
}
const login = await request("/auth/login", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({
    username,
    password,
    session_type: "device",
    device_name: "Native fixture generator",
  }),
});
const { token } = await login.json();
const authorization = { Authorization: `Bearer ${token}` };
const fixtures = {};
async function create(name, count, { corrupt = false, quota = 0 } = {}) {
  const key = await generateKey();
  const transfer = await (
    await request("/transfers", {
      method: "POST",
      headers: { ...authorization, "Content-Type": "application/json" },
      body: JSON.stringify({ max_downloads: quota }),
    })
  ).json();
  const files = [];
  const expected = [];
  for (let index = 0; index < count; index++) {
    const filename = index < 2 ? `${name}-same.txt` : `${name}-unicode-ä.txt`;
    const plaintext = new TextEncoder().encode(
      `psst.zip native interoperability ${name} file ${index}\n`,
    );
    const encrypted = new Uint8Array(await encrypt(key, plaintext.buffer));
    if (corrupt && index === count - 1) encrypted[encrypted.length - 1] ^= 1;
    const created = await request(`/transfers/${transfer.id}/files`, {
      method: "POST",
      headers: {
        ...authorization,
        "Tus-Resumable": "1.0.0",
        "Upload-Length": String(encrypted.length),
      },
    });
    const location = new URL(created.headers.get("location"), base);
    if (location.origin !== new URL(base).origin)
      throw new Error("Unexpected fixture upload origin");
    const uploaded = await fetch(location, {
      method: "PATCH",
      redirect: "error",
      headers: {
        ...authorization,
        "Tus-Resumable": "1.0.0",
        "Upload-Offset": "0",
        "Content-Type": "application/offset+octet-stream",
      },
      body: encrypted,
    });
    if (!uploaded.ok) throw new Error(`Fixture upload failed: ${uploaded.status}`);
    const blobId = location.pathname.split("/").pop();
    files.push({
      name: filename,
      size: plaintext.length,
      mime_type: "text/plain",
      blob_id: blobId,
    });
    const localName = `${name}-expected-${index}.txt`;
    await writeFile(join(output, localName), plaintext);
    expected.push({
      filename,
      blobId,
      localName,
      sha256: createHash("sha256").update(plaintext).digest("hex"),
    });
  }
  await request(`/transfers/${transfer.id}/manifest`, {
    method: "POST",
    headers: { ...authorization, "Content-Type": "application/octet-stream" },
    body: await encryptManifest(key, { files }),
  });
  await request(`/transfers/${transfer.id}/complete`, { method: "POST", headers: authorization });
  const link = `${nativeBase}/d/${transfer.id}#${await exportKey(key)}`;
  await QRCode.toFile(join(output, `${name}.png`), link, { margin: 4, scale: 8 });
  fixtures[name] = { id: transfer.id, link, expected };
}
await create("single", 1);
await create("multi", 3);
await create("corrupt", 2, { corrupt: true });
await create("quota", 2, { quota: 1 });
const slot = await (await request("/slots", { method: "POST", headers: authorization })).json();
const slotKey = await generateKey();
fixtures.upload = { id: slot.id, link: `${nativeBase}/u/${slot.id}#${await exportKey(slotKey)}` };
await QRCode.toFile(join(output, "upload.png"), fixtures.upload.link, { margin: 4, scale: 8 });
await writeFile(join(output, "fixtures.json"), JSON.stringify(fixtures, null, 2), { mode: 0o600 });
console.log(
  `Created native interoperability fixtures in ${output}; keys are in fixtures.json, not this log.`,
);
