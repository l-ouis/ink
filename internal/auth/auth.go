// Package auth provides single-owner session auth backed by HMAC-signed
// cookies, plus a deterministic CSRF token derived from the session. No
// database or server-side session store is needed.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookie = "ink_session"
	sessionTTL    = 30 * 24 * time.Hour
)

// Manager issues and validates sessions and CSRF tokens.
type Manager struct {
	secret []byte
	secure bool
	limit  *limiter
}

// New returns a Manager. secure controls the cookie Secure flag (enable behind
// HTTPS in production).
func New(secret []byte, secure bool) *Manager {
	return &Manager{secret: secret, secure: secure, limit: newLimiter(5, 5*time.Minute)}
}

func (m *Manager) b64() *base64.Encoding { return base64.RawURLEncoding }

func (m *Manager) sign(payload []byte) []byte {
	mac := hmac.New(sha256.New, m.secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

// token builds a "<payload>.<mac>" value where payload encodes the expiry.
func (m *Manager) token(exp time.Time) string {
	payload := make([]byte, 8)
	binary.BigEndian.PutUint64(payload, uint64(exp.Unix()))
	mac := m.sign(payload)
	return m.b64().EncodeToString(payload) + "." + m.b64().EncodeToString(mac)
}

func (m *Manager) valid(tok string) bool {
	dot := -1
	for i := 0; i < len(tok); i++ {
		if tok[i] == '.' {
			dot = i
			break
		}
	}
	if dot < 0 {
		return false
	}
	payload, err := m.b64().DecodeString(tok[:dot])
	if err != nil || len(payload) != 8 {
		return false
	}
	mac, err := m.b64().DecodeString(tok[dot+1:])
	if err != nil {
		return false
	}
	if !hmac.Equal(mac, m.sign(payload)) {
		return false
	}
	exp := int64(binary.BigEndian.Uint64(payload))
	return time.Now().Unix() < exp
}

// Issue sets a fresh session cookie on w.
func (m *Manager) Issue(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    m.token(time.Now().Add(sessionTTL)),
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})
}

// Clear removes the session cookie.
func (m *Manager) Clear(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   m.secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

// Authed reports whether r carries a valid session.
func (m *Manager) Authed(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return m.valid(c.Value)
}

// Require wraps next so it only runs for authenticated requests; otherwise it
// redirects to the login page.
func (m *Manager) Require(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !m.Authed(r) {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}

// CSRFToken returns a token bound to the current session cookie. It is stable
// for the life of the session and unforgeable without the server secret.
func (m *Manager) CSRFToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	mac := hmac.New(sha256.New, m.secret)
	mac.Write([]byte("csrf|"))
	mac.Write([]byte(c.Value))
	return m.b64().EncodeToString(mac.Sum(nil))
}

// CheckCSRF validates the "csrf" form field against the session.
func (m *Manager) CheckCSRF(r *http.Request) bool {
	want := m.CSRFToken(r)
	if want == "" {
		return false
	}
	got := r.FormValue("csrf")
	return hmac.Equal([]byte(got), []byte(want))
}

// Throttle reports whether a login attempt is currently allowed.
func (m *Manager) Throttle() bool { return m.limit.allow() }

// Fail records a failed login attempt for throttling.
func (m *Manager) Fail() { m.limit.fail() }

// Reset clears the failure counter after a successful login.
func (m *Manager) Reset() { m.limit.reset() }

// limiter is a tiny global failure counter. Single-owner means we don't need
// per-IP buckets; we just slow down brute force after repeated failures.
type limiter struct {
	mu       sync.Mutex
	max      int
	window   time.Duration
	fails    int
	blockEnd time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window}
}

func (l *limiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return time.Now().After(l.blockEnd)
}

func (l *limiter) fail() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails++
	if l.fails >= l.max {
		l.blockEnd = time.Now().Add(l.window)
		l.fails = 0
	}
}

func (l *limiter) reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.fails = 0
	l.blockEnd = time.Time{}
}
