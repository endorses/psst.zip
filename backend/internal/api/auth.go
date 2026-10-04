package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/endorses/psst.zip/backend/internal/database"
	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "psst_session"

type authKey struct{}
type authentication struct {
	user    *database.User
	session *database.Session
}

func identity(r *http.Request) *authentication {
	a, _ := r.Context().Value(authKey{}).(*authentication)
	return a
}
func bearer(r *http.Request) string {
	p := strings.Fields(r.Header.Get("Authorization"))
	if len(p) == 2 && strings.EqualFold(p[0], "Bearer") {
		return p[1]
	}
	return ""
}
func tokenHash(token string) []byte { h := sha256.Sum256([]byte(token)); return h[:] }
func (s *Server) requestOrigin(r *http.Request) string {
	if s.cfg.PublicURL != "" {
		u, err := url.Parse(s.cfg.PublicURL)
		if err == nil {
			return u.Scheme + "://" + u.Host
		}
	}
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
func (s *Server) secureAuth(w http.ResponseWriter, r *http.Request) bool {
	if !s.cfg.AuthAllowInsecureHTTP && !strings.HasPrefix(s.requestOrigin(r), "https://") {
		writeError(w, http.StatusForbidden, "authentication requires HTTPS; configure PUBLIC_URL or enable AUTH_ALLOW_INSECURE_HTTP only for local development")
		return false
	}
	return true
}
func (s *Server) sameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("Origin") != s.requestOrigin(r) {
		writeError(w, http.StatusForbidden, "same-origin request required")
		return false
	}
	return true
}
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/auth/") || strings.HasPrefix(r.URL.Path, "/api/v1/admin/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		token := bearer(r)
		cookieAuth := false
		if token == "" && r.Header.Get("Authorization") == "" {
			if c, err := r.Cookie(sessionCookie); err == nil {
				token = c.Value
				cookieAuth = true
			}
		}
		if token != "" {
			session, user, err := s.queries.SessionByHash(tokenHash(token))
			if err == nil && !user.Disabled && time.Now().Before(session.ExpiresAt) {
				if !s.secureAuth(w, r) {
					return
				}
				if cookieAuth && r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" && !s.sameOrigin(w, r) {
					return
				}
				r = r.WithContext(context.WithValue(r.Context(), authKey{}, &authentication{user, session}))
			}
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) requireLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if identity(r) == nil {
			writeError(w, http.StatusUnauthorized, "sign in required")
			return
		}
		if identity(r).user.MustChangePassword && r.URL.Path != "/api/v1/auth/me" && r.URL.Path != "/api/v1/auth/password" && r.URL.Path != "/api/v1/auth/logout" {
			accountRestriction(w, "password_change_required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
func accountRestriction(w http.ResponseWriter, code string) {
	message := "Change your temporary password before continuing."
	if code == "admin_transfer_forbidden" {
		message = "Administrator accounts manage the server. Use a regular account to transfer files."
	}
	writeJSON(w, http.StatusForbidden, map[string]string{"code": code, "error": message})
}
func (s *Server) requireRegularUser(next http.Handler) http.Handler {
	return s.requireLogin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if identity(r).user.Role != "user" {
			accountRestriction(w, "admin_transfer_forbidden")
			return
		}
		next.ServeHTTP(w, r)
	}))
}
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return s.requireLogin(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if identity(r).user.Role != "admin" {
			writeError(w, http.StatusForbidden, "administrator required")
			return
		}
		next.ServeHTTP(w, r)
	}))
}
func (s *Server) owns(r *http.Request, kind, id string) bool {
	a := identity(r)
	if a == nil || a.user.MustChangePassword {
		return false
	}
	if a.user.Role == "admin" {
		return true
	}
	owner, err := s.queries.Owner(kind, id)
	return err == nil && owner == a.user.ID
}
func (s *Server) slotOwnerActive(w http.ResponseWriter, id string) bool {
	owner, err := s.queries.Owner("slot", id)
	if err != nil {
		writeError(w, 404, "slot not found")
		return false
	}
	if owner == "" {
		writeError(w, 403, "legacy receive links cannot accept new uploads; create a new receive link")
		return false
	}
	u, err := s.queries.UserByID(owner)
	if err != nil || u.Disabled {
		incidentFailure(w, database.ErrAccountDisabled)
		return false
	}
	return true
}
func (s *Server) requireUpload(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "transferID")
		if !isValidUUID(id) {
			writeError(w, 400, "invalid transfer ID")
			return
		}
		t, err := s.queries.GetTransfer(id)
		if err != nil {
			writeError(w, 404, "transfer not found")
			return
		}
		if t.Status == "revoked" {
			incidentFailure(w, database.ErrResourceRevoked)
			return
		}
		slots, err := s.queries.TransferSlotIDs(id)
		if err != nil {
			writeError(w, 500, "database error")
			return
		}
		for _, slot := range slots {
			policy, err := s.queries.GetSlot(slot)
			if err != nil || policy.ReceiveProtocol != 2 {
				policyError(w, 403, "legacy_receive_disabled", "legacy receive links cannot accept submissions; create a new receive link")
				return
			}
			if !s.slotOwnerActive(w, slot) {
				return
			}
		}
		token := bearer(r)
		if len(slots) > 0 && token != "" && len(t.DeleteTokenHash) > 0 && subtle.ConstantTimeCompare(tokenHash(token), t.DeleteTokenHash) == 1 {
			next.ServeHTTP(w, r)
			return
		}
		if a := identity(r); a != nil {
			if a.user.MustChangePassword {
				accountRestriction(w, "password_change_required")
				return
			}
			if a.user.Role != "user" {
				accountRestriction(w, "admin_transfer_forbidden")
				return
			}
			if s.owns(r, "transfer", id) {
				next.ServeHTTP(w, r)
				return
			}
		}
		writeError(w, 403, "only the owner or invited uploader may modify this transfer")
	})
}

