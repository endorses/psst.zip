package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
	"golang.org/x/crypto/bcrypt"
)

// This invokes the production entry point, including migrations, startup recovery,
// all background workers, the real router and signal-driven shutdown. The child
// receives an explicit environment rather than inheriting operator configuration.
func TestRestoreServerProcess(t *testing.T) {
	mode := os.Getenv("PSST_RESTORE_TEST_PROCESS")
	if mode == "" {
		return
	}
	if mode != "server" && mode != "pause" && mode != "resume" && mode != "incident-status" {
		t.Fatal("invalid restore helper mode")
	}
	os.Args = []string{os.Args[0]}
	if mode != "server" {
		os.Args = append(os.Args, mode)
	}
	main()
	os.Exit(0)
}

type restoreProcess struct {
	cmd     *exec.Cmd
	done    chan struct{}
	err     error
	logPath string
	logFile *os.File
	base    string
	stopped bool
}

func restoreEnvironment(directory, address, mode string) []string {
	return []string{
		"PSST_RESTORE_TEST_PROCESS=" + mode,
		"DB_PATH=" + filepath.Join(directory, "server.db"),
		"STORAGE_PATH=" + filepath.Join(directory, "files"),
		"LISTEN_ADDR=" + address,
		"PUBLIC_URL=http://" + address,
		"AUTH_ALLOW_INSECURE_HTTP=true",
		"RATE_LIMIT_GLOBAL=1000", "RATE_LIMIT_BURST=1000",
		"RATE_LIMIT_CREATION=1000", "RATE_LIMIT_CREATION_BURST=1000",
		"CLEANUP_INTERVAL=1h", "TZ=UTC", "GORACE=halt_on_error=1 atexit_sleep_ms=0",
	}
}

func startRestoreProcess(t *testing.T, ctx context.Context, directory string) *restoreProcess {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err = listener.Close(); err != nil {
		t.Fatal(err)
	}
	_, port, err := net.SplitHostPort(address)
	if err != nil || port == "8080" || port == "18480" || port == "80" {
		t.Fatal("unsafe restore test address")
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	logFile, err := os.CreateTemp(t.TempDir(), "server-*.log")
	if err != nil {
		t.Fatal(err)
	}
	p := &restoreProcess{done: make(chan struct{}), logPath: logFile.Name(), logFile: logFile, base: "http://" + address}
	p.cmd = exec.CommandContext(ctx, executable, "-test.run=^TestRestoreServerProcess$", "-test.timeout=70s")
	p.cmd.Env = restoreEnvironment(directory, address, "server")
	p.cmd.Stdout = logFile
	p.cmd.Stderr = logFile
	if err = p.cmd.Start(); err != nil {
		_ = logFile.Close()
		t.Fatal(err)
	}
	go func() { p.err = p.cmd.Wait(); close(p.done) }()
	t.Cleanup(func() { p.stop(t) })
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case <-p.done:
			t.Fatalf("restore server exited: %v\n%s", p.err, p.logs())
		default:
		}
		response, requestErr := client.Get(p.base + "/api/v1/health")
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				select {
				case <-p.done:
					t.Fatalf("restore server exited: %v\n%s", p.err, p.logs())
				default:
				}
				return p
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatalf("restore server did not become ready\n%s", p.logs())
	return nil
}
func (p *restoreProcess) logs() string { data, _ := os.ReadFile(p.logPath); return string(data) }
func (p *restoreProcess) stop(t *testing.T) {
	t.Helper()
	if p.stopped {
		return
	}
	p.stopped = true
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) {
		t.Errorf("signal restore server: %v", err)
	}
	reaped := false
	select {
	case <-p.done:
		reaped = true
	case <-time.After(15 * time.Second):
		_ = p.cmd.Process.Kill()
		select {
		case <-p.done:
			reaped = true
		case <-time.After(2 * time.Second):
			t.Error("restore subprocess could not be reaped")
		}
		t.Errorf("restore server failed graceful shutdown\n%s", p.logs())
	}
	if reaped && p.err != nil {
		t.Errorf("restore subprocess exited: %v\n%s", p.err, p.logs())
	}
	if err := p.logFile.Close(); err != nil {
		t.Error(err)
	}
}
func restoreCLI(t *testing.T, ctx context.Context, directory, mode string) database.IncidentState {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(commandCtx, executable, "-test.run=^TestRestoreServerProcess$")
	cmd.Env = restoreEnvironment(directory, "127.0.0.1:0", mode)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("restore %s command: %v %s", mode, err, output)
	}
	var state database.IncidentState
	if err = json.Unmarshal(output, &state); err != nil {
		t.Fatalf("restore command response: %v %s", err, output)
	}
	return state
}
func restoreHTTP(t *testing.T, p *restoreProcess, method, path, token string, body []byte, want int) ([]byte, *http.Response) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, p.base+"/api/v1"+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", p.base)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 128*1024+1))
	closeErr := response.Body.Close()
	if err != nil || closeErr != nil || len(data) > 128*1024 {
		t.Fatalf("read restore response: %v %v", err, closeErr)
	}
	if response.StatusCode != want {
		t.Fatalf("%s %s: status %d want %d: %s\n%s", method, path, response.StatusCode, want, data, p.logs())
	}
	return data, response
}
func restoreLogin(t *testing.T, p *restoreProcess, name string, admin bool) string {
	t.Helper()
	kind := "device"
	if admin {
		kind = "web"
	}
	body, _ := json.Marshal(map[string]string{"username": name, "password": "restore-test-password", "session_type": kind})
	data, response := restoreHTTP(t, p, http.MethodPost, "/auth/login", "", body, http.StatusOK)
	if admin {
		for _, cookie := range response.Cookies() {
			if cookie.Name == "psst_session" {
				return cookie.Value
			}
		}
		t.Fatal("missing restored admin session")
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(data, &result); err != nil || result.Token == "" {
		t.Fatal("missing restored owner session")
	}
	return result.Token
}
func restoreJSON(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatalf("decode restore response: %v", err)
	}
}

