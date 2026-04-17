package store

import (
	"context"
	"strings"
	"time"

	"openbid/internal/models"
)

const emailSettingsID = "global"

func defaultEmailSettings() models.EmailSettings {
	now := time.Now().UTC()
	return models.EmailSettings{
		ID:               emailSettingsID,
		Enabled:          false,
		SMTPPort:         587,
		SMTPSecurityMode: "starttls",
		SMTPAuthRequired: true,
		TimeoutSeconds:   10,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

func normalizeEmailSettings(settings models.EmailSettings) models.EmailSettings {
	settings.ID = emailSettingsID
	settings.SMTPHost = strings.TrimSpace(settings.SMTPHost)
	settings.SMTPSecurityMode = strings.ToLower(strings.TrimSpace(settings.SMTPSecurityMode))
	if settings.SMTPSecurityMode == "" {
		settings.SMTPSecurityMode = "starttls"
	}
	settings.SMTPUsername = strings.TrimSpace(settings.SMTPUsername)
	settings.SMTPFromEmail = strings.TrimSpace(settings.SMTPFromEmail)
	settings.SMTPFromName = strings.Join(strings.Fields(strings.TrimSpace(settings.SMTPFromName)), " ")
	settings.SMTPReplyTo = strings.TrimSpace(settings.SMTPReplyTo)
	settings.TestRecipient = strings.TrimSpace(settings.TestRecipient)
	if settings.SMTPPort <= 0 {
		settings.SMTPPort = 587
	}
	if settings.TimeoutSeconds <= 0 {
		settings.TimeoutSeconds = 10
	}
	if settings.TimeoutSeconds > 120 {
		settings.TimeoutSeconds = 120
	}
	return settings
}

func (s *SQLiteStore) GetEmailSettings(ctx context.Context) (models.EmailSettings, error) {
	settings, err := sqliteGetJSON[models.EmailSettings](ctx, s.db, "email_settings", emailSettingsID)
	if err == ErrNotFound {
		return defaultEmailSettings(), nil
	}
	if err != nil {
		return models.EmailSettings{}, err
	}
	settings = normalizeEmailSettings(settings)
	if settings.CreatedAt.IsZero() {
		settings.CreatedAt = settings.UpdatedAt
	}
	return settings, nil
}

func (s *SQLiteStore) UpsertEmailSettings(ctx context.Context, settings models.EmailSettings) error {
	settings = normalizeEmailSettings(settings)
	now := time.Now().UTC()
	existing, err := s.GetEmailSettings(ctx)
	if err == nil && !existing.CreatedAt.IsZero() {
		settings.CreatedAt = existing.CreatedAt
	}
	if settings.CreatedAt.IsZero() {
		settings.CreatedAt = now
	}
	settings.UpdatedAt = now
	return sqliteUpsertJSON(ctx, s.db, "email_settings", emailSettingsID, settings)
}
