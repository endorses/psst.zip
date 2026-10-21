package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/endorses/psst.zip/client/protocol"
)

const testTransfer = "12345678-1234-4234-8234-123456789abc"
const testBlob = "22345678-1234-4234-8234-123456789abc"
const testToken = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type memoryStore struct {
	mu      sync.Mutex
	records map[string][]byte
}

func newMemoryStore() *memoryStore { return &memoryStore{records: map[string][]byte{}} }
func (s *memoryStore) Get(k string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.records[k]
	if !ok {
		return ErrNotFound
	}
	return json.Unmarshal(b, v)
}
func (s *memoryStore) Put(k string, v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, e := json.Marshal(v)
	if e == nil {
		s.records[k] = b
	}
	return e
}
func (s *memoryStore) Delete(k string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, k)
	return nil
}
func (s *memoryStore) Keys(prefix string) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := []string{}
	for k := range s.records {
		if strings.HasPrefix(k, prefix) {
			r = append(r, k)
		}
	}
	return r, nil
}
func jsonReply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
func TestOriginAndRedirectIsolation(t *testing.T) {
	var leaked bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("cookie forwarded")
		}
		http.Redirect(w, r, destination.URL, 307)
	}))
	defer server.Close()
	c, _ := New(server.URL, nil, func(context.Context) (string, error) { return testToken, nil })
	_, e := c.GetTransfer(context.Background(), testTransfer)
	if !errors.Is(e, ErrRedirect) || leaked {
		t.Fatalf("redirect followed or wrong error: %v", e)
	}
	if _, e := c.uploadPath(destination.URL + "/api/v1/transfers/" + testTransfer + "/files/" + testBlob); e == nil {
		t.Fatal("accepted foreign upload origin")
	}
	c.credentials = func(context.Context) (string, error) { t.Fatal("anonymous credential lookup"); return "", nil }
	public := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.URL.Fragment != "" {
			t.Error("credential or fragment leak")
		}
		jsonReply(w, 404, map[string]string{"code": "link_expired", "error": "secret\x1b[31m"})
	}))
	defer public.Close()
	a, _ := New(public.URL, c.http, nil)
	_, e = a.GetTransfer(context.Background(), testTransfer)
	var api *APIError
	if !errors.As(e, &api) || api.Code != "link_expired" || strings.Contains(e.Error(), "secret") {
		t.Fatal("error redaction", e)
	}
}
func TestBoundedResponsesCancellationAndRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("mode") {
		case "large":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, strings.Repeat(" ", int(controlLimit)+1))
		case "retry":
			w.Header().Set("Retry-After", "9999999999")
			w.Header().Set("X-Psst-Error-Code", "traffic_paused")
			jsonReply(w, 429, map[string]string{"error": "private"})
		default:
			<-r.Context().Done()
		}
	}))
	defer server.Close()
	c, _ := New(server.URL, nil, nil)
	var v any
	if e := c.control(context.Background(), "GET", "/?mode=large", nil, &v, 200); !errors.Is(e, ErrProtocol) {
		t.Fatal("oversize accepted", e)
	}
	e := c.control(context.Background(), "GET", "/?mode=retry", nil, &v, 200)
	var api *APIError
	if !errors.As(e, &api) || api.RetryAfter != 24*time.Hour || api.Code != "traffic_paused" {
		t.Fatal("retry bounds", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e = c.control(ctx, "GET", "/", nil, &v, 200); !errors.Is(e, context.Canceled) {
		t.Fatal("cancellation lost", e)
	}
}
func TestSendFetchAtomicFilesAndReceiptRetry(t *testing.T) {
	var mu sync.Mutex
	expiresAt := time.Now().Add(time.Hour)
	blob := []byte{}
	manifest := []byte{}
	status := "pending"
	receipts, downloads, completions := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		base := "/api/v1/transfers/" + testTransfer
		switch {
		case r.URL.Path == "/api/v1/config":
			jsonReply(w, 200, protocol.Config{MaxFileSize: protocol.MaxFileSize})
		case r.Method == "POST" && r.URL.Path == "/api/v1/transfers":
			if r.Header.Get("Authorization") != "Bearer "+testToken {
				t.Error("send missing account auth")
			}
			jsonReply(w, 201, protocol.CreatedTransfer{ID: testTransfer, DeleteToken: testToken, ExpiresAt: expiresAt})
		case r.Method == "POST" && r.URL.Path == base+"/files":
			w.Header().Set("Location", base+"/files/"+testBlob)
			w.WriteHeader(201)
		case r.Method == "HEAD":
			w.Header().Set("Upload-Offset", strconv.Itoa(len(blob)))
			w.WriteHeader(200)
		case r.Method == "PATCH":
			b, _ := io.ReadAll(r.Body)
			offset, _ := strconv.Atoi(r.Header.Get("Upload-Offset"))
			if offset != len(blob) {
				w.WriteHeader(409)
				return
			}
			blob = append(blob, b...)
			w.Header().Set("Upload-Offset", strconv.Itoa(len(blob)))
			w.WriteHeader(204)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/manifest"):
			manifest, _ = io.ReadAll(r.Body)
			w.WriteHeader(204)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/complete"):
			status = "complete"
			completions++
			if completions == 1 {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(204)
			}
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/downloaded"):
			receipts++
			if receipts == 1 {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(204)
			}
		case r.Method == "GET" && r.URL.Path == base:
			jsonReply(w, 200, protocol.Transfer{ID: testTransfer, ExpiresAt: expiresAt, Status: status, HasManifest: len(manifest) > 0, FileCount: 1, Files: []protocol.FileInfo{{ID: testBlob, Size: int64(len(blob)), UploadOffset: int64(len(blob)), UploadComplete: true}}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/manifest"):
			if r.Header.Get("Authorization") != "" {
				t.Error("public fetch forwarded account")
			}
			w.Write(manifest)
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/files/"+testBlob):
			downloads++
			w.Header().Set("Content-Length", strconv.Itoa(len(blob)))
			w.Write(blob)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, _ := New(server.URL, nil, func(context.Context) (string, error) { return testToken, nil })
	s := newMemoryStore()
	plain := []byte("small authenticated transfer\n")
	inputs := []InputFile{{Name: "report.txt", Size: int64(len(plain)), Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(plain)), nil }}}
	share, e := c.Send(context.Background(), inputs, SendOptions{SpoolDir: t.TempDir()}, s)
	var interrupted *OperationError
	if !errors.As(e, &interrupted) {
		t.Fatalf("lost completion response must retain operation: %v", e)
	}
	share, e = c.ResumeSend(context.Background(), interrupted.OperationID, s, nil)
	if e != nil || completions != 1 {
		t.Fatalf("lost completion recovery repeated mutation: %v completions%d", e, completions)
	}
	link, e := protocol.ParseLink(share.Link)
	if e != nil {
		t.Fatal(e)
	}
	publicManifest, e := c.ReadManifest(context.Background(), link)
	if e != nil || len(publicManifest.Files) != 1 || publicManifest.Files[0].BlobID != testBlob {
		t.Fatalf("public SDK manifest workflow: %v", e)
	}
	out := t.TempDir()
	if e = os.WriteFile(filepath.Join(out, "report.txt"), []byte("preserve"), 0600); e != nil {
		t.Fatal(e)
	}
	result, e := c.Fetch(context.Background(), link, out, s)
	var receipt *ReceiptError
	if !errors.As(e, &receipt) || !result.ReceiptPending {
		t.Fatal("failed receipt not retained", e)
	}
	if len(result.Files) != 1 || filepath.Base(result.Files[0]) != "report (1).txt" {
		t.Fatalf("collision result %+v", result)
	}
	actual, _ := os.ReadFile(result.Files[0])
	if !bytes.Equal(actual, plain) {
		t.Fatal("plaintext mismatch")
	}
	result, e = c.Fetch(context.Background(), link, out, s)
	if e != nil || result.ReceiptPending || downloads != 1 || receipts != 2 {
		t.Fatalf("receipt redownloaded: %v downloads%d receipts%d", e, downloads, receipts)
	}
	original, _ := os.ReadFile(filepath.Join(out, "report.txt"))
	if string(original) != "preserve" {
		t.Fatal("overwrote collision")
	}
	files, _ := filepath.Glob(filepath.Join(out, "*.partial"))
	if len(files) != 0 {
		t.Fatal("temporary files leaked")
	}
	actual[0] ^= 1
	os.WriteFile(result.Files[0], actual, 0600)
	if _, e = c.Fetch(context.Background(), link, out, s); e == nil {
		t.Fatal("changed completed output accepted")
	}
}
func TestHostileOutputAndFileReplacement(t *testing.T) {
	for _, name := range []string{"../escape", "/escape", "C:\\escape", "a/b", "..", "a\x1b[2J", "a.", "a ", "NUL.txt", "COM1", "lpt9.csv"} {
		if validFilename(name) {
			t.Errorf("accepted hostile filename %q", name)
		}
	}
	root := t.TempDir()
	path := filepath.Join(root, "file.txt")
	os.WriteFile(path, []byte("aaaa"), 0600)
	files, e := FileInputs([]string{path})
	if e != nil {
		t.Fatal(e)
	}
	other := filepath.Join(root, "other.txt")
	os.WriteFile(other, []byte("bbbb"), 0600)
	os.Remove(path)
	os.Symlink(other, path)
	if r, e := files[0].Open(); e == nil {
		r.Close()
		t.Fatal("input replacement accepted")
	}
}

type failingStore struct {
	*memoryStore
	puts   int
	failAt int
}

func (s *failingStore) Put(k string, v any) error {
	s.puts++
	if s.puts == s.failAt {
		return errors.New("injected protected persistence failure")
	}
	return s.memoryStore.Put(k, v)
}
func TestAllocationCleanupAfterSpoolJournalFailure(t *testing.T) {
	revoked := false
	expiresAt := time.Now().Add(time.Hour)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/config" {
			jsonReply(w, 200, protocol.Config{MaxFileSize: protocol.MaxFileSize})
			return
		}
		if r.Method == "POST" && r.URL.Path == "/api/v1/transfers" {
			jsonReply(w, 201, protocol.CreatedTransfer{ID: testTransfer, DeleteToken: testToken, ExpiresAt: expiresAt})
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/v1/transfers/"+testTransfer {
			jsonReply(w, 200, protocol.Transfer{ID: testTransfer, Status: "pending", ExpiresAt: expiresAt})
			return
		}
		if r.Method == "DELETE" && r.URL.Path == "/api/v1/transfers/"+testTransfer {
			revoked = true
			if r.Header.Get("Authorization") != "Bearer "+testToken {
				t.Error("cleanup lost deletion capability")
			}
			w.WriteHeader(204)
			return
		}
		t.Error("unexpected remote allocation step")
		w.WriteHeader(404)
	}))
	defer server.Close()
	c, _ := New(server.URL, nil, nil)
	s := &failingStore{memoryStore: newMemoryStore(), failAt: 4}
	dir := t.TempDir()
	_, e := c.Send(context.Background(), []InputFile{{Name: "a.txt", Size: 1, Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("a")), nil }}}, SendOptions{SpoolDir: dir}, s)
	if e == nil || !revoked {
		t.Fatalf("unrecoverable allocation not cleaned up %v revoked%v", e, revoked)
	}
	var op *OperationError
	if errors.As(e, &op) {
		t.Fatal("advertised an unusable resume journal")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("unpersisted ciphertext spool leaked")
	}
}

