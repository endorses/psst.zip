import { test, expect, retryAuth, signIn } from "./auth-fixture";
import type { APIRequestContext } from "@playwright/test";
import { randomUUID, webcrypto } from "node:crypto";
import { readFile } from "node:fs/promises";
import {
  generateReceiveKeyPair,
  sealSubmissionKey,
  openSubmissionKey,
  encodeReceiveEnvelope,
  decodeReceiveEnvelope,
} from "../../src/lib/receive-crypto.ts";
import {
  generateKey,
  encryptManifest,
  decryptManifest,
  type EncryptionKey,
  type FileManifestEntry,
} from "../../src/lib/crypto.ts";
import {
  encryptFile,
  decryptFileStream,
  newEncryptionId,
  wireSize,
} from "../../src/lib/chunked-files.ts";

// Real disposable backend and production client crypto, not mocked authorization
// or fixture ciphertext. Every sender deliberately retains its own plaintext key.
async function submit(
  sender: APIRequestContext,
  slot: string,
  publicKey: Uint8Array,
  name: string,
  contents: string,
) {
  const created = await sender.post(`/api/v1/slots/${slot}/transfers`);
  expect(created.status(), await created.text()).toBe(201);
  const { id, delete_token: capability } = await created.json();
  const key = await generateKey();
  const blob = new Blob([contents]);
  const encryptionId = newEncryptionId();
  const allocated = await sender.post(`/api/v1/transfers/${id}/files`, {
    headers: {
      Authorization: `Bearer ${capability}`,
      "Tus-Resumable": "1.0.0",
      "Upload-Length": String(wireSize(blob.size)),
    },
  });
  expect(allocated.status(), await allocated.text()).toBe(201);
  const location = allocated.headers().location;
  expect(location).toMatch(new RegExp(`/api/v1/transfers/${id}/files/[0-9a-f-]{36}$`));
  const file: FileManifestEntry = {
    name,
    size: blob.size,
    mime_type: "text/plain",
    blob_id: location.split("/").pop()!,
    encoding: "chunked-v1",
    chunk_size: 4194304,
    encryption_id: encryptionId,
  };
  const frames: Buffer[] = [];
  for await (const frame of encryptFile(key, blob, encryptionId)) frames.push(Buffer.from(frame));
  const encryptedFile = Buffer.concat(frames);
  expect(encryptedFile.length).toBe(wireSize(file.size));
  const uploaded = await sender.patch(location, {
    headers: {
      Authorization: `Bearer ${capability}`,
      "Tus-Resumable": "1.0.0",
      "Upload-Offset": "0",
      "Content-Type": "application/offset+octet-stream",
    },
    data: encryptedFile,
  });
  expect(uploaded.status(), await uploaded.text()).toBe(204);
  const manifest = { files: [file] };
  const envelope = encodeReceiveEnvelope(
    await sealSubmissionKey(publicKey, slot, id, key),
    new Uint8Array(await encryptManifest(key, manifest)),
  );
  const persisted = await sender.post(`/api/v1/transfers/${id}/manifest`, {
    headers: {
      Authorization: `Bearer ${capability}`,
      "Content-Type": "application/octet-stream",
    },
    data: Buffer.from(envelope),
  });
  expect(persisted.status(), await persisted.text()).toBe(204);
  const completed = await sender.post(`/api/v1/transfers/${id}/complete`, {
    headers: { Authorization: `Bearer ${capability}` },
  });
  expect(completed.status(), await completed.text()).toBe(204);
  return { id, capability, key, file, manifest, envelope, encryptedFile, contents };
}

async function plaintextFile(key: EncryptionKey, file: FileManifestEntry, ciphertext: Uint8Array) {
  const chunks: Buffer[] = [];
  const stream = new ReadableStream<Uint8Array>({
    start(controller) {
      controller.enqueue(ciphertext);
      controller.close();
    },
  });
  for await (const chunk of decryptFileStream(key, file, stream)) chunks.push(Buffer.from(chunk));
  return Buffer.concat(chunks).toString();
}

