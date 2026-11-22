package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	SessionCookieName = "gwf_session"
	CSRFCookieName    = "gwf_csrf"
	CSRFHeaderName    = "X-CSRF-Token"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUnauthenticated    = errors.New("authentication required")
	ErrInvalidCSRF        = errors.New("invalid or missing CSRF token")
)

// SessionPrincipal represents an authenticated control-plane user session.
type SessionPrincipal struct {
	SessionID   string
	UserID      string
	Email       string
	DisplayName string
	CSRFToken   string
	ExpiresAt   time.Time
}

// SessionManager issues, validates, and revokes PostgreSQL-backed control-plane sessions.
type SessionManager struct {
	pool          *pgxpool.Pool
	hasher        PasswordHasher
	ttl           time.Duration
	secureCookies bool
	now           func() time.Time
}

// NewSessionManager constructs a SessionManager with safe defaults.
func NewSessionManager(pool *pgxpool.Pool, hasher PasswordHasher, ttl time.Duration, secureCookies bool) *SessionManager {
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	return &SessionManager{
		pool:          pool,
		hasher:        hasher,
		ttl:           ttl,
		secureCookies: secureCookies,
		now:           func() time.Time { return time.Now().UTC() },
	}
}

// SetClock overrides the wall clock for deterministic expiry testing.
func (m *SessionManager) SetClock(fn func() time.Time) {
	if fn != nil {
		m.now = fn
	}
}

// Login verifies email/password and creates a new server-side session returning the opaque token and principal.
func (m *SessionManager) Login(ctx context.Context, email, password string) (string, SessionPrincipal, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" || password == "" {
		return "", SessionPrincipal{}, ErrInvalidCredentials
	}

	var userID, displayName, pwHash string
	var active bool
	err := m.pool.QueryRow(ctx, `
		SELECT id, display_name, password_hash, is_active
		FROM users
		WHERE email = $1
	`, normalized).Scan(&userID, &displayName, &pwHash, &active)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", SessionPrincipal{}, ErrInvalidCredentials
	}
	if err != nil {
		return "", SessionPrincipal{}, fmt.Errorf("lookup user: %w", err)
	}
	if !active {
		return "", SessionPrincipal{}, ErrInvalidCredentials
	}

	ok, err := m.hasher.Verify(password, pwHash)
	if err != nil || !ok {
		return "", SessionPrincipal{}, ErrInvalidCredentials
	}

	rawToken, err := randomHex(32)
	if err != nil {
		return "", SessionPrincipal{}, fmt.Errorf("generate session token: %w", err)
	}
	csrfToken, err := randomHex(32)
	if err != nil {
		return "", SessionPrincipal{}, fmt.Errorf("generate csrf token: %w", err)
	}

	now := m.now()
	expiresAt := now.Add(m.ttl)
	tokenHash := sha256Hex(rawToken)

	var sessionID string
	if err := m.pool.QueryRow(ctx, `
		INSERT INTO sessions (user_id, token_hash, csrf_token, expires_at, created_at, last_seen_at)
		VALUES ($1, $2, $3, $4, $5, $5)
		RETURNING id
	`, userID, tokenHash, csrfToken, expiresAt, now).Scan(&sessionID); err != nil {
		return "", SessionPrincipal{}, fmt.Errorf("create session: %w", err)
	}

	return rawToken, SessionPrincipal{
		SessionID:   sessionID,
		UserID:      userID,
		Email:       normalized,
		DisplayName: displayName,
		CSRFToken:   csrfToken,
		ExpiresAt:   expiresAt,
	}, nil
}

// Authenticate validates an opaque session token against PostgreSQL.
func (m *SessionManager) Authenticate(ctx context.Context, rawToken string) (SessionPrincipal, error) {
	rawToken = strings.TrimSpace(rawToken)
	if len(rawToken) < 32 {
		return SessionPrincipal{}, ErrUnauthenticated
	}
	tokenHash := sha256Hex(rawToken)
	now := m.now()

	var p SessionPrincipal
	var active bool
	var revokedAt *time.Time
	err := m.pool.QueryRow(ctx, `
		SELECT s.id, u.id, u.email, u.display_name, u.is_active, s.csrf_token, s.expires_at, s.revoked_at
		FROM sessions s
		JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1
	`, tokenHash).Scan(&p.SessionID, &p.UserID, &p.Email, &p.DisplayName, &active, &p.CSRFToken, &p.ExpiresAt, &revokedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SessionPrincipal{}, ErrUnauthenticated
	}
	if err != nil {
		return SessionPrincipal{}, fmt.Errorf("lookup session: %w", err)
	}
	if !active || revokedAt != nil || !p.ExpiresAt.After(now) {
		return SessionPrincipal{}, ErrUnauthenticated
	}
	return p, nil
}

// Revoke revokes a session server-side so subsequent requests with the token are rejected.
func (m *SessionManager) Revoke(ctx context.Context, sessionID string) error {
	now := m.now()
	if _, err := m.pool.Exec(ctx, `
		UPDATE sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL
	`, sessionID, now); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	return nil
}

// VerifyCSRF checks the X-CSRF-Token header against the session's stored CSRF token in constant time.
func VerifyCSRF(r *http.Request, p SessionPrincipal) error {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return nil
	}
	header := strings.TrimSpace(r.Header.Get(CSRFHeaderName))
	if header == "" || p.CSRFToken == "" {
		return ErrInvalidCSRF
	}
	if subtle.ConstantTimeCompare([]byte(header), []byte(p.CSRFToken)) != 1 {
		return ErrInvalidCSRF
	}
	return nil
}

// SetSessionCookies writes the HttpOnly session cookie and readable CSRF cookie.
func (m *SessionManager) SetSessionCookies(w http.ResponseWriter, rawToken string, p SessionPrincipal) {
	maxAge := int(time.Until(p.ExpiresAt).Seconds())
	if maxAge < 60 {
		maxAge = int(m.ttl.Seconds())
	}
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    rawToken,
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    p.CSRFToken,
		Path:     "/",
		HttpOnly: false,
		Secure:   m.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   maxAge,
	})
}

// ClearSessionCookies expires both session and CSRF cookies on the client.
func (m *SessionManager) ClearSessionCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	http.SetCookie(w, &http.Cookie{
		Name:     CSRFCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: false,
		Secure:   m.secureCookies,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func randomHex(numBytes int) (string, error) {
	buf := make([]byte, numBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func sha256Hex(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
