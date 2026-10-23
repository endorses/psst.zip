package api

import (
	"context"
	"net/http"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/tus"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// transferRoutes is the common transfer boundary used by the hosted server and
// temporary LAN sessions. Authentication and public route selection are supplied
// by each embedding mode; admission, tus, quotas and acknowledgement remain shared.
func (s *Server) transferRoutes(r chi.Router, creationRL *rateLimiter) {
	// Transfer endpoints (send flow)
	r.With(rateLimitMiddleware(creationRL, s.clientIP), s.requireRegularUser, s.requirePublicTransfers).Post("/transfers", s.createTransfer)
	r.With(s.requireTransferRead).Get("/transfers/{transferID}", s.getTransfer)
	r.Delete("/transfers/{transferID}", s.deleteTransfer)
	r.With(s.requireRegularUser).Patch("/transfers/{transferID}/title", s.renameTransferTitle)
	r.With(s.requireUpload, s.requirePublicTransfers).Post("/transfers/{transferID}/complete", s.completeTransfer)
	r.With(s.requireTransferRead).Post("/transfers/{transferID}/downloaded", s.acknowledgeDownload)
	r.With(s.requireUpload, s.admitPayload, s.measureUpload).Post("/transfers/{transferID}/manifest", s.uploadManifest)
	r.With(s.requireTransferRead, s.admitPayload, s.measureDownload).Get("/transfers/{transferID}/manifest", s.downloadManifest)

	r.Get("/transfers/{transferID}/upload-status", s.uploadStatus)

	// Tus file upload endpoints
	r.Options("/transfers/{transferID}/files", tus.ServeOptions)
	r.With(s.requireUpload, s.requirePublicTransfers).Post("/transfers/{transferID}/files", s.tusCreate)
	r.With(s.requireUpload).Head("/transfers/{transferID}/files/{fileID}", s.tusHead)
	r.With(s.requireUpload, s.admitPayload, s.measureUpload).Patch("/transfers/{transferID}/files/{fileID}", s.tusPatch)

	// File download
	r.With(s.requireTransferRead, s.admitPayload, s.measureDownload).Get("/transfers/{transferID}/files/{fileID}", s.downloadFile)

	// Slot endpoints (receive flow)
	r.With(rateLimitMiddleware(creationRL, s.clientIP), s.requireRegularUser, s.requirePublicTransfers).Post("/slots", s.createSlot)
	r.With(s.requireInboxOwner).Get("/slots/{slotID}", s.getSlot)
	r.With(s.requireInboxOwner).Get("/slots/{slotID}/inbox", s.getInboxPage)
	r.With(s.requireInboxOwner).Get("/slots/{slotID}/transfers/{transferID}/membership", s.inboxTransferMembership)
	r.Delete("/slots/{slotID}", s.deleteSlot)
	r.With(s.requireRegularUser).Patch("/slots/{slotID}/title", s.renameSlotTitle)
	r.With(s.requireInboxOwner, s.admitEvents).Get("/slots/{slotID}/events", s.slotEvents)

	r.Get("/slots/{slotID}/availability", s.slotAvailability)

	// Slot-scoped transfer creation
	r.With(rateLimitMiddleware(creationRL, s.clientIP), s.requirePublicTransfers).Post("/slots/{slotID}/transfers", s.createSlotTransfer)
}

// LocalTransferHandler exposes transfer handlers without hosted account routes.
// A nonnil owner supplies an in-process identity, never a network credential.
// The embedding caller must keep that owner handler off public listeners.
func (s *Server) LocalTransferHandler(owner *database.User) http.Handler {
	r := chi.NewRouter()
	r.Use(s.trackRequests)
	r.Use(requestLimits)
	r.Use(middleware.Recoverer)
	r.Use(securityHeadersMiddleware)
	r.Use(s.admitRequest)
	if owner != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r = r.WithContext(context.WithValue(r.Context(), authKey{}, &authentication{user: owner}))
				next.ServeHTTP(w, r)
			})
		})
	}
	creation := newRateLimiter(s.cfg.RateLimitCreation, s.cfg.RateLimitCreationBurst)
	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.health)
		r.Get("/config", s.publicConfig)
		r.Get("/transfers/{transferID}/traffic-status", s.transferTrafficStatus)
		r.Get("/slots/{slotID}/traffic-status", s.slotTrafficStatus)
		s.transferRoutes(r, creation)
	})
	return r
}
