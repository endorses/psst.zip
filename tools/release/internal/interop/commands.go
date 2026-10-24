package interop

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/endorses/psst.zip/tools/release/internal/artifact"
	"github.com/endorses/psst.zip/tools/release/internal/contract"
)

type fixture struct {
	ctx                             context.Context
	root, directory, binary, origin string
	api                             *api
	failure                         error
}

func (f *fixture) check(condition bool, message string) {
	if !condition && f.failure == nil {
		f.failure = errors.New(message)
	}
}
func (f *fixture) decode(data []byte, target any) {
	if f.failure != nil {
		return
	}
	var value any
	if err := contract.Decode(data, maxChildOutput, &value); err != nil {
		f.failure = errors.New("private integration JSON is invalid")
		return
	}
	if err := json.Unmarshal(data, target); err != nil {
		f.failure = errors.New("private integration JSON fields are invalid")
	}
}
func (f *fixture) text(data map[string]any, key string) string {
	if f.failure != nil {
		return ""
	}
	value, ok := data[key].(string)
	f.check(ok && value != "", "private integration response is missing a required string")
	return value
}
func (f *fixture) request(method, path string, body any, token string, statuses ...int) map[string]any {
	if f.failure != nil {
		return nil
	}
	result, err := f.api.request(method, path, body, token, statuses...)
	if err != nil {
		f.failure = err
	}
	return result
}
func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

type envelope struct {
	Schema int             `json:"schema_version"`
	OK     bool            `json:"ok"`
	Data   json.RawMessage `json:"data"`
	Error  struct {
		Code string `json:"code"`
	} `json:"error"`
}
type share struct {
	ID         string    `json:"id"`
	Link       string    `json:"link,omitempty"`
	ExpiresAt  time.Time `json:"expires_at,omitzero"`
	PrivateKey string    `json:"private_key,omitempty"`
	PublicKey  string    `json:"public_key,omitempty"`
}

var uuidPattern = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func (f *fixture) resource(raw []byte) share {
	var result share
	f.decode(raw, &result)
	f.check(uuidPattern.MatchString(result.ID), "private integration resource identity is invalid")
	if result.Link != "" {
		parsed, err := url.Parse(result.Link)
		origin, _ := url.Parse(f.origin)
		f.check(err == nil && parsed.Scheme == origin.Scheme && parsed.Host == origin.Host && parsed.User == nil && parsed.RawQuery == "", "integration share left the disposable origin")
	}
	return result
}

type cli struct {
	fixture    *fixture
	directory  string
	passphrase []byte
}

