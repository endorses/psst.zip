package deployment

import (
	"archive/tar"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCheckpointCreationPolicyChecksumAndExportReceipt(t *testing.T) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		t.Skip("sqlite3 not installed")
	}
	for _, policy := range []string{"local-only", "off-host"} {
		t.Run(policy, func(t *testing.T) {
			root := t.TempDir()
			c := fixtureConfig(root)
			c.CheckpointProtection = policy
			hook := filepath.Join(root, "export-hook")
			c.CheckpointHook = &hook
			h := NewHost(c, filepath.Join(root, "state"), filepath.Join(root, "backups"))
			h.protect = func(string, bool, bool, bool) error { return nil }
			for _, name := range []string{c.Environment, "compose.yml", "Caddyfile"} {
				if err := os.WriteFile(filepath.Join(root, name), []byte("synthetic operator input\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			database := filepath.Join(root, "database.db")
			if out, err := exec.Command("sqlite3", database, "CREATE TABLE checkpoint(id INTEGER PRIMARY KEY);").CombinedOutput(); err != nil {
				t.Fatal(string(out), err)
			}
			id := "20261023T120000Z-000000000001"
			checkpoint := filepath.Join(root, id)
			images := Object{"backend": "sha256:" + strings.Repeat("a", 64), "caddy": "sha256:" + strings.Repeat("b", 64)}
			volumes := Object{}
			for key, name := range c.VolumeNames {
				service, dest, _ := strings.Cut(key, ":")
				volumes[name] = Object{"image": images[service], "mountpoint": root, "service": service, "destination": dest}
			}
			adopted := Object{"images": images, "volumes": volumes, "database": database}
			transaction := Object{"id": id, "prior_pause": false}
			exported := false
			plain := NewHost(c, h.State, h.Backups)
			h.command = func(ctx context.Context, input []byte, dst string, github bool, args ...string) ([]byte, error) {
				if args[0] == "sqlite3" {
					return plain.run(ctx, input, dst, github, args...)
				}
				if args[0] == hook {
					exported = true
					hash, err := fingerprint(filepath.Join(checkpoint, "checkpoint.json"))
					if err != nil {
						return nil, err
					}
					return json.Marshal(Object{"checkpoint_sha256": hash, "encrypted_off_host_receipt": "Encrypted synthetic checkpoint retained externally.", "restore_exercise": "Restored synthetic checkpoint bytes in isolated copy.", "verified_at": time.Now().UTC().Format(time.RFC3339Nano)})
				}
				if dst != "" {
					f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
					if err != nil {
						return nil, err
					}
					w := tar.NewWriter(f)
					if err = w.WriteHeader(&tar.Header{Name: "fixture", Typeflag: tar.TypeReg, Mode: 0o600, Size: 7}); err != nil {
						return nil, err
					}
					_, err = w.Write([]byte("payload"))
					if err == nil {
						err = w.Close()
					}
					closeErr := f.Close()
					if err != nil {
						return nil, err
					}
					return nil, closeErr
				}
				if len(args) > 2 && args[0] == "docker" && args[1] == "image" && args[2] == "tag" {
					return nil, nil
				}
				return nil, fmt.Errorf("unexpected checkpoint fixture command")
			}
			if err := h.Backup(context.Background(), fixtureCompose(), adopted, checkpoint, transaction); err != nil {
				t.Fatal(err)
			}
			record, err := h.VerifyBackup(checkpoint)
			if err != nil {
				t.Fatal(err)
			}
			if record["checkpoint_protection"] != policy || transaction["checkpoint_protection"] != policy || exported != (policy == "off-host") {
				t.Fatal(record, exported)
			}
			counts := map[string]int{}
			for _, row := range object(record["records"]) {
				counts[text(object(row)["kind"])]++
			}
			if counts["volume"] != 3 || counts["image"] != 2 || counts["configuration"] != 4 {
				t.Fatal(counts)
			}
			h.Config.CheckpointProtection = "off-host"
			if _, err = h.VerifyBackup(checkpoint); err != nil {
				t.Fatal("creation policy changed with host policy", err)
			}
			if policy == "off-host" {
				receiptPath := filepath.Join(checkpoint, "off-host-receipt.json")
				receipt, err := load(receiptPath)
				if err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"checkpoint_sha256", "restore_exercise", "verified_at"} {
					bad := clone(receipt)
					bad[key] = "invalid"
					if err = verifyCheckpointReceipt(checkpoint, bad, true, time.Now()); err == nil {
						t.Fatalf("bad receipt %s accepted", key)
					}
				}
				if err = os.Remove(receiptPath); err != nil {
					t.Fatal(err)
				}
				if _, err = h.VerifyBackup(checkpoint); err == nil {
					t.Fatal("missing off-host receipt accepted")
				}
				if err = atomicJSON(receiptPath, receipt); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.WriteFile(filepath.Join(checkpoint, "image-0.tar"), []byte("corrupted checkpoint"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err = h.VerifyBackup(checkpoint); err == nil {
				t.Fatal("checkpoint corruption accepted")
			}
		})
	}
}
