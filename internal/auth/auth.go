package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/SadNoo/gosspanel/internal/config"
)

const CookieName = "goss_session"

type Manager struct {
	cfg config.Config
}

func New(cfg config.Config) *Manager {
	return &Manager{cfg: cfg}
}

func (m *Manager) SetSession(w http.ResponseWriter, user string, version string) {
	expires := time.Now().Add(24 * time.Hour)
	payload := fmt.Sprintf("%s|%d|%s", user, expires.Unix(), version)
	sig := m.sign(payload)
	value := base64.RawURLEncoding.EncodeToString([]byte(payload + "|" + sig))
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Manager) ClearSession(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(0, 0),
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func (m *Manager) User(r *http.Request) (string, bool) {
	user, _, ok := m.Session(r)
	return user, ok
}

func (m *Manager) Session(r *http.Request) (string, string, bool) {
	cookie, err := r.Cookie(CookieName)
	if err != nil || cookie.Value == "" {
		return "", "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return "", "", false
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 4 {
		return "", "", false
	}
	payload := parts[0] + "|" + parts[1] + "|" + parts[2]
	if !hmac.Equal([]byte(parts[3]), []byte(m.sign(payload))) {
		return "", "", false
	}
	expires, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || time.Now().Unix() > expires {
		return "", "", false
	}
	return parts[0], parts[2], true
}

func (m *Manager) CheckAgentToken(r *http.Request) bool {
	token := r.Header.Get("X-Goss-Agent-Token")
	if token == "" {
		authz := r.Header.Get("Authorization")
		token = strings.TrimPrefix(authz, "Bearer ")
	}
	return token != "" && subtle.ConstantTimeCompare([]byte(token), []byte(m.cfg.AgentToken)) == 1
}

func (m *Manager) sign(payload string) string {
	mac := hmac.New(sha256.New, []byte(m.cfg.SessionSecret))
	_, _ = mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
