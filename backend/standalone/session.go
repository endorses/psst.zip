// Package standalone embeds the hosted transfer implementation for a temporary,
// single-resource sharing session. It never reads production configuration or
// creates network credentials, administrator routes or background account workers.
package standalone

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
	"github.com/google/uuid"
)

// Config supplies all session policy explicitly. Directory is a private staging
// parent; New creates and owns only a new child directory. Origin must identify
// the already-bound listener, including its actual port. All limits are finite.
type Config struct {
	Directory    string
	Origin       string
	ExpiresAt    time.Time
	MaxBytes     int64
	MaxFiles     int
	MaxDownloads int
	MaxStreams   int
}

// Stats reports completed work without exposing ciphertext or private keys.
type Stats struct {
	Downloads      int
	CompletedFiles int
}

// Session owns database/storage handles and one public Send or Receive resource.
// Private inbox/decryption keys belong to its caller and never enter this package.
type Session struct {
	mu        sync.Mutex
	requests  sync.WaitGroup
	cfg       Config
	dir       string
	db        *sql.DB
	queries   *database.Queries
	server    *api.Server
	public    http.Handler
	owner     http.Handler
	kind, id  string
	closed    bool
	closeOnce sync.Once
	closeErr  error
}

// New prepares an isolated transfer service without starting any listeners.
func New(cfg Config) (_ *Session, err error) {
	origin, err := url.Parse(cfg.Origin)
	if err != nil || origin.Host == "" || origin.User != nil || origin.RawQuery != "" || origin.Fragment != "" || (origin.Path != "" && origin.Path != "/") || (origin.Scheme != "http" && origin.Scheme != "https") {
		return nil, errors.New("standalone origin must be an HTTP or HTTPS origin")
	}
	cfg.Origin = origin.Scheme + "://" + origin.Host
	lifetime := time.Until(cfg.ExpiresAt)
	if cfg.Directory == "" || lifetime <= 0 || lifetime > 365*24*time.Hour || cfg.MaxBytes < 1<<20 || cfg.MaxBytes > 1<<40 || cfg.MaxFiles < 1 || cfg.MaxFiles > 1000000 || cfg.MaxDownloads < 1 || cfg.MaxDownloads > 1<<31-1 || cfg.MaxStreams < 1 || cfg.MaxStreams > 4096 {
		return nil, errors.New("standalone requires a private directory, future expiry within one year, byte budget 1 MiB–1 TiB and finite positive file/download/stream limits")
	}
	dir, err := os.MkdirTemp(cfg.Directory, "transfer-")
	if err != nil {
		return nil, fmt.Errorf("create standalone staging: %w", err)
	}
	var db *sql.DB
	defer func() {
		if err != nil {
			if db != nil {
				_ = db.Close()
			}
			_ = os.RemoveAll(dir)
		}
	}()
	dbPath := filepath.Join(dir, "session.db")
	db, err = database.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("open standalone database: %w", err)
	}
	q := database.NewQueries(db)
	user := &database.User{ID: uuid.NewString(), Username: "local-owner", Role: "user", PasswordHash: []byte{}}
	if err = q.CreateUser(*user, false); err != nil {
		return nil, fmt.Errorf("create private session owner: %w", err)
	}
	retention := max(int64(60), int64((lifetime+time.Second-1)/time.Second))
	policy := database.ResourcePolicy{
		ServerStorageBytes: cfg.MaxBytes, AccountStorageBytes: cfg.MaxBytes,
		ServerFiles: int64(cfg.MaxFiles), AccountFiles: int64(cfg.MaxFiles),
		ServerTransfers: int64(cfg.MaxFiles), AccountTransfers: int64(cfg.MaxFiles),
		ServerSlots: 1, AccountSlots: 1,
		MaxRetentionSeconds: retention, PendingUploadSeconds: retention,
		ReserveDiskBytes: 1 << 20, ReserveDiskPercent: 1,
	}
	if err = q.SetResourcePolicy(policy); err != nil {
		return nil, err
	}
	if err = q.SetMaxFileSize(cfg.MaxBytes); err != nil {
		return nil, err
	}
	fs, err := store.NewDiskStore(filepath.Join(dir, "files"))
	if err != nil {
		return nil, err
	}
	backendConfig := config.Config{
		PublicURL: cfg.Origin, CORSOrigin: cfg.Origin,
		StoragePath: filepath.Join(dir, "files"), DBPath: dbPath,
		MaxFileSize: cfg.MaxBytes, MaxSlotSize: cfg.MaxBytes,
		MaxSlotTransfers: cfg.MaxFiles, MaxSlotExpiry: time.Duration(retention) * time.Second,
		DefaultExpiry: lifetime, MaxManifestSize: database.MaxManifestBytes,
		MaxFilesPerTransfer: min(cfg.MaxFiles, database.MaxTransferFiles),
		MaxActiveStreams:    cfg.MaxStreams, MaxStreamsPerAccount: cfg.MaxStreams,
		MaxStreamsPerIP: cfg.MaxStreams, MaxStreamsPerTransfer: cfg.MaxStreams,
		MaxStreamsPerSlot: cfg.MaxStreams,
		RateLimitGlobal:   100, RateLimitBurst: 200,
		RateLimitCreation: 10, RateLimitCreationBurst: 20,
	}
	server := api.NewServer(backendConfig, q, fs)
	return &Session{
		cfg: cfg, dir: dir, db: db, queries: q, server: server,
		public: server.LocalTransferHandler(nil), owner: server.LocalTransferHandler(user),
	}, nil
}

