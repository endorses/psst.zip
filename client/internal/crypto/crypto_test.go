package crypto

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/cloudflare/circl/hpke"
	"github.com/endorses/psst.zip/client/protocol"
)

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func fixture(t *testing.T, path string, out any) {
	t.Helper()
	// Keep public vectors in the Go module source archive too. In a full
	// checkout, compare the copies so cross-client fixture changes cannot drift.
	b, err := os.ReadFile(filepath.Join("testdata", filepath.Base(path)))
	if err != nil {
		t.Fatal(err)
	}
	if original, err := os.ReadFile("../../../docs/" + path); err == nil {
		if !bytes.Equal(b, original) {
			t.Fatal("module fixture differs from shared cross-client fixture")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}
}

func testFile(size int64) protocol.ManifestFile {
	return protocol.ManifestFile{
		Name: "fixture.bin", MIMEType: "application/octet-stream", Size: size,
		BlobID: "12345678-1234-1234-1234-123456789014", Encoding: "chunked-v1",
		ChunkSize: ChunkSize, EncryptionID: "00112233445566778899aabbccddeeff",
	}
}

func testManifest() protocol.Manifest {
	return protocol.Manifest{Files: []protocol.ManifestFile{testFile(23)}}
}

func TestExistingChunkedVector(t *testing.T) {
	var vector struct {
		Key          string `json:"key"`
		EncryptionID string `json:"encryption_id"`
		Plaintext    string `json:"plaintext"`
		Frame        string `json:"frame"`
		TotalSize    int64  `json:"total_size"`
	}
	fixture(t, "protocol/chunked-file-vector.json", &vector)
	file := testFile(vector.TotalSize)
	file.EncryptionID = vector.EncryptionID
	var out bytes.Buffer
	if err := DecryptFile(context.Background(), &out, bytes.NewReader(unhex(t, vector.Frame)), unhex(t, vector.Key), file); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), unhex(t, vector.Plaintext)) {
		t.Fatal("cross-client frame plaintext mismatch")
	}
	frame := unhex(t, vector.Frame)
	key := unhex(t, vector.Key)
	tests := map[string]struct {
		frame []byte
		file  protocol.ManifestFile
	}{
		"truncated": {frame[:len(frame)-1], file}, "trailing": {append(bytes.Clone(frame), 0), file},
	}
	tamper := bytes.Clone(frame)
	tamper[len(tamper)-1] ^= 1
	tests["tag"] = struct {
		frame []byte
		file  protocol.ManifestFile
	}{tamper, file}
	other := file
	other.EncryptionID = "10112233445566778899aabbccddeeff"
	tests["other file identity"] = struct {
		frame []byte
		file  protocol.ManifestFile
	}{frame, other}
	other = file
	other.Size++
	tests["other declared size"] = struct {
		frame []byte
		file  protocol.ManifestFile
	}{frame, other}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if err := DecryptFile(context.Background(), io.Discard, bytes.NewReader(test.frame), key, test.file); !errors.Is(err, ErrInvalidData) {
				t.Fatalf("expected cryptographic rejection, got %v", err)
			}
		})
	}
	// Authenticate deliberately wrong headers with the public fixture key to
	// exercise the encrypted binding, beyond GCM validity.
	for _, offset := range []int{0, 16, 24} {
		plain := make([]byte, 32+out.Len())
		copy(plain, unhex(t, file.EncryptionID))
		binary.BigEndian.PutUint64(plain[24:32], uint64(file.Size))
		copy(plain[32:], out.Bytes())
		plain[offset] ^= 1
		ciphertext, err := encryptBytes(key, plain)
		if err != nil {
			t.Fatal(err)
		}
		if err := DecryptFile(context.Background(), io.Discard, bytes.NewReader(ciphertext), key, file); !errors.Is(err, ErrInvalidData) {
			t.Fatalf("accepted authenticated wrong header at %d: %v", offset, err)
		}
	}
}

