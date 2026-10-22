/** Private, disposable interoperability peer using the production web crypto.
 * All credentials, links and keys arrive over stdin; stdout is captured by the
 * integration driver, never displayed. No browser or persistent web state.
 */
import { readFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import {
  generateKey,
  exportKey,
  importKey,
  encryptManifest,
  decryptManifest,
} from "../../web/src/lib/crypto.ts";
import {
  FILE_CHUNK_SIZE,
  newEncryptionId,
  encryptFile,
  decryptFileStream,
} from "../../web/src/lib/chunked-files.ts";
import {
  generateReceiveKeyPair,
  sealSubmissionKey,
  openSubmissionKey,
  encodeReceiveEnvelope,
  decodeReceiveEnvelope,
} from "../../web/src/lib/receive-crypto.ts";

const input = JSON.parse(await new Response(process.stdin).text());
const mode = process.argv[2];
const base = new URL(input.origin);
if (base.protocol !== "http:" || base.hostname !== "127.0.0.1") {
  throw new Error("Integration peer only accepts a disposable loopback origin");
}
const bytes = (value) => Buffer.from(value, "base64url");
const encode = (value) => Buffer.from(value).toString("base64url");
const hash = (value) => createHash("sha256").update(value).digest("hex");
const buffer = (value) => new Uint8Array(value).buffer;

async function request(path, { token, ...init } = {}) {
  const target = new URL(path, base);
  if (target.origin !== base.origin || target.hash)
    throw new Error("Unsafe fixture request");
  const response = await fetch(target, {
    ...init,
    headers: {
      ...(token ? { Authorization: `Bearer ${token}` } : {}),
      ...init.headers,
    },
    redirect: "error",
    signal: AbortSignal.timeout(8000),
  });
  if (!response.ok)
    throw new Error(`Integration API status ${response.status}`);
  return response;
}
async function create(path, body, token) {
  return (
    await request(`/api/v1${path}`, {
      method: "POST",
      token,
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    })
  ).json();
}

async function publish(transfer, token, key, slot) {
  const files = [];
  for (const item of input.files) {
    const plaintext = await readFile(item.path);
    const encryptionID = newEncryptionId();
    const frames = [];
    for await (const frame of encryptFile(
      key,
      new Blob([plaintext]),
      encryptionID,
    )) {
      frames.push(Buffer.from(frame));
    }
    const ciphertext = Buffer.concat(frames);
    const allocated = await request(`/api/v1/transfers/${transfer.id}/files`, {
      method: "POST",
      token,
      headers: {
        "Tus-Resumable": "1.0.0",
        "Upload-Length": String(ciphertext.length),
      },
    });
    const location = new URL(allocated.headers.get("location"), base);
    if (location.origin !== base.origin)
      throw new Error("Unsafe tus upload origin");
    await request(location.pathname, {
      method: "PATCH",
      token,
      headers: {
        "Tus-Resumable": "1.0.0",
        "Upload-Offset": "0",
        "Content-Type": "application/offset+octet-stream",
      },
      body: ciphertext,
    });
    files.push({
      name: item.name,
      size: plaintext.length,
      mime_type: "application/octet-stream",
      blob_id: location.pathname.split("/").at(-1),
      encoding: "chunked-v1",
      chunk_size: FILE_CHUNK_SIZE,
      encryption_id: encryptionID,
    });
  }
  let manifest = new Uint8Array(await encryptManifest(key, { files }));
  if (slot) {
    const wrapped = await sealSubmissionKey(
      slot.publicKey,
      slot.id,
      transfer.id,
      key,
    );
    manifest = encodeReceiveEnvelope(wrapped, manifest);
  }
  await request(`/api/v1/transfers/${transfer.id}/manifest`, {
    method: "POST",
    token,
    headers: { "Content-Type": "application/octet-stream" },
    body: manifest,
  });
  await request(`/api/v1/transfers/${transfer.id}/complete`, {
    method: "POST",
    token,
  });
  return files;
}

async function verify(transferID, key, token, slot) {
  const encrypted = new Uint8Array(
    await (
      await request(`/api/v1/transfers/${transferID}/manifest`, { token })
    ).arrayBuffer(),
  );
  let manifest;
  if (slot) {
    const envelope = decodeReceiveEnvelope(encrypted);
    key = await openSubmissionKey(
      slot.privateKey,
      slot.publicKey,
      slot.id,
      transferID,
      envelope.wrappedKey,
    );
    manifest = await decryptManifest(key, buffer(envelope.encryptedManifest));
  } else {
    manifest = await decryptManifest(key, buffer(encrypted));
  }
  if (manifest.files.length !== input.files.length)
    throw new Error("Manifest file count mismatch");
  const verified = [];
  for (const entry of manifest.files) {
    const expected = input.files.find((file) => file.name === entry.name);
    if (!expected) throw new Error("Unexpected encrypted filename");
    const response = await request(
      `/api/v1/transfers/${transferID}/files/${entry.blob_id}`,
      {
        token,
      },
    );
    const plaintext = [];
    for await (const frame of decryptFileStream(key, entry, response.body)) {
      plaintext.push(Buffer.from(frame));
    }
    const actual = Buffer.concat(plaintext);
    const original = await readFile(expected.path);
    if (!actual.equals(original))
      throw new Error("Authenticated plaintext mismatch");
    verified.push({
      name: entry.name,
      sha256: hash(actual),
      file_id: entry.blob_id,
    });
  }
  await request(`/api/v1/transfers/${transferID}/downloaded`, {
    method: "POST",
    token,
  });
  return verified;
}

let result;
if (mode === "send-create") {
  const key = await generateKey();
  const transfer = await create(
    "/transfers",
    { max_downloads: 1 },
    input.token,
  );
  await publish(transfer, input.token, key);
  result = {
    id: transfer.id,
    link: `${base.origin}/d/${transfer.id}#${await exportKey(key)}`,
  };
} else if (mode === "send-decrypt") {
  const link = new URL(input.link);
  result = await verify(
    link.pathname.split("/").at(-1),
    await importKey(link.hash.slice(1)),
  );
} else if (mode === "receive-create") {
  const pair = await generateReceiveKeyPair();
  const slot = await create(
    "/slots",
    {
      receive_protocol: 2,
      recipient_public_key: encode(pair.publicKey),
      max_files: 1,
    },
    input.token,
  );
  result = {
    id: slot.id,
    link: `${base.origin}/u/${slot.id}#v2.${encode(pair.publicKey)}`,
    private_key: encode(pair.privateKey),
    public_key: encode(pair.publicKey),
  };
} else if (mode === "receive-upload") {
  const link = new URL(input.link);
  const id = link.pathname.split("/").at(-1);
  const publicKey = bytes(link.hash.slice(4));
  const policy = await (
    await request(`/api/v1/slots/${id}/availability`)
  ).json();
  if (
    policy.receive_protocol !== 2 ||
    policy.recipient_public_key !== encode(publicKey)
  ) {
    throw new Error("Receive invitation disagrees with authoritative policy");
  }
  const transfer = await create(`/slots/${id}/transfers`, {});
  const key = await generateKey();
  await publish(transfer, transfer.delete_token, key, { id, publicKey });
  result = { id: transfer.id };
} else if (mode === "receive-decrypt") {
  const slot = input.slot;
  const page = await (
    await request(`/api/v1/slots/${slot.id}/inbox?limit=10`, {
      token: input.token,
    })
  ).json();
  if (page.transfers.length !== 1)
    throw new Error("Unexpected inbox membership");
  const transferID = page.transfers[0].transfer_id;
  const member = await (
    await request(
      `/api/v1/slots/${slot.id}/transfers/${transferID}/membership`,
      {
        token: input.token,
      },
    )
  ).json();
  if (member.slot_id !== slot.id || member.transfer_id !== transferID) {
    throw new Error("Owner membership identity mismatch");
  }
  result = await verify(transferID, null, input.token, {
    id: slot.id,
    privateKey: bytes(slot.private_key),
    publicKey: bytes(slot.public_key),
  });
} else {
  throw new Error("Unknown integration crypto action");
}
process.stdout.write(JSON.stringify(result));