const restoreTransfer = "11111111-1111-4111-8111-111111111111"
const restoreFile = "22222222-2222-4222-8222-222222222222"
const revokeTransfer = "33333333-3333-4333-8333-333333333333"
const revokeFile = "44444444-4444-4444-8444-444444444444"
const restoreOwner = "restore-owner"

func seedRestoreFiles(t *testing.T, directory string, payload, manifest []byte) {
	t.Helper()
	db, err := database.Open(filepath.Join(directory, "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	q := database.NewQueries(db)
	hash, err := bcrypt.GenerateFromPassword([]byte("restore-test-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range []database.User{{ID: "restore-admin", Username: "restore-admin", Role: "admin", PasswordHash: hash}, {ID: restoreOwner, Username: restoreOwner, Role: "user", PasswordHash: hash}} {
		if err = q.CreateUser(user, false); err != nil {
			t.Fatal(err)
		}
	}
	disk, err := store.NewDiskStore(filepath.Join(directory, "files"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{{restoreTransfer, restoreFile}, {revokeTransfer, revokeFile}} {
		if err = q.CreateTransfer(pair[0], time.Now().Add(time.Hour), 2, nil, restoreOwner); err != nil {
			t.Fatal(err)
		}
		if err = q.CreateFileWithQuota(pair[1], pair[0], int64(len(payload)), 1<<20); err != nil {
			t.Fatal(err)
		}
		if err = disk.Save(pair[0]+"/"+pair[1], bytes.NewReader(payload)); err != nil {
			t.Fatal(err)
		}
		if err = q.UpdateFileOffset(pair[1], int64(len(payload)), true); err != nil {
			t.Fatal(err)
		}
		if err = q.SaveManifest(pair[0], manifest); err != nil {
			t.Fatal(err)
		}
		if err = q.CompleteTransfer(pair[0]); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := q.ReserveFileDownload(restoreTransfer, restoreFile); err != nil || !ok {
		t.Fatalf("seed consumed allowance: %v %v", ok, err)
	}
}

// All DB connections are closed and WAL is checkpointed before copying. The
// fixture has two payload files; copying never consults the live instance.
func coldRestoreCopy(t *testing.T, source, destination string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-journal"} {
		info, err := os.Stat(filepath.Join(source, "server.db") + suffix)
		if err == nil && info.Size() != 0 {
			t.Fatalf("refusing non-cold database copy: %s", suffix)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	copyFile := func(from, to string, mode fs.FileMode) {
		t.Helper()
		input, err := os.Open(from)
		if err != nil {
			t.Fatal(err)
		}
		output, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
		if err != nil {
			_ = input.Close()
			t.Fatal(err)
		}
		_, copyErr := io.Copy(output, input)
		syncErr := output.Sync()
		err = errors.Join(copyErr, syncErr, input.Close(), output.Close())
		if err != nil {
			t.Fatal(err)
		}
	}
	copyFile(filepath.Join(source, "server.db"), filepath.Join(destination, "server.db"), 0600)
	err := filepath.WalkDir(filepath.Join(source, "files"), func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.Mkdir(target, 0700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported fixture file")
		}
		copyFile(path, target, 0600)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// The fixture follows the repository chunked-v1 framing: nonce + GCM of
// 16-byte context, 8-byte frame index, 8-byte plaintext length, and plaintext.
// Keys stay in parent-test memory, outside both backup trees and child envs.
// This verifies protocol fixture decryption, not an Android/iOS device gate.
func restoreEncryptedFixture(t *testing.T) (key, plain, header, payload, manifest []byte) {
	t.Helper()
	key = bytes.Repeat([]byte{0x42}, 32)
	plain = []byte("psst.zip isolated cold restore: authenticated bytes survive restart")
	header = make([]byte, 32)
	for i := 0; i < 16; i++ {
		header[i] = byte(i + 1)
	}
	binary.BigEndian.PutUint64(header[24:], uint64(len(plain)))
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{0x19}, aead.NonceSize())
	payload = append(append([]byte{}, nonce...), aead.Seal(nil, nonce, append(append([]byte{}, header...), plain...), nil)...)
	metadata, err := json.Marshal(map[string]any{"files": []map[string]any{{"name": "restore.bin", "size": len(plain), "mime_type": "application/octet-stream", "blob_id": restoreFile, "encoding": "chunked-v1", "chunk_size": 4 * 1024 * 1024, "encryption_id": fmt.Sprintf("%x", header[:16])}}})
	if err != nil {
		t.Fatal(err)
	}
	manifestNonce := bytes.Repeat([]byte{0x23}, aead.NonceSize())
	manifest = append(append([]byte{}, manifestNonce...), aead.Seal(nil, manifestNonce, metadata, nil)...)
	return
}
func restoreDecrypt(t *testing.T, key, wire []byte) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(wire) < aead.NonceSize()+aead.Overhead() {
		t.Fatal("truncated restored encrypted frame")
	}
	plain, err := aead.Open(nil, wire[:aead.NonceSize()], wire[aead.NonceSize():], nil)
	if err != nil {
		t.Fatalf("restored data failed authenticated decryption: %v", err)
	}
	return plain
}

func TestColdRestoreRunsRealServerStartupAndHTTPPolicies(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	source, restored := t.TempDir(), t.TempDir()
	key, plain, header, payload, manifest := restoreEncryptedFixture(t)
	seedRestoreFiles(t, source, payload, manifest)
	original := startRestoreProcess(t, ctx, source)
	ownerToken := restoreLogin(t, original, restoreOwner, false)
	adminToken := restoreLogin(t, original, "restore-admin", true)
	restoreHTTP(t, original, http.MethodGet, "/auth/me", ownerToken, nil, http.StatusOK)
	original.stop(t)
	// Model a cold backup with a durable unacknowledged IO lease and historical
	// coverage. Data/accounts use canonical query/store APIs; only cached progress
	// stamps are fault-injected to prove startup does not reuse the old clean mark.
	db, err := database.Open(filepath.Join(source, "server.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	q := database.NewQueries(db)
	policy := database.DefaultTrafficPolicy()
	policy.EnforcementEnabled = true
	policy.Basis = "combined"
	policy.ServerBudgetBytes = 1 << 30
	policy.DefaultAccountBudgetBytes = 1 << 30
	if err = q.SetTrafficPolicy(policy); err != nil {
		t.Fatal(err)
	}
	observed, err := q.ReserveTraffic(restoreOwner, true, 13, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err = q.SettleTraffic(observed.ID, 13); err != nil {
		t.Fatal(err)
	}
	if _, err = q.ReserveTraffic(restoreOwner, false, 80, time.Now()); err != nil {
		t.Fatal(err)
	}
	const oldCoverage = int64(946684800)
	for _, statement := range []string{
		`UPDATE file_reconciliation_progress SET cursor='zzzz',last_scan_completed_at=946684800 WHERE id=1`,
		`UPDATE counter_rebuild_progress SET phase='idle',last_scan_completed_at=946684800 WHERE id=1`,
		`UPDATE orphan_progress SET completed_at=946684800 WHERE id=1`,
	} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	var generation int64
	if err = db.QueryRow(`SELECT generation FROM file_reconciliation_progress WHERE id=1`).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	var busy, logFrames, checkpointed int
	if err = db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logFrames, &checkpointed); err != nil || busy != 0 {
		t.Fatalf("checkpoint: %v busy=%d", err, busy)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	coldRestoreCopy(t, source, restored)
	// Revocations made after the snapshot are absent from an old restore. Mutate
	// only the stopped source through canonical APIs, never recopy its new state.
	func() {
		sourceDB, openErr := database.Open(filepath.Join(source, "server.db"))
		if openErr != nil {
			t.Fatal(openErr)
		}
		defer sourceDB.Close()
		sourceQueries := database.NewQueries(sourceDB)
		tokenDigest := sha256.Sum256([]byte(ownerToken))
		session, owner, lookupErr := sourceQueries.SessionByHash(tokenDigest[:])
		if lookupErr != nil || owner.ID != restoreOwner {
			t.Fatalf("source session lookup: %v", lookupErr)
		}
		if err := sourceQueries.DeleteAccountSession(session.ID, restoreOwner); err != nil {
			t.Fatal(err)
		}
		if _, _, err := sourceQueries.SessionByHash(tokenDigest[:]); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("source session was not revoked: %v", err)
		}
		if err := sourceQueries.RevokeTransfer(revokeTransfer); err != nil {
			t.Fatal(err)
		}
		revoked, err := sourceQueries.GetTransfer(revokeTransfer)
		if err != nil || revoked.Status != "revoked" {
			t.Fatalf("source resource was not revoked: %v", err)
		}
	}()
	if state := restoreCLI(t, ctx, restored, "pause"); !state.PublicTransfersPaused {
		t.Fatal("pre-start pause not persisted")
	}
	server := startRestoreProcess(t, ctx, restored)
	// Restored accounts and sessions work, but exposure is denied from the first
	// health-ready response; neither metadata reads nor denied IO spend attempts.
	// Both were revoked in the source after backup. Their restored authority is
	// proof that rollback can resurrect sessions and links, even while paused.
	restoreHTTP(t, server, http.MethodGet, "/auth/me", ownerToken, nil, http.StatusOK)
	restoreHTTP(t, server, http.MethodGet, "/transfers/"+revokeTransfer, "", nil, http.StatusOK)
	configData, _ := restoreHTTP(t, server, http.MethodGet, "/config", "", nil, http.StatusOK)
	var configState struct {
		Paused bool `json:"public_transfers_paused"`
	}
	restoreJSON(t, configData, &configState)
	if !configState.Paused {
		t.Fatal("restored server opened while paused")
	}
	for _, path := range []string{"/transfers/" + restoreTransfer + "/manifest", "/transfers/" + restoreTransfer + "/files/" + restoreFile} {
		data, _ := restoreHTTP(t, server, http.MethodGet, path, "", nil, http.StatusServiceUnavailable)
		if !bytes.Contains(data, []byte(`"public_transfers_paused"`)) {
			t.Fatalf("pause policy missing: %s", data)
		}
	}
	restoreHTTP(t, server, http.MethodPost, "/transfers", ownerToken, []byte(`{}`), http.StatusServiceUnavailable)
	for _, path := range []string{"/admin/storage-checks", "/admin/counter-checks", "/admin/orphan-checks"} {
		data, _ := restoreHTTP(t, server, http.MethodGet, path, adminToken, nil, http.StatusOK)
		var status struct {
			Completed *time.Time `json:"last_scan_completed_at"`
		}
		restoreJSON(t, data, &status)
		if status.Completed != nil && status.Completed.Unix() == oldCoverage {
			t.Fatalf("startup retained backup coverage: %s", path)
		}
	}
	uri := url.URL{Scheme: "file", Path: filepath.Join(restored, "server.db"), RawQuery: "mode=ro&_pragma=busy_timeout(1000)"}
	reader, err := sql.Open("sqlite", uri.String())
	if err != nil {
		t.Fatal(err)
	}
	var restoredGeneration int64
	err = reader.QueryRowContext(ctx, `SELECT generation FROM file_reconciliation_progress WHERE id=1`).Scan(&restoredGeneration)
	closeErr := reader.Close()
	if err != nil || closeErr != nil || restoredGeneration != generation+1 {
		t.Fatalf("startup coverage generation: %d -> %d errors %v %v", generation, restoredGeneration, err, closeErr)
	}
	checkTraffic := func(p *restoreProcess) database.TrafficBudgetSnapshot {
		t.Helper()
		data, _ := restoreHTTP(t, p, http.MethodGet, "/auth/traffic-usage", ownerToken, nil, http.StatusOK)
		var snapshot database.TrafficBudgetSnapshot
		restoreJSON(t, data, &snapshot)
		if snapshot.Usage.ConservativeDownloadedBytes != 80 || snapshot.Usage.ReservedDownloadedBytes != 0 || snapshot.Usage.ObservedUploadedBytes != 13 || snapshot.Usage.ChargedBytes < 93 {
			t.Fatalf("restored traffic not preserved/conservative: %+v", snapshot.Usage)
		}
		return snapshot
	}
	checkTraffic(server)
	checkAllowance := func(p *restoreProcess, remaining int) {
		t.Helper()
		data, _ := restoreHTTP(t, p, http.MethodGet, "/transfers/"+restoreTransfer, "", nil, http.StatusOK)
		var transfer struct {
			MaxDownloads int `json:"max_downloads"`
			Files        []struct {
				ID        string `json:"id"`
				Remaining int    `json:"remaining_downloads"`
			} `json:"files"`
		}
		restoreJSON(t, data, &transfer)
		if transfer.MaxDownloads != 2 || len(transfer.Files) != 1 || transfer.Files[0].ID != restoreFile || transfer.Files[0].Remaining != remaining {
			t.Fatalf("restored allowance metadata: %s", data)
		}
	}
	checkAllowance(server, 1)
	if state := restoreCLI(t, ctx, restored, "resume"); state.PublicTransfersPaused {
		t.Fatal("deliberate resume failed")
	}
	for _, item := range []struct {
		path string
		want []byte
	}{{"/transfers/" + restoreTransfer + "/manifest", manifest}, {"/transfers/" + restoreTransfer + "/files/" + restoreFile, payload}} {
		data, _ := restoreHTTP(t, server, http.MethodGet, item.path, "", nil, http.StatusOK)
		if !bytes.Equal(data, item.want) || sha256.Sum256(data) != sha256.Sum256(item.want) {
			t.Fatalf("restored bytes differ: %s", item.path)
		}
		decrypted := restoreDecrypt(t, key, data)
		if strings.HasSuffix(item.path, "/manifest") {
			var metadata struct {
				Files []struct {
					BlobID   string `json:"blob_id"`
					Encoding string `json:"encoding"`
					Size     int    `json:"size"`
					Context  string `json:"encryption_id"`
				} `json:"files"`
			}
			restoreJSON(t, decrypted, &metadata)
			if len(metadata.Files) != 1 || metadata.Files[0].BlobID != restoreFile || metadata.Files[0].Encoding != "chunked-v1" || metadata.Files[0].Size != len(plain) || metadata.Files[0].Context != fmt.Sprintf("%x", header[:16]) {
				t.Fatal("restored authenticated manifest differs")
			}
		} else if !bytes.Equal(decrypted, append(append([]byte{}, header...), plain...)) {
			t.Fatal("restored authenticated file/header differs")
		}

	}
	checkAllowance(server, 0)
	exhausted, _ := restoreHTTP(t, server, http.MethodGet, "/transfers/"+restoreTransfer+"/files/"+restoreFile, "", nil, http.StatusGone)
	if !bytes.Contains(exhausted, []byte(`"download_limit"`)) {
		t.Fatalf("missing exhausted policy: %s", exhausted)
	}
	restoreHTTP(t, server, http.MethodDelete, "/transfers/"+revokeTransfer, ownerToken, nil, http.StatusNoContent)
	restoreHTTP(t, server, http.MethodGet, "/transfers/"+revokeTransfer, "", nil, http.StatusNotFound)
	beforeRestart := checkTraffic(server)
	server.stop(t)
	server = startRestoreProcess(t, ctx, restored)
	if state := restoreCLI(t, ctx, restored, "incident-status"); state.PublicTransfersPaused {
		t.Fatal("deliberate resume did not persist")
	}
	restoreHTTP(t, server, http.MethodGet, "/transfers/"+revokeTransfer, "", nil, http.StatusNotFound)
	restoreHTTP(t, server, http.MethodGet, "/transfers/"+revokeTransfer+"/files/"+revokeFile, "", nil, http.StatusNotFound)
	checkAllowance(server, 0)
	restoreHTTP(t, server, http.MethodGet, "/transfers/"+restoreTransfer+"/files/"+restoreFile, "", nil, http.StatusGone)
	afterRestart := checkTraffic(server)
	if afterRestart.Usage.ConservativeDownloadedBytes != beforeRestart.Usage.ConservativeDownloadedBytes || afterRestart.Usage.ObservedDownloadedBytes != beforeRestart.Usage.ObservedDownloadedBytes {
		t.Fatal("restart recharged recovered or observed traffic")
	}
	server.stop(t)
	t.Logf("Cold-copy restore exercised actual main startup, paused exposure, coverage generation %d -> %d, exact payload/manifest bytes, one remaining download, post-backup revocations resurrected by rollback, new revocation persisted and idempotent conservative lease recovery", generation, restoredGeneration)
}
