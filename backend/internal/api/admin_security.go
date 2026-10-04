package api

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"
	"golang.org/x/crypto/bcrypt"
)

func adminSecurityFailure(w http.ResponseWriter, err error) {
	var locked *database.AdminAuthenticationLocked
	switch {
	case errors.As(err, &locked):
		retry := locked.RetryAt.UTC().Format(time.RFC3339)
		w.Header().Set("Retry-After", strconv.FormatInt(max(1, int64(time.Until(locked.RetryAt).Seconds()+1)), 10))
		w.Header().Set("X-Psst-Retry-At", retry)
		w.Header().Set("X-Psst-Error-Code", "administrator_authentication_locked")
		writeJSON(w, 429, map[string]string{"code": "administrator_authentication_locked", "error": "Administrator authentication is temporarily locked. Try again after the cooldown.", "retry_at": retry})
	case errors.Is(err, database.ErrAdminFactorRequired):
		policyError(w, 401, "administrator_factor_required", "Enter an authenticator code or one recovery code.")
	case errors.Is(err, database.ErrAdminFactorInvalid):
		policyError(w, 401, "administrator_factor_invalid", "The authentication code is invalid or has already been used.")
	case errors.Is(err, database.ErrAdminRecentRequired):
		policyError(w, 403, "recent_authentication_required", "Confirm your current administrator credentials before continuing.")
	case errors.Is(err, database.ErrAdminAuthenticationChanged):
		policyError(w, 401, "administrator_authentication_changed", "Administrator credentials changed. Sign in again.")
	case errors.Is(err, database.ErrAdminFactorEnabled):
		policyError(w, 409, "administrator_factor_enabled", "An administrator factor is already enabled.")
	case errors.Is(err, database.ErrAdminFactorDisabled):
		policyError(w, 409, "administrator_factor_disabled", "No administrator factor is enabled.")
	case errors.Is(err, database.ErrAdminEnrollmentPending):
		policyError(w, 409, "enrollment_pending", "Enrollment is already pending. Cancel it in the original session or wait for expiry.")
	case errors.Is(err, database.ErrAdminEnrollmentExpired):
		policyError(w, 410, "enrollment_expired", "Enrollment expired or belongs to another session. Start again.")
	default:
		policyError(w, 503, "administrator_security_unavailable", "Administrator security is temporarily unavailable.")
	}
}

