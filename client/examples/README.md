# Offline QR examples

`send.json` and `receive.json` are synthetic version-1 CLI results for the reserved
`files.example` hostname. They do not refer to a running service or real files.
Their keys come from the repository's **public protocol test vectors** and must
never be used for real transfers. No private Receive key or account token is
included.

```sh
psst qr --json-file client/examples/send.json
psst qr --json-file client/examples/receive.json
```

These examples exercise offline rendering only. Normal results are produced by
`psst send ... --json` or `psst receive create --json` and contain actual share
capabilities; keep those results private when the links are private.
