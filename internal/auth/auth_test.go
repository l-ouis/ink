package auth

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func issuedCookie(t *testing.T, m *Manager) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Issue(rec)
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatal("no session cookie issued")
	return nil
}

func TestSessionValidAndRejected(t *testing.T) {
	m := New([]byte("test-secret"), false)
	sc := issuedCookie(t, m)

	withCookie := httptest.NewRequest("GET", "/", nil)
	withCookie.AddCookie(sc)
	if !m.Authed(withCookie) {
		t.Error("Authed = false for a freshly issued session")
	}

	none := httptest.NewRequest("GET", "/", nil)
	if m.Authed(none) {
		t.Error("Authed = true with no cookie")
	}

	tampered := httptest.NewRequest("GET", "/", nil)
	tampered.AddCookie(&http.Cookie{Name: sessionCookie, Value: sc.Value + "x"})
	if m.Authed(tampered) {
		t.Error("Authed = true for a tampered cookie")
	}

	// A session signed with a different secret must not validate.
	other := New([]byte("different-secret"), false)
	if other.Authed(withCookie) {
		t.Error("Authed = true under a different secret")
	}
}

func TestCSRF(t *testing.T) {
	m := New([]byte("test-secret"), false)
	sc := issuedCookie(t, m)

	g := httptest.NewRequest("GET", "/", nil)
	g.AddCookie(sc)
	tok := m.CSRFToken(g)
	if tok == "" {
		t.Fatal("CSRFToken empty for a session")
	}

	post := func(token string, withSession bool) *http.Request {
		r := httptest.NewRequest("POST", "/", strings.NewReader(url.Values{"csrf": {token}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if withSession {
			r.AddCookie(sc)
		}
		return r
	}

	if !m.CheckCSRF(post(tok, true)) {
		t.Error("CheckCSRF = false for the correct token")
	}
	if m.CheckCSRF(post("wrong", true)) {
		t.Error("CheckCSRF = true for a wrong token")
	}
	if m.CheckCSRF(post(tok, false)) {
		t.Error("CheckCSRF = true without a session cookie")
	}
}

func TestThrottle(t *testing.T) {
	m := New([]byte("k"), false)
	for i := 0; i < 5; i++ {
		if !m.Throttle() {
			t.Fatalf("throttled early at attempt %d", i)
		}
		m.Fail()
	}
	if m.Throttle() {
		t.Error("should be throttled after the failure limit")
	}
	m.Reset()
	if !m.Throttle() {
		t.Error("Reset should clear the throttle")
	}
}
