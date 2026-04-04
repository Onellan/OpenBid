package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"openbid/internal/auth"
	"openbid/internal/models"
)

func newSessionID() string {
	return auth.RandomString(48)
}

func (a *App) saveSession(ctx context.Context, w http.ResponseWriter, session models.Session) (models.Session, error) {
	now := time.Now().UTC()
	if session.ID == "" {
		session.ID = newSessionID()
	}
	if session.CSRF == "" {
		session.CSRF = auth.RandomString(32)
	}
	if session.CreatedAt.IsZero() {
		session.CreatedAt = now
	}
	if session.Expires.IsZero() {
		session.Expires = now.Add(time.Duration(a.Config.SessionHours) * time.Hour)
	}
	session.UpdatedAt = now
	if err := a.Store.UpsertSession(ctx, session); err != nil {
		return models.Session{}, err
	}
	if err := auth.SetSessionCookie(w, a.Config.SecretKey, session, a.Config.SecureCookies); err != nil {
		return models.Session{}, err
	}
	return session, nil
}

func (a *App) issueSession(ctx context.Context, w http.ResponseWriter, user models.User, tenantID string) (models.Session, error) {
	return a.saveSession(ctx, w, models.Session{
		ID:             newSessionID(),
		UserID:         user.ID,
		TenantID:       tenantID,
		CSRF:           auth.RandomString(32),
		SessionVersion: user.SessionVersion,
		Expires:        time.Now().UTC().Add(time.Duration(a.Config.SessionHours) * time.Hour),
	})
}

func (a *App) revokeUserSessions(ctx context.Context, userID string) error {
	if strings.TrimSpace(userID) == "" {
		return nil
	}
	return a.Store.DeleteSessionsForUser(ctx, userID)
}

func (a *App) clearSession(w http.ResponseWriter, r *http.Request) {
	if session, ok := a.currentSession(r); ok && strings.TrimSpace(session.ID) != "" {
		_ = a.Store.DeleteSession(r.Context(), session.ID)
	}
	auth.ClearSessionCookie(w, a.Config.SecureCookies)
}
