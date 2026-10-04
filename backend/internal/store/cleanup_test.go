package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBoundedDiskDeletionProgressAndIsolation(t *testing.T) {
	root := t.TempDir()
	disk, err := NewDiskStore(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		if err := disk.Save(fmt.Sprintf("resource/%03d", i), strings.NewReader("payload")); err != nil {
			t.Fatal(err)
		}
	}
	if err := disk.Save("resource-sibling/keep", strings.NewReader("keep")); err != nil {
		t.Fatal(err)
	}
	done, err := disk.DeleteAllBounded(context.Background(), "resource", 256)
	if err != nil || done {
		t.Fatal("large directory wasn't incremental", done, err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "store", "resource"))
	if err != nil || len(entries) < 172 || len(entries) >= 300 {
		t.Fatal("deletion budget violated", len(entries), err)
	}
	for attempt := 0; !done && attempt < 20; attempt++ {
		done, err = disk.DeleteAllBounded(context.Background(), "resource", 256)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !done {
		t.Fatal("bounded deletion did not finish")
	}
	if size, err := disk.Size("resource-sibling/keep"); err != nil || size != 4 {
		t.Fatal("sibling removed", size, err)
	}
	done, err = disk.DeleteAllBounded(context.Background(), "resource", 256)
	if err != nil || !done {
		t.Fatal("missing directory is not idempotent", done, err)
	}
}
func TestBoundedDiskDeletionCancellationSymlinksAndTraversal(t *testing.T) {
	root := t.TempDir()
	disk, err := NewDiskStore(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(root, "outside")
	if err := os.Mkdir(outside, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := disk.Save("resource/keep", strings.NewReader("inside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "store", "resource", "external")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if done, err := disk.DeleteAllBounded(ctx, "resource", 256); done || !errors.Is(err, context.Canceled) {
		t.Fatal(done, err)
	}
	if size, err := disk.Size("resource/keep"); err != nil || size != 6 {
		t.Fatal(size, err)
	}
	for _, invalid := range []string{"", ".", "..", "../outside", "resource/keep"} {
		if _, err := disk.DeleteAllBounded(context.Background(), invalid, 256); err == nil {
			t.Fatal("unsafe resource accepted", invalid)
		}
	}
	done, err := disk.DeleteAllBounded(context.Background(), "resource", 256)
	if err != nil || !done {
		t.Fatal(done, err)
	}
	if data, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(data) != "keep" {
		t.Fatal("followed symlink", err)
	}
}
