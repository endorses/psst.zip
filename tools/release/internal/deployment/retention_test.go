package deployment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScopedRetentionProtectsForeignAliasesAndStoppedContainers(t *testing.T) {
	u, f := newFixture(t)
	u.protect = func(string, bool, bool, bool) error { return nil }
	f.images = map[string]ImageFacts{}
	f.containers = set()
	var current Object
	ids := []string{}
	for i := 1; i <= 5; i++ {
		id := fmt.Sprintf("20261023T12000%dZ-00000000000%d", i, i)
		ids = append(ids, id)
		image := "sha256:" + strings.Repeat(fmt.Sprint(i), 64)
		images := Object{"backend": image, "caddy": image}
		dir := filepath.Join(u.State, "transactions", id)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		checkpoint := filepath.Join(u.Backups, id)
		if err := os.Mkdir(checkpoint, 0o700); err != nil {
			t.Fatal(err)
		}
		compose := fixtureCompose()
		for _, row := range object(compose["services"]) {
			object(row)["image"] = image
		}
		record := Object{"schema_version": 1, "id": id, "version": "v1.2.3", "phase": "completed", "checkpoint": checkpoint, "adopted": Object{"images": images}, "restoring": false}
		if err := atomicJSON(filepath.Join(dir, "transaction.json"), record); err != nil {
			t.Fatal(err)
		}
		if err := atomicJSON(filepath.Join(dir, "public.compose.json"), compose); err != nil {
			t.Fatal(err)
		}
		if err := atomicJSON(filepath.Join(checkpoint, "checkpoint.json"), Object{"transaction": id, "images": images}); err != nil {
			t.Fatal(err)
		}
		tag := "psst-checkpoint-" + strings.ToLower(id) + ":0"
		facts := ImageFacts{Tags: set(tag), Digests: set()}
		if i == 2 {
			facts.Tags["operator-owned:keep"] = true
		}
		if i == 3 {
			f.containers[image] = true
		}
		f.images[image] = facts
		if i == 5 {
			current = record
			if err := atomicJSON(filepath.Join(u.State, "active.json"), Object{"transaction": id, "version": "v1.2.3", "compose": compose}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := u.write(current, "completed"); err != nil {
		t.Fatal(err)
	}
	if err := u.Retention(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) != 2 || f.removed[0] != "psst-checkpoint-"+strings.ToLower(ids[0])+":0" || f.removed[1] != "sha256:"+strings.Repeat("1", 64) {
		t.Fatalf("removed foreign or retained image: %v", f.removed)
	}
	for i, id := range ids {
		_, err := os.Stat(filepath.Join(u.Backups, id))
		if (err == nil) != (i >= 3) {
			t.Fatalf("checkpoint %d existence: %v", i, err)
		}
	}
	r, err := u.Read()
	if err != nil || object(r["cleanup"])["status"] != "completed" {
		t.Fatal(r, err)
	}
}
func TestRetentionUnknownCheckpointFailsWithoutDeletion(t *testing.T) {
	u, f := newFixture(t)
	u.Config.VerificationProfile = "runtime-v1"
	r, err := u.Update(context.Background(), "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	u.protect = func(string, bool, bool, bool) error { return nil }
	if err = os.Mkdir(filepath.Join(u.Backups, "untracked"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err = u.Retention(context.Background()); err == nil {
		t.Fatal("untracked checkpoint removed")
	}
	if len(f.removed) != 0 {
		t.Fatal(f.removed)
	}
	r, err = u.Read()
	if err != nil || r["phase"] != "completed" || object(r["cleanup"])["status"] != "incomplete" {
		t.Fatal(r, err)
	}
}