for (const noSubtle of [false, true]) {
  test(`two real senders remain isolated even with copied ciphertext (${noSubtle ? "LAN crypto" : "WebCrypto"})`, async ({
    request: owner,
    adminRequest,
    playwright,
    baseURL,
    page,
  }) => {
    test.setTimeout(90_000);
    const anonymous = await playwright.request.newContext({ baseURL });
    const senderA = await playwright.request.newContext({ baseURL });
    const senderB = await playwright.request.newContext({ baseURL });
    const unrelated = await playwright.request.newContext({ baseURL });
    const descriptor = Object.getOwnPropertyDescriptor(globalThis, "crypto");
    let slot = "";
    try {
      const username = `unrelated-${randomUUID()}`;
      const password = "Confidentiality-temporary-2026";
      const user = await adminRequest.post("/api/v1/admin/users", {
        data: { username, password, role: "user" },
      });
      expect(user.status(), await user.text()).toBe(201);
      const login = await retryAuth(() =>
        unrelated.post("/api/v1/auth/login", {
          data: { username, password, session_type: "device" },
        }),
      );
      expect(login.status(), await login.text()).toBe(200);
      const temporaryToken = (await login.json()).token;
      const changed = await unrelated.post("/api/v1/auth/password", {
        headers: { Authorization: `Bearer ${temporaryToken}` },
        data: { current_password: password, password: password + "-changed" },
      });
      expect(changed.status(), await changed.text()).toBe(204);
      const signedIn = await retryAuth(() =>
        unrelated.post("/api/v1/auth/login", {
          data: { username, password: password + "-changed", session_type: "device" },
        }),
      );
      expect(signedIn.status(), await signedIn.text()).toBe(200);
      const unrelatedToken = (await signedIn.json()).token;

      if (noSubtle) {
        Object.defineProperty(globalThis, "crypto", {
          configurable: true,
          value: { getRandomValues: webcrypto.getRandomValues.bind(webcrypto) },
        });
        expect(globalThis.crypto.subtle).toBeUndefined();
        await page.addInitScript(() =>
          Object.defineProperty(globalThis.crypto, "subtle", { value: undefined }),
        );
      }
      const pair = await generateReceiveKeyPair();
      const publicKey = Buffer.from(pair.publicKey).toString("base64url");
      const created = await owner.post("/api/v1/slots", {
        data: { receive_protocol: 2, recipient_public_key: publicKey },
      });
      expect(created.status(), await created.text()).toBe(201);
      slot = (await created.json()).id;
      const invitation = `${baseURL}/u/${slot}#v2.${publicKey}`;
      expect(new URL(invitation).hash).toBe(`#v2.${publicKey}`);
      const first = await submit(
        senderA,
        slot,
        pair.publicKey,
        "sender-a.txt",
        "sender A private content",
      );
      const victim = await submit(
        senderB,
        slot,
        pair.publicKey,
        "sender-b.txt",
        "sender B private content",
      );
      expect(Buffer.from(first.key)).not.toEqual(Buffer.from(victim.key));
      const paths = [
        `/api/v1/slots/${slot}`,
        `/api/v1/slots/${slot}/inbox`,
        `/api/v1/slots/${slot}/events`,
        `/api/v1/slots/${slot}/transfers/${victim.id}/membership`,
        `/api/v1/transfers/${victim.id}`,
        `/api/v1/transfers/${victim.id}/manifest`,
        `/api/v1/transfers/${victim.id}/files/${victim.file.blob_id}`,
      ];
      for (const actor of [
        { context: anonymous, token: "", status: 401 },
        { context: senderA, token: first.capability, status: 401 },
        { context: senderB, token: victim.capability, status: 401 },
        { context: unrelated, token: unrelatedToken, status: 403 },
        { context: adminRequest, token: "", status: 403 },
      ]) {
        const headers: Record<string, string> = actor.token
          ? { Authorization: `Bearer ${actor.token}` }
          : {};
        for (const path of paths) {
          const denied = await actor.context.get(path, { headers });
          expect(denied.status(), `${path}: ${await denied.text()}`).toBe(actor.status);
        }
        const denied = await actor.context.post(`/api/v1/transfers/${victim.id}/downloaded`, {
          headers,
        });
        expect(denied.status(), await denied.text()).toBe(actor.status);
      }
      const availability = await anonymous.get(`/api/v1/slots/${slot}/availability`);
      expect(availability.status()).toBe(200);
      const publicMetadata = await availability.text();
      for (const secret of [
        first.id,
        victim.id,
        first.file.name,
        victim.file.name,
        '"transfers":',
        "reserved_files",
      ])
        expect(publicMetadata).not.toContain(secret);

      for (const submission of [first, victim]) {
        const membership = await owner.get(
          `/api/v1/slots/${slot}/transfers/${submission.id}/membership`,
        );
        expect(membership.status()).toBe(200);
        expect(await membership.json()).toMatchObject({
          slot_id: slot,
          transfer_id: submission.id,
          receive_protocol: 2,
          recipient_public_key: publicKey,
        });
        const response = await owner.get(`/api/v1/transfers/${submission.id}/manifest`);
        expect(response.status()).toBe(200);
        const envelope = decodeReceiveEnvelope(new Uint8Array(await response.body()));
        const recovered = await openSubmissionKey(
          pair.privateKey,
          pair.publicKey,
          slot,
          submission.id,
          envelope.wrappedKey,
        );
        expect(Buffer.from(recovered)).toEqual(Buffer.from(submission.key));
        expect(await decryptManifest(recovered, envelope.encryptedManifest.buffer)).toEqual(
          submission.manifest,
        );
        const file = await owner.get(
          `/api/v1/transfers/${submission.id}/files/${submission.file.blob_id}`,
        );
        expect(file.status()).toBe(200);
        expect(
          await plaintextFile(recovered, submission.file, new Uint8Array(await file.body())),
        ).toBe(submission.contents);
      }

      // Give the attacker the victim envelope and file outside the API: an API
      // denial alone is insufficient to demonstrate cryptographic sender isolation.
      const copied = decodeReceiveEnvelope(victim.envelope);
      await expect(decryptManifest(first.key, copied.encryptedManifest.buffer)).rejects.toThrow();
      await expect(
        decryptManifest(pair.publicKey, copied.encryptedManifest.buffer),
      ).rejects.toThrow();
      await expect(plaintextFile(first.key, victim.file, victim.encryptedFile)).rejects.toThrow();
      const attacker = await generateReceiveKeyPair();
      await expect(
        openSubmissionKey(attacker.privateKey, pair.publicKey, slot, victim.id, copied.wrappedKey),
      ).rejects.toThrow();
      await expect(
        openSubmissionKey(pair.publicKey, pair.publicKey, slot, victim.id, copied.wrappedKey),
      ).rejects.toThrow();
      await expect(
        openSubmissionKey(pair.privateKey, pair.publicKey, slot, first.id, copied.wrappedKey),
      ).rejects.toThrow();
      const swapped = decodeReceiveEnvelope(first.envelope);
      await expect(
        openSubmissionKey(pair.privateKey, pair.publicKey, slot, victim.id, swapped.wrappedKey),
      ).rejects.toThrow();
      const victimInfo = await owner.get(`/api/v1/transfers/${victim.id}`);
      const victimMetadata = await victimInfo.json();
      expect(victimMetadata.downloaded_at).toBeNull();
      expect(victimMetadata.files).toHaveLength(1);
      expect(victimMetadata.files).toMatchObject([
        { id: victim.file.blob_id, download_count: 1, remaining_downloads: null },
      ]);

      // Exercise the real owner UI using the same accepted recipient pair.
      const identity = await (await owner.get("/api/v1/auth/me")).json();
      await page.addInitScript(({ name, value }) => localStorage.setItem(name, value), {
        name: `psst.receive-key.v2.${identity.user.id}.${slot}`,
        value: JSON.stringify({
          publicKey,
          privateKey: Buffer.from(pair.privateKey).toString("base64url"),
        }),
      });
      await signIn(page);
      await page.goto(`/d/${victim.id}?inbox=${slot}`);
      await expect(page.getByText(victim.file.name, { exact: true })).toBeVisible();
      const downloaded = page.waitForEvent("download");
      await page.getByRole("button", { name: "Save file", exact: true }).click();
      expect((await readFile((await (await downloaded).path())!)).toString()).toBe(victim.contents);
    } finally {
      if (descriptor) Object.defineProperty(globalThis, "crypto", descriptor);
      else Reflect.deleteProperty(globalThis, "crypto");
      if (slot) expect((await owner.delete(`/api/v1/slots/${slot}`)).status()).toBe(204);
      await Promise.all([
        anonymous.dispose(),
        senderA.dispose(),
        senderB.dispose(),
        unrelated.dispose(),
      ]);
    }
  });
}
