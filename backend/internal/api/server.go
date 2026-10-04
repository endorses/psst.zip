package api

import (
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
	"github.com/endorses/psst.zip/backend/internal/tus"
)

// Server holds the HTTP server dependencies.
type Server struct {
	requests            sync.WaitGroup
	admission           *streamAdmission
	applicationRequests chan struct{}
	recoveryRequests    chan struct{}
	trafficDegraded     atomic.Bool
	cfg                 config.Config
	loginAccounts       *rateLimiter
	passwordWork        chan struct{}
	queries             *database.Queries
	fileStore           store.FileStore
	tusH                *tus.Handler
	sseHub              *SSEHub
}

// NewServer creates a Server with all dependencies wired up.
func NewServer(cfg config.Config, q *database.Queries, fs store.FileStore) *Server {
	q.SetCapacityPaths(cfg.StoragePath, cfg.DBPath)
	s := &Server{
		cfg:                 cfg,
		queries:             q,
		fileStore:           fs,
		sseHub:              NewSSEHub(),
		loginAccounts:       newRateLimiter(1.0/30, 10),
		passwordWork:        make(chan struct{}, 4),
		admission:           newStreamAdmission(cfg),
		applicationRequests: make(chan struct{}, finiteLimit(cfg.MaxActiveRequests, 128)),
		recoveryRequests:    make(chan struct{}, finiteLimit(cfg.MaxRecoveryRequests, 32)),
	}
	ts := &tusStore{queries: q, maxSlotSize: cfg.MaxSlotSize}
	s.tusH = tus.NewHandler(ts, fs, 0)
	return s
}

// Router builds and returns the chi router with all routes registered.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(s.trackRequests)
	r.Use(requestLimits)
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(middleware.RequestID)
	r.Use(securityHeadersMiddleware)
	r.Use(s.corsMiddleware)
	r.Use(s.admitRequest)

	// Global rate limiter (looser).
	globalRL := newRateLimiter(s.cfg.RateLimitGlobal, s.cfg.RateLimitBurst)
	recoveryRL := newRateLimiter(s.cfg.RateLimitGlobal, s.cfg.RateLimitBurst)
	r.Use(s.limitRequestRates(globalRL, recoveryRL))
	r.Use(s.authenticate)

	// Stricter rate limiter for creation endpoints.
	creationRL := newRateLimiter(s.cfg.RateLimitCreation, s.cfg.RateLimitCreationBurst)

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.health)
		r.Get("/config", s.publicConfig)
		r.With(s.requireAdmin).Patch("/admin/settings", s.updateSettings)
		r.With(s.requireAdmin).Get("/admin/resource-policy", s.getResourcePolicy)
		r.With(s.requireAdmin).Patch("/admin/resource-policy", s.updateResourcePolicy)
		r.With(s.requireRegularUser).Get("/auth/usage", s.accountUsage)
		r.With(s.requireAdmin).Get("/admin/overview", s.getOverview)
		r.With(s.requireAdmin).Get("/admin/traffic", s.getTraffic)
		r.With(s.requireAdmin).Patch("/admin/traffic/settings", s.updateTrafficSettings)
		s.authRoutes(r)

		// Transfer endpoints (send flow)
		r.With(rateLimitMiddleware(creationRL, s.clientIP), s.requireRegularUser).Post("/transfers", s.createTransfer)
		r.With(s.requireTransferRead).Get("/transfers/{transferID}", s.getTransfer)
		r.Delete("/transfers/{transferID}", s.deleteTransfer)
		r.With(s.requireUpload).Post("/transfers/{transferID}/complete", s.completeTransfer)
		r.With(s.requireTransferRead).Post("/transfers/{transferID}/downloaded", s.acknowledgeDownload)
		r.With(s.requireUpload, s.admitPayload, s.measureUpload).Post("/transfers/{transferID}/manifest", s.uploadManifest)
		r.With(s.requireTransferRead, s.admitPayload, s.measureDownload).Get("/transfers/{transferID}/manifest", s.downloadManifest)

		r.Get("/transfers/{transferID}/upload-status", s.uploadStatus)

		// Tus file upload endpoints
		r.Options("/transfers/{transferID}/files", tus.ServeOptions)
		r.With(s.requireUpload).Post("/transfers/{transferID}/files", s.tusCreate)
		r.With(s.requireUpload).Head("/transfers/{transferID}/files/{fileID}", s.tusHead)
		r.With(s.requireUpload, s.admitPayload, s.measureUpload).Patch("/transfers/{transferID}/files/{fileID}", s.tusPatch)

		// File download
		r.With(s.requireTransferRead, s.admitPayload, s.measureDownload).Get("/transfers/{transferID}/files/{fileID}", s.downloadFile)

		// Slot endpoints (receive flow)
		r.With(rateLimitMiddleware(creationRL, s.clientIP), s.requireRegularUser).Post("/slots", s.createSlot)
		r.With(s.requireInboxOwner).Get("/slots/{slotID}", s.getSlot)
		r.Delete("/slots/{slotID}", s.deleteSlot)
		r.With(s.requireInboxOwner, s.admitEvents).Get("/slots/{slotID}/events", s.slotEvents)

		r.Get("/slots/{slotID}/availability", s.slotAvailability)

		// Slot-scoped transfer creation
		r.With(rateLimitMiddleware(creationRL, s.clientIP)).Post("/slots/{slotID}/transfers", s.createSlotTransfer)
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
