package deployment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func checkpointProtection(v Object) (string, error) {
	policy := text(v["checkpoint_protection"])
	if _, ok := v["checkpoint_protection"]; !ok {
		policy = "off-host"
	}
	if policy != "off-host" && policy != "local-only" {
		return "", fmt.Errorf("invalid checkpoint protection policy")
	}
	return policy, nil
}
func (h *Host) sqliteIntegrity(ctx context.Context, database string) error {
	info, err := os.Lstat(database)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("read-only database verification failed")
	}
	// The shell opens mode=ro and executes one snapshot transaction. No migrations,
	// repair, vacuum, writable connection or journal deletion are performed.
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	query := "BEGIN; SELECT json_object('integrity', (SELECT json_group_array(integrity_check) FROM pragma_integrity_check), 'foreign_keys', (SELECT count(*) FROM pragma_foreign_key_check)); ROLLBACK;"
	raw, err := h.run(ctx, nil, "", false, "sqlite3", "-readonly", "-batch", "-noheader", "-cmd", ".timeout 5000", database, query)
	if err != nil {
		return fmt.Errorf("read-only database verification failed or timed out")
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	var result Object
	if err = decodeHost(raw, maxJSON, &result); err != nil {
		return fmt.Errorf("invalid read-only database verification result")
	}
	integrity := stringsOf(result["integrity"])
	if len(integrity) != 1 || integrity[0] != "ok" {
		return fmt.Errorf("database integrity check failed")
	}
	if !integer(result["foreign_keys"]) || number(result["foreign_keys"]) != 0 {
		return fmt.Errorf("database foreign-key check failed")
	}
	return nil
}
func (h *Host) Backup(ctx context.Context, current, adopted Object, checkpoint string, t Object) error {
	policy := h.Config.Protection()
	t["checkpoint_protection"] = policy
	if err := os.Mkdir(checkpoint, 0o700); err != nil {
		return err
	}
	records := Object{}
	for i, volume := range keys(object(adopted["volumes"])) {
		facts := object(object(adopted["volumes"])[volume])
		name := fmt.Sprintf("volume-%d.tar", i)
		destination := filepath.Join(checkpoint, name)
		_, err := h.run(ctx, nil, destination, false, "docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--entrypoint", "tar", "--mount", "type=volume,src="+volume+",dst=/snapshot,readonly", text(facts["image"]), "-C", "/snapshot", "-cpf", "-", ".")
		if err != nil {
			return err
		}
		if err = VerifyArchive(destination); err != nil {
			return err
		}
		hash, err := fingerprint(destination)
		if err != nil {
			return err
		}
		record := clone(facts)
		record["sha256"] = hash
		record["kind"] = "volume"
		record["volume"] = volume
		records[name] = record
	}
	images := set()
	for _, image := range object(adopted["images"]) {
		images[text(image)] = true
	}
	ordered := []string{}
	for image := range images {
		ordered = append(ordered, image)
	}
	sort.Strings(ordered)
	for i, image := range ordered {
		name := fmt.Sprintf("image-%d.tar", i)
		destination := filepath.Join(checkpoint, name)
		if _, err := h.run(ctx, nil, destination, false, "docker", "image", "save", image); err != nil {
			return err
		}
		if err := VerifyArchive(destination); err != nil {
			return err
		}
		hash, err := fingerprint(destination)
		if err != nil {
			return err
		}
		records[name] = Object{"sha256": hash, "kind": "image", "image": image}
		if _, err = h.run(ctx, nil, "", false, "docker", "image", "tag", image, "psst-checkpoint-"+strings.ToLower(text(t["id"]))+fmt.Sprintf(":%d", i)); err != nil {
			return err
		}
	}
	configs := filepath.Join(checkpoint, "configuration")
	if err := os.Mkdir(configs, 0o700); err != nil {
		return err
	}
	inputs, err := h.ProtectedInputs()
	if err != nil {
		return err
	}
	for _, path := range inputs {
		relative := filepath.Join("managed", fmt.Sprint(len(records)), filepath.Base(path))
		if inside(path, h.Config.Installation) {
			relative, err = filepath.Rel(h.Config.Installation, path)
			if err != nil {
				return err
			}
		}
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		copy := filepath.Join(configs, relative)
		if err = copyFile(path, copy, info.Mode().Perm()); err != nil {
			return err
		}
		hash, err := fingerprint(copy)
		if err != nil {
			return err
		}
		records["configuration/"+filepath.ToSlash(relative)] = Object{"sha256": hash, "kind": "configuration", "source": path}
	}
	if err = atomicJSON(filepath.Join(checkpoint, "current.compose.json"), current); err != nil {
		return err
	}
	hash, err := fingerprint(filepath.Join(checkpoint, "current.compose.json"))
	if err != nil {
		return err
	}
	records["current.compose.json"] = Object{"sha256": hash, "kind": "configuration"}
	record := Object{"schema_version": 1, "checkpoint_protection": policy, "transaction": t["id"], "created_at": h.Now().UTC().Format(time.RFC3339Nano), "prior_pause": t["prior_pause"], "images": adopted["images"], "records": records}
	if err = atomicJSON(filepath.Join(checkpoint, "checkpoint.json"), record); err != nil {
		return err
	}
	if _, err = h.verifyBackup(checkpoint, false); err != nil {
		return err
	}
	if err = h.sqliteIntegrity(ctx, text(adopted["database"])); err != nil {
		return err
	}
	if policy == "off-host" {
		if h.Config.CheckpointHook == nil {
			return fmt.Errorf("encrypted off-host checkpoint protection required")
		}
		hook := *h.Config.CheckpointHook
		if err = h.check(hook, false, true, true); err != nil {
			return err
		}
		receipt, e := h.json(ctx, hook, "--checkpoint", checkpoint)
		if e != nil {
			return e
		}
		if e = verifyCheckpointReceipt(checkpoint, receipt, true, h.Now()); e != nil {
			return e
		}
		if e = atomicJSON(filepath.Join(checkpoint, "off-host-receipt.json"), receipt); e != nil {
			return e
		}
	}
	_, err = h.VerifyBackup(checkpoint)
	return err
}
func verifyCheckpointReceipt(checkpoint string, report Object, fresh bool, now time.Time) error {
	if len(report) != 4 {
		return fmt.Errorf("invalid protected backup receipt")
	}
	for _, name := range []string{"checkpoint_sha256", "encrypted_off_host_receipt", "restore_exercise", "verified_at"} {
		if _, ok := report[name]; !ok {
			return fmt.Errorf("invalid protected backup receipt")
		}
	}
	hash, err := fingerprint(filepath.Join(checkpoint, "checkpoint.json"))
	if err != nil {
		return err
	}
	if report["checkpoint_sha256"] != hash {
		return fmt.Errorf("off-host receipt does not cover stopped checkpoint")
	}
	for _, name := range []string{"encrypted_off_host_receipt", "restore_exercise"} {
		v := text(report[name])
		if len(v) < 12 || len(v) > 1000 {
			return fmt.Errorf("off-host encryption/restore evidence required")
		}
	}
	stamp, err := time.Parse(time.RFC3339Nano, text(report["verified_at"]))
	if err != nil || fresh && now.Sub(stamp).Abs() >= 15*time.Minute {
		return fmt.Errorf("off-host checkpoint receipt stale or invalid")
	}
	return nil
}
func (h *Host) VerifyBackup(checkpoint string) (Object, error) {
	record, err := load(filepath.Join(checkpoint, "checkpoint.json"))
	if err != nil {
		return nil, err
	}
	policy, err := checkpointProtection(record)
	if err != nil {
		return nil, err
	}
	return h.verifyBackup(checkpoint, policy == "off-host")
}
func (h *Host) verifyBackup(checkpoint string, offHost bool) (Object, error) {
	if err := h.check(checkpoint, true, false, false); err != nil {
		return nil, err
	}
	if err := h.check(filepath.Join(checkpoint, "checkpoint.json"), false, false, true); err != nil {
		return nil, err
	}
	record, err := load(filepath.Join(checkpoint, "checkpoint.json"))
	if err != nil {
		return nil, err
	}
	if _, err = checkpointProtection(record); err != nil {
		return nil, err
	}
	if number(record["schema_version"]) != 1 || !idRE.MatchString(text(record["transaction"])) || record["transaction"] != filepath.Base(checkpoint) {
		return nil, fmt.Errorf("invalid checkpoint record")
	}
	records := object(record["records"])
	if len(records) == 0 {
		return nil, fmt.Errorf("checkpoint has no retained artifacts")
	}
	image, volume := false, false
	for name, item := range records {
		entry := object(item)
		if !relative(name) || filepath.Clean(name) != name {
			return nil, fmt.Errorf("unsafe checkpoint record")
		}
		path := filepath.Join(checkpoint, name)
		kind := text(entry["kind"])
		if kind != "image" && kind != "volume" && kind != "configuration" {
			return nil, fmt.Errorf("unsupported checkpoint record kind")
		}
		if err = h.check(path, false, false, kind != "configuration"); err != nil {
			return nil, err
		}
		hash, e := fingerprint(path)
		if e != nil {
			return nil, e
		}
		if entry["sha256"] != hash {
			return nil, fmt.Errorf("checkpoint checksum failed; preserve and investigate")
		}
		if kind == "image" || kind == "volume" {
			if err = VerifyArchive(path); err != nil {
				return nil, err
			}
		}
		image = image || kind == "image"
		volume = volume || kind == "volume"
	}
	if !image || !volume {
		return nil, fmt.Errorf("checkpoint missing images or storage")
	}
	if offHost {
		path := filepath.Join(checkpoint, "off-host-receipt.json")
		if err = h.check(path, false, false, true); err != nil {
			return nil, err
		}
		receipt, e := load(path)
		if e != nil {
			return nil, e
		}
		if e = verifyCheckpointReceipt(checkpoint, receipt, false, h.Now()); e != nil {
			return nil, e
		}
	}
	return record, nil
}
func (h *Host) Restore(ctx context.Context, checkpoint, target string, t Object) (Object, error) {
	backup, err := h.VerifyBackup(checkpoint)
	if err != nil {
		return nil, err
	}
	current, err := load(filepath.Join(checkpoint, "current.compose.json"))
	if err != nil {
		return nil, err
	}
	restored := map[string]string{}
	records := object(backup["records"])
	for _, name := range keys(records) {
		record := object(records[name])
		if record["kind"] != "image" {
			continue
		}
		if _, err = h.run(ctx, nil, "", false, "docker", "image", "load", "--input", filepath.Join(checkpoint, name)); err != nil {
			return nil, err
		}
		if _, err = h.run(ctx, nil, "", false, "docker", "image", "inspect", text(record["image"])); err != nil {
			return nil, err
		}
	}
	for _, name := range keys(records) {
		record := object(records[name])
		if record["kind"] != "volume" {
			continue
		}
		volume := "psst-restore-" + text(t["id"]) + "-" + fmt.Sprint(len(restored))
		b, e := h.run(ctx, nil, "", false, "docker", "volume", "ls", "-q", "--filter", "name=^"+volume+"$")
		if e != nil {
			return nil, e
		}
		if strings.TrimSpace(string(b)) != "" {
			return nil, fmt.Errorf("restore destination exists; never overwrite partial restore")
		}
		if _, err = h.run(ctx, nil, "", false, "docker", "volume", "create", "--label", "zip.psst.restore="+text(t["id"]), volume); err != nil {
			return nil, err
		}
		facts, e := h.jsonList(ctx, "docker", "volume", "inspect", volume)
		if e != nil || len(facts) != 1 {
			return nil, fmt.Errorf("restore volume unavailable")
		}
		mount := text(object(facts[0])["Mountpoint"])
		entries, e := os.ReadDir(mount)
		if e != nil {
			return nil, e
		}
		if len(entries) != 0 {
			return nil, fmt.Errorf("restore destination is not empty")
		}
		if _, err = h.run(ctx, nil, "", false, "tar", "--numeric-owner", "-C", mount, "-xpf", filepath.Join(checkpoint, name)); err != nil {
			return nil, err
		}
		restored[text(record["volume"])] = volume
	}
	for _, item := range object(current["volumes"]) {
		volume := object(item)
		name := restored[text(volume["name"])]
		if name == "" {
			return nil, fmt.Errorf("checkpoint does not cover all state volumes")
		}
		volume["name"] = name
	}
	for service, image := range object(backup["images"]) {
		s := object(object(current["services"])[service])
		s["image"] = image
		delete(s, "build")
		for _, item := range array(s["volumes"]) {
			mount := object(item)
			if mount["type"] != "bind" {
				continue
			}
			var copies []string
			for name, item := range records {
				entry := object(item)
				if entry["source"] == mount["source"] {
					copies = append(copies, filepath.Join(checkpoint, name))
				}
			}
			if len(copies) != 1 {
				return nil, fmt.Errorf("unprotected restore configuration bind")
			}
			if err = h.check(copies[0], false, false, false); err != nil {
				return nil, err
			}
			mount["source"] = copies[0]
		}
	}
	env := object(object(object(current["services"])["backend"])["environment"])
	env["ADMIN_USERNAME"] = ""
	env["ADMIN_PASSWORD"] = ""
	dbVolume := restored[h.Config.VolumeNames["backend:/app/data"]]
	facts, err := h.jsonList(ctx, "docker", "volume", "inspect", dbVolume)
	if err != nil || len(facts) != 1 {
		return nil, fmt.Errorf("restored database volume unavailable")
	}
	database := filepath.Join(text(object(facts[0])["Mountpoint"]), strings.TrimPrefix(h.Config.DBPath, "/app/data/"))
	if err = h.sqliteIntegrity(ctx, database); err != nil {
		return nil, err
	}
	if _, err = h.Incident(ctx, current, "pause"); err != nil {
		return nil, err
	}
	names := Object{}
	for old, new := range restored {
		names[old] = new
	}
	t["restored_volumes"] = names
	return current, nil
}
