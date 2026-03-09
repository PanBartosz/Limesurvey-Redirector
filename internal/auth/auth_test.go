package auth

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheckAdminCredentials(t *testing.T) {
	manager := New("Admin", "super-secret", "01234567890123456789012345678901", false)
	if !manager.CheckAdminCredentials("admin", "super-secret") {
		t.Fatal("expected admin credentials to match case-insensitively")
	}
	if manager.CheckAdminCredentials("admin", "wrong") {
		t.Fatal("expected wrong password to fail")
	}
}

func TestSessionRoundTripAndTamperDetection(t *testing.T) {
	manager := New("admin", "super-secret", "01234567890123456789012345678901", true)
	rr := httptest.NewRecorder()
	manager.SetSession(rr, Session{Username: "routeuser", Role: RoleUser, SessionVersion: 4, ExpiresAt: time.Now().Add(time.Hour)})
	resp := rr.Result()
	cookies := resp.Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected 1 cookie, got %d", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly {
		t.Fatal("session cookie should be HttpOnly")
	}
	if !cookie.Secure {
		t.Fatal("session cookie should be Secure when configured")
	}
	if cookie.SameSite != 3 { // http.SameSiteStrictMode
		t.Fatalf("unexpected SameSite mode: %v", cookie.SameSite)
	}

	req := httptest.NewRequest("GET", "/", nil)
	req.AddCookie(cookie)
	session, ok := manager.CurrentSession(req)
	if !ok {
		t.Fatal("expected session to validate")
	}
	if session.Username != "routeuser" || session.Role != RoleUser || session.SessionVersion != 4 {
		t.Fatalf("unexpected session payload: %+v", session)
	}

	tampered := *cookie
	tampered.Value += "x"
	req = httptest.NewRequest("GET", "/", nil)
	req.AddCookie(&tampered)
	if _, ok := manager.CurrentSession(req); ok {
		t.Fatal("expected tampered session to fail validation")
	}
}

func TestPasswordHashRoundTrip(t *testing.T) {
	hash, err := HashPassword("long-enough-password")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if !CheckPasswordHash(hash, "long-enough-password") {
		t.Fatal("expected password hash to validate")
	}
	if CheckPasswordHash(hash, "wrong-password") {
		t.Fatal("expected wrong password to fail")
	}
}
