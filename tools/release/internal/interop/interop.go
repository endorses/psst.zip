// Package interop runs one bounded, disposable backend/CLI/production-TypeScript
// compatibility scenario. Protocol encryption remains in the independent client
// executable and unchanged TypeScript peer, not a test-only Go implementation.
package interop

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"
)

func (f *fixture) exercise(gateway *gateway, adminPassword string) {
	password, token := f.bootstrap(adminPassword)
	account := f.newCLI(f.directory)
	account.call([]string{"auth", "login", "--server", f.origin, "--username", "integration-member", "--password-stdin"}, password+"\n", 0, nil)
	// A real short-lived invitation ages while all crypto scenarios execute. This
	// replaces direct SQLite mutation without adding an SQL driver/tool dependency
	// or sleeping through normal retention. At most its remaining 1s is awaited.
	expires := f.resource(account.call([]string{"receive", "create", "--expires", "1s"}, "", 0, nil).Data)
	f.check(!expires.ExpiresAt.IsZero(), "expiring Receive invitation lacks its authoritative deadline")
	f.request("GET", "/slots/"+expires.ID+"/availability", nil, "", 200)
	large, empty := filepath.Join(f.directory, "boundary-ä.bin"), filepath.Join(f.directory, "empty.txt")
	payload := append(bytes.Repeat([]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}, 262144), []byte("second authenticated frame")...)
	f.write(large, payload)
	f.write(empty, []byte{})
	files := []fileInput{{"boundary-ä.bin", large}, {"empty.txt", empty}}
	sent := f.resource(account.call([]string{"send", large, empty, "--max-downloads", "1"}, "", 0, nil).Data)
	var verified []peerFile
	f.decode(f.peer("send-decrypt", map[string]any{"link": sent.Link, "files": files}), &verified)
	f.check(len(verified) == 2, "TypeScript did not authenticate both CLI files")
	if len(verified) > 0 {
		response := f.request("GET", "/transfers/"+sent.ID+"/files/"+verified[0].FileID, nil, "", 410)
		f.check(f.text(response, "code") == "download_limit", "download allowance did not stay exhausted")
	}
	browser := f.resource(f.peer("send-create", map[string]any{"token": token, "files": files}))
	gateway.failReceipt("/api/v1/transfers/" + browser.ID + "/downloaded")
	destination := filepath.Join(f.directory, "download")
	failed := account.call([]string{"fetch", "--link-stdin", "--output-dir", destination}, browser.Link+"\n", 5, nil)
	f.check(failed.Error.Code == "receipt_pending", "interrupted acknowledgement did not preserve pending receipt")
	f.compare(destination, files)
	before := gateway.counts()
	retried := account.call([]string{"fetch", "--link-stdin", "--output-dir", destination}, browser.Link+"\n", 0, nil)
	var receipt struct {
		Pending *bool `json:"receipt_pending"`
	}
	f.decode(retried.Data, &receipt)
	f.check(receipt.Pending != nil && !*receipt.Pending && maps.Equal(before, gateway.counts()), "receipt retry redownloaded files or remained pending")
	f.compare(destination, files)
	receive := f.resource(account.call([]string{"receive", "create", "--max-files", "1"}, "", 0, nil).Data)
	submission := f.resource(f.peer("receive-upload", map[string]any{"link": receive.Link, "files": files[:1]}))
	received, backup := filepath.Join(f.directory, "received"), filepath.Join(f.directory, "receive-backup.age")
	backupPhrase := []byte(randomSecret())
	f.check(len(backupPhrase) > 0, "private backup phrase could not be generated")
	account.call([]string{"vault", "backup", "--output", backup}, "", 0, backupPhrase)
	info, err := os.Lstat(backup)
	f.check(err == nil && info != nil && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0, "encrypted CLI backup has unsafe permissions")
	recovered := f.newCLI(filepath.Join(f.directory, "recovered"))
	recovered.call([]string{"vault", "restore", "--input", backup}, "", 0, backupPhrase)
	recovered.call([]string{"receive", "fetch", receive.ID, "--output-dir", received}, "", 0, nil)
	f.compare(filepath.Join(received, submission.ID), files[:1])
	response := f.request("POST", "/slots/"+receive.ID+"/transfers", map[string]any{}, "", 403)
	f.check(f.text(response, "code") == "receive_file_limit", "Receive allocation allowance was replenished")
	browserReceive := f.resource(f.peer("receive-create", map[string]any{"token": token}))
	account.call([]string{"upload", "--link-stdin", large}, browserReceive.Link+"\n", 0, nil)
	verified = nil
	f.decode(f.peer("receive-decrypt", map[string]any{"token": token, "slot": browserReceive, "files": files[:1]}), &verified)
	f.check(len(verified) == 1, "TypeScript owner could not authenticate CLI guest upload")
	account.call([]string{"receive", "revoke", receive.ID}, "", 0, nil)
	response = f.request("POST", "/slots/"+receive.ID+"/transfers", map[string]any{}, "", 404)
	f.check(f.text(response, "code") == "resource_not_found", "revoked Receive invitation accepted uploads")
	if f.failure == nil {
		remaining := time.Until(expires.ExpiresAt.Add(10 * time.Millisecond))
		f.check(remaining <= time.Second+20*time.Millisecond, "fixture expiry unexpectedly exceeds its one-second budget")
		if remaining > 0 && f.failure == nil {
			timer := time.NewTimer(remaining)
			select {
			case <-f.ctx.Done():
				f.check(false, "integration exceeded its 60-second deadline")
			case <-timer.C:
			}
			timer.Stop()
		}
	}
	// Production's incident monitor may already have removed the expired row.
	// Both typed denials enforce expiry; neither may allocate a transfer.
	response = f.request("POST", "/slots/"+expires.ID+"/transfers", map[string]any{}, "", 410, 404)
	expiryCode := f.text(response, "code")
	f.check(expiryCode == "link_expired" || expiryCode == "resource_not_found", "expired Receive invitation accepted uploads")
}
func waitReady(ctx context.Context, backend *child, api *api) error {
	timer := time.NewTicker(20 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-backend.done:
			return errors.New("disposable backend exited before readiness")
		default:
		}
		if response, err := api.request("GET", "/health", nil, "", 200); err == nil && response["service"] == "psst.zip" {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("integration exceeded its 60-second deadline")
		case <-backend.done:
			return errors.New("disposable backend exited before readiness")
		case <-timer.C:
		}
	}
}