func TestAuthenticatedFailureNeverPublishesPlaintext(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/manifest") {
			w.Write(make([]byte, 64))
			return
		}
		jsonReply(w, 200, protocol.Transfer{ID: testTransfer, Status: "complete", HasManifest: true, Files: []protocol.FileInfo{{ID: testBlob, Size: 61, UploadComplete: true}}})
	}))
	defer server.Close()
	c, _ := New(server.URL, nil, nil)
	dir := t.TempDir()
	_, e := c.Fetch(context.Background(), protocol.Link{Origin: server.URL, Kind: "send", ID: testTransfer, Key: key}, dir, newMemoryStore())
	if e == nil {
		t.Fatal("unauthenticated manifest accepted")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatal("published output for unauthenticated manifest")
	}
}

func TestReceiveAnonymousGuestAndExactOwnerMembership(t *testing.T) {
	const slot = "32345678-1234-4234-8234-123456789abc"
	const childToken = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	var mu sync.Mutex
	expiresAt := time.Now().Add(time.Hour)
	var public string
	var blob, manifest []byte
	complete := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		base := "/api/v1/transfers/" + testTransfer
		switch {
		case r.URL.Path == "/api/v1/config":
			jsonReply(w, 200, protocol.Config{MaxFileSize: protocol.MaxFileSize})
		case r.Method == "POST" && r.URL.Path == "/api/v1/slots":
			var in protocol.CreateReceiveRequest
			json.NewDecoder(r.Body).Decode(&in)
			public = in.RecipientPublicKey
			jsonReply(w, 201, protocol.CreatedTransfer{ID: slot, DeleteToken: testToken, ExpiresAt: expiresAt})
		case strings.HasSuffix(r.URL.Path, "/availability"):
			if r.Header.Get("Authorization") != "" {
				t.Error("availability leaked account credential")
			}
			jsonReply(w, 200, protocol.Availability{ID: slot, Available: true, ExpiresAt: expiresAt, ReceiveProtocol: 2, RecipientPublicKey: public})
		case strings.HasSuffix(r.URL.Path, "/membership"):
			if r.Header.Get("Authorization") != "Bearer "+testToken {
				t.Error("membership missing owner")
			}
			jsonReply(w, 200, protocol.Membership{SlotID: slot, TransferID: testTransfer, ReceiveProtocol: 2, RecipientPublicKey: public})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/inbox"):
			transfers := []protocol.SlotTransfer{}
			if complete {
				transfers = append(transfers, protocol.SlotTransfer{TransferID: testTransfer, Status: "complete", FileCount: 1})
			}
			jsonReply(w, 200, protocol.InboxPage{Slot: protocol.Slot{ID: slot, ExpiresAt: expiresAt, ReceiveProtocol: 2, RecipientPublicKey: public, MaxFiles: 2, Transfers: transfers}, Paginated: true})
		case r.Method == "POST" && r.URL.Path == "/api/v1/slots/"+slot+"/transfers":
			if r.Header.Get("Authorization") != "" {
				t.Error("child allocation leaked account credential")
			}
			jsonReply(w, 201, protocol.CreatedTransfer{ID: testTransfer, DeleteToken: childToken})
		case r.Method == "POST" && r.URL.Path == base+"/files":
			if r.Header.Get("Authorization") != "Bearer "+childToken {
				t.Error("guest file creation did not use child capability")
			}
			w.Header().Set("Location", base+"/files/"+testBlob)
			w.WriteHeader(201)
		case r.Method == "HEAD":
			w.Header().Set("Upload-Offset", strconv.Itoa(len(blob)))
			w.WriteHeader(200)
		case r.Method == "PATCH":
			if r.Header.Get("Authorization") != "Bearer "+childToken {
				t.Error("guest upload leaked account credential")
			}
			b, _ := io.ReadAll(r.Body)
			blob = append(blob, b...)
			w.Header().Set("Upload-Offset", strconv.Itoa(len(blob)))
			w.WriteHeader(204)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/manifest"):
			manifest, _ = io.ReadAll(r.Body)
			w.WriteHeader(204)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/complete"):
			complete = true
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/upload-status"):
			if r.Header.Get("Authorization") != "Bearer "+childToken {
				t.Error("upload status leaked account")
			}
			currentStatus := "pending"
			if complete {
				currentStatus = "complete"
			}
			jsonReply(w, 200, map[string]string{"id": testTransfer, "status": currentStatus})
		case r.Method == "GET" && r.URL.Path == base:
			jsonReply(w, 200, protocol.Transfer{ID: testTransfer, Status: "complete", HasManifest: true, Files: []protocol.FileInfo{{ID: testBlob, Size: int64(len(blob)), UploadComplete: true}}})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/manifest"):
			w.Write(manifest)
		case r.Method == "GET" && strings.Contains(r.URL.Path, "/files/"):
			w.Header().Set("Content-Length", strconv.Itoa(len(blob)))
			w.Write(blob)
		case strings.HasSuffix(r.URL.Path, "/downloaded"):
			w.WriteHeader(204)
		default:
			t.Errorf("unexpected receive %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	c, _ := New(server.URL, nil, func(context.Context) (string, error) { return testToken, nil })
	s := newMemoryStore()
	share, e := c.CreateReceive(context.Background(), ReceiveOptions{MaxFiles: 2}, s)
	if e != nil {
		t.Fatal(e)
	}
	invitation, e := protocol.ParseLink(share.Link)
	if e != nil {
		t.Fatal(e)
	}
	_, e = c.UploadWithOptions(context.Background(), invitation, []InputFile{{Name: "hello.txt", Size: 5, Open: func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader("hello")), nil }}}, SendOptions{SpoolDir: t.TempDir()}, s)
	if e != nil {
		t.Fatal(e)
	}
	results, e := c.FetchInbox(context.Background(), slot, t.TempDir(), s)
	if e != nil {
		t.Fatal(e)
	}
	if len(results) != 1 || len(results[0].Files) != 1 {
		t.Fatal("missing received file")
	}
	b, _ := os.ReadFile(results[0].Files[0])
	if string(b) != "hello" {
		t.Fatal("owner decryption mismatch")
	}
	foreign := newMemoryStore()
	if _, e = c.FetchInbox(context.Background(), slot, t.TempDir(), foreign); e == nil || !strings.Contains(e.Error(), "private key") {
		t.Fatal("missing private key silently accepted")
	}
}

