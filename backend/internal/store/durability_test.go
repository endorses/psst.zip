package store

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func testDisk(t *testing.T) *DiskStore {
	t.Helper()
	disk, err := NewDiskStore(filepath.Join(t.TempDir(), "store"))
	if err != nil {
		t.Fatal(err)
	}
	return disk
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func partialReader(err error) io.Reader {
	return io.MultiReader(strings.NewReader("partial"), errorReader{err})
}

func TestDurablePartialWriteAndDirectoryPublicationOrder(t *testing.T) {
	disk := testDisk(t)
	var calls []string
	role := func(file *os.File) string {
		if file.Name() == "transfer" {
			return "parent"
		}
		if file.Name() == "store" {
			return "root"
		}
		return "file"
	}
	disk.ioHooks = &diskIOHooks{
		sync: func(file *os.File) error {
			calls = append(calls, role(file)+".sync")
			return file.Sync()
		},
		close: func(file *os.File) error {
			calls = append(calls, role(file)+".close")
			return file.Close()
		},
	}
	inputErr := errors.New("input interrupted")
	n, err := disk.SaveAt("transfer/file", partialReader(inputErr), 0)
	if n != 7 || !errors.Is(err, inputErr) {
		t.Fatalf("durable partial bytes: n=%d err=%v", n, err)
	}
	want := []string{"file.sync", "file.close", "parent.sync", "parent.close", "root.sync", "root.close"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("publication order %v, want %v", calls, want)
	}
	disk.ioHooks = nil
	if info, err := disk.Inspect("transfer/file"); err != nil || !info.Exists || info.Size != 7 {
		t.Fatalf("partial payload: %+v %v", info, err)
	}
	if n, err := disk.SaveAt("transfer/file", strings.NewReader("-resumed"), 7); n != 8 || err != nil {
		t.Fatal(n, err)
	}
	reader, err := disk.Load("transfer/file")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil || string(data) != "partial-resumed" {
		t.Fatalf("resumed data %q: %v %v", data, err, closeErr)
	}
}

func TestDurabilityFailuresNeverAcknowledgeUnprovenBytes(t *testing.T) {
	for _, operation := range []string{"save", "saveAt", "partialSaveAt", "truncate"} {
		for _, fault := range []string{"file.sync", "file.close", "parent.sync", "parent.close", "root.sync", "root.close"} {
			t.Run(operation+"/"+fault, func(t *testing.T) {
				disk := testDisk(t)
				if err := disk.Save("transfer/file", strings.NewReader("old payload")); err != nil {
					t.Fatal(err)
				}
				faultErr := errors.New("injected durability failure")
				inputErr := errors.New("input interrupted")
				hit := false
				stage := func(file *os.File, action string) error {
					role := "file"
					if file.Name() == "transfer" {
						role = "parent"
					} else if file.Name() == "store" {
						role = "root"
					}
					if role+"."+action == fault {
						hit = true
						return faultErr
					}
					return nil
				}
				disk.ioHooks = &diskIOHooks{
					sync: func(file *os.File) error {
						if err := stage(file, "sync"); err != nil {
							return err
						}
						return file.Sync()
					},
					close: func(file *os.File) error {
						// Always release the real descriptor, even when injecting
						// the filesystem's close-time error.
						return errors.Join(stage(file, "close"), file.Close())
					},
				}
				var err error
				var n int64
				switch operation {
				case "save":
					err = disk.Save("transfer/file", strings.NewReader("replacement"))
				case "saveAt":
					n, err = disk.SaveAt("transfer/file", strings.NewReader("replacement"), 0)
				case "partialSaveAt":
					n, err = disk.SaveAt("transfer/file", partialReader(inputErr), 0)
				case "truncate":
					err = disk.Truncate("transfer/file", 3)
				}
				if !hit || !errors.Is(err, faultErr) || n != 0 {
					t.Fatalf("unproven bytes acknowledged: hit=%v n=%d err=%v", hit, n, err)
				}
				if operation == "partialSaveAt" && !errors.Is(err, inputErr) {
					t.Fatalf("lost input error: %v", err)
				}
			})
		}
	}
}

func TestInspectMissingEmptyAndRegularPayloads(t *testing.T) {
	disk := testDisk(t)
	if info, err := disk.Inspect("missing/file"); err != nil || info.Exists || info.Size != 0 {
		t.Fatal(info, err)
	}
	if err := disk.Save("transfer/empty", strings.NewReader("")); err != nil {
		t.Fatal(err)
	}
	if info, err := disk.Inspect("transfer/empty"); err != nil || !info.Exists || info.Size != 0 {
		t.Fatal(info, err)
	}
	if info, err := disk.Inspect("transfer/missing"); err != nil || info.Exists {
		t.Fatal(info, err)
	}
	if err := disk.Save("transfer/file", strings.NewReader("payload")); err != nil {
		t.Fatal(err)
	}
	if err := disk.Truncate("transfer/file", 3); err != nil {
		t.Fatal(err)
	}
	if info, err := disk.Inspect("transfer/file"); err != nil || !info.Exists || info.Size != 3 {
		t.Fatal(info, err)
	}
}

func TestPayloadOperationsRejectTraversalSymlinksAndSpecialFiles(t *testing.T) {
	disk := testDisk(t)
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "keep")
	if err := os.WriteFile(outsideFile, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(disk.basePath, "transfer"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{"file-link": outsideFile, "missing-link": filepath.Join(outside, "missing")} {
		if err := os.Symlink(target, filepath.Join(disk.basePath, "transfer", name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(disk.basePath, "directory-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(disk.basePath, "transfer", "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(disk.basePath, "transfer", "fifo"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"", ".", "..", "single", "../keep", "transfer/../keep", "/transfer/file", "transfer//file", "transfer/", "transfer/.", "transfer/..", "transfer/file/extra", "transfer/\\keep", "transfer/file-link", "transfer/missing-link", "directory-link/keep", "transfer/directory", "transfer/fifo"} {
		t.Run(key, func(t *testing.T) {
			if info, err := disk.Inspect(key); err == nil {
				t.Fatalf("unsafe inspect: %+v", info)
			}
			if reader, err := disk.Load(key); err == nil {
				_ = reader.Close()
				t.Fatal("unsafe load")
			}
			if err := disk.Save(key, strings.NewReader("bad")); err == nil {
				t.Fatal("unsafe save")
			}
			if n, err := disk.SaveAt(key, strings.NewReader("bad"), 0); err == nil || n != 0 {
				t.Fatal("unsafe write", n, err)
			}
			if err := disk.Truncate(key, 0); err == nil {
				t.Fatal("unsafe truncate")
			}
		})
	}
	data, err := os.ReadFile(outsideFile)
	if err != nil || string(data) != "untouched" {
		t.Fatal("outside payload changed", string(data), err)
	}
	if _, err := os.Stat(filepath.Join(outside, "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("followed dangling symlink", err)
	}
}

func TestConfiguredStorePathRejectsSymlinkComponents(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDiskStore(filepath.Join(root, "linked", "new-store")); err == nil {
		t.Fatal("constructor followed configured symlink")
	}
	if _, err := os.Stat(filepath.Join(outside, "new-store")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("constructor modified symlink target", err)
	}
	disk, err := NewDiskStore(filepath.Join(root, "store"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(disk.basePath, filepath.Join(root, "previous")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, disk.basePath); err != nil {
		t.Fatal(err)
	}
	if _, err := disk.Inspect("transfer/file"); err == nil {
		t.Fatal("inspection followed replaced storage root")
	}
	if err := disk.Save("transfer/file", strings.NewReader("bad")); err == nil {
		t.Fatal("write followed replaced storage root")
	}
}