// Run accepts --binary ABSOLUTE and an optional already-built --backend-binary.
// Without a backend binary, one normal backend build is included in the 60s
// deadline; CI can reuse an existing binary to avoid that build.
func Run(ctx context.Context, root string, args []string, stdout, stderr io.Writer) error {
	flags := flag.NewFlagSet("interop", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	binary := flags.String("binary", "", "absolute built psst executable")
	backend := flags.String("backend-binary", "", "optional absolute built backend executable")
	if err := flags.Parse(args); err != nil {
		return errors.New("interop requires --binary ABSOLUTE [--backend-binary ABSOLUTE]")
	}
	if flags.NArg() != 0 {
		return errors.New("interop accepts flags only")
	}
	validBinary := func(path string) bool {
		info, err := os.Stat(path)
		return filepath.IsAbs(path) && err == nil && info.Mode().IsRegular()
	}
	if !validBinary(*binary) {
		return errors.New("supply an absolute built CLI binary")
	}
	if *backend != "" && !validBinary(*backend) {
		return errors.New("supply an absolute built backend binary")
	}
	if runtime.GOOS == "windows" {
		return errors.New("the private file-descriptor interoperability fixture requires Linux or macOS")
	}
	if info, err := os.Stat(filepath.Join(root, "web/node_modules")); err != nil || !info.IsDir() {
		return errors.New("install web dependencies before interoperability checks")
	}
	started := time.Now()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	directory, err := os.MkdirTemp("", "psst-cli-integration-")
	if err != nil {
		return errors.New("private interoperability directory could not be created")
	}
	defer os.RemoveAll(directory)
	if *backend == "" {
		*backend = filepath.Join(directory, "server")
		goTool := os.Getenv("GO")
		if goTool == "" {
			goTool = "go"
		}
		command := exec.Command(goTool, "build", "-o", *backend, "./cmd/server")
		command.Dir = filepath.Join(root, "backend")
		command.Env = environment(map[string]string{"GOWORK": "off"})
		if _, code, err := runChild(ctx, command); err != nil || code != 0 {
			return errors.New("disposable backend build failed or exceeded the 60-second deadline")
		}
	}
	template, err := os.ReadFile(filepath.Join(root, "web/src/app.html"))
	if err != nil {
		return errors.New("production web identity template is unavailable")
	}
	selected, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return errors.New("private backend address could not be selected")
	}
	backendAddress := selected.Addr().String()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		_ = selected.Close()
		return errors.New("private interoperability gateway could not listen")
	}
	gateway := newGateway(listener, backendAddress, template)
	gatewayDone := gateway.serve()
	defer func() { gateway.close(); <-gatewayDone }()
	adminPassword := "Admin-" + randomSecret()
	if len(adminPassword) <= len("Admin-") {
		_ = selected.Close()
		return errors.New("disposable administrator credential could not be generated")
	}
	command := exec.Command(*backend)
	command.Dir = directory
	command.Stdout = io.Discard
	command.Stderr = io.Discard
	command.Env = []string{"PATH=" + os.Getenv("PATH"), "LANG=C.UTF-8", "ADMIN_USERNAME=integration-admin", "ADMIN_PASSWORD=" + adminPassword, "DB_PATH=" + filepath.Join(directory, "server.db"), "STORAGE_PATH=" + filepath.Join(directory, "files"), "LISTEN_ADDR=" + backendAddress, "PUBLIC_URL=" + gateway.origin, "AUTH_ALLOW_INSECURE_HTTP=true", "RATE_LIMIT_GLOBAL=200", "RATE_LIMIT_BURST=400", "RATE_LIMIT_CREATION=20", "RATE_LIMIT_CREATION_BURST=50"}
	_ = selected.Close()
	process, err := startChild(command)
	if err != nil {
		return err
	}
	defer process.stop()
	api := newAPI(ctx, gateway.origin)
	defer api.client.CloseIdleConnections()
	if err = waitReady(ctx, process, api); err != nil {
		return err
	}
	fixture := &fixture{ctx: ctx, root: root, directory: directory, binary: *binary, origin: gateway.origin, api: api}
	fixture.exercise(gateway, adminPassword)
	if fixture.failure != nil {
		return fixture.failure
	}
	if ctx.Err() != nil {
		return errors.New("integration exceeded its 60-second deadline")
	}
	fmt.Fprintf(stdout, "CLI integration passed in %.1fs: bidirectional Send/Receive web crypto, multiframe/empty files, encrypted Receive-key recovery, receipt retry, expiry/revocation/allowances.\n", time.Since(started).Seconds())
	return nil
}