func TestDownloadPublicationCrashRecoveryWithoutPayloadRequests(t *testing.T) {
	for _, alreadyPublished := range []bool{false, true} {
		t.Run(strconv.FormatBool(alreadyPublished), func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "POST" || r.URL.Path != "/api/v1/transfers/"+testTransfer+"/downloaded" {
					t.Errorf("recovery fetched a payload: %s %s", r.Method, r.URL.Path)
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			key := bytes.Repeat([]byte{6}, 32)
			dir := t.TempDir()
			plaintext := []byte("abc")
			sum := sha256.Sum256(plaintext)
			identity := sha256.Sum256(key)
			temp := ".psst-42345678-1234-4234-8234-123456789abc.partial"
			target := "report.txt"
			if alreadyPublished {
				os.WriteFile(filepath.Join(dir, target), plaintext, 0600)
			} else {
				os.WriteFile(filepath.Join(dir, temp), plaintext, 0600)
				os.WriteFile(filepath.Join(dir, target), []byte("keep"), 0600)
			}
			j := downloadJournal{Origin: server.URL, ID: testTransfer, Directory: dir, KeyDigest: hex.EncodeToString(identity[:]), Files: map[string]completedFile{}, Pending: map[string]pendingFile{testBlob: {Temp: temp, BaseName: target, ReadyToPublish: true, completedFile: completedFile{Name: target, Size: 3, SHA256: hex.EncodeToString(sum[:])}}}, Manifest: protocol.Manifest{Files: []protocol.ManifestFile{{Name: target, Size: 3, BlobID: testBlob}}}}
			s := newMemoryStore()
			s.Put(checkpointID(server.URL, testTransfer, dir), j)
			c, _ := New(server.URL, nil, nil)
			result, e := c.Fetch(context.Background(), protocol.Link{Origin: server.URL, ID: testTransfer, Kind: "send", Key: key}, dir, s)
			if e != nil || calls != 1 || len(result.Files) != 1 {
				t.Fatalf("crash recovery %+v %v requests%d", result, e, calls)
			}
			b, _ := os.ReadFile(result.Files[0])
			if !bytes.Equal(b, plaintext) {
				t.Fatal("recovered plaintext mismatch")
			}
			if !alreadyPublished {
				old, _ := os.ReadFile(filepath.Join(dir, target))
				if string(old) != "keep" {
					t.Fatal("recovery overwrote collision")
				}
			}
			if _, e = os.Stat(filepath.Join(dir, temp)); !errors.Is(e, os.ErrNotExist) {
				t.Fatal("recovered temporary output leaked")
			}
		})
	}
}

