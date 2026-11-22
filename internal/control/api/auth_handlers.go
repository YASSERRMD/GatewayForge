package api

import (
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/YASSERRMD/GatewayForge/internal/control/auth"
)

// AuthHandler serves authentication, session, and dev-only bootstrap endpoints.
type AuthHandler struct {
	Bootstrap *auth.BootstrapService
	Sessions  *auth.SessionManager
}

// MountAuthRoutes registers /auth/* endpoints on v1.
func (h *AuthHandler) MountAuthRoutes(v1 chi.Router) {
	v1.Route("/auth", func(r chi.Router) {
		r.Post("/bootstrap", h.handleBootstrap)
		r.Post("/login", h.handleLogin)
		r.Post("/logout", h.handleLogout)
		r.With(RequireAuth(h.Sessions)).Get("/me", h.handleMe)
	})
}

func (h *AuthHandler) handleMe(w http.ResponseWriter, r *http.Request) {
	principal, ok := PrincipalFromContext(r.Context())
	if !ok {
		WriteProblem(w, r, http.StatusUnauthorized, "unauthenticated", "Unauthorized", "Authentication required.")
		return
	}
	memberships, err := h.Sessions.ListUserMemberships(r.Context(), principal.UserID)
	if err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Profile Lookup Failed", "Unable to resolve organization memberships.")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":           principal.UserID,
			"email":        principal.Email,
			"display_name": principal.DisplayName,
		},
		"csrf_token":  principal.CSRFToken,
		"expires_at":  principal.ExpiresAt,
		"memberships": memberships,
	})
}

func (h *AuthHandler) handleBootstrap(w http.ResponseWriter, r *http.Request) {
	if h.Bootstrap == nil {
		WriteProblem(w, r, http.StatusForbidden, "bootstrap_disabled", "Bootstrap Disabled", "First-admin bootstrap is not enabled.")
		return
	}
	var in auth.BootstrapInput
	if !DecodeStrictJSON(w, r, 64<<10, &in) {
		return
	}
	res, err := h.Bootstrap.Bootstrap(r.Context(), in)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrBootstrapDisabled):
			WriteProblem(w, r, http.StatusForbidden, "bootstrap_disabled", "Bootstrap Disabled", err.Error())
		case errors.Is(err, auth.ErrAlreadyBootstrapped):
			WriteProblem(w, r, http.StatusConflict, "already_bootstrapped", "Already Bootstrapped", err.Error())
		case errors.Is(err, auth.ErrInvalidBootstrapInput):
			WriteProblem(w, r, http.StatusUnprocessableEntity, "validation_error", "Validation Failed", err.Error())
		default:
			WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Bootstrap Failed", "Could not complete initial admin bootstrap.")
		}
		return
	}
	WriteJSON(w, http.StatusCreated, res)
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (h *AuthHandler) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if !DecodeStrictJSON(w, r, 32<<10, &in) {
		return
	}
	rawToken, principal, err := h.Sessions.Login(r.Context(), in.Email, in.Password)
	if err != nil {
		if errors.Is(err, auth.ErrInvalidCredentials) {
			WriteProblem(w, r, http.StatusUnauthorized, "invalid_credentials", "Unauthorized", "Invalid email or password.")
			return
		}
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Login Failed", "Unable to authenticate user.")
		return
	}
	h.Sessions.SetSessionCookies(w, rawToken, principal)
	WriteJSON(w, http.StatusOK, map[string]any{
		"user": map[string]any{
			"id":           principal.UserID,
			"email":        principal.Email,
			"display_name": principal.DisplayName,
		},
		"csrf_token": principal.CSRFToken,
		"expires_at": principal.ExpiresAt,
	})
}

func (h *AuthHandler) handleLogout(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(auth.SessionCookieName)
	if err != nil || cookie.Value == "" {
		WriteProblem(w, r, http.StatusUnauthorized, "unauthenticated", "Unauthorized", "Authentication required.")
		return
	}
	principal, err := h.Sessions.Authenticate(r.Context(), cookie.Value)
	if err != nil {
		WriteProblem(w, r, http.StatusUnauthorized, "unauthenticated", "Unauthorized", "Session is invalid or expired.")
		return
	}
	if err := auth.VerifyCSRF(r, principal); err != nil {
		WriteProblem(w, r, http.StatusForbidden, "csrf_invalid", "Forbidden", "Missing or invalid X-CSRF-Token header.")
		return
	}
	if err := h.Sessions.Revoke(r.Context(), principal.SessionID); err != nil {
		WriteProblem(w, r, http.StatusInternalServerError, "internal_error", "Logout Failed", "Could not revoke session.")
		return
	}
	h.Sessions.ClearSessionCookies(w)
	WriteJSON(w, http.StatusOK, map[string]string{"status": "logged_out"})
}
