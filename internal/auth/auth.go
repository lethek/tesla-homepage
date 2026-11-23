// Package auth hashes passwords and issues HMAC-signed session cookies.
package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	CookieName = "homepage_session"
	SessionTTL = 365 * 24 * time.Hour
)

var ErrInvalidSession = errors.New("invalid session")

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func CheckPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

// Session is the signed content of a session cookie.
type Session struct {
	UserID  string `json:"u"`
	Version int    `json:"v"`
	Expires int64  `json:"e"`
}

type Sessions struct {
	Secret []byte
	Now    func() time.Time
}

func (s *Sessions) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Sessions) sign(payload string) string {
	m := hmac.New(sha256.New, s.Secret)
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// Encode returns the cookie value for a new session.
func (s *Sessions) Encode(userID string, version int) string {
	b, _ := json.Marshal(Session{UserID: userID, Version: version, Expires: s.now().Add(SessionTTL).Unix()})
	payload := base64.RawURLEncoding.EncodeToString(b)
	return payload + "." + s.sign(payload)
}

// Decode verifies a cookie value and returns its session. It does not check
// the session version against the User; the caller does that.
func (s *Sessions) Decode(value string) (Session, error) {
	payload, sig, ok := strings.Cut(value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(s.sign(payload))) {
		return Session{}, ErrInvalidSession
	}
	b, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Session{}, ErrInvalidSession
	}
	var sess Session
	if err := json.Unmarshal(b, &sess); err != nil || sess.UserID == "" {
		return Session{}, ErrInvalidSession
	}
	if s.now().Unix() >= sess.Expires {
		return Session{}, ErrInvalidSession
	}
	return sess, nil
}

// SetCookie writes a session cookie for the User to w.
func (s *Sessions) SetCookie(w http.ResponseWriter, r *http.Request, userID string, version int) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    s.Encode(userID, version),
		Path:     "/",
		MaxAge:   int(SessionTTL / time.Second),
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie removes the session cookie.
func ClearCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   isHTTPS(r),
		SameSite: http.SameSiteLaxMode,
	})
}

// isHTTPS reports whether the request reached us, or a reverse proxy in
// front of us, over HTTPS.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}
