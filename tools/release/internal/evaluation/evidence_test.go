package evaluation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/endorses/psst.zip/tools/release/internal/containerbuild"
	"github.com/endorses/psst.zip/tools/release/internal/github"
	"github.com/endorses/psst.zip/tools/release/internal/scanning"
)

func TestNativeClaimsAreNeverUsedBeforeBothDescriptorsAuthenticate(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "native"), 0700)
	os.WriteFile(filepath.Join(root, "candidate-transfer.json"), []byte(`{"untrusted":"transfer"}`), 0600)
	os.WriteFile(filepath.Join(root, "native/native-artifacts.json"), []byte(`{"untrusted":"native-configurations"}`), 0600)
	for _, deny := range []int{1, 2} {
		calls, usedClaims := 0, false
		cache := &authenticationCache{verified: map[string]bool{}, paths: map[string][]byte{}, verify: func(_ context.Context, _ []byte) error {
			calls++
			if calls == deny {
				return errors.New("signature scope rejected")
			}
			return nil
		}}
		verify := func(context.Context, string, scanning.NativeIdentity, string) (containerbuild.TransferVerification, error) {
			usedClaims = true
			return containerbuild.TransferVerification{}, nil
		}
		_, err := authenticateNative(context.Background(), cache, verify, github.Binding{}, "linux/amd64", root)
		if err == nil || usedClaims || calls != deny {
			t.Fatal("untrusted descriptor reached the tree/claim consumer")
		}
	}
}
func TestNativeVerifierMustReturnTheAuthenticatedDescriptorBytes(t *testing.T) {
	root := t.TempDir()
	os.Mkdir(filepath.Join(root, "native"), 0700)
	metadata := []byte(`{"transfer":"snapshot"}`)
	descriptor := []byte(`{"descriptor":"snapshot"}`)
	os.WriteFile(filepath.Join(root, "candidate-transfer.json"), metadata, 0600)
	os.WriteFile(filepath.Join(root, "native/native-artifacts.json"), descriptor, 0600)
	cache := &authenticationCache{verified: map[string]bool{}, paths: map[string][]byte{}, verify: func(context.Context, []byte) error { return nil }}
	verify := func(_ context.Context, _ string, identity scanning.NativeIdentity, kind string) (containerbuild.TransferVerification, error) {
		if identity.Platform != "linux/amd64" || kind != "version-tag" {
			t.Fatal("verification scope changed")
		}
		return containerbuild.TransferVerification{MetadataRaw: metadata, DescriptorRaw: []byte(`{"descriptor":"substituted"}`)}, nil
	}
	_, err := authenticateNative(context.Background(), cache, verify, github.Binding{Repository: "endorses/psst.zip", Version: "v1.2.3", Commit: strings.Repeat("a", 40)}, "linux/amd64", root)
	if err == nil {
		t.Fatal("accepted descriptor path-read race after signature verification")
	}
}
func TestAuthenticationCacheSharesExactBytesAndRejectsChangedPaths(t *testing.T) {
	root := t.TempDir()
	one, two := filepath.Join(root, "one.json"), filepath.Join(root, "two.json")
	raw := []byte(`{"same":"verified-byte-snapshot"}`)
	os.WriteFile(one, raw, 0600)
	os.WriteFile(two, raw, 0600)
	calls := 0
	cache := &authenticationCache{verified: map[string]bool{}, paths: map[string][]byte{}, verify: func(context.Context, []byte) error { calls++; return nil }}
	for _, name := range []string{one, two, one} {
		if _, err := cache.file(context.Background(), name); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal("repeated one authenticated fact across identical inputs")
	}
	os.WriteFile(two, []byte(`{"changed":"input"}`), 0600)
	if err := cache.recheck(); err == nil {
		t.Fatal("accepted changed retained input after replay")
	}
	if _, err := cache.file(context.Background(), two); err == nil {
		t.Fatal("re-authenticated substituted path as if same measurement")
	}
}
