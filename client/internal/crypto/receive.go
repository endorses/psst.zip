package crypto

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	"github.com/cloudflare/circl/hpke"
	"github.com/endorses/psst.zip/client/protocol"
)

const receiveMagic = "PSSTRCV2"
const receiveDomain = "psst.zip/receive-key/v2\x00"
const receiveWrappedSize = 80
const receivePrefixSize = len(receiveMagic) + receiveWrappedSize

var receiveSuite = hpke.NewSuite(hpke.KEM_X25519_HKDF_SHA256, hpke.KDF_HKDF_SHA256, hpke.AEAD_AES256GCM)

func GenerateReceiveKey() (private, public []byte, err error) {
	pub, secret, err := hpke.KEM_X25519_HKDF_SHA256.Scheme().GenerateKeyPair()
	if err != nil {
		return nil, nil, invalid("receive key generation failed")
	}
	private, err = secret.MarshalBinary()
	if err != nil {
		return nil, nil, invalid("receive key serialization failed")
	}
	public, err = pub.MarshalBinary()
	if err != nil || len(private) != 32 || len(public) != 32 {
		clear(private)
		return nil, nil, invalid("receive key serialization failed")
	}
	return private, public, nil
}

func receiveInfo(slot, transfer string, public []byte) ([]byte, error) {
	if !validUUID(slot) || !validUUID(transfer) || len(public) != 32 {
		return nil, invalid("invalid receive identity")
	}
	slotBytes, _ := hex.DecodeString(strings.ReplaceAll(slot, "-", ""))
	transferBytes, _ := hex.DecodeString(strings.ReplaceAll(transfer, "-", ""))
	info := append([]byte(receiveDomain), 0, 32, 0, 1, 0, 2)
	info = append(info, slotBytes...)
	info = append(info, transferBytes...)
	return append(info, public...), nil
}

func sealSubmissionKey(slot, transfer string, public, key []byte) ([]byte, error) {
	if len(key) != keySize {
		return nil, invalid("invalid submission key length")
	}
	info, err := receiveInfo(slot, transfer, public)
	if err != nil {
		return nil, err
	}
	pub, err := hpke.KEM_X25519_HKDF_SHA256.Scheme().UnmarshalBinaryPublicKey(public)
	if err != nil {
		return nil, invalid("invalid receive public key")
	}
	sender, err := receiveSuite.NewSender(pub, info)
	if err != nil {
		return nil, invalid("receive encryption initialization failed")
	}
	enc, sealer, err := sender.Setup(rand.Reader)
	if err != nil {
		return nil, invalid("receive encryption initialization failed")
	}
	ciphertext, err := sealer.Seal(key, nil)
	if err != nil || len(enc) != 32 || len(ciphertext) != 48 {
		return nil, invalid("receive encryption failed")
	}
	return append(enc, ciphertext...), nil
}

func openSubmissionKey(slot, transfer string, private, wrapped []byte) ([]byte, error) {
	if len(private) != 32 || len(wrapped) != receiveWrappedSize {
		return nil, invalid("invalid receive key framing")
	}
	secret, err := hpke.KEM_X25519_HKDF_SHA256.Scheme().UnmarshalBinaryPrivateKey(private)
	if err != nil {
		return nil, invalid("invalid receive private key")
	}
	public, err := secret.Public().MarshalBinary()
	if err != nil {
		return nil, invalid("invalid receive private key")
	}
	info, err := receiveInfo(slot, transfer, public)
	if err != nil {
		return nil, err
	}
	receiver, err := receiveSuite.NewReceiver(secret, info)
	if err != nil {
		return nil, invalid("receive decryption initialization failed")
	}
	opener, err := receiver.Setup(wrapped[:32])
	if err != nil {
		return nil, invalid("receive decryption initialization failed")
	}
	key, err := opener.Open(wrapped[32:], nil)
	if err != nil || len(key) != keySize {
		clear(key)
		return nil, invalid("receive key authentication failed")
	}
	return key, nil
}

// SealReceiveManifest wraps a submission key with a fresh one-shot HPKE Base
// context and encrypts its manifest. A resumable upload must persist/reuse the
// returned envelope, rather than generating a replacement during retries.
func SealReceiveManifest(slot, transfer string, public, key []byte, m protocol.Manifest) ([]byte, error) {
	ciphertext, err := EncryptManifest(key, m)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) > MaxManifestBytes-receivePrefixSize {
		return nil, invalid("receive envelope size limit exceeded")
	}
	wrapped, err := sealSubmissionKey(slot, transfer, public, key)
	if err != nil {
		return nil, err
	}
	envelope := make([]byte, 0, receivePrefixSize+len(ciphertext))
	envelope = append(envelope, receiveMagic...)
	envelope = append(envelope, wrapped...)
	return append(envelope, ciphertext...), nil
}

func OpenReceiveManifest(slot, transfer string, private, envelope []byte) ([]byte, protocol.Manifest, error) {
	var m protocol.Manifest
	if len(envelope) < receivePrefixSize+nonceSize+16 || len(envelope) > MaxManifestBytes ||
		string(envelope[:len(receiveMagic)]) != receiveMagic {
		return nil, m, invalid("invalid or unsupported receive envelope")
	}
	key, err := openSubmissionKey(slot, transfer, private, envelope[len(receiveMagic):receivePrefixSize])
	if err != nil {
		return nil, m, err
	}
	m, err = DecryptManifest(key, envelope[receivePrefixSize:])
	if err != nil {
		clear(key)
		return nil, protocol.Manifest{}, err
	}
	return key, m, nil
}
