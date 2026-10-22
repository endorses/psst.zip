// Package crypto implements the existing psst.zip client encryption formats.
// It never sends or persists keys. Callers own key storage and atomic publication
// of verified downloads.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/endorses/psst.zip/client/protocol"
)

const (
	ChunkSize              = protocol.ChunkSize
	FrameOverhead          = protocol.FrameOverhead
	MaxFileSize      int64 = protocol.MaxFileSize
	MaxManifestBytes       = protocol.MaxManifestBytes
	maxFiles               = protocol.MaxFiles
	nonceSize              = 12
	keySize                = 32
)

// ErrInvalidData identifies rejected ciphertext or protocol values. Errors never
// include keys, decrypted filenames, private URLs, or provider diagnostics.
var ErrInvalidData = errors.New("cryptographic validation failed")

func invalid(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidData, reason) }

func NewKey() ([]byte, error) {
	key := make([]byte, keySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, errors.New("secure key generation failed")
	}
	return key, nil
}

func NewEncryptionID() (string, error) {
	var id [16]byte
	if _, err := io.ReadFull(rand.Reader, id[:]); err != nil {
		return "", errors.New("secure file identity generation failed")
	}
	return hex.EncodeToString(id[:]), nil
}

// WireSize returns the exact chunked-v1 ciphertext length, including the empty
// authenticated frame required for zero-byte files.
func WireSize(size int64) (int64, error) {
	if size < 0 || size > MaxFileSize {
		return 0, invalid("invalid file size")
	}
	return size + frameCount(size)*FrameOverhead, nil
}

func frameCount(size int64) int64 {
	return max(1, (size+ChunkSize-1)/ChunkSize)
}

func newGCM(key []byte) (cipher.AEAD, error) {
	if len(key) != keySize {
		return nil, invalid("invalid key length")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, invalid("invalid key")
	}
	return cipher.NewGCM(block)
}

func encryptBytes(key, plain []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, nonceSize, nonceSize+len(plain)+aead.Overhead())
	if _, err = io.ReadFull(rand.Reader, out); err != nil {
		return nil, errors.New("secure nonce generation failed")
	}
	return aead.Seal(out, out[:nonceSize], plain, nil), nil
}

func decryptBytes(key, ciphertext []byte) ([]byte, error) {
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < nonceSize+aead.Overhead() {
		return nil, invalid("truncated encrypted data")
	}
	plain, err := aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], nil)
	if err != nil {
		return nil, invalid("authentication failed")
	}
	return plain, nil
}

func validUUID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range []byte(id) {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}

func fileContext(file protocol.ManifestFile) ([]byte, error) {
	if _, err := WireSize(file.Size); err != nil {
		return nil, err
	}
	if file.Encoding != "chunked-v1" || file.ChunkSize != ChunkSize {
		return nil, invalid("unsupported file framing")
	}
	if len(file.EncryptionID) != 32 || strings.ToLower(file.EncryptionID) != file.EncryptionID {
		return nil, invalid("invalid file encryption identity")
	}
	id, err := hex.DecodeString(file.EncryptionID)
	if err != nil {
		return nil, invalid("invalid file encryption identity")
	}
	return id, nil
}

// ValidateManifest verifies authenticated manifest semantics. Filename collision
// resolution and exclusive output creation belong to the destination writer.
func ValidateManifest(m protocol.Manifest) error {
	if len(m.Files) < 1 || len(m.Files) > maxFiles {
		return invalid("invalid manifest file count")
	}
	blobs := make(map[string]bool, len(m.Files))
	contexts := make(map[string]bool, len(m.Files))
	var total int64
	for _, file := range m.Files {
		if _, err := fileContext(file); err != nil {
			return err
		}
		if !utf8.ValidString(file.Name) || strings.TrimSpace(file.Name) == "" ||
			len(utf16.Encode([]rune(file.Name))) > 1024 || strings.ContainsAny(file.Name, "/\\\x00") ||
			file.Name == "." || file.Name == ".." || !utf8.ValidString(file.MIMEType) ||
			len(file.MIMEType) > 1024 || !validUUID(file.BlobID) {
			return invalid("invalid manifest metadata")
		}
		blob := strings.ToLower(file.BlobID)
		if blobs[blob] || contexts[file.EncryptionID] {
			return invalid("duplicate manifest identity")
		}
		blobs[blob], contexts[file.EncryptionID] = true, true
		if file.Size > MaxFileSize-total {
			return invalid("manifest size limit exceeded")
		}
		total += file.Size
	}
	return nil
}

func EncryptManifest(key []byte, m protocol.Manifest) ([]byte, error) {
	if err := ValidateManifest(m); err != nil {
		return nil, err
	}
	plain, err := json.Marshal(m)
	if err != nil {
		return nil, invalid("invalid manifest")
	}
	defer clear(plain)
	if len(plain) > MaxManifestBytes-nonceSize-16 {
		return nil, invalid("manifest size limit exceeded")
	}
	return encryptBytes(key, plain)
}

func DecryptManifest(key, ciphertext []byte) (protocol.Manifest, error) {
	var m protocol.Manifest
	if len(ciphertext) > MaxManifestBytes {
		return m, invalid("manifest size limit exceeded")
	}
	plain, err := decryptBytes(key, ciphertext)
	if err != nil {
		return m, err
	}
	defer clear(plain)
	if !utf8.Valid(plain) || json.Unmarshal(plain, &m) != nil {
		return protocol.Manifest{}, invalid("invalid manifest JSON")
	}
	if err := ValidateManifest(m); err != nil {
		return protocol.Manifest{}, err
	}
	return m, nil
}
