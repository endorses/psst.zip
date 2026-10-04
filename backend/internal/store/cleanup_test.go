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

func TestBoundedDeletionDirectorySwapCannotFollowSymlink(t *testing.T) {
	for _, nested := range []bool{false, true} {
		name := "resource swap"
		if nested {
			name = "nested directory swap"
		}
		t.Run(name, func(t *testing.T) {
			disk := testDisk(t)
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "sentinel"), []byte("outside"), 0600); err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(disk.basePath, "resource")
			entry := "resource"
			if nested {
				target = filepath.Join(target, "nested")
				entry = "nested"
			}
			if err := os.MkdirAll(target, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "original"), []byte("inside"), 0600); err != nil {
				t.Fatal(err)
			}
			swapped := false
			_, err := disk.deleteAllBounded(context.Background(), "resource", 256, func(_ *os.File, name string) {
				if swapped || name != entry {
					return
				}
				swapped = true
				if err := os.Rename(target, target+"-saved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, target); err != nil {
					t.Fatal(err)
				}
			})
			if err != nil || !swapped {
				t.Fatal("swap regression did not reach safe unlink", swapped, err)
			}
			if data, err := os.ReadFile(filepath.Join(outside, "sentinel")); err != nil || string(data) != "outside" {
				t.Fatal("cleanup followed replacement symlink", string(data), err)
			}
			if data, err := os.ReadFile(filepath.Join(target+"-saved", "original")); err != nil || string(data) != "inside" {
				t.Fatal("cleanup traversed replaced directory", string(data), err)
			}
		})
	}
}

func TestBoundedDeletionConfiguredRootReplacementIsRejected(t *testing.T) {
	disk := testDisk(t)
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(outside, "resource"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "resource", "sentinel"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(disk.basePath, disk.basePath+"-saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, disk.basePath); err != nil {
		t.Fatal(err)
	}
	if done, err := disk.DeleteAllBounded(context.Background(), "resource", 256); done || err == nil {
		t.Fatal("cleanup accepted symlinked storage root", done, err)
	}
	if data, err := os.ReadFile(filepath.Join(outside, "resource", "sentinel")); err != nil || string(data) != "outside" {
		t.Fatal("cleanup traversed replaced storage root", string(data), err)
	}
}

func TestBoundedDeletionDurabilityFailuresKeepWorkPending(t *testing.T) {
	for _, fault := range []string{"resource sync", "root sync", "resource close", "root close"} {
		t.Run(fault, func(t *testing.T) {
			disk := testDisk(t)
			if err := disk.Save("resource/file", strings.NewReader("payload")); err != nil {
				t.Fatal(err)
			}
			failure := errors.New("injected cleanup durability failure")
			hit := false
			fail := func(file *os.File, action string) error {
				name := file.Name()
				if name == "store" {
					name = "root"
				}
				if name+" "+action == fault {
					hit = true
					return failure
				}
				return nil
			}
			disk.ioHooks = &diskIOHooks{
				sync: func(file *os.File) error {
					if err := fail(file, "sync"); err != nil {
						return err
					}
					return file.Sync()
				},
				close: func(file *os.File) error {
					return errors.Join(fail(file, "close"), file.Close())
				},
			}
			if done, err := disk.DeleteAllBounded(context.Background(), "resource", 256); done || !errors.Is(err, failure) || !hit {
				t.Fatal("unproven cleanup acknowledged", done, err, hit)
			}
			disk.ioHooks = nil
			if done, err := disk.DeleteAllBounded(context.Background(), "resource", 256); !done || err != nil {
				t.Fatal("durability retry did not finish", done, err)
			}
			if _, err := os.Lstat(filepath.Join(disk.basePath, "resource")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("resource remains after durable retry", err)
			}
		})
	}
}

func TestBoundedDeletionBudgetDepthAndExactNames(t *testing.T) {
	disk := testDisk(t)
	if err := disk.Save("resource/file", strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{"", ".", "..", "../resource", "/resource", "resource/", "resource/../resource", "resource/./", "resource\\file"} {
		if done, err := disk.DeleteAllBounded(context.Background(), invalid, 256); done || err == nil {
			t.Fatal("inexact resource name accepted", invalid, done, err)
		}
	}
	for _, budget := range []int{-1, 0, 1025} {
		if done, err := disk.DeleteAllBounded(context.Background(), "resource", budget); done || err == nil {
			t.Fatal("invalid budget accepted", budget, done, err)
		}
	}
	for _, budget := range []int{1, 2} {
		if done, err := disk.DeleteAllBounded(context.Background(), "resource", budget); done || err != nil {
			t.Fatal("tiny budget unexpectedly removed a directory tree", budget, done, err)
		}
		if data, err := os.ReadFile(filepath.Join(disk.basePath, "resource", "file")); err != nil || string(data) != "payload" {
			t.Fatal("tiny budget exceeded shared enumeration/unlink limit", budget, string(data), err)
		}
	}
	deep := filepath.Join(disk.basePath, "deep")
	for range 34 {
		deep = filepath.Join(deep, "child")
	}
	if err := os.MkdirAll(deep, 0700); err != nil {
		t.Fatal(err)
	}
	if done, err := disk.DeleteAllBounded(context.Background(), "deep", 1024); done || err == nil || !strings.Contains(err.Error(), "too deep") {
		t.Fatal("cleanup depth limit ignored", done, err)
	}
}

func TestBoundedDeletionDetectsReplacementOfOpenedDirectory(t *testing.T) {
	disk := testDisk(t)
	if err := disk.Save("resource/file", strings.NewReader("original")); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "file"), []byte("outside"), 0600); err != nil {
		t.Fatal(err)
	}
	swapped := false
	done, err := disk.deleteAllBounded(context.Background(), "resource", 256, func(_ *os.File, name string) {
		if name != "file" || swapped {
			return
		}
		swapped = true
		resource := filepath.Join(disk.basePath, "resource")
		if err := os.Rename(resource, resource+"-saved"); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, resource); err != nil {
			t.Fatal(err)
		}
	})
	if done || !swapped || !errors.Is(err, errCleanupEntryChanged) {
		t.Fatal("changed opened directory acknowledged", done, swapped, err)
	}
	if data, err := os.ReadFile(filepath.Join(outside, "file")); err != nil || string(data) != "outside" {
		t.Fatal("opened-directory replacement redirected cleanup", string(data), err)
	}
}