func TestSizeAndExpiryPreflightAvoidAllocationAndEncryption(t *testing.T) {
	requests := 0
	expired := false
	key := bytes.Repeat([]byte{1}, 32)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path == "/api/v1/config" {
			limit := int64(1)
			if expired {
				limit = 10
			}
			jsonReply(w, 200, protocol.Config{MaxFileSize: limit})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/availability") {
			jsonReply(w, 200, protocol.Availability{ID: testTransfer, Available: true, ReceiveProtocol: 2, RecipientPublicKey: base64.RawURLEncoding.EncodeToString(key), ExpiresAt: time.Now().Add(-time.Hour)})
			return
		}
		t.Errorf("preflight allocated resource: %s", r.URL.Path)
		w.WriteHeader(500)
	}))
	defer server.Close()
	c, _ := New(server.URL, nil, nil)
	files := []InputFile{{Name: "large.txt", Size: 2, Open: func() (io.ReadCloser, error) { t.Fatal("preflight encrypted rejected file"); return nil, nil }}}
	_, e := c.Send(context.Background(), files, SendOptions{}, newMemoryStore())
	var policy *PolicyError
	if !errors.As(e, &policy) || policy.Code != "file_size_limit" || requests != 1 {
		t.Fatalf("size preflight %v requests%d", e, requests)
	}
	expired = true
	_, e = c.Upload(context.Background(), protocol.Link{Origin: server.URL, Kind: "receive", Version: 2, ID: testTransfer, Key: key}, files, newMemoryStore())
	if !errors.As(e, &policy) || policy.Code != "link_expired" || requests != 3 {
		t.Fatalf("expiry preflight %v requests%d", e, requests)
	}
	files = append(files, InputFile{Name: "b.txt", Size: protocol.MaxFileSize, Open: files[0].Open})
	files[0].Size = protocol.MaxFileSize
	if e = validateInputs(files); e == nil {
		t.Fatal("combined size overflow accepted")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type closingReader struct {
	io.Reader
	closed *bool
}

func (r closingReader) Close() error { *r.closed = true; return nil }

func TestDownloadFileToExistingVectorAndUncommittedFailure(t *testing.T) {
	var vector struct {
		Key          string `json:"key"`
		EncryptionID string `json:"encryption_id"`
		Plaintext    string `json:"plaintext"`
		Frame        string `json:"frame"`
		TotalSize    int64  `json:"total_size"`
	}
	raw, err := os.ReadFile("../docs/protocol/chunked-file-vector.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &vector); err != nil {
		t.Fatal(err)
	}
	key, _ := hex.DecodeString(vector.Key)
	frame, _ := hex.DecodeString(vector.Frame)
	plaintext, _ := hex.DecodeString(vector.Plaintext)
	file := protocol.ManifestFile{Name: "fixture.bin", MIMEType: "application/octet-stream", BlobID: testBlob, Size: vector.TotalSize, Encoding: "chunked-v1", ChunkSize: protocol.ChunkSize, EncryptionID: vector.EncryptionID}
	corrupt := bytes.Clone(frame)
	corrupt[len(corrupt)-1] ^= 1
	for _, tc := range []struct {
		name    string
		payload []byte
		valid   bool
	}{
		{"existing independent fixture", frame, true},
		{"authentication failure", corrupt, false},
		{"truncation", frame[:len(frame)-1], false},
		{"unknown-length trailing ciphertext", append(bytes.Clone(frame), 0), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			closed, requests := false, 0
			httpClient := &http.Client{Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				requests++
				if r.Method != "GET" || r.URL.Path != "/api/v1/transfers/"+testTransfer+"/files/"+testBlob || r.URL.Fragment != "" || r.Header.Get("Authorization") != "" {
					t.Fatal("writer operation issued a receipt or leaked a credential")
				}
				return &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: -1, Body: closingReader{Reader: bytes.NewReader(tc.payload), closed: &closed}}, nil
			})}
			c, _ := New("https://files.example", httpClient, nil)
			// A buffer represents an unpublished destination. Callers may commit
			// it only on success; failure never produces a completion receipt.
			var unpublished bytes.Buffer
			err := c.DownloadFileTo(context.Background(), testTransfer, file, key, &unpublished)
			if tc.valid && (err != nil || !bytes.Equal(unpublished.Bytes(), plaintext)) {
				t.Fatalf("writer interoperability failed: %v", err)
			}
			if !tc.valid && err == nil {
				t.Fatal("invalid stream was eligible for publication")
			}
			if !closed || requests != 1 {
				t.Fatalf("body cleanup or implicit receipt: closed%v requests%d", closed, requests)
			}
		})
	}
}

