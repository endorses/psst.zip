package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
	"github.com/endorses/psst.zip/backend/internal/tus"
)

// Server holds the HTTP server dependencies.
type Server struct {
	cfg       config.Config
	queries   *database.Queries
	fileStore store.FileStore
	tusH      *tus.Handler
	sseHub    *SSEHub
}

// NewServer creates a Server with all dependencies wired up.
func NewServer(cfg config.Config, q *database.Queries, fs store.FileStore) *Server {
	s := &Server{
		cfg:       cfg,
		queries:   q,
		fileStore: fs,
		sseHub:    NewSSEHub(),
	}
	ts := &tusStore{queries: q}
	s.tusH = tus.NewHandler(ts, fs, cfg.MaxFileSize)
	return s
}

// Router builds and returns the chi router with all routes registered.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(securityHeadersMiddleware)
	r.Use(s.corsMiddleware)

	// Global rate limiter (looser).
	globalRL := newRateLimiter(s.cfg.RateLimitGlobal, s.cfg.RateLimitBurst)
	r.Use(rateLimitMiddleware(globalRL))

	// Stricter rate limiter for creation endpoints.
	creationRL := newRateLimiter(s.cfg.RateLimitCreation, s.cfg.RateLimitCreationBurst)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.health)

		// Transfer endpoints (send flow)
		r.With(rateLimitMiddleware(creationRL)).Post("/transfers", s.createTransfer)
		r.Get("/transfers/{transferID}", s.getTransfer)
		r.Delete("/transfers/{transferID}", s.deleteTransfer)
		r.Post("/transfers/{transferID}/complete", s.completeTransfer)
		r.Post("/transfers/{transferID}/downloaded", s.acknowledgeDownload)
		r.Post("/transfers/{transferID}/manifest", s.uploadManifest)
		r.Get("/transfers/{transferID}/manifest", s.downloadManifest)

		// Tus file upload endpoints
		r.Options("/transfers/{transferID}/files", tus.ServeOptions)
		r.Post("/transfers/{transferID}/files", s.tusCreate)
		r.Head("/transfers/{transferID}/files/{fileID}", s.tusHead)
		r.Patch("/transfers/{transferID}/files/{fileID}", s.tusPatch)

		// File download
		r.Get("/transfers/{transferID}/files/{fileID}", s.downloadFile)

		// Slot endpoints (receive flow)
		r.With(rateLimitMiddleware(creationRL)).Post("/slots", s.createSlot)
		r.Get("/slots/{slotID}", s.getSlot)
		r.Delete("/slots/{slotID}", s.deleteSlot)
		r.Get("/slots/{slotID}/events", s.slotEvents)

		// Slot-scoped transfer creation
		r.With(rateLimitMiddleware(creationRL)).Post("/slots/{slotID}/transfers", s.createSlotTransfer)
	})

	return r
}

// corsMiddleware sets CORS headers using the configured origin.
func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := s.cfg.CORSOrigin
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PATCH, HEAD, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, Tus-Resumable, Upload-Length, Upload-Offset, Upload-Metadata")
		w.Header().Set("Access-Control-Expose-Headers", "Location, Tus-Resumable, Upload-Offset, Upload-Length, Tus-Version, Tus-Extension")

		if origin != "*" {
			w.Header().Set("Vary", "Origin")
		}

		// Browsers preflight JSON creation, manifest uploads, and tus PATCH routes.
		// These requests do not reach the endpoint's method-specific handler.
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			tus.ServeOptions(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// lockTransfer serializes mutation of one transfer (including completion) without
// retaining an unbounded map of mutexes for expired resources.
func (s *Server) lockTransfer(id string) func() { return store.LockTransfer(id) }