// BootstrapAdmin creates the first administrator only from operator-provided credentials.
func (s *Server) BootstrapAdmin() error {
	if _, err := parseTrustedProxies(s.cfg.TrustedProxies); err != nil {
		return err
	}
	if s.cfg.PublicURL != "" {
		u, err := url.Parse(s.cfg.PublicURL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return errors.New("PUBLIC_URL must be an absolute HTTP(S) origin without a path")
		}
	}
	n, err := s.queries.UserCount()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	if s.cfg.AdminUsername == "" || s.cfg.AdminPassword == "" {
		return errors.New("no accounts exist: set ADMIN_USERNAME and ADMIN_PASSWORD (12–72 bytes) to create the first administrator")
	}
	name := strings.TrimSpace(s.cfg.AdminUsername)
	if !validUsername(name) {
		return errors.New("ADMIN_USERNAME must be 3–64 letters, digits, dots, underscores or hyphens")
	}
	hash, err := passwordHash(s.cfg.AdminPassword)
	if err != nil {
		return err
	}
	return s.queries.CreateUser(database.User{ID: uuid.NewString(), Username: name, Role: "admin", PasswordHash: hash}, true)
}
func validUsername(name string) bool {
	if len(name) < 3 || len(name) > 64 {
		return false
	}
	for _, c := range name {
		if !unicode.IsLetter(c) && !unicode.IsDigit(c) && c != '.' && c != '_' && c != '-' {
			return false
		}
	}
	return true
}
func passwordHash(p string) ([]byte, error) {
	if len(p) < 12 || len(p) > 72 {
		return nil, errors.New("password must contain 12–72 UTF-8 bytes")
	}
	return bcrypt.GenerateFromPassword([]byte(p), bcrypt.DefaultCost)
}
func authJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeError(w, 400, "invalid request body")
		return false
	}
	return true
}
func (s *Server) authRoutes(r chi.Router) {
	// Use the same explicit proxy trust boundary as all other request limits.
	limiter := newRateLimiter(0.2, 10)
	limited := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ip := s.clientIP(r)
			if !limiter.allow(ip) {
				writeError(w, 429, "too many sign-in attempts; try again later")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
	s.adminSecurityRoutes(r, limited)
	r.Get("/auth/status", s.authStatus)
	r.With(limited, s.limitPasswordWork).Post("/auth/login", s.login)
	r.With(limited).Post("/auth/pairings/redeem", s.redeemPairing)
	r.Group(func(r chi.Router) {
		r.Use(s.requireLogin)
		r.Get("/auth/me", s.me)
		r.Post("/auth/logout", s.logout)
		r.With(s.limitPasswordWork).Post("/auth/password", s.changePassword)
		r.Get("/auth/sessions", s.sessions)
		r.Delete("/auth/sessions/{sessionID}", s.deleteSession)
		r.With(s.requireRegularUser).Post("/auth/pairings", s.createPairing)
		r.With(s.requireRegularUser).Get("/auth/pairings/{pairingID}", s.pairingStatus)
		r.With(s.requireRegularUser).Delete("/auth/pairings/{pairingID}", s.cancelPairing)
		r.Get("/auth/resources", s.resources)
	})
	r.Group(func(r chi.Router) {
		r.Use(s.requireAdmin)
		r.Get("/admin/users", s.users)
		r.With(s.limitPasswordWork).Post("/admin/users", s.createUser)
		r.With(s.limitPasswordWork).Patch("/admin/users/{userID}", s.updateUser)
	})
}
func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.queries.UserCount()
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"setup_required": n == 0})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.secureAuth(w, r) {
		return
	}
	var req struct {
		Username     string `json:"username"`
		Password     string `json:"password"`
		DeviceName   string `json:"device_name"`
		SessionType  string `json:"session_type"`
		Code         string `json:"code"`
		RecoveryCode string `json:"recovery_code"`
	}
	if !decodeCreation(w, r, &req) {
		return
	}
	if req.Code != "" && req.RecoveryCode != "" {
		writeError(w, 400, "provide one authenticator or recovery code")
		return
	}
	if req.SessionType == "" {
		req.SessionType = "web"
	}
	if req.SessionType != "web" && req.SessionType != "device" {
		writeError(w, 400, "invalid session type")
		return
	}
	if req.SessionType == "web" && !s.sameOrigin(w, r) {
		return
	}
	// Hash bounded normalized identities rather than retaining raw account names in
	// the limiter. Every name has the same limiter behavior, including nonexistent ones.
	account := strings.ToLower(strings.TrimSpace(req.Username))
	accountHash := sha256.Sum256([]byte(account))
	if !s.loginAccounts.allow(string(accountHash[:])) {
		w.Header().Set("Retry-After", "30")
		writeError(w, http.StatusTooManyRequests, "too many sign-in attempts; try again later")
		return
	}
	user, err := s.queries.UserByName(strings.TrimSpace(req.Username))
	var administrator database.AdminSecurityState
	if err == nil && user.Role == "admin" {
		administrator, err = s.queries.AdminSecurity(user.ID)
		if err != nil {
			adminSecurityFailure(w, err)
			return
		}
	}
	// Always perform an expensive comparison, including for unknown usernames.
	hash := []byte("$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy")
	if err == nil {
		hash = user.PasswordHash
	}
	passwordErr := bcrypt.CompareHashAndPassword(hash, []byte(req.Password))
	if err != nil || passwordErr != nil || user.Disabled {
		if err == nil && user.Role == "admin" && !user.Disabled {
			// Persist account failures without revealing account type or lock state to
			// callers who have not proved the password.
			_ = s.queries.RecordAdminAuthenticationFailure(user.ID, user.PasswordHash, administrator.Revision, time.Now())
		}
		writeError(w, 401, "invalid username or password")
		return
	}
	if user.Role == "admin" && req.SessionType == "device" {
		accountRestriction(w, "admin_transfer_forbidden")
		return
	}
	token, hash, err := newDeleteToken()
	if err != nil {
		writeError(w, 500, "could not create session")
		return
	}
	session := newSession(user.ID, req.DeviceName)
	if user.Role == "admin" {
		err = s.queries.CreateAdminSession(session, hash, user.PasswordHash, administrator.Revision, req.Code, req.RecoveryCode, time.Now())
	} else {
		err = s.queries.CreateSession(session, hash, user.PasswordHash)
	}
	if err != nil {
		if user.Role == "admin" {
			adminSecurityFailure(w, err)
			return
		}
		writeError(w, 401, "account changed; sign in again")
		return
	}
	s.loginResponse(w, r, user, session, token, req.SessionType == "device")
}
func newSession(user, name string) database.Session {
	if len(name) > 100 {
		name = name[:100]
	}
	if strings.TrimSpace(name) == "" {
		name = "Web browser"
	}
	now := time.Now().UTC()
	return database.Session{ID: uuid.NewString(), UserID: user, DeviceName: name, CreatedAt: now, ExpiresAt: now.Add(30 * 24 * time.Hour)}
}
func (s *Server) loginResponse(w http.ResponseWriter, r *http.Request, u *database.User, session database.Session, token string, device bool) {
	w.Header().Set("Cache-Control", "no-store")
	out := map[string]any{"user": u, "session_id": session.ID, "expires_at": session.ExpiresAt}
	if device {
		out["token"] = token
	} else {
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/api/v1", HttpOnly: true, Secure: !s.cfg.AuthAllowInsecureHTTP, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 3600})
	}
	writeJSON(w, 200, out)
}
func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	a := identity(r)
	writeJSON(w, 200, map[string]any{"user": a.user, "session_id": a.session.ID})
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	a := identity(r)
	if err := s.queries.DeleteSession(a.session.ID, a.user.ID); err != nil {
		writeError(w, 500, "could not revoke session")
		return
	}
	s.clearCookie(w)
	w.WriteHeader(204)
}
func (s *Server) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Path: "/api/v1", MaxAge: -1, HttpOnly: true, Secure: !s.cfg.AuthAllowInsecureHTTP, SameSite: http.SameSiteStrictMode})
}
func (s *Server) sessions(w http.ResponseWriter, r *http.Request) {
	a := identity(r)
	listing, err := s.queries.AuthenticationSessions(a.user.ID, a.session.ID)
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	list := listing.Sessions
	for i := range list {
		list[i].Current = list[i].ID == a.session.ID
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"sessions": list, "sessions_limited": listing.Limited, "total_active_sessions": listing.TotalActive, "total_active_sessions_exact": listing.TotalActiveExact, "session_limit": database.MaxAuthSessionsPerUser})
}
func (s *Server) deleteSession(w http.ResponseWriter, r *http.Request) {
	a := identity(r)
	id := chi.URLParam(r, "sessionID")
	if err := s.queries.DeleteSession(id, a.user.ID, adminActor(r)); err != nil {
		if adminActor(r) != nil && (errors.Is(err, database.ErrAdminAuthenticationChanged) || errors.Is(err, database.ErrAdminRecentRequired)) {
			adminSecurityFailure(w, err)
			return
		}
		writeError(w, 500, "could not revoke session")
		return
	}
	if id == a.session.ID {
		s.clearCookie(w)
	}
	w.WriteHeader(204)
}
func (s *Server) createPairing(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ReplaceID string `json:"replace_id"`
	}
	if r.ContentLength != 0 && !authJSON(w, r, &req) {
		return
	}
	id := uuid.NewString()
	code, hash, err := newDeleteToken()
	if err != nil {
		writeError(w, 500, "could not create pairing")
		return
	}
	expiry := time.Now().UTC().Add(5 * time.Minute)
	a := identity(r)
	if err := s.queries.CreateTrackedPairing(id, hash, a.user.ID, a.session.ID, expiry, req.ReplaceID); err != nil {
		pairingError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 201, map[string]any{"id": id, "code": code, "expires_at": expiry})
}
func pairingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeError(w, 404, "pairing not found")
	case errors.Is(err, database.ErrPairingCapacity):
		w.Header().Set("Retry-After", "60")
		writeJSON(w, 429, map[string]string{"code": "pairing_capacity", "error": "Too many login QR codes. Cancel unused codes or wait for old codes to expire before creating another."})
	case errors.Is(err, database.ErrPairingConnected):
		writeError(w, 409, "Phone already connected. Revoke its session in Connected devices if needed.")
	default:
		writeError(w, 500, "could not update pairing")
	}
}
func (s *Server) pairingStatus(w http.ResponseWriter, r *http.Request) {
	a := identity(r)
	status, err := s.queries.PairingStatus(chi.URLParam(r, "pairingID"), a.user.ID, a.session.ID)
	if err != nil {
		pairingError(w, err)
		return
	}
	writeJSON(w, 200, status)
}
func (s *Server) cancelPairing(w http.ResponseWriter, r *http.Request) {
	a := identity(r)
	if err := s.queries.CancelPairing(chi.URLParam(r, "pairingID"), a.user.ID, a.session.ID); err != nil {
		pairingError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (s *Server) redeemPairing(w http.ResponseWriter, r *http.Request) {
	if !s.secureAuth(w, r) {
		return
	}
	var req struct {
		Code       string `json:"code"`
		DeviceName string `json:"device_name"`
	}
	if !authJSON(w, r, &req) {
		return
	}
	token, hash, err := newDeleteToken()
	if err != nil {
		writeError(w, 500, "could not create session")
		return
	}
	session := newSession("", req.DeviceName)
	u, err := s.queries.RedeemPairing(tokenHash(req.Code), hash, session)
	if err != nil {
		if errors.Is(err, database.ErrAdminTransfer) {
			accountRestriction(w, "admin_transfer_forbidden")
			return
		}
		if errors.Is(err, database.ErrPasswordChangeRequired) {
			accountRestriction(w, "password_change_required")
			return
		}
		writeError(w, 401, "pairing code is invalid or expired")
		return
	}
	s.loginResponse(w, r, u, session, token, true)
}
func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	limit, ok := pageLimit(w, r)
	if !ok {
		return
	}
	users, next, err := s.queries.UsersPage(limit, r.URL.Query().Get("after"))
	if errors.Is(err, database.ErrInvalidPage) {
		writeError(w, 400, err.Error())
		return
	}
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"users": users, "next_cursor": next})
}
func (s *Server) createUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !authJSON(w, r, &req) {
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Role == "" {
		req.Role = "user"
	}
	if !validUsername(req.Username) || (req.Role != "user" && req.Role != "admin") {
		writeError(w, 400, "invalid username or role")
		return
	}
	hash, err := passwordHash(req.Password)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	u := database.User{ID: uuid.NewString(), Username: req.Username, Role: req.Role, PasswordHash: hash, MustChangePassword: req.Role == "user"}
	if err := s.queries.CreateUser(u, false, adminActor(r)); err != nil {
		if errors.Is(err, database.ErrAdminAuthenticationChanged) || errors.Is(err, database.ErrAdminRecentRequired) {
			adminSecurityFailure(w, err)
			return
		}
		if errors.Is(err, database.ErrAccountCapacity) {
			writeJSON(w, 409, map[string]string{"code": "account_capacity", "error": "The server has reached its 1,000-account limit, including disabled accounts."})
			return
		}
		writeError(w, 409, "username already exists or account could not be created")
		return
	}
	writeJSON(w, 201, map[string]any{"user": u})
}
func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Disabled *bool   `json:"disabled"`
		Password *string `json:"password"`
	}
	if !authJSON(w, r, &req) {
		return
	}
	if req.Disabled == nil && req.Password == nil {
		writeError(w, 400, "no account change supplied")
		return
	}
	var hash []byte
	var err error
	if req.Password != nil {
		hash, err = passwordHash(*req.Password)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
	}
	id := chi.URLParam(r, "userID")
	if err = s.queries.UpdateUser(id, req.Disabled, hash, adminActor(r)); err != nil {
		if errors.Is(err, database.ErrAdminAuthenticationChanged) || errors.Is(err, database.ErrAdminRecentRequired) {
			adminSecurityFailure(w, err)
			return
		}
		if errors.Is(err, database.ErrLastAdmin) {
			writeError(w, 409, err.Error())
		} else if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 404, "user not found")
		} else {
			writeError(w, 500, "could not update user")
		}
		return
	}
	u, err := s.queries.UserByID(id)
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	writeJSON(w, 200, map[string]any{"user": u})
}
func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Current  string `json:"current_password"`
		Password string `json:"password"`
	}
	if !authJSON(w, r, &req) {
		return
	}
	a := identity(r)
	if bcrypt.CompareHashAndPassword(a.user.PasswordHash, []byte(req.Current)) != nil {
		writeError(w, 403, "current password is incorrect")
		return
	}
	if req.Current == req.Password {
		writeError(w, 400, "choose a different password")
		return
	}
	hash, err := passwordHash(req.Password)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if a.user.Role == "admin" {
		err = s.queries.ChangeAdminPassword(a.user.ID, a.session.ID, a.user.PasswordHash, hash)
	} else {
		err = s.queries.ChangePassword(a.user.ID, a.user.PasswordHash, hash)
	}
	if err != nil {
		if a.user.Role == "admin" && (errors.Is(err, database.ErrAdminAuthenticationChanged) || errors.Is(err, database.ErrAdminRecentRequired)) {
			adminSecurityFailure(w, err)
			return
		}
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, 401, "account changed; sign in again")
			return
		}
		writeError(w, 500, "could not change password")
		return
	}
	s.clearCookie(w)
	w.WriteHeader(204)
}
func (s *Server) resources(w http.ResponseWriter, r *http.Request) {
	a := identity(r)
	transfers := []TransferResponse{}
	slots := []SlotResponse{}
	owner := a.user.ID
	if a.user.Role == "admin" && r.URL.Query().Get("all") != "true" {
		accountRestriction(w, "admin_transfer_forbidden")
		return
	}
	if r.URL.Query().Get("all") == "true" {
		if a.user.Role != "admin" {
			writeError(w, 403, "administrator required")
			return
		}
		owner = ""
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			writeError(w, 400, "page limit must be between 1 and 100")
			return
		}
		limit = value
	}
	page, err := s.queries.ResourceIDsPage(owner, limit, r.URL.Query().Get("after"))
	ids := page.Transfers
	if errors.Is(err, database.ErrInvalidPage) {
		writeError(w, 400, err.Error())
		return
	}
	if err != nil {
		writeError(w, 500, "database error")
		return
	}
	for _, id := range ids {
		t, err := s.queries.GetTransfer(id)
		if err != nil {
			writeError(w, 500, "could not read resource")
			return
		}
		count, size, err := s.queries.FileCountAndSize(id)
		if err != nil {
			writeError(w, 500, "could not read file counters")
			return
		}
		manifest, err := s.queries.HasManifest(id)
		if err != nil {
			writeError(w, 500, "could not read manifest status")
			return
		}
		item := TransferResponse{ID: t.ID, Status: t.Status, FileCount: count, TotalSize: size, HasManifest: manifest, ExpiresAt: t.ExpiresAt, MaxDownloads: t.MaxDownloads, DownloadCount: t.DownloadCount, CreatedAt: t.CreatedAt, Files: []FileInfo{}}
		if t.CompletedAt.Valid {
			item.CompletedAt = &t.CompletedAt.Time
		}
		if t.DownloadedAt.Valid {
			item.DownloadedAt = &t.DownloadedAt.Time
		}
		if owner == "" {
			item.OwnerID, err = s.queries.Owner("transfer", id)
			if err != nil {
				writeError(w, 500, "could not read resource owner")
				return
			}
		}
		transfers = append(transfers, item)
	}
	for _, id := range page.Slots {
		slot, err := s.queries.GetSlot(id)
		if err != nil {
			writeError(w, 500, "could not read inbox")
			return
		}
		count, completed, total, err := s.queries.SlotResourceCounts(id)
		if err != nil {
			writeError(w, 500, "could not read inbox counters")
			return
		}
		item := SlotResponse{ID: id, Status: slot.Status, Transfers: []SlotTransferInfo{}, ExpiresAt: slot.ExpiresAt, CreatedAt: slot.CreatedAt, FileCount: &count, TotalSize: &total}
		if err := s.slotPolicy(&item, slot, completed); err != nil {
			writeError(w, 500, "could not read inbox counters")
			return
		}
		if owner == "" {
			item.OwnerID, err = s.queries.Owner("slot", id)
			if err != nil {
				writeError(w, 500, "could not read inbox owner")
				return
			}
		}
		slots = append(slots, item)
	}

	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, map[string]any{"transfers": transfers, "slots": slots, "next_cursor": page.NextCursor})
}

func pageLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := 100
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, 400, "page limit must be between 1 and 100")
			return 0, false
		}
		limit = parsed
	}
	return limit, true
}