// Apply to every authenticated administrator mutation, including resource
// deletes outside /admin. Signing in/out and discarding a pending secret are
// explicit recovery exceptions; neither grants privileges.
func (s *Server) requireRecentAdminMutation(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := identity(r)
		if a == nil || a.user.Role != "admin" || r.Method == "GET" || r.Method == "HEAD" || r.Method == "OPTIONS" {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/v1/auth/login" || r.URL.Path == "/api/v1/auth/logout" || r.URL.Path == "/api/v1/admin/security/reauth" || (r.Method == "DELETE" && r.URL.Path == "/api/v1/admin/security/enrollment") {
			next.ServeHTTP(w, r)
			return
		}
		protected := strings.HasPrefix(r.URL.Path, "/api/v1/admin/") || r.URL.Path == "/api/v1/auth/password" || strings.HasPrefix(r.URL.Path, "/api/v1/auth/sessions/") || (r.Method == "DELETE" && (strings.HasPrefix(r.URL.Path, "/api/v1/transfers/") || strings.HasPrefix(r.URL.Path, "/api/v1/slots/")))
		if !protected {
			next.ServeHTTP(w, r)
			return
		}
		if err := s.queries.RequireRecentAdmin(a.user.ID, a.session.ID, time.Now()); err != nil {
			adminSecurityFailure(w, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (s *Server) adminSecurityRoutes(r chi.Router, limited func(http.Handler) http.Handler) {
	r.Group(func(r chi.Router) {
		r.Use(s.requireAdmin)
		r.Get("/admin/security", s.getAdminSecurity)
		r.With(limited).Post("/admin/security/enrollment", s.beginAdminEnrollment)
		r.With(limited).Post("/admin/security/enrollment/confirm", s.confirmAdminEnrollment)
		r.Delete("/admin/security/enrollment", s.cancelAdminEnrollment)
		r.With(limited, s.limitPasswordWork).Post("/admin/security/reauth", s.reauthenticateAdmin)
		r.Delete("/admin/security/factor", s.disableAdminFactor)
		r.Post("/admin/security/recovery-codes", s.regenerateAdminRecovery)
	})
}
func (s *Server) getAdminSecurity(w http.ResponseWriter, r *http.Request) {
	a := identity(r)
	state, err := s.queries.AdminSecurityMetadata(a.user.ID, a.session.ID, time.Now())
	if err != nil {
		adminSecurityFailure(w, err)
		return
	}
	writeJSON(w, 200, state)
}
func (s *Server) beginAdminEnrollment(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if !decodeCreation(w, r, &req) {
		return
	}
	a := identity(r)
	state, err := s.queries.AdminSecurity(a.user.ID)
	if err != nil {
		adminSecurityFailure(w, err)
		return
	}
	origin, _ := url.Parse(s.requestOrigin(r))
	host := origin.Host
	if len(host) > 255 {
		host = host[:255]
	}
	key, err := totp.Generate(totp.GenerateOpts{Issuer: "psst.zip", AccountName: a.user.Username + "@" + host, Period: 30, SecretSize: 20, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1})
	if err != nil {
		adminSecurityFailure(w, err)
		return
	}
	expires, err := s.queries.BeginAdminEnrollment(a.user.ID, a.session.ID, a.user.PasswordHash, state.Revision, key.Secret(), time.Now())
	if err != nil {
		adminSecurityFailure(w, err)
		return
	}
	writeJSON(w, 201, map[string]any{"secret": key.Secret(), "otpauth_url": key.URL(), "expires_at": expires})
}
func (s *Server) cancelAdminEnrollment(w http.ResponseWriter, r *http.Request) {
	var req struct{}
	if !decodeCreation(w, r, &req) {
		return
	}
	a := identity(r)
	if err := s.queries.CancelAdminEnrollment(a.user.ID, a.session.ID); err != nil {
		adminSecurityFailure(w, err)
		return
	}
	w.WriteHeader(204)
}
func (s *Server) confirmAdminEnrollment(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
	}
	if !decodeCreation(w, r, &req) {
		return
	}
	a := identity(r)
	state, err := s.queries.AdminSecurity(a.user.ID)
	if err != nil {
		adminSecurityFailure(w, err)
		return
	}
	codes, err := s.queries.ConfirmAdminEnrollment(a.user.ID, a.session.ID, a.user.PasswordHash, state.Revision, req.Code, time.Now())
	if err != nil {
		adminSecurityFailure(w, err)
		return
	}
	s.clearCookie(w)
	writeJSON(w, 200, map[string]any{"recovery_codes": codes, "reauthentication_required": true})
}
func (s *Server) reauthenticateAdmin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Password     string `json:"password"`
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
	a := identity(r)
	state, err := s.queries.AdminSecurity(a.user.ID)
	if err != nil {
		adminSecurityFailure(w, err)
		return
	}
	// Reuse the account and global expensive-password gates used by login.
	account := strings.ToLower(strings.TrimSpace(a.user.Username))
	if !s.loginAccounts.allow(string(tokenHash(account))) {
		w.Header().Set("Retry-After", "30")
		writeError(w, 429, "too many sign-in attempts; try again later")
		return
	}
	if bcrypt.CompareHashAndPassword(a.user.PasswordHash, []byte(req.Password)) != nil {
		err = s.queries.RecordAdminAuthenticationFailure(a.user.ID, a.user.PasswordHash, state.Revision, time.Now())
		if err != nil && !errors.Is(err, database.ErrAdminFactorInvalid) {
			adminSecurityFailure(w, err)
			return
		}
		policyError(w, 401, "administrator_authentication_invalid", "The administrator credentials are invalid.")
		return
	}
	until, err := s.queries.ReauthenticateAdmin(a.user.ID, a.session.ID, a.user.PasswordHash, state.Revision, req.Code, req.RecoveryCode, time.Now())
	if err != nil {
		adminSecurityFailure(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"recent_until": until})
}
func (s *Server) changeAdminFactor(w http.ResponseWriter, r *http.Request, disable bool) {
	var req struct{}
	if !decodeCreation(w, r, &req) {
		return
	}
	a := identity(r)
	state, err := s.queries.AdminSecurity(a.user.ID)
	if err != nil {
		adminSecurityFailure(w, err)
		return
	}
	codes, err := s.queries.ChangeAdminFactor(a.user.ID, a.session.ID, a.user.PasswordHash, state.Revision, disable, time.Now())
	if err != nil {
		adminSecurityFailure(w, err)
		return
	}
	s.clearCookie(w)
	if disable {
		w.WriteHeader(204)
	} else {
		writeJSON(w, 200, map[string]any{"recovery_codes": codes, "reauthentication_required": true})
	}
}
func (s *Server) disableAdminFactor(w http.ResponseWriter, r *http.Request) {
	s.changeAdminFactor(w, r, true)
}
func (s *Server) regenerateAdminRecovery(w http.ResponseWriter, r *http.Request) {
	s.changeAdminFactor(w, r, false)
}

func adminActor(r *http.Request) *database.AdminActor {
	a := identity(r)
	if a == nil || a.user.Role != "admin" {
		return nil
	}
	return &database.AdminActor{UserID: a.user.ID, SessionID: a.session.ID}
}
