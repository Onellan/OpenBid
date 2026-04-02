package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"tenderhub-za/internal/models"
	"time"
)

func sign(secret, payload string) string {
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func EncodeSession(secret string, s models.Session) (string, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	p := base64.RawURLEncoding.EncodeToString(b)
	return p + "." + sign(secret, p), nil
}
func DecodeSession(secret, raw string) (models.Session, bool) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || sign(secret, parts[0]) != parts[1] {
		return models.Session{}, false
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return models.Session{}, false
	}
	var s models.Session
	if json.Unmarshal(b, &s) != nil || time.Now().After(s.Expires) {
		return models.Session{}, false
	}
	return s, true
}
func SetSessionCookie(w http.ResponseWriter, secret string, s models.Session, secure bool) error {
	raw, err := EncodeSession(secret, s)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: "th_session", Value: raw, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, Expires: s.Expires})
	return nil
}
func ClearSessionCookie(w http.ResponseWriter, secure bool) {
	http.SetCookie(w, &http.Cookie{Name: "th_session", Value: "", Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
}
