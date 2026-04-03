package app

import (
	"net/mail"
	"net/url"
	"strings"

	"tenderhub-za/internal/models"
	"tenderhub-za/internal/netguard"
	"tenderhub-za/internal/source"
)

func normalizeUsername(raw string) string {
	return strings.TrimSpace(raw)
}

func normalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func normalizeTenantSlug(rawSlug, tenantName string) string {
	if slug := source.NormalizeKey(rawSlug); slug != "" {
		return slug
	}
	return source.NormalizeKey(tenantName)
}

func validEmailAddress(raw string) bool {
	parsed, err := mail.ParseAddress(strings.TrimSpace(raw))
	return err == nil && parsed.Address != ""
}

func isValidRole(role models.Role) bool {
	switch role {
	case models.RoleAdmin, models.RolePortfolioManager, models.RoleTenantAdmin, models.RoleAnalyst, models.RoleReviewer, models.RoleOperator, models.RoleViewer:
		return true
	default:
		return false
	}
}

func hasUserWithUsername(users []models.User, username string) bool {
	username = strings.TrimSpace(username)
	for _, user := range users {
		if strings.EqualFold(strings.TrimSpace(user.Username), username) {
			return true
		}
	}
	return false
}

func hasUserWithEmail(users []models.User, email string) bool {
	email = normalizeEmail(email)
	for _, user := range users {
		if normalizeEmail(user.Email) == email {
			return true
		}
	}
	return false
}

func hasTenantWithSlug(tenants []models.Tenant, slug string) bool {
	slug = normalizeTenantSlug(slug, "")
	for _, tenant := range tenants {
		if normalizeTenantSlug(tenant.Slug, "") == slug {
			return true
		}
	}
	return false
}

func normalizeSafeOutboundURL(raw string) (string, error) {
	return netguard.NormalizePublicHTTPURL(raw)
}

func validateSafeOutboundURL(raw string) error {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return err
	}
	return netguard.ValidatePublicHTTPURL(parsed)
}
