# Encrypted file frames: chunked-v1

Every new file is represented by one server blob and an entry in the encrypted manifest:

```json
{
  "name": "example.bin",
  "size": 123,
  "mime_type": "application/octet-stream",
  "blob_id": "server-issued UUID",
  "encoding": "chunked-v1",
  "chunk_size": 4194304,
  "encryption_id": "00112233445566778899aabbccddeeff"
}
```

`encryption_id` is a cryptographically random 16-byte identifier, represented as 32 lowercase hexadecimal digits. It must be unique within the manifest. File size is plaintext bytes; the authenticated manifest remains encrypted using the transfer key and the existing nonce+ciphertext+GCM-tag envelope. Filenames and framing metadata are never sent as tus metadata.

Each file consists of `max(1, ceil(size / 4194304))` consecutive frames. A zero-byte file has one authenticated empty frame. All non-final plaintext chunks are exactly 4 MiB; the final chunk contains the remaining bytes.

Each frame is `nonce[12] || AES-256-GCM(key, nonce, header || plaintext)[32 + plaintext_length + 16]`. Generate a fresh cryptographically random 96-bit nonce for every frame. The 32-byte encrypted header is `encryption_id[16] || uint64_be(chunk_index)[8] || uint64_be(total_plaintext_size)[8]`. Chunk indices start at zero. The header is inside the authenticated ciphertext, not a separate unprotected framing field. Each frame therefore adds exactly 60 bytes.

The manifest determines every frame boundary. Encrypted blob size is `size + 60 * frame_count`. Receivers verify the manifest encoding, fixed chunk size, identifier uniqueness, declared sizes and server blob totals before receiving file bytes. For every frame they authenticate GCM, compare all header fields with the expected file and position, and verify exact plaintext length. After the final frame they require end-of-stream. Reordering, substitution, altered bytes, truncation and trailing bytes fail the file.

Receivers write verified chunks into an unpublished temporary destination; they expose the complete file only after the whole stream passes. A valid early chunk does not make an incomplete file a completed download. Existing per-file journals, collision handling and whole-transfer completion receipts still apply. Interrupted attempts may restart the incomplete file; this format does not promise byte-range resume or background execution.

The chunk size is independent of the administrator's per-file upload limit. All implementations use bounded working buffers and sequential disk IO; they must not concatenate a whole large file into RAM. Browser environments without a suitable disk-backed save API retain an explicit small-file fallback limit. The implementation's arithmetic ceiling is 1 TiB; the operator's `MAX_FILE_SIZE` setting normally imposes a smaller ceiling (5 GiB by default), within which the administrator chooses a limit (25 MiB initially).

The user confirmed there are no existing files to preserve. This development format intentionally requires updated clients and does not include legacy file-ciphertext fallback. Transfer URL/QR formats and encrypted-manifest envelope remain unchanged.

[The deterministic cross-platform vector](chunked-file-vector.json) uses a public test key and fixed nonce solely for reproducible decryption tests. Production encryption must never reuse that nonce/key pair.
