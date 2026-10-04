package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func inventoryTestStore(t *testing.T) *DiskStore {
	t.Helper()
	d, err := NewDiskStore(filepath.Join(t.TempDir(), "payloads"))
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func inventoryTestWrite(t *testing.T, d *DiskStore, key string) {
	t.Helper()
	if err := d.Save(key, strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}
}
func inventoryTestAll(t *testing.T, d *DiskStore, directory string, budget int) []InventoryEntry {
	t.Helper()
	var cursor InventoryCursor
	var entries []InventoryEntry
	for pass := 0; pass < 1000; pass++ {
		page, err := d.InventoryDirectory(context.Background(), directory, cursor, budget)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Entries) > budget || page.Unstable {
			t.Fatalf("invalid page %+v", page)
		}
		entries = append(entries, page.Entries...)
		if page.Done {
			return entries
		}
		if page.Next.Cookie == cursor.Cookie {
			t.Fatal("cursor stalled")
		}
		data, err := json.Marshal(page.Next)
		if err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, &cursor); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("inventory never terminated")
	return nil
}
func inventoryTestEntry(t *testing.T, d *DiskStore, directory, name string) InventoryEntry {
	t.Helper()
	for _, entry := range inventoryTestAll(t, d, directory, 64) {
		if entry.Name == name {
			return entry
		}
	}
	t.Fatalf("entry %q missing", name)
	return InventoryEntry{}
}
func TestInventoryBoundedMultipage(t *testing.T) {
	d := inventoryTestStore(t)
	for i := 0; i < 137; i++ {
		inventoryTestWrite(t, d, fmt.Sprintf("transfer/file-%03d", i))
	}
	inventoryTestWrite(t, d, "transfer/"+strings.Repeat("z", 255))
	for _, budget := range []int{1, 2, 7, 64} {
		t.Run(fmt.Sprint(budget), func(t *testing.T) {
			entries := inventoryTestAll(t, d, "transfer", budget)
			if len(entries) != 138 {
				t.Fatalf("got %d", len(entries))
			}
			seen := map[string]bool{}
			for _, entry := range entries {
				if seen[entry.Name] || entry.Unsupported || entry.Directory != "transfer" || entry.File.Size != 7 || entry.File.Inode == 0 {
					t.Fatalf("bad entry %+v", entry)
				}
				seen[entry.Name] = true
			}
		})
	}
	for _, budget := range []int{0, 65} {
		if _, err := d.InventoryDirectory(context.Background(), "", InventoryCursor{}, budget); err == nil {
			t.Fatal("invalid budget accepted")
		}
	}
	for _, name := range []string{"..", "a/b", "/bad"} {
		if _, err := d.InventoryDirectory(context.Background(), name, InventoryCursor{}, 1); !errors.Is(err, ErrInventoryUnsupported) {
			t.Fatalf("invalid directory: %v", err)
		}
	}
}
func TestInventoryChangedDirectoryContinuesAndReplacementResets(t *testing.T) {
	d := inventoryTestStore(t)
	for i := 0; i < 20; i++ {
		inventoryTestWrite(t, d, fmt.Sprintf("transfer/file-%02d", i))
	}
	page, err := d.InventoryDirectory(context.Background(), "transfer", InventoryCursor{}, 3)
	if err != nil {
		t.Fatal(err)
	}
	original := page.Next
	inventoryTestWrite(t, d, "transfer/added")
	page, err = d.InventoryDirectory(context.Background(), "transfer", original, 3)
	if err != nil || !page.Unstable || !page.Next.Unstable || page.Next.Cookie == original.Cookie {
		t.Fatalf("stamp drift stalled %+v %v", page, err)
	}
	for i := 0; i < 100 && !page.Done; i++ {
		page, err = d.InventoryDirectory(context.Background(), "transfer", page.Next, 3)
		if err != nil || !page.Unstable {
			t.Fatalf("lost instability %+v %v", page, err)
		}
	}
	if !page.Done {
		t.Fatal("unstable pass never finished")
	}
	if err := os.Rename(filepath.Join(d.basePath, "transfer"), filepath.Join(d.basePath, "old")); err != nil {
		t.Fatal(err)
	}
	inventoryTestWrite(t, d, "transfer/new")
	page, err = d.InventoryDirectory(context.Background(), "transfer", original, 3)
	if !errors.Is(err, ErrInventoryChanged) || page.Next.Begun || !page.Unstable {
		t.Fatalf("replacement not invalidated %+v %v", page, err)
	}
}
func TestInventoryUnsupportedEntriesNeverFollowed(t *testing.T) {
	d := inventoryTestStore(t)
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	inventoryTestWrite(t, d, "transfer/live")
	for _, path := range []string{"root-link", "transfer/link"} {
		if err := os.Symlink(outside, filepath.Join(d.basePath, path)); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{"transfer/nested", "bad name"} {
		if err := os.Mkdir(filepath.Join(d.basePath, path), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(d.basePath, "unexpected"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{"", "root-link"}, {"", "unexpected"}, {"", "bad name"}, {"transfer", "link"}, {"transfer", "nested"}} {
		entry := inventoryTestEntry(t, d, pair[0], pair[1])
		if !entry.Unsupported {
			t.Fatalf("supported %+v", entry)
		}
		if done, err := d.RemoveOrphan(context.Background(), entry); done || !errors.Is(err, ErrInventoryUnsupported) {
			t.Fatalf("unsupported deletion %v %v", done, err)
		}
	}
	if _, err := d.InventoryDirectory(context.Background(), "root-link", InventoryCursor{}, 64); err == nil {
		t.Fatal("followed symlink")
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
		t.Fatalf("sentinel lost %q %v", got, err)
	}
}
func TestRemoveOrphanExactFileAndEmptyDirectoryOnly(t *testing.T) {
	d := inventoryTestStore(t)
	inventoryTestWrite(t, d, "transfer/orphan")
	inventoryTestWrite(t, d, "transfer/live")
	orphan := inventoryTestEntry(t, d, "transfer", "orphan")
	directory := inventoryTestEntry(t, d, "", "transfer")
	if done, err := d.RemoveOrphan(context.Background(), directory); done || err != nil {
		t.Fatalf("nonempty dir %v %v", done, err)
	}
	inventoryTestWrite(t, d, "transfer/another")
	if done, err := d.RemoveOrphan(context.Background(), orphan); !done || err != nil {
		t.Fatalf("orphan removal %v %v", done, err)
	}
	if _, err := os.Stat(filepath.Join(d.basePath, "transfer", "live")); err != nil {
		t.Fatal(err)
	}
	if done, err := d.RemoveOrphan(context.Background(), directory); done || !errors.Is(err, ErrInventoryChanged) {
		t.Fatalf("changed dir accepted %v %v", done, err)
	}
	if err := os.Mkdir(filepath.Join(d.basePath, "empty"), 0700); err != nil {
		t.Fatal(err)
	}
	empty := inventoryTestEntry(t, d, "", "empty")
	if done, err := d.RemoveOrphan(context.Background(), empty); !done || err != nil {
		t.Fatalf("empty dir removal %v %v", done, err)
	}
	if _, err := os.Stat(filepath.Join(d.basePath, "empty")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("empty directory remains")
	}
}
func TestRemoveOrphanRejectsChangedTargetsAndAssociations(t *testing.T) {
	for _, change := range []string{"file", "parent", "root", "symlink"} {
		t.Run(change, func(t *testing.T) {
			d := inventoryTestStore(t)
			inventoryTestWrite(t, d, "transfer/orphan")
			expected := inventoryTestEntry(t, d, "transfer", "orphan")
			outside := t.TempDir()
			if err := os.WriteFile(filepath.Join(outside, "orphan"), []byte("sentinel"), 0600); err != nil {
				t.Fatal(err)
			}
			done, err := d.removeOrphan(context.Background(), expected, func() {
				switch change {
				case "file":
					inventoryTestWrite(t, d, "transfer/orphan")
				case "parent", "symlink":
					if err := os.Rename(filepath.Join(d.basePath, "transfer"), filepath.Join(d.basePath, "old")); err != nil {
						t.Fatal(err)
					}
					if change == "parent" {
						inventoryTestWrite(t, d, "transfer/orphan")
					} else if err := os.Symlink(outside, filepath.Join(d.basePath, "transfer")); err != nil {
						t.Fatal(err)
					}
				case "root":
					if err := os.Rename(d.basePath, d.basePath+"-old"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(d.basePath, 0700); err != nil {
						t.Fatal(err)
					}
				}
			})
			if done || !errors.Is(err, ErrInventoryChanged) {
				t.Fatalf("changed association deletion %v %v", done, err)
			}
			if got, err := os.ReadFile(filepath.Join(outside, "orphan")); err != nil || string(got) != "sentinel" {
				t.Fatalf("sentinel lost %q %v", got, err)
			}
		})
	}
}
func TestRemoveOrphanDurableRetriesAndCancellation(t *testing.T) {
	for _, failure := range []string{"sync", "close"} {
		t.Run(failure, func(t *testing.T) {
			d := inventoryTestStore(t)
			inventoryTestWrite(t, d, "transfer/orphan")
			expected := inventoryTestEntry(t, d, "transfer", "orphan")
			injected := errors.New("injected " + failure)
			syncs := 0
			d.ioHooks = &diskIOHooks{sync: func(f *os.File) error {
				syncs++
				if failure == "sync" {
					return injected
				}
				return f.Sync()
			}, close: func(f *os.File) error {
				err := f.Close()
				if failure == "close" && f.Name() == "transfer" {
					return errors.Join(err, injected)
				}
				return err
			}}
			if done, err := d.RemoveOrphan(context.Background(), expected); done || !errors.Is(err, injected) {
				t.Fatalf("durability acknowledged %v %v", done, err)
			}
			if _, err := os.Stat(filepath.Join(d.basePath, "transfer", "orphan")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("expected physical unlink")
			}
			d.ioHooks = &diskIOHooks{sync: func(f *os.File) error { syncs++; return f.Sync() }}
			previous := syncs
			if done, err := d.RemoveOrphan(context.Background(), expected); !done || err != nil || syncs <= previous {
				t.Fatalf("missing retry not synced %v %v", done, err)
			}
		})
	}
	d := inventoryTestStore(t)
	inventoryTestWrite(t, d, "transfer/orphan")
	expected := inventoryTestEntry(t, d, "transfer", "orphan")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if done, err := d.RemoveOrphan(ctx, expected); done || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled removal %v %v", done, err)
	}
	if _, err := d.InventoryDirectory(ctx, "", InventoryCursor{}, 64); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled inventory %v", err)
	}
	if _, err := os.Stat(filepath.Join(d.basePath, "transfer", "orphan")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(d.basePath, "transfer")); err != nil {
		t.Fatal(err)
	}
	synced := false
	d.ioHooks = &diskIOHooks{sync: func(f *os.File) error { synced = true; return f.Sync() }}
	if done, err := d.RemoveOrphan(context.Background(), expected); !done || err != nil || !synced {
		t.Fatalf("missing parent retry %v %v sync=%v", done, err, synced)
	}
}

func TestInventoryCloseFailureCannotCompletePass(t *testing.T) {
	d := inventoryTestStore(t)
	injected := errors.New("close failed")
	d.ioHooks = &diskIOHooks{close: func(f *os.File) error { return errors.Join(f.Close(), injected) }}
	page, err := d.InventoryDirectory(context.Background(), "", InventoryCursor{}, 64)
	if page.Done || !errors.Is(err, injected) {
		t.Fatalf("failed close completed pass %+v %v", page, err)
	}
}

func TestRemoveOrphanChangedFileSymlinkNeverDeletesTarget(t *testing.T) {
	d := inventoryTestStore(t)
	inventoryTestWrite(t, d, "transfer/orphan")
	expected := inventoryTestEntry(t, d, "transfer", "orphan")
	sentinel := filepath.Join(t.TempDir(), "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	done, err := d.removeOrphan(context.Background(), expected, func() {
		path := filepath.Join(d.basePath, "transfer", "orphan")
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(sentinel, path); err != nil {
			t.Fatal(err)
		}
	})
	if done || !errors.Is(err, ErrInventoryChanged) {
		t.Fatalf("changed file removal %v %v", done, err)
	}
	if got, err := os.ReadFile(sentinel); err != nil || string(got) != "keep" {
		t.Fatalf("sentinel lost %q %v", got, err)
	}
	if _, err := os.Lstat(filepath.Join(d.basePath, "transfer", "orphan")); err != nil {
		t.Fatal("replacement symlink removed")
	}
}

func TestRemoveOrphanCanceledBeforeMutation(t *testing.T) {
	d := inventoryTestStore(t)
	inventoryTestWrite(t, d, "transfer/orphan")
	expected := inventoryTestEntry(t, d, "transfer", "orphan")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if done, err := d.removeOrphan(ctx, expected, cancel); done || !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled before unlink %v %v", done, err)
	}
	if _, err := os.Stat(filepath.Join(d.basePath, "transfer", "orphan")); err != nil {
		t.Fatal("canceled candidate removed")
	}
}
