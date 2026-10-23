package deployment

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var flowChecks = []string{"existing_account_and_session", "administrator_second_factor", "persisted_settings_and_quotas", "existing_encrypted_download", "new_upload_and_receive", "expiry_budget_and_revocation", "cleanup_and_restart", "client_authenticated_decryption"}
var runtimeChecks = []string{"trusted_https_and_health", "release_and_static_assets", "preserved_settings_and_account_state", "runtime_storage_bindings", "sqlite_integrity_and_foreign_keys"}
var restoreChecks = []string{"post_checkpoint_changes_reviewed", "restored_sessions_links_and_factors_reconciled", "independent_traffic_allowances_reconciled"}

func RequireFlowReport(report Object, restore bool, profile string, now time.Time) error {
	if profile != "full" && profile != "runtime-v1" || restore && profile != "full" {
		return fmt.Errorf("invalid verification profile")
	}
	fields := set("checks", "observed_at", "version", "source_commit")
	required := flowChecks
	if profile == "runtime-v1" {
		fields["verification_profile"] = true
		required = runtimeChecks
		if report["verification_profile"] != profile {
			return fmt.Errorf("verification profile mismatch")
		}
	} else if restore {
		required = append(append([]string{}, flowChecks...), restoreChecks...)
	}
	if len(report) != len(fields) {
		return fmt.Errorf("invalid verification report")
	}
	for k := range report {
		if !fields[k] {
			return fmt.Errorf("unknown verification report field")
		}
	}
	if text(report["version"]) == "" || text(report["source_commit"]) == "" {
		return fmt.Errorf("verification identity missing")
	}
	checks := object(report["checks"])
	if len(checks) != len(required) {
		return fmt.Errorf("missing required verification checks")
	}
	for _, name := range required {
		s, ok := checks[name].(string)
		if !ok || len(s) < 12 || len(s) > 1000 {
			return fmt.Errorf("verification requires an evidence description, not a success flag")
		}
	}
	stamp, err := time.Parse(time.RFC3339Nano, text(report["observed_at"]))
	if err != nil || now.Sub(stamp).Abs() >= 10*time.Minute {
		return fmt.Errorf("stale verification report")
	}
	return nil
}
func runtimeEvidence(t Object, now time.Time) Object {
	checks := Object{"trusted_https_and_health": "Trusted HTTPS and API health responded successfully on isolated routing.", "release_and_static_assets": "Served release identity matched the selected source and compiled assets loaded.", "preserved_settings_and_account_state": "Initialized account state and persisted public operator settings matched the adopted baseline.", "runtime_storage_bindings": "Container hardening, isolated listeners and adopted physical storage bindings were verified.", "sqlite_integrity_and_foreign_keys": "Candidate database passed read-only SQLite integrity and foreign-key checks."}
	return Object{"verification_profile": "runtime-v1", "version": t["version"], "source_commit": object(object(t["manifest"])["source"])["commit"], "observed_at": now.UTC().Format(time.RFC3339Nano), "checks": checks}
}
func persistedSettings(value Object) (Object, error) {
	v := clone(value)
	if capability, ok := v["history_sync_version"]; ok {
		if !integer(capability) || number(capability) < 0 {
			return nil, fmt.Errorf("invalid history synchronization capability")
		}
		delete(v, "history_sync_version")
	}
	return v, nil
}
func reconciledSettings(baseline, observed Object) (Object, error) {
	actual := clone(observed)
	if actual["public_transfers_paused"] != true {
		return nil, fmt.Errorf("restored verification must remain paused")
	}
	delete(actual, "public_transfers_paused")
	actual, err := persistedSettings(actual)
	if err != nil {
		return nil, err
	}
	baseline, err = persistedSettings(baseline)
	if err != nil {
		return nil, err
	}
	if equal(actual, baseline) {
		return actual, nil
	}
	before, after := clone(baseline), clone(actual)
	oldPolicy, newPolicy := object(before["traffic_policy"]), object(after["traffic_policy"])
	delete(before, "traffic_policy")
	delete(after, "traffic_policy")
	if !equal(before, after) || oldPolicy == nil || newPolicy == nil {
		return nil, fmt.Errorf("restore reconciliation changed unrelated operator settings")
	}
	oldPolicy, newPolicy = clone(oldPolicy), clone(newPolicy)
	for _, key := range []string{"server_budget_bytes", "default_account_budget_bytes"} {
		a, b := oldPolicy[key], newPolicy[key]
		if !integer(a) || !integer(b) || number(b) <= 0 || number(b) > number(a) {
			return nil, fmt.Errorf("restore reconciliation may only decrease positive traffic budgets")
		}
		delete(oldPolicy, key)
		delete(newPolicy, key)
	}
	if !equal(oldPolicy, newPolicy) {
		return nil, fmt.Errorf("restore reconciliation changed enforcement policy")
	}
	p := object(actual["traffic_policy"])
	if number(p["default_account_budget_bytes"]) > number(p["server_budget_bytes"]) {
		return nil, fmt.Errorf("restored account budget exceeds server budget")
	}
	return actual, nil
}
func Bindings(compose Object, service string) (Object, error) {
	result := Object{}
	for _, item := range array(object(object(compose["services"])[service])["volumes"]) {
		m := object(item)
		if m["type"] == "volume" {
			target := text(m["target"])
			name := text(object(object(compose["volumes"])[text(m["source"])])["name"])
			if target == "" || name == "" || result[target] != nil {
				return nil, fmt.Errorf("ambiguous storage binding")
			}
			result[target] = name
		}
	}
	return result, nil
}
func (h *Host) HTTPS(ctx context.Context, compose Object, path, cookie string, raw bool) ([]byte, error) {
	name := "caddy"
	if h.Config.ExternalProxy {
		name = "external-proxy"
	}
	var ports []Object
	for _, item := range array(object(object(compose["services"])[name])["ports"]) {
		p := object(item)
		if number(p["target"]) == 8443 && (p["protocol"] == nil || p["protocol"] == "tcp") {
			ports = append(ports, p)
		}
	}
	if len(ports) != 1 {
		return nil, fmt.Errorf("expected a single HTTPS ingress")
	}
	port := scalar(ports[0]["published"])
	args := []string{"curl", "--fail", "--silent", "--show-error", "--max-time", "15", "--proto", "=https", "--noproxy", "*", "--resolve", h.Config.Domain + ":" + port + ":127.0.0.1", "https://" + h.Config.Domain + ":" + port + path}
	var input []byte
	if cookie != "" {
		if strings.ContainsAny(cookie, "\r\n\"\\") {
			return nil, fmt.Errorf("invalid private cookie")
		}
		args = append(args, "--config", "-")
		input = []byte("header = \"Cookie: " + cookie + "\"\n")
	}
	data, err := h.run(ctx, input, "", false, args...)
	if err != nil {
		return nil, err
	}
	if !raw {
		var v Object
		if err = decodeHost(data, maxJSON, &v); err != nil || v == nil {
			return nil, fmt.Errorf("invalid HTTPS JSON response")
		}
	}
	return data, nil
}
func scalar(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if integer(v) {
		return strconv.FormatInt(number(v), 10)
	}
	return ""
}
func (h *Host) httpsJSON(ctx context.Context, c Object, path, cookie string) (Object, error) {
	b, err := h.HTTPS(ctx, c, path, cookie, false)
	if err != nil {
		return nil, err
	}
	var v Object
	err = decodeHost(b, maxJSON, &v)
	return v, err
}
func (h *Host) RuntimeContext(ctx context.Context, t Object) (context.Context, context.CancelFunc) {
	if profile(t) != "runtime-v1" {
		return context.WithCancel(ctx)
	}
	id := text(t["id"])
	deadline, ok := h.deadlines[id]
	if !ok {
		deadline = h.Now().Add(120 * time.Second)
		h.deadlines[id] = deadline
	}
	return context.WithDeadline(ctx, deadline)
}
func wait(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return fmt.Errorf("runtime verification timed out")
	case <-timer.C:
		return nil
	}
}
func (h *Host) OriginalHealth(ctx context.Context, compose, adopted Object) error {
	for attempt := range 40 {
		if _, err := h.httpsJSON(ctx, compose, "/api/v1/health", ""); err == nil {
			auth, e := h.httpsJSON(ctx, compose, "/api/v1/auth/status", "")
			if e == nil && auth["setup_required"] == false {
				settings, e := h.httpsJSON(ctx, compose, "/api/v1/config", "")
				if e == nil {
					delete(settings, "public_transfers_paused")
					a, e1 := persistedSettings(settings)
					b, e2 := persistedSettings(object(adopted["baseline_config"]))
					if e1 == nil && e2 == nil && equal(a, b) {
						return nil
					}
				}
			}
		}
		if attempt < 39 {
			if err := wait(ctx, 2*time.Second); err != nil {
				return err
			}
		}
	}
	return fmt.Errorf("previous deployment did not recover; keep traffic closed")
}
func (h *Host) AutomaticChecks(ctx context.Context, compose, t Object) error {
	ctx, cancel := h.RuntimeContext(ctx, t)
	defer cancel()
	if err := h.runtimeChecks(ctx, compose); err != nil {
		return err
	}
	healthy := false
	for attempt := range 40 {
		if _, err := h.httpsJSON(ctx, compose, "/api/v1/health", ""); err == nil {
			healthy = true
			break
		}
		if attempt < 39 {
			if deadline, ok := ctx.Deadline(); ok && time.Until(deadline) < 2*time.Second {
				return fmt.Errorf("runtime verification timed out")
			}
			if err := wait(ctx, 2*time.Second); err != nil {
				return err
			}
		}
	}
	if !healthy {
		return fmt.Errorf("candidate HTTPS/API health did not become ready")
	}
	auth, err := h.httpsJSON(ctx, compose, "/api/v1/auth/status", "")
	if err != nil {
		return err
	}
	if auth["setup_required"] != false {
		return fmt.Errorf("candidate lost initialized account state")
	}
	settings, err := h.httpsJSON(ctx, compose, "/api/v1/config", "")
	if err != nil {
		return err
	}
	if settings["public_transfers_paused"] != true {
		return fmt.Errorf("candidate is not transfer-paused")
	}
	delete(settings, "public_transfers_paused")
	actual, err := persistedSettings(settings)
	if err != nil {
		return err
	}
	baseline, err := persistedSettings(object(object(t["adopted"])["baseline_config"]))
	if err != nil {
		return err
	}
	if !equal(actual, baseline) {
		return fmt.Errorf("candidate changed persisted operator settings")
	}
	html, err := h.HTTPS(ctx, compose, "/", "", true)
	if err != nil {
		return err
	}
	if !strings.Contains(string(html), "psst-web") {
		return fmt.Errorf("compiled website marker missing")
	}
	scripts := regexp.MustCompile(`(?:src|href)="([^" ]+\.js)"`).FindAllStringSubmatch(string(html), 3)
	if len(scripts) == 0 {
		return fmt.Errorf("compiled website scripts missing")
	}
	for _, match := range scripts {
		path := match[1]
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
			return fmt.Errorf("invalid compiled asset locator")
		}
		b, e := h.HTTPS(ctx, compose, path, "", true)
		if e != nil {
			return e
		}
		if len(b) <= 10 {
			return fmt.Errorf("compiled asset unavailable")
		}
	}
	if !boolean(t["restoring"]) {
		release, e := h.httpsJSON(ctx, compose, "/licenses/release.json", "")
		if e != nil {
			return e
		}
		m := object(t["manifest"])
		if release["version"] != m["version"] || release["revision"] != object(m["source"])["commit"] {
			return fmt.Errorf("served source/release metadata mismatch")
		}
	}
	paused, err := h.Incident(ctx, compose, "incident-status")
	if err != nil {
		return err
	}
	if !paused {
		return fmt.Errorf("candidate must remain paused until verification")
	}
	if profile(t) == "runtime-v1" {
		return h.sqliteIntegrity(ctx, text(object(t["adopted"])["database"]))
	}
	return nil
}
func (h *Host) runtimeChecks(ctx context.Context, compose Object) error {
	rows, err := h.containers(ctx, true, true)
	if err != nil {
		return err
	}
	actual := Object{}
	for _, row := range rows {
		v := object(row)
		name := text(object(object(v["Config"])["Labels"])["com.docker.compose.service"])
		if name == "" || actual[name] != nil {
			return fmt.Errorf("candidate has ambiguous containers")
		}
		actual[name] = v
	}
	services := object(compose["services"])
	if len(actual) != len(services) {
		return fmt.Errorf("candidate has unexpected containers")
	}
	for name, item := range services {
		s := object(item)
		c := object(actual[name])
		limits := object(c["HostConfig"])
		state := object(c["State"])
		if state["Running"] != true || boolean(state["Restarting"]) || object(c["Config"])["Image"] != s["image"] {
			return fmt.Errorf("candidate runtime image/service mismatch")
		}
		security := false
		for _, v := range stringsOf(limits["SecurityOpt"]) {
			if has([]string{"no-new-privileges", "no-new-privileges=true", "no-new-privileges:true"}, v) {
				security = true
			}
		}
		if limits["ReadonlyRootfs"] != true || !has(stringsOf(limits["CapDrop"]), "ALL") || len(array(limits["CapAdd"])) != 0 || boolean(limits["Privileged"]) || !security {
			return fmt.Errorf("candidate runtime lost hardening")
		}
		physical, e := Bindings(compose, name)
		if e != nil {
			return e
		}
		mounted := Object{}
		for _, row := range array(c["Mounts"]) {
			m := object(row)
			if m["Type"] == "volume" {
				mounted[text(m["Destination"])] = m["Name"]
			}
		}
		if !equal(mounted, physical) {
			return fmt.Errorf("candidate runtime uses different storage")
		}
		if err := checkPorts(s, limits, true); err != nil {
			return err
		}
		for logical, item := range object(s["networks"]) {
			network := object(item)
			if ip := text(network["ipv4_address"]); ip != "" {
				physical := text(object(object(compose["networks"])[logical])["name"])
				actualIP := object(object(object(c["NetworkSettings"])["Networks"])[physical])
				if actualIP["IPAddress"] != ip {
					return fmt.Errorf("candidate proxy network address differs from trust policy")
				}
			}
		}
	}
	return nil
}
func checkPorts(service, limits Object, isolated bool) error {
	expected := array(service["ports"])
	ports := object(limits["PortBindings"])
	wanted := set()
	for _, item := range expected {
		p := object(item)
		protocol := text(p["protocol"])
		if protocol == "" {
			protocol = "tcp"
		}
		wanted[scalar(p["target"])+"/"+protocol] = true
	}
	if len(ports) != len(wanted) {
		return fmt.Errorf("published ports differ from adopted Compose")
	}
	for _, item := range expected {
		p := object(item)
		protocol := text(p["protocol"])
		if protocol == "" {
			protocol = "tcp"
		}
		bindings := array(ports[scalar(p["target"])+"/"+protocol])
		if len(bindings) == 0 {
			return fmt.Errorf("expected listener is missing")
		}
		host := text(p["host_ip"])
		if host == "" {
			host = "0.0.0.0"
		}
		for _, item := range bindings {
			b := object(item)
			actual := text(b["HostIp"])
			allowed := actual == host
			if host == "0.0.0.0" || isolated && host != "127.0.0.1" {
				allowed = allowed || actual == "" || actual == "::"
			}
			if scalar(b["HostPort"]) != scalar(p["published"]) || !allowed {
				return fmt.Errorf("listener differs from adopted ingress policy")
			}
		}
	}
	return nil
}
func (h *Host) AuthenticatedChecks(ctx context.Context, private Object, cookie string) error {
	identity, err := h.httpsJSON(ctx, private, "/api/v1/auth/me", cookie)
	if err != nil {
		return err
	}
	if identity["role"] != "admin" && object(identity["user"])["role"] != "admin" {
		return fmt.Errorf("verification requires a current administrator session")
	}
	for _, name := range []string{"storage", "counter", "orphan"} {
		status, e := h.httpsJSON(ctx, private, "/api/v1/admin/"+name+"-checks", cookie)
		if e != nil {
			return e
		}
		if status["state"] != "checked" || status["scan_pending"] != false || text(status["last_scan_completed_at"]) == "" || status["scan_error_code"] != nil && status["scan_error_code"] != "" {
			return fmt.Errorf("storage/security reconciliation is not complete")
		}
		for _, key := range []string{"issue_count", "busy_count", "unavailable_count", "failed_count", "pending_count", "pending_directories", "pending_candidates", "unsupported_count", "saturated", "unstable"} {
			if number(status[key]) != 0 || boolean(status[key]) {
				return fmt.Errorf("storage/security reconciliation reported unresolved state")
			}
		}
	}
	return nil
}
func (h *Host) VerificationHook(ctx context.Context, private, transaction string) (Object, error) {
	if h.Config.VerificationHook == nil {
		return nil, nil
	}
	hook := *h.Config.VerificationHook
	if err := h.check(hook, false, true, true); err != nil {
		return nil, err
	}
	return h.json(ctx, hook, "--transaction", transaction, "--compose", private)
}
func (h *Host) FreePorts(compose Object) error {
	seen := set()
	for _, item := range object(compose["services"]) {
		for _, item := range array(object(item)["ports"]) {
			p := object(item)
			number, err := strconv.Atoi(scalar(p["published"]))
			if err != nil || number < 1 || number > 65535 || seen[strconv.Itoa(number)] || p["protocol"] != nil && p["protocol"] != "tcp" {
				return fmt.Errorf("duplicate or unsupported deployment port")
			}
			seen[strconv.Itoa(number)] = true
			l, err := net.Listen("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(number)))
			if err != nil {
				return fmt.Errorf("deployment port is still occupied after shutdown")
			}
			if err = l.Close(); err != nil {
				return err
			}
		}
	}
	return nil
}
func (h *Host) ValidateCaddy(ctx context.Context, file string) error {
	compose, err := load(file)
	if err != nil {
		return err
	}
	for _, name := range []string{"caddy", "external-proxy"} {
		s := object(object(compose["services"])[name])
		if s == nil {
			continue
		}
		env := filepath.Join(filepath.Dir(file), name+".validate.env")
		var b strings.Builder
		for _, key := range keys(object(s["environment"])) {
			v := scalar(object(s["environment"])[key])
			if strings.ContainsAny(v, "\r\n") {
				return fmt.Errorf("invalid Caddy validation environment")
			}
			b.WriteString(key + "=" + v + "\n")
		}
		if err = os.WriteFile(env, []byte(b.String()), 0o600); err != nil {
			return err
		}
		args := []string{"docker", "run", "--rm", "--network", "none", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--entrypoint", "caddy", "--env-file", env}
		for _, dir := range []string{"/data", "/config", "/tmp"} {
			args = append(args, "--tmpfs", dir+":rw,noexec,nosuid,nodev,mode=1777")
		}
		for _, item := range array(s["volumes"]) {
			m := object(item)
			if m["type"] == "bind" {
				if m["read_only"] != true {
					_ = os.Remove(env)
					return fmt.Errorf("Caddy configuration must be read-only")
				}
				args = append(args, "--mount", "type=bind,src="+text(m["source"])+",dst="+text(m["target"])+",readonly")
			}
		}
		_, err = h.run(ctx, nil, "", false, append(args, text(s["image"]), "validate", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile")...)
		removeErr := os.Remove(env)
		if err != nil {
			return err
		}
		if removeErr != nil {
			return removeErr
		}
	}
	return nil
}