// patternReader generates arbitrary-length source bytes without allocating a
// whole-file fixture, and rejects oversized read buffers to guard streaming IO.
type patternReader struct {
	remaining, offset int64
	maxRead           int
}

func (r *patternReader) Read(p []byte) (int, error) {
	if len(p) > r.maxRead {
		return 0, fmt.Errorf("unbounded read: %d", len(p))
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	n := int(min(int64(len(p)), r.remaining))
	for i := range p[:n] {
		p[i] = byte((r.offset + int64(i)) * 31)
	}
	r.remaining -= int64(n)
	r.offset += int64(n)
	return n, nil
}

func TestFileStreamingAndBoundaries(t *testing.T) {
	key := bytes.Repeat([]byte{0x27}, 32)
	for _, size := range []int64{0, 17, ChunkSize, ChunkSize + 19} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			file := testFile(size)
			pr, pw := io.Pipe()
			encrypted := make(chan error, 1)
			go func() {
				err := EncryptFile(context.Background(), pw, &patternReader{remaining: size, maxRead: ChunkSize}, key, file)
				_ = pw.CloseWithError(err)
				encrypted <- err
			}()
			defer pr.Close()
			got := sha256.New()
			if err := DecryptFile(context.Background(), got, pr, key, file); err != nil {
				t.Fatal(err)
			}
			if err := <-encrypted; err != nil {
				t.Fatal(err)
			}
			want := sha256.New()
			if _, err := io.Copy(want, &patternReader{remaining: size, maxRead: ChunkSize}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
				t.Fatal("streamed plaintext differs")
			}
			wire, err := WireSize(size)
			if err != nil || wire != size+max(1, (size+ChunkSize-1)/ChunkSize)*FrameOverhead {
				t.Fatalf("wrong wire size %d: %v", wire, err)
			}
		})
	}
	for _, size := range []int64{-1, MaxFileSize + 1} {
		if _, err := WireSize(size); err == nil {
			t.Fatal("accepted invalid size")
		}
	}
	if _, err := WireSize(MaxFileSize); err != nil {
		t.Fatal(err)
	}
	for _, input := range [][]byte{nil, {1, 2, 3}} {
		if err := EncryptFile(context.Background(), io.Discard, bytes.NewReader(input), key, testFile(1)); !errors.Is(err, ErrInvalidData) {
			t.Fatalf("accepted plaintext length mismatch: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := EncryptFile(ctx, io.Discard, bytes.NewReader(nil), key, testFile(0)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := DecryptFile(ctx, io.Discard, bytes.NewReader(nil), key, testFile(0)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestStreamingMemoryDoesNotGrowWithFileSize(t *testing.T) {
	// Generate 64 MiB without a file-sized fixture, then discard ciphertext. A
	// whole-file buffer regression exceeds the 16 MiB allocation budget even if
	// each individual Read still uses a small slice.
	const size = int64(64 * 1024 * 1024)
	key := bytes.Repeat([]byte{0x27}, 32)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err := EncryptFile(context.Background(), io.Discard, &patternReader{remaining: size, maxRead: ChunkSize}, key, testFile(size))
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated > 16*1024*1024 {
		t.Fatalf("streaming 64 MiB allocated %d bytes; expected bounded working buffers", allocated)
	}
	t.Logf("64 MiB generated input, %d allocated bytes", allocated)
}

func TestManifestValidationAndAuthentication(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	manifest := testManifest()
	encrypted, err := EncryptManifest(key, manifest)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecryptManifest(key, encrypted)
	if err != nil || !reflect.DeepEqual(got, manifest) {
		t.Fatalf("manifest roundtrip: %v", err)
	}
	for _, ciphertext := range [][]byte{encrypted[:12], append(bytes.Clone(encrypted), 0), make([]byte, MaxManifestBytes+1)} {
		if _, err := DecryptManifest(key, ciphertext); !errors.Is(err, ErrInvalidData) {
			t.Fatalf("accepted invalid manifest: %v", err)
		}
	}
	wrongKey := bytes.Clone(key)
	wrongKey[0] ^= 1
	if _, err := DecryptManifest(wrongKey, encrypted); err == nil {
		t.Fatal("accepted wrong key")
	}
	mutations := map[string]func(*protocol.Manifest){
		"empty": func(m *protocol.Manifest) { m.Files = nil },
		"many":  func(m *protocol.Manifest) { m.Files = make([]protocol.ManifestFile, 101) },
		"duplicate blob": func(m *protocol.Manifest) {
			other := m.Files[0]
			other.EncryptionID = "10112233445566778899aabbccddeeff"
			m.Files = append(m.Files, other)
		},
		"duplicate context": func(m *protocol.Manifest) {
			other := m.Files[0]
			other.BlobID = "12345678-1234-1234-1234-123456789015"
			m.Files = append(m.Files, other)
		},
		"path":       func(m *protocol.Manifest) { m.Files[0].Name = "../escape" },
		"dot":        func(m *protocol.Manifest) { m.Files[0].Name = ".." },
		"blob":       func(m *protocol.Manifest) { m.Files[0].BlobID = "bad" },
		"negative":   func(m *protocol.Manifest) { m.Files[0].Size = -1 },
		"size":       func(m *protocol.Manifest) { m.Files[0].Size = MaxFileSize + 1 },
		"encoding":   func(m *protocol.Manifest) { m.Files[0].Encoding = "unknown" },
		"chunk size": func(m *protocol.Manifest) { m.Files[0].ChunkSize = 1 },
		"context":    func(m *protocol.Manifest) { m.Files[0].EncryptionID = "00112233445566778899AABBCCDDEEFF" },
		"total": func(m *protocol.Manifest) {
			m.Files[0].Size = MaxFileSize
			other := m.Files[0]
			other.Size = 1
			other.BlobID = "12345678-1234-1234-1234-123456789015"
			other.EncryptionID = "10112233445566778899aabbccddeeff"
			m.Files = append(m.Files, other)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			m := testManifest()
			mutate(&m)
			if _, err := EncryptManifest(key, m); !errors.Is(err, ErrInvalidData) {
				t.Fatalf("seal accepted invalid manifest: %v", err)
			}
			plain, err := json.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			encrypted, err := encryptBytes(key, plain)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecryptManifest(key, encrypted); !errors.Is(err, ErrInvalidData) {
				t.Fatalf("open accepted invalid manifest: %v", err)
			}
		})
	}
}

func TestReceiveExistingProviderFixtures(t *testing.T) {
	var f struct {
		Private  string `json:"private_key"`
		Public   string `json:"public_key"`
		Slot     string `json:"slot_id"`
		Transfer string `json:"transfer_id"`
		Key      string `json:"submission_key"`
		Info     string `json:"context_info"`
		Browser  string `json:"wrapped_key"`
		Tink     string `json:"tink_wrapped_key"`
		Swift    string `json:"swift_wrapped_key"`
	}
	fixture(t, "security/fixtures/hpke-receive-v2.json", &f)
	info, err := receiveInfo(f.Slot, f.Transfer, unhex(t, f.Public))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(info, unhex(t, f.Info)) {
		t.Fatal("receive info differs from cross-platform fixture")
	}
	for name, wrapper := range map[string]string{"browser": f.Browser, "Tink": f.Tink, "Swift": f.Swift} {
		t.Run(name, func(t *testing.T) {
			key, err := openSubmissionKey(f.Slot, f.Transfer, unhex(t, f.Private), unhex(t, wrapper))
			if err != nil || !bytes.Equal(key, unhex(t, f.Key)) {
				t.Fatalf("provider wrapper mismatch: %v", err)
			}
		})
	}
}

func TestRFC9180ExactSuiteVector(t *testing.T) {
	var f struct {
		Mode                               int `json:"mode"`
		KEMID                              int `json:"kem_id"`
		KDFID                              int `json:"kdf_id"`
		AEADID                             int `json:"aead_id"`
		Info, SkRm, PkRm, Enc, AAD, CT, PT string
	}
	fixture(t, "security/fixtures/hpke-rfc9180-x25519-aes256.json", &f)
	if f.Mode != 0 || f.KEMID != 32 || f.KDFID != 1 || f.AEADID != 2 {
		t.Fatal("fixture uses another HPKE suite")
	}
	// The provider boundary uses the official first-message vector's nonempty
	// AAD; production receive wrappers always use empty AAD.
	secret, err := hpke.KEM_X25519_HKDF_SHA256.Scheme().UnmarshalBinaryPrivateKey(unhex(t, f.SkRm))
	if err != nil {
		t.Fatal(err)
	}
	receiver, err := receiveSuite.NewReceiver(secret, unhex(t, f.Info))
	if err != nil {
		t.Fatal(err)
	}
	opener, err := receiver.Setup(unhex(t, f.Enc))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := opener.Open(unhex(t, f.CT), unhex(t, f.AAD))
	if err != nil || !bytes.Equal(plain, unhex(t, f.PT)) {
		t.Fatalf("RFC exact-suite mismatch: %v", err)
	}
}

func TestReceiveRoundtripAndRejections(t *testing.T) {
	private, public, err := GenerateReceiveKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	slot := "12345678-1234-1234-1234-123456789012"
	transfer := "12345678-1234-1234-1234-123456789013"
	m := testManifest()
	envelope, err := SealReceiveManifest(slot, transfer, public, key, m)
	if err != nil {
		t.Fatal(err)
	}
	gotKey, got, err := OpenReceiveManifest(slot, transfer, private, envelope)
	if err != nil || !bytes.Equal(gotKey, key) || !reflect.DeepEqual(got, m) {
		t.Fatalf("receive roundtrip: %v", err)
	}
	second, err := SealReceiveManifest(slot, transfer, public, key, m)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(envelope, second) {
		t.Fatal("fresh envelope reused randomness")
	}
	for name, offset := range map[string]int{"magic": 0, "encapsulation": 8, "wrapped key": 40, "manifest": len(envelope) - 1} {
		t.Run(name, func(t *testing.T) {
			b := bytes.Clone(envelope)
			b[offset] ^= 1
			if _, _, err := OpenReceiveManifest(slot, transfer, private, b); !errors.Is(err, ErrInvalidData) {
				t.Fatalf("accepted tampering: %v", err)
			}
		})
	}
	for _, b := range [][]byte{nil, envelope[:115], envelope[:len(envelope)-1], append(bytes.Clone(envelope), 0), make([]byte, MaxManifestBytes+1)} {
		if _, _, err := OpenReceiveManifest(slot, transfer, private, b); err == nil {
			t.Fatal("accepted malformed envelope")
		}
	}
	if _, _, err := OpenReceiveManifest(transfer, slot, private, envelope); err == nil {
		t.Fatal("accepted swapped identities")
	}
	wrong, _, err := GenerateReceiveKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenReceiveManifest(slot, transfer, wrong, envelope); err == nil {
		t.Fatal("accepted wrong recipient")
	}
	for _, public := range [][]byte{nil, make([]byte, 31), make([]byte, 33), make([]byte, 32), append([]byte{1}, make([]byte, 31)...)} {
		if _, err := SealReceiveManifest(slot, transfer, public, key, m); err == nil {
			t.Fatal("accepted invalid or low-order recipient")
		}
	}
	for _, private := range [][]byte{make([]byte, 31), make([]byte, 33)} {
		if _, _, err := OpenReceiveManifest(slot, transfer, private, envelope); err == nil {
			t.Fatal("accepted invalid private key length")
		}
	}
}