// Activate selects the single prepared resource before its link is advertised.
// It can be called once. Child Receive transfers remain scoped to this inbox.
func (s *Session) Activate(kind, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.id != "" {
		return errors.New("standalone session is closed or already active")
	}
	if kind == "send" {
		transfer, err := s.queries.GetTransfer(id)
		if err != nil || transfer.Status != "complete" || transfer.MaxDownloads != s.cfg.MaxDownloads {
			return errors.New("standalone Send must be complete with the configured download limit")
		}
	} else if kind == "receive" {
		slot, err := s.queries.GetSlot(id)
		if err != nil || slot.ReceiveProtocol != 2 || slot.MaxFiles != s.cfg.MaxFiles {
			return errors.New("standalone Receive must use protocol v2 and the configured file limit")
		}
	} else {
		return errors.New("standalone resource kind must be send or receive")
	}
	s.kind, s.id = kind, id
	return nil
}

// Handler returns only guest transfer APIs. The caller mounts matching browser
// assets separately; owner/account/admin operations are never network routes.
func (s *Session) Handler() http.Handler { return s.handler(false) }

// OwnerHandler returns the private transfer control plane for an in-process HTTP
// transport. Never mount it on a listener: it carries process-owner authority.
func (s *Session) OwnerHandler() http.Handler { return s.handler(true) }

func (s *Session) handler(owner bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		if s.closed || !time.Now().Before(s.cfg.ExpiresAt) {
			s.mu.Unlock()
			http.Error(w, "LAN sharing session ended", http.StatusGone)
			return
		}
		s.requests.Add(1)
		kind, id := s.kind, s.id
		s.mu.Unlock()
		defer s.requests.Done()
		if owner {
			s.owner.ServeHTTP(w, r)
			return
		}
		origin, _ := url.Parse(s.cfg.Origin)
		if !strings.EqualFold(r.Host, origin.Host) || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != s.cfg.Origin) || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(w, "same-origin LAN request required", http.StatusForbidden)
			return
		}
		if !s.allowed(r, kind, id) {
			http.NotFound(w, r)
			return
		}
		s.public.ServeHTTP(w, r)
	})
}

func (s *Session) allowed(r *http.Request, kind, id string) bool {
	if r.Method == http.MethodGet && (r.URL.Path == "/api/v1/config" || r.URL.Path == "/api/v1/health") {
		return true
	}
	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if id == "" || len(parts) < 4 || parts[0] != "api" || parts[1] != "v1" {
		return false
	}
	if kind == "receive" && parts[2] == "slots" && parts[3] == id && len(parts) == 5 {
		return r.Method == http.MethodGet && (parts[4] == "availability" || parts[4] == "traffic-status") || r.Method == http.MethodPost && parts[4] == "transfers"
	}
	if parts[2] != "transfers" {
		return false
	}
	if kind == "send" {
		if parts[3] != id {
			return false
		}
		return r.Method == http.MethodGet && (len(parts) == 4 || len(parts) == 5 && (parts[4] == "manifest" || parts[4] == "traffic-status") || len(parts) == 6 && parts[4] == "files") || r.Method == http.MethodPost && len(parts) == 5 && parts[4] == "downloaded"
	}
	slots, err := s.queries.TransferSlotIDsContext(r.Context(), parts[3])
	if err != nil || len(slots) != 1 || slots[0] != id {
		return false
	}
	if len(parts) == 4 {
		return r.Method == http.MethodDelete
	}
	if len(parts) == 5 {
		return r.Method == http.MethodGet && (parts[4] == "upload-status" || parts[4] == "traffic-status") || r.Method == http.MethodPost && (parts[4] == "files" || parts[4] == "manifest" || parts[4] == "complete") || r.Method == http.MethodOptions && parts[4] == "files"
	}
	return len(parts) == 6 && parts[4] == "files" && (r.Method == http.MethodHead || r.Method == http.MethodPatch)
}

// Stats obtains completion evidence using the shared acknowledgement and inbox
// counters. Download requests alone are not recipient decryption evidence.
func (s *Session) Stats() (Stats, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Stats{}, errors.New("standalone session closed")
	}
	if s.kind == "send" {
		t, err := s.queries.GetTransfer(s.id)
		if err != nil {
			return Stats{}, err
		}
		if t.DownloadedAt.Valid {
			return Stats{Downloads: t.DownloadCount}, nil
		}
		return Stats{}, nil
	}
	if s.kind == "receive" {
		n, err := s.queries.CompletedSlotFiles(s.id)
		return Stats{CompletedFiles: int(n)}, err
	}
	return Stats{}, nil
}

// Close stops new admission, waits for handlers and removes only owned staging.
// Stop and drain/abort the listener first so active streams cannot wait forever.
func (s *Session) Close() error {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.mu.Unlock()
		s.requests.Wait()
		s.server.WaitForRequests()
		s.closeErr = errors.Join(s.db.Close(), os.RemoveAll(s.dir))
	})
	return s.closeErr
}