func TestRequestedPolicyCannotBecomeAnUnlimitedOrLongerLink(t *testing.T) {
	for _, tc := range []struct {
		name       string
		requested  SendOptions
		serverSkew time.Duration
	}{
		{"ignored download limit", SendOptions{MaxDownloads: 1}, 24 * time.Hour},
		{"ignored requested expiry", SendOptions{ExpiresInSeconds: 60}, -24 * time.Hour},
	} {
		t.Run(tc.name, func(t *testing.T) {
			serverNow := time.Now().Add(tc.serverSkew).Truncate(time.Second)
			expiresAt := serverNow.Add(time.Hour)
			revoked := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Date", serverNow.Format(http.TimeFormat))
				switch {
				case r.URL.Path == "/api/v1/config":
					jsonReply(w, 200, protocol.Config{MaxFileSize: 1024})
				case r.Method == "POST" && r.URL.Path == "/api/v1/transfers":
					jsonReply(w, 201, protocol.CreatedTransfer{ID: testTransfer, DeleteToken: testToken, ExpiresAt: expiresAt})
				case r.Method == "GET" && r.URL.Path == "/api/v1/transfers/"+testTransfer:
					jsonReply(w, 200, protocol.Transfer{ID: testTransfer, Status: "pending", ExpiresAt: expiresAt, MaxDownloads: 0})
				case r.Method == "DELETE" && r.URL.Path == "/api/v1/transfers/"+testTransfer:
					revoked = true
					if r.Header.Get("Authorization") != "Bearer "+testToken {
						t.Fatal("policy mismatch cleanup lost deletion capability")
					}
					w.WriteHeader(204)
				default:
					t.Errorf("policy mismatch attempted payload allocation: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			c, _ := New(server.URL, nil, nil)
			share, err := c.Send(context.Background(), []InputFile{{Name: "private.txt", Size: 1, Open: func() (io.ReadCloser, error) {
				t.Fatal("policy mismatch opened plaintext input")
				return nil, nil
			}}}, tc.requested, newMemoryStore())
			var policy *PolicyError
			if !errors.As(err, &policy) || policy.Code != "requested_policy_unsupported" || !revoked || share.Link != "" {
				t.Fatalf("unsafe policy accepted: %v revoked%v share%+v", err, revoked, share)
			}
		})
	}
	// A correctly bounded policy is accepted against Date even when the
	// server clock differs from the client clock; local skew is not an expiry.
	serverNow := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	if err := acceptedExpiry(protocol.CreatedTransfer{ServerTime: serverNow, ExpiresAt: serverNow.Add(60 * time.Second)}, 60); err != nil {
		t.Fatal("server-clock policy incorrectly depended on the client clock", err)
	}
}
