package deployment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ImageFacts includes all aliases: any foreign alias or any container protects
// the complete image, including stopped containers and untracked workloads.
type ImageFacts struct{ Tags, Digests map[string]bool }

func (h *Host) RetentionInventory(ctx context.Context) (map[string]ImageFacts, map[string]bool, error) {
	images := map[string]ImageFacts{}
	containers := set()
	for _, kind := range []string{"image", "container"} {
		args := []string{"docker", "image", "ls", "-aq", "--no-trunc"}
		if kind == "container" {
			args = []string{"docker", "ps", "-aq", "--no-trunc"}
		}
		b, err := h.run(ctx, nil, "", false, args...)
		if err != nil {
			return nil, nil, err
		}
		ids := strings.Fields(string(b))
		seen := set()
		for _, id := range ids {
			valid := imageRE.MatchString(id)
			if kind == "container" {
				valid = regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(id)
			}
			if !valid {
				return nil, nil, fmt.Errorf("ambiguous Docker retention inventory")
			}
			seen[id] = true
		}
		ids = ids[:0]
		for id := range seen {
			ids = append(ids, id)
		}
		if len(ids) > maxRetentionItems {
			return nil, nil, fmt.Errorf("Docker retention inventory exceeds bounds")
		}
		sort.Strings(ids)
		for offset := 0; offset < len(ids); offset += 128 {
			batch := ids[offset:min(offset+128, len(ids))]
			rows, err := h.jsonList(ctx, append([]string{"docker", kind, "inspect"}, batch...)...)
			if err != nil {
				return nil, nil, err
			}
			observed := set()
			if len(rows) != len(batch) {
				return nil, nil, fmt.Errorf("Docker inventory changed; retry cleanup")
			}
			for _, row := range rows {
				v := object(row)
				id := text(v["Id"])
				if !has(batch, id) || observed[id] {
					return nil, nil, fmt.Errorf("Docker inventory changed; retry cleanup")
				}
				observed[id] = true
				if kind == "container" {
					image := text(v["Image"])
					if !imageRE.MatchString(image) {
						return nil, nil, fmt.Errorf("container image identity ambiguous")
					}
					containers[image] = true
					continue
				}
				facts := ImageFacts{Tags: set(), Digests: set()}
				for _, key := range []string{"RepoTags", "RepoDigests"} {
					entries := array(v[key])
					if v[key] != nil && entries == nil || len(entries) > maxRetentionItems {
						return nil, nil, fmt.Errorf("ambiguous Docker image references")
					}
					for _, item := range entries {
						s, ok := item.(string)
						if !ok || s == "" {
							return nil, nil, fmt.Errorf("ambiguous Docker image reference")
						}
						if key == "RepoTags" {
							facts.Tags[s] = true
						} else {
							facts.Digests[s] = true
						}
					}
				}
				images[id] = facts
			}
		}
	}
	return images, containers, nil
}
func (u *Updater) Retention(ctx context.Context) (err error) {
	current, err := u.Read()
	if err != nil {
		return err
	}
	if current == nil || current["phase"] != "completed" {
		return fmt.Errorf("retention requires completed deployment")
	}
	current["cleanup"] = Object{"status": "incomplete"}
	if err = u.write(current, "completed"); err != nil {
		return err
	}
	if err = u.pruneTrackedImages(ctx, current); err != nil {
		_ = u.write(current, "completed")
		return err
	}
	current["cleanup"] = Object{"status": "completed"}
	return u.write(current, "completed")
}
func references(compose Object) (map[string]bool, error) {
	services := object(compose["services"])
	if services == nil {
		return nil, fmt.Errorf("retained deployment configuration ambiguous")
	}
	values := set()
	for _, row := range services {
		image := text(object(row)["image"])
		if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9./:@_-]{0,511}$`).MatchString(image) {
			return nil, fmt.Errorf("retained image reference ambiguous")
		}
		values[image] = true
	}
	return values, nil
}
func add(dst, src map[string]bool) {
	for k := range src {
		dst[k] = true
	}
}
func (u *Updater) pruneTrackedImages(ctx context.Context, current Object) error {
	if u.Config.RetentionCount < 2 {
		return fmt.Errorf("retain at least two checkpoints")
	}
	root := filepath.Join(u.State, "transactions")
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	if len(entries) > maxRetentionItems {
		return fmt.Errorf("transaction retention inventory exceeds bounds")
	}
	records := map[string]Object{}
	for _, entry := range entries {
		id := entry.Name()
		directory := filepath.Join(root, id)
		if !idRE.MatchString(id) {
			return fmt.Errorf("transaction retention identity ambiguous")
		}
		if err = u.check(directory, true, false, false); err != nil {
			return err
		}
		file := filepath.Join(directory, "transaction.json")
		if err = u.check(file, false, false, true); err != nil {
			return err
		}
		record, e := load(file)
		if e != nil {
			return e
		}
		if number(record["schema_version"]) != 1 || record["id"] != id || text(record["phase"]) == "" || record["checkpoint"] != filepath.Join(u.Backups, id) {
			return fmt.Errorf("transaction retention identity ambiguous")
		}
		records[id] = record
	}
	if !equal(records[text(current["id"])], current) {
		return fmt.Errorf("current retention transaction differs from history")
	}
	backups, err := os.ReadDir(u.Backups)
	if err != nil {
		return err
	}
	for _, entry := range backups {
		if records[entry.Name()] == nil {
			return fmt.Errorf("untracked checkpoint requires operator review")
		}
	}
	var successful []string
	for id, r := range records {
		if r["phase"] == "completed" && !boolean(r["restoring"]) {
			successful = append(successful, id)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(successful)))
	keep := set(text(current["id"]))
	for _, id := range successful[:min(len(successful), u.Config.RetentionCount)] {
		keep[id] = true
	}
	expired := set()
	for _, id := range successful {
		if !keep[id] {
			expired[id] = true
		}
	}
	tracked, required := set(), set()
	type tagOwner struct{ id, image string }
	tags := map[string]tagOwner{}
	for id, record := range records {
		values := set()
		adopted := object(object(record["adopted"])["images"])
		if adopted == nil {
			adopted = Object{}
		}
		imageIDs := set()
		for _, value := range adopted {
			image := text(value)
			if !imageRE.MatchString(image) {
				return fmt.Errorf("tracked checkpoint image identity ambiguous")
			}
			values[image] = true
			imageIDs[image] = true
		}
		ordered := []string{}
		for id := range imageIDs {
			ordered = append(ordered, id)
		}
		sort.Strings(ordered)
		for i, image := range ordered {
			tags["psst-checkpoint-"+strings.ToLower(id)+fmt.Sprintf(":%d", i)] = tagOwner{id, image}
		}
		for _, name := range []string{"previous", "public", "private"} {
			p := filepath.Join(root, id, name+".compose.json")
			if exists(p) {
				if err = u.check(p, false, false, true); err != nil {
					return err
				}
				v, e := load(p)
				if e != nil {
					return e
				}
				refs, e := references(v)
				if e != nil {
					return e
				}
				add(values, refs)
			}
		}
		for _, image := range object(object(record["manifest"])["images"]) {
			ref := text(object(image)["index"])
			if !regexp.MustCompile(`^[a-z0-9./_-]+@sha256:[0-9a-f]{64}$`).MatchString(ref) {
				return fmt.Errorf("tracked release image identity ambiguous")
			}
			values[ref] = true
		}
		checkpoint := filepath.Join(u.Backups, id)
		if keep[id] && record["phase"] == "completed" && !exists(checkpoint) {
			return fmt.Errorf("retained successful checkpoint missing; preserve images")
		}
		if !expired[id] && exists(checkpoint) {
			if err = u.check(checkpoint, true, false, false); err != nil {
				return err
			}
			p := filepath.Join(checkpoint, "checkpoint.json")
			if err = u.check(p, false, false, true); err != nil {
				return err
			}
			backup, e := load(p)
			if e != nil {
				return e
			}
			if backup["transaction"] != id || !equal(backup["images"], adopted) {
				return fmt.Errorf("retained checkpoint images differ from history")
			}
		}
		add(tracked, values)
		if !expired[id] {
			add(required, values)
		}
	}
	activePath := filepath.Join(u.State, "active.json")
	if err = u.check(activePath, false, false, true); err != nil {
		return err
	}
	active, err := load(activePath)
	if err != nil {
		return err
	}
	if active["transaction"] != current["id"] {
		return fmt.Errorf("active deployment differs from retention transaction")
	}
	activeRefs, err := references(object(active["compose"]))
	if err != nil {
		return err
	}
	add(required, activeRefs)
	candidates := func() (map[string]map[string]bool, error) {
		images, containers, e := u.Host.RetentionInventory(ctx)
		if e != nil {
			return nil, e
		}
		lookup := map[string]string{}
		for id, facts := range images {
			refs := set(id)
			add(refs, facts.Tags)
			add(refs, facts.Digests)
			for ref := range refs {
				if _, ok := lookup[ref]; ok {
					return nil, fmt.Errorf("Docker image reference ambiguous")
				}
				lookup[ref] = id
			}
		}
		for ref := range activeRefs {
			if lookup[ref] == "" {
				return nil, fmt.Errorf("active Docker images missing")
			}
		}
		protected := set()
		add(protected, containers)
		for ref := range required {
			if lookup[ref] != "" {
				protected[lookup[ref]] = true
			}
		}
		eligible := set()
		for ref := range tracked {
			if id := lookup[ref]; id != "" && !protected[id] {
				eligible[id] = true
			}
		}
		removable := map[string]map[string]bool{}
		for image := range eligible {
			facts := images[image]
			owned := set()
			for tag, owner := range tags {
				if expired[owner.id] && owner.image == image {
					owned[tag] = true
				}
			}
			foreign := false
			for tag := range facts.Tags {
				foreign = foreign || !owned[tag]
			}
			for digest := range facts.Digests {
				foreign = foreign || !tracked[digest]
			}
			if !foreign {
				removable[image] = facts.Tags
			}
		}
		return removable, nil
	}
	if _, err = candidates(); err != nil {
		return err
	}
	ids := []string{}
	for id := range expired {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		record := records[id]
		checkpoint := filepath.Join(u.Backups, id)
		if !exists(checkpoint) {
			continue
		}
		if err = u.check(checkpoint, true, false, false); err != nil {
			return err
		}
		if record["checkpoint_prune_started"] != true {
			backup, e := u.Host.VerifyBackup(checkpoint)
			if e != nil {
				return e
			}
			if backup["transaction"] != id || !equal(backup["images"], object(record["adopted"])["images"]) {
				return fmt.Errorf("expired checkpoint images differ from history")
			}
			record["checkpoint_prune_started"] = true
			if err = atomicJSON(filepath.Join(root, id, "transaction.json"), record); err != nil {
				return err
			}
		}
		if err = os.RemoveAll(checkpoint); err != nil {
			return err
		}
	}
	removable, err := candidates()
	if err != nil {
		return err
	}
	images := []string{}
	for id := range removable {
		images = append(images, id)
	}
	sort.Strings(images)
	for _, image := range images {
		fresh, e := candidates()
		if e != nil {
			return e
		}
		tags, ok := fresh[image]
		if !ok {
			continue
		}
		ordered := []string{}
		for tag := range tags {
			ordered = append(ordered, tag)
		}
		sort.Strings(ordered)
		for _, tag := range ordered {
			fresh, e = candidates()
			if e != nil {
				return e
			}
			if _, ok = fresh[image]; !ok {
				break
			}
			if e = u.Host.RemoveImage(ctx, tag); e != nil {
				return e
			}
		}
		fresh, e = candidates()
		if e != nil {
			return e
		}
		if _, ok = fresh[image]; ok {
			if e = u.Host.RemoveImage(ctx, image); e != nil {
				return e
			}
		}
	}
	return nil
}
