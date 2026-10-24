// Package evaluation authenticates native facts before substantive source and
// scanner replay, then produces one unsigned-to-be-attested publication result.
package evaluation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
	"github.com/endorses/psst.zip/tools/release/internal/github"
)

const jsonLimit int64 = 16 << 20

func digest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func read(name string) ([]byte, error) {
	info, err := os.Lstat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > jsonLimit {
		return nil, fmt.Errorf("evaluation evidence must be a bounded regular JSON file")
	}
	file, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, fmt.Errorf("evaluation evidence changed before read")
	}
	raw, err := io.ReadAll(io.LimitReader(file, jsonLimit+1))
	after, statErr := file.Stat()
	if err != nil || statErr != nil || int64(len(raw)) != info.Size() || after.Size() != info.Size() || !after.ModTime().Equal(info.ModTime()) {
		return nil, fmt.Errorf("evaluation evidence changed or exceeds bounds")
	}
	return raw, nil
}

type authenticationCache struct {
	verify   func(context.Context, []byte) error
	verified map[string]bool
	paths    map[string][]byte
}

func newCache(ctx context.Context, client github.Client, binding github.Binding) *authenticationCache {
	return &authenticationCache{verify: func(ctx context.Context, raw []byte) error { return client.VerifyBytes(ctx, raw, binding, nil) }, verified: map[string]bool{}, paths: map[string][]byte{}}
}
func (c *authenticationCache) bytes(ctx context.Context, raw []byte) error {
	if len(raw) == 0 || int64(len(raw)) > jsonLimit {
		return fmt.Errorf("authenticated evaluation evidence exceeds bounds")
	}
	sha := digest(raw)
	if c.verified[sha] {
		return nil
	}
	if err := c.verify(ctx, raw); err != nil {
		return err
	}
	c.verified[sha] = true
	return nil
}
func (c *authenticationCache) file(ctx context.Context, name string) ([]byte, error) {
	raw, err := read(name)
	if err != nil {
		return nil, err
	}
	if previous, ok := c.paths[name]; ok && !bytes.Equal(previous, raw) {
		return nil, fmt.Errorf("authenticated evidence path changed")
	}
	if err := c.bytes(ctx, raw); err != nil {
		return nil, err
	}
	c.paths[name] = raw
	return raw, nil
}
func (c *authenticationCache) recheck() error {
	for name, raw := range c.paths {
		current, err := read(name)
		if err != nil || !bytes.Equal(current, raw) {
			return fmt.Errorf("authenticated evaluation input changed during replay")
		}
	}
	return nil
}

func writeNew(directory, name string, value any) (string, error) {
	raw, err := contract.JSON(value)
	if err != nil || len(raw) == 0 || int64(len(raw)) > jsonLimit {
		return "", fmt.Errorf("evaluation output exceeds bounds")
	}
	file, err := os.OpenFile(filepath.Join(directory, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", err
	}
	_, err = file.Write(raw)
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	return digest(raw), nil
}
