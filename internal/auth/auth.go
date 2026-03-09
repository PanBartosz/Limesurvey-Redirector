package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookieName = "lsr_session"

type Role string

const (
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

type Session struct {
	Username       string
	Role           Role
	SessionVersion int64
	ExpiresAt      time.Time
}

func (s Session) Authenticated() bool {
	return s.Username != "" && s.Role != "" && time.Now().Before(s.ExpiresAt)
}

func (s Session) IsAdmin() bool {
	return s.Authenticated() && s.Role == RoleAdmin
}

type Manager struct {
	adminUsername string
	adminPassword string
	secret        []byte
	secureCookies bool
}

func New(adminUsername, adminPassword, secret string, secureCookies bool) *Manager {
	return &Manager{
		adminUsername: adminUsername,
		adminPassword: adminPassword,
		secret:        []byte(secret),
		secureCookies: secureCookies,
	}
}

func (m *Manager) AdminUsername() string {
	return m.adminUsername
}

func (m *Manager) CheckAdminCredentials(username, password string) bool {
	return strings.EqualFold(strings.TrimSpace(username), strings.TrimSpace(m.adminUsername)) && hmac.Equal([]byte(password), []byte(m.adminPassword))
}

func (m *Manager) SetSession(w http.ResponseWriter, session Session) {
	if session.Username == "" || session.Role == "" {
		return
	}
	if session.ExpiresAt.IsZero() {
		session.ExpiresAt = time.Now().Add(24 * time.Hour)
	}
	payload := fmt.Sprintf("%s|%s|%d|%d", session.Username, session.Role, session.SessionVersion, session.ExpiresAt.Unix())
	sig := m.sign(payload)
	value := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + base64.RawURLEncoding.EncodeToString(sig)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   m.secureCookies,
		MaxAge:   86400,
	})
}

func (m *Manager) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   m.secureCookies,
		MaxAge:   -1,
	})
}

func (m *Manager) CurrentSession(r *http.Request) (Session, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return Session{}, false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return Session{}, false
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return Session{}, false
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Session{}, false
	}
	payload := string(payloadBytes)
	if !hmac.Equal(sig, m.sign(payload)) {
		return Session{}, false
	}
	items := strings.Split(payload, "|")
	if len(items) != 3 && len(items) != 4 {
		return Session{}, false
	}
	expiresIndex := 2
	sessionVersion := int64(0)
	if len(items) == 4 {
		value, err := strconv.ParseInt(items[2], 10, 64)
		if err != nil {
			return Session{}, false
		}
		sessionVersion = value
		expiresIndex = 3
	}
	expiresAtUnix, err := strconv.ParseInt(items[expiresIndex], 10, 64)
	if err != nil {
		return Session{}, false
	}
	session := Session{
		Username:       items[0],
		Role:           Role(items[1]),
		SessionVersion: sessionVersion,
		ExpiresAt:      time.Unix(expiresAtUnix, 0),
	}
	if !session.Authenticated() {
		return Session{}, false
	}
	return session, true
}

func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func CheckPasswordHash(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

func (m *Manager) sign(value string) []byte {
	h := hmac.New(sha256.New, m.secret)
	_, _ = h.Write([]byte(value))
	return h.Sum(nil)
}
