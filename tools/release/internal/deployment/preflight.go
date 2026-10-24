package deployment

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

func versionAtLeast(value, minimum string) bool {
	parts := regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`).FindStringSubmatch(value)
	if parts == nil {
		return false
	}
	required := strings.Split(minimum, ".")
	for i := range 3 {
		a, e1 := strconv.Atoi(parts[i+1])
		b, e2 := strconv.Atoi(required[i])
		if e1 != nil || e2 != nil {
			return false
		}
		if a > b {
			return true
		}
		if a < b {
			return false
		}
	}
	return true
}
func (h *Host) Preflight(ctx context.Context, current, candidate Object, m contract.ManifestV1, checkpoint string) (Object, error) {
	if h.Config.Protection() != "local-only" && h.Config.CheckpointHook == nil {
		return nil, fmt.Errorf("configure encrypted off-host checkpoint protection before production updates")
	}
	for _, item := range []struct {
		args    []string
		minimum string
	}{{[]string{"docker", "version", "--format", "{{.Server.Version}}"}, m.Requirements.Docker}, {[]string{"docker", "compose", "version", "--short"}, m.Requirements.Compose}} {
		b, err := h.run(ctx, nil, "", false, item.args...)
		if err != nil {
			return nil, err
		}
		if !versionAtLeast(strings.TrimPrefix(strings.TrimSpace(string(b)), "v"), item.minimum) {
			return nil, fmt.Errorf("Docker/Compose does not meet release requirements")
		}
	}
	// Integrity checks must be available before pausing or stopping production.
	probe, probeErr := h.run(ctx, nil, "", false, "sqlite3", "-readonly", "-batch", ":memory:", "SELECT json_valid('{}');")
	if probeErr != nil || strings.TrimSpace(string(probe)) != "1" {
		return nil, fmt.Errorf("install SQLite with JSON support before deployment; no production state changed")
	}
	info, err := h.json(ctx, "docker", "info", "--format", "{{json .}}")
	if err != nil {
		return nil, err
	}
	architecture := map[string]string{"x86_64": "amd64", "aarch64": "arm64", "amd64": "amd64", "arm64": "arm64"}[text(info["Architecture"])]
	if info["OSType"] != "linux" || architecture == "" {
		return nil, fmt.Errorf("unsupported daemon platform")
	}
	names := set("backend", "caddy")
	if h.Config.ExternalProxy {
		names["external-proxy"] = true
	}
	currentServices, candidateServices := object(current["services"]), object(candidate["services"])
	if len(currentServices) != len(names) || len(candidateServices) != len(names) {
		return nil, fmt.Errorf("unexpected deployment services")
	}
	for name := range names {
		if object(currentServices[name]) == nil || object(candidateServices[name]) == nil {
			return nil, fmt.Errorf("missing deployment service")
		}
	}
	actual, err := h.containers(ctx, true, true)
	if err != nil {
		return nil, err
	}
	services := Object{}
	ownedIDs := set()
	for _, row := range actual {
		item := object(row)
		name := text(object(object(item["Config"])["Labels"])["com.docker.compose.service"])
		if name == "" || services[name] != nil || !names[name] {
			return nil, fmt.Errorf("ambiguous current containers")
		}
		services[name] = item
		ownedIDs[text(item["Id"])] = true
	}
	if len(services) != len(names) {
		return nil, fmt.Errorf("missing current containers")
	}
	volumes := Object{}
	for key, expected := range h.Config.VolumeNames {
		serviceName, destination, _ := strings.Cut(key, ":")
		container := object(services[serviceName])
		state := object(container["State"])
		if state["Running"] != true || boolean(state["Restarting"]) {
			return nil, fmt.Errorf("current deployment is not healthy/running")
		}
		var matches []Object
		for _, item := range array(container["Mounts"]) {
			mount := object(item)
			if mount["Destination"] == destination {
				matches = append(matches, mount)
			}
		}
		if len(matches) != 1 || matches[0]["Type"] != "volume" || matches[0]["Name"] != expected || matches[0]["RW"] != true {
			return nil, fmt.Errorf("actual volume mapping differs from explicit adoption")
		}
		rows, e := h.jsonList(ctx, "docker", "volume", "inspect", expected)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 {
			return nil, fmt.Errorf("volume identity missing")
		}
		v := object(rows[0])
		owner := object(v["Labels"])["com.docker.compose.project"]
		mountpoint := text(v["Mountpoint"])
		file, e := os.Stat(mountpoint)
		if e != nil || !file.IsDir() || v["Driver"] != "local" || (v["Options"] != nil && object(v["Options"]) == nil) || len(object(v["Options"])) != 0 {
			return nil, fmt.Errorf("only existing local named volumes are supported")
		}
		if owner != nil && owner != h.Config.Project {
			return nil, fmt.Errorf("volume belongs to another Compose project")
		}
		volumes[expected] = Object{"mountpoint": mountpoint, "image": container["Image"], "service": serviceName, "destination": destination}
	}
	for name, item := range services {
		if err := checkPorts(object(currentServices[name]), object(object(item)["HostConfig"]), false); err != nil {
			return nil, err
		}
	}
	all, err := h.containers(ctx, true, false)
	if err != nil {
		return nil, err
	}
	for _, row := range all {
		c := object(row)
		if ownedIDs[text(c["Id"])] {
			continue
		}
		for _, item := range array(c["Mounts"]) {
			mount := object(item)
			if volumes[text(mount["Name"])] != nil && mount["RW"] == true {
				return nil, fmt.Errorf("another container can write installation storage")
			}
		}
	}
	if !equal(mapOrEmpty(current["networks"]), mapOrEmpty(candidate["networks"])) {
		return nil, fmt.Errorf("candidate changes operator network configuration")
	}
	approved, err := h.ProtectedInputs()
	if err != nil {
		return nil, err
	}
	for name, item := range candidateServices {
		s, prior := object(item), object(currentServices[name])
		if _, ok := s["user"]; ok {
			return nil, fmt.Errorf("service user override needs ownership adaptation")
		}
		if _, ok := prior["user"]; ok {
			return nil, fmt.Errorf("prior service user override needs ownership adaptation")
		}
		component := "web"
		if name == "backend" {
			component = "backend"
		}
		if s["image"] != m.Images[component].Index {
			return nil, fmt.Errorf("operator override changes authenticated image pair")
		}
		if !equal(mapOrEmpty(s["networks"]), mapOrEmpty(prior["networks"])) {
			return nil, fmt.Errorf("candidate changes network membership/addresses")
		}
		if boolean(s["privileged"]) || len(array(s["cap_add"])) > 0 || len(array(s["devices"])) > 0 || s["network_mode"] == "host" || s["network_mode"] == "service:backend" || s["pid"] == "host" {
			return nil, fmt.Errorf("candidate enables unsafe host privileges")
		}
		security := false
		for _, option := range stringsOf(s["security_opt"]) {
			if strings.ReplaceAll(option, ":", "=") == "no-new-privileges=true" {
				security = true
			}
		}
		if s["read_only"] != true || !has(stringsOf(s["cap_drop"]), "ALL") || !security {
			return nil, fmt.Errorf("candidate loses container hardening")
		}
		actualEnv := map[string]string{}
		for _, entry := range stringsOf(object(object(services[name])["Config"])["Env"]) {
			key, value, ok := strings.Cut(entry, "=")
			if ok {
				actualEnv[key] = value
			}
		}
		for key, value := range object(prior["environment"]) {
			old := scalar(value)
			if actualEnv[key] != old {
				return nil, fmt.Errorf("current Compose environment differs from running deployment")
			}
			if key == "ADMIN_USERNAME" || key == "ADMIN_PASSWORD" {
				if old != "" {
					return nil, fmt.Errorf("remove bootstrap credentials before adoption")
				}
			} else if scalar(object(s["environment"])[key]) != old {
				return nil, fmt.Errorf("candidate changes an operator setting; review release override")
			}
		}
		for _, key := range []string{"pids_limit", "mem_limit", "cpus", "tmpfs", "logging"} {
			if !equal(s[key], prior[key]) {
				return nil, fmt.Errorf("candidate changes operator resource/logging limits")
			}
		}
		before, e := Bindings(current, name)
		if e != nil {
			return nil, e
		}
		after, e := Bindings(candidate, name)
		if e != nil {
			return nil, e
		}
		expected := Object{}
		for key, volume := range h.Config.VolumeNames {
			service, target, _ := strings.Cut(key, ":")
			if service == name {
				expected[target] = volume
			}
		}
		if !equal(before, after) || !equal(after, expected) {
			return nil, fmt.Errorf("candidate/current volume identities differ from adopted storage")
		}
		if !equal(listOrEmpty(s["ports"]), listOrEmpty(prior["ports"])) {
			return nil, fmt.Errorf("candidate changes public ports")
		}
		for _, row := range array(object(services[name])["Mounts"]) {
			mount := object(row)
			if mount["Type"] == "bind" && (!has(approved, text(mount["Source"])) || mount["RW"] != false) {
				return nil, fmt.Errorf("unrecorded/writable operator bind mount")
			}
		}
		for _, row := range array(s["volumes"]) {
			mount := object(row)
			if mount["type"] == "bind" {
				path := text(mount["source"])
				if mount["read_only"] != true || !has(approved, path) && !inside(path, filepath.Join(h.State, "transactions", filepath.Base(checkpoint), "bundle")) {
					return nil, fmt.Errorf("untrusted candidate bind mount")
				}
			}
		}
	}
	env := object(object(candidateServices["backend"])["environment"])
	if env["DB_PATH"] != h.Config.DBPath || env["STORAGE_PATH"] != "/app/data/files" || text(env["ADMIN_USERNAME"]) != "" || text(env["ADMIN_PASSWORD"]) != "" {
		return nil, fmt.Errorf("candidate database/storage/bootstrap mismatch")
	}
	if env["AUTH_ALLOW_INSECURE_HTTP"] != "false" || env["PUBLIC_URL"] != "https://"+h.Config.Domain {
		return nil, fmt.Errorf("production requires adopted HTTPS public URL")
	}
	database := filepath.Join(text(object(volumes[h.Config.VolumeNames["backend:/app/data"]])["mountpoint"]), strings.TrimPrefix(h.Config.DBPath, "/app/data/"))
	db, err := os.Lstat(database)
	if err != nil || !db.Mode().IsRegular() || db.Size() <= 0 || db.Mode().Perm()&0o077 != 0 {
		return nil, fmt.Errorf("existing private database is missing or unsafe")
	}
	status := clone(current)
	object(object(status["services"])["backend"])["image"] = object(services["backend"])["Image"]
	if _, err = h.Incident(ctx, status, "incident-status"); err != nil {
		return nil, err
	}
	var total int64
	for _, item := range volumes {
		size, e := directorySize(text(object(item)["mountpoint"]))
		if e != nil {
			return nil, e
		}
		total += size
	}
	images := set()
	for _, row := range actual {
		images[text(object(row)["Image"])] = true
	}
	var imageBytes int64
	for image := range images {
		rows, e := h.jsonList(ctx, "docker", "image", "inspect", image)
		if e != nil {
			return nil, e
		}
		if len(rows) != 1 {
			return nil, fmt.Errorf("adopted image unavailable")
		}
		imageBytes += number(object(rows[0])["Size"])
	}
	free, err := freeBytes(filepath.Dir(checkpoint))
	if err != nil {
		return nil, err
	}
	if free < uint64(2*total+imageBytes+h.Config.DiskReserveBytes) {
		return nil, fmt.Errorf("insufficient space for complete checkpoint and isolated restore")
	}
	free, err = freeBytes(text(info["DockerRootDir"]))
	if err != nil {
		return nil, err
	}
	if free < uint64(h.Config.ImageReserveBytes+h.Config.DiskReserveBytes+total) {
		return nil, fmt.Errorf("insufficient Docker image/restore headroom")
	}
	baseline, err := h.httpsJSON(ctx, current, "/api/v1/config", "")
	if err != nil {
		return nil, err
	}
	delete(baseline, "public_transfers_paused")
	auth, err := h.httpsJSON(ctx, current, "/api/v1/auth/status", "")
	if err != nil {
		return nil, err
	}
	if auth["setup_required"] != false {
		return nil, fmt.Errorf("existing administrator has not been initialized")
	}
	baseline, err = persistedSettings(baseline)
	if err != nil {
		return nil, err
	}
	containerIDs, imageIDs := Object{}, Object{}
	for name, row := range services {
		containerIDs[name] = object(row)["Id"]
		imageIDs[name] = object(row)["Image"]
	}
	return Object{"volumes": volumes, "containers": containerIDs, "images": imageIDs, "platform": "linux/" + architecture, "baseline_config": baseline, "database": database}, nil
}
func (h *Host) Ownership(ctx context.Context, m contract.ManifestV1, adopted Object) error {
	newBackendUID := ""
	for _, pair := range [][2]string{{"backend", "backend"}, {"web", "caddy"}} {
		old := text(object(adopted["images"])[pair[1]])
		var ids []string
		for _, image := range []string{old, m.Images[pair[0]].Index} {
			b, err := h.run(ctx, nil, "", false, "docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--entrypoint", "id", image, "-u")
			if err != nil {
				return err
			}
			ids = append(ids, strings.TrimSpace(string(b)))
		}
		if ids[0] != ids[1] || !regexp.MustCompile(`^[0-9]+$`).MatchString(ids[1]) || ids[1] == "0" {
			return fmt.Errorf("image user IDs differ; ownership migration requires separate work")
		}
		if pair[0] == "backend" {
			newBackendUID = ids[1]
		}
	}
	info, err := os.Stat(text(adopted["database"]))
	if err != nil {
		return err
	}
	uid, err := strconv.ParseUint(newBackendUID, 10, 32)
	if err != nil || fileUID(info) != uint32(uid) {
		return fmt.Errorf("database ownership incompatible with selected image")
	}
	return nil
}
func (h *Host) Incident(ctx context.Context, compose Object, action string) (bool, error) {
	if !has([]string{"pause", "resume", "incident-status"}, action) {
		return false, fmt.Errorf("invalid incident action")
	}
	backend := object(object(compose["services"])["backend"])
	bindings, err := Bindings(compose, "backend")
	if err != nil {
		return false, err
	}
	output, err := h.json(ctx, "docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--entrypoint", "/app/server", "--mount", "type=volume,src="+text(bindings["/app/data"])+",dst=/app/data", "--env", "DB_PATH="+text(object(backend["environment"])["DB_PATH"]), text(backend["image"]), action)
	if err != nil {
		return false, err
	}
	value, ok := output["public_transfers_paused"].(bool)
	if !ok || action != "incident-status" && value != (action == "pause") {
		return false, fmt.Errorf("incident CLI returned invalid state")
	}
	return value, nil
}
func (h *Host) Stopped(ctx context.Context, path string, volumes Object) error {
	if err := h.Compose(ctx, path, "stop", "--timeout", "60"); err != nil {
		return err
	}
	rows, err := h.containers(ctx, false, false)
	if err != nil {
		return err
	}
	for _, row := range rows {
		for _, item := range array(object(row)["Mounts"]) {
			mount := object(item)
			if volumes[text(mount["Name"])] != nil && mount["RW"] == true {
				return fmt.Errorf("a writer survived shutdown")
			}
		}
	}
	return nil
}