func (f *fixture) newCLI(directory string) *cli {
	secret := randomSecret()
	f.check(secret != "", "private integration passphrase could not be generated")
	return &cli{f, directory, []byte(secret)}
}
func (cli *cli) call(args []string, input string, expected int, backup []byte) envelope {
	result := envelope{}
	f := cli.fixture
	if f.failure != nil {
		return result
	}
	reader, err := secretPipe(cli.passphrase)
	if err != nil {
		f.failure = err
		return result
	}
	defer reader.Close()
	files := []*os.File{reader}
	if backup != nil {
		file, err := secretPipe(backup)
		if err != nil {
			f.failure = err
			return result
		}
		defer file.Close()
		files = append(files, file)
		args = append(append([]string{}, args...), "--backup-passphrase-fd", strconv.Itoa(4))
	}
	arguments := []string{"--json", "--non-interactive", "--timeout", "10s", "--state-dir", filepath.Join(cli.directory, "vault"), "--vault-passphrase-fd", "3"}
	arguments = append(arguments, args...)
	command := exec.Command(f.binary, arguments...)
	command.Stdin = strings.NewReader(input)
	command.ExtraFiles = files
	command.Env = environment(map[string]string{"XDG_CACHE_HOME": filepath.Join(cli.directory, "cache"), "HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "NO_PROXY": "127.0.0.1,localhost"})
	data, code, err := runChild(f.ctx, command)
	if err != nil {
		f.failure = err
		return result
	}
	f.check(code == expected, fmt.Sprintf("CLI %s returned status %d, expected %d", args[0], code, expected))
	f.decode(data, &result)
	f.check(result.Schema == 1 && result.OK == (expected == 0), "CLI JSON schema or success state is invalid")
	return result
}
func (f *fixture) peer(action string, input map[string]any) json.RawMessage {
	if f.failure != nil {
		return nil
	}
	input["origin"] = f.origin
	data, err := json.Marshal(input)
	if err != nil {
		f.failure = errors.New("private TypeScript peer input is invalid")
		return nil
	}
	command := exec.Command("node", "--experimental-strip-types", filepath.Join(f.root, "client/scripts/integration-crypto.mjs"), action)
	command.Dir = f.root
	command.Stdin = bytes.NewReader(data)
	command.Env = environment(map[string]string{"HTTP_PROXY": "", "HTTPS_PROXY": "", "ALL_PROXY": "", "NO_PROXY": "127.0.0.1,localhost", "NODE_USE_ENV_PROXY": "0"})
	data, code, err := runChild(f.ctx, command)
	if err != nil {
		f.failure = err
		return nil
	}
	f.check(code == 0, "production TypeScript crypto interoperability failed during "+action)
	return data
}

type fileInput struct {
	Name string `json:"name"`
	Path string `json:"path"`
}
type peerFile struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	FileID string `json:"file_id"`
}

func (f *fixture) compare(directory string, files []fileInput) {
	if f.failure != nil {
		return
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		f.failure = errors.New("authenticated output directory is missing")
		return
	}
	actual := map[string]string{}
	for _, entry := range entries {
		f.check(entry.Type().IsRegular(), "CLI published unexpected files or partial outputs")
		actual[entry.Name()] = filepath.Join(directory, entry.Name())
	}
	f.check(len(actual) == len(files), "CLI published unexpected files or partial outputs")
	for _, file := range files {
		path, ok := actual[file.Name]
		f.check(ok, "authenticated download is missing")
		if !ok {
			continue
		}
		have, err1 := artifact.HashFile(path)
		want, err2 := artifact.HashFile(file.Path)
		f.check(err1 == nil && err2 == nil && have == want, "CLI authenticated download did not match source")
	}
}
func (f *fixture) write(path string, data []byte) {
	if f.failure != nil {
		return
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		f.failure = errors.New("disposable integration file could not be created")
	}
}
func (f *fixture) bootstrap(adminPassword string) (string, string) {
	f.request("POST", "/auth/login", map[string]any{"username": "integration-admin", "password": adminPassword, "session_type": "web"}, "", 200)
	enrollment := f.request("POST", "/admin/security/enrollment", map[string]any{}, "", 201)
	code, err := totp(f.text(enrollment, "secret"), time.Now())
	if err != nil && f.failure == nil {
		f.failure = err
	}
	confirmed := f.request("POST", "/admin/security/enrollment/confirm", map[string]any{"code": code}, "", 200)
	codes, ok := confirmed["recovery_codes"].([]any)
	f.check(ok && len(codes) > 0, "disposable administrator recovery codes are missing")
	recovery := ""
	if len(codes) > 0 {
		recovery, ok = codes[0].(string)
		f.check(ok && recovery != "", "disposable administrator recovery code is invalid")
	}
	f.request("POST", "/auth/login", map[string]any{"username": "integration-admin", "password": adminPassword, "session_type": "web", "recovery_code": recovery}, "", 200)
	temporary, password := "Temporary-"+randomSecret(), "Member-"+randomSecret()
	f.request("POST", "/admin/users", map[string]any{"username": "integration-member", "password": temporary, "role": "user"}, "", 201)
	first := f.request("POST", "/auth/login", map[string]any{"username": "integration-member", "password": temporary, "session_type": "device"}, "", 200)
	f.request("POST", "/auth/password", map[string]any{"current_password": temporary, "password": password}, f.text(first, "token"), 204)
	session := f.request("POST", "/auth/login", map[string]any{"username": "integration-member", "password": password, "session_type": "device"}, "", 200)
	return password, f.text(session, "token")
}
