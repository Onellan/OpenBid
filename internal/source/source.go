package source

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"tenderhub-za/internal/models"
	"tenderhub-za/internal/netguard"
)

const (
	TypeJSONFeed       = "json_feed"
	TypeETendersPortal = "etenders_portal"
	TypePublicWorks    = "publicworks_portal"
	TypeCIDBPortal     = "cidb_portal"
)

type Adapter interface {
	Key() string
	Fetch(context.Context) ([]models.Tender, string, error)
}
type Registry struct{ Adapters []Adapter }

func NewRegistry(adapters ...Adapter) Registry { return Registry{Adapters: adapters} }

func cloneFacts(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func NormalizeKey(raw string) string {
	raw = strings.ToLower(strings.TrimSpace(raw))
	var b strings.Builder
	lastDash := false
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		case r == '-' || r == '_' || r == ' ':
			if b.Len() > 0 && !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

func DefaultConfigs(feedURL string) []models.SourceConfig {
	return []models.SourceConfig{{
		Key:                 "treasury",
		Name:                "National Treasury",
		Type:                TypeJSONFeed,
		FeedURL:             strings.TrimSpace(feedURL),
		Enabled:             true,
		ManualChecksEnabled: true,
		AutoCheckEnabled:    true,
	}}
}

func IsSupportedType(sourceType string) bool {
	switch strings.TrimSpace(sourceType) {
	case "", TypeJSONFeed, TypeETendersPortal, TypePublicWorks, TypeCIDBPortal:
		return true
	default:
		return false
	}
}

func AdapterFromConfig(cfg models.SourceConfig) (Adapter, error) {
	key := NormalizeKey(cfg.Key)
	if key == "" {
		return nil, fmt.Errorf("source key is required")
	}
	if strings.TrimSpace(cfg.FeedURL) != "" {
		if _, err := netguard.NormalizePublicHTTPURL(cfg.FeedURL); err != nil {
			return nil, fmt.Errorf("invalid feed url for %s: %w", key, err)
		}
	}
	switch cfg.Type {
	case "", TypeJSONFeed:
		return NewFeedAdapter(key, cfg.FeedURL), nil
	case TypeETendersPortal:
		return NewETendersAdapter(key, cfg.FeedURL), nil
	case TypePublicWorks:
		return NewPublicWorksAdapter(key, cfg.FeedURL), nil
	case TypeCIDBPortal:
		return NewCIDBAdapter(key, cfg.FeedURL), nil
	default:
		return nil, fmt.Errorf("unsupported source type %q", cfg.Type)
	}
}

func RegistryFromConfigs(configs []models.SourceConfig) Registry {
	sorted := append([]models.SourceConfig(nil), configs...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Name == sorted[j].Name {
			return sorted[i].Key < sorted[j].Key
		}
		return sorted[i].Name < sorted[j].Name
	})
	adapters := []Adapter{}
	seen := map[string]bool{}
	for _, cfg := range sorted {
		if !cfg.Enabled {
			continue
		}
		adapter, err := AdapterFromConfig(cfg)
		if err != nil || seen[adapter.Key()] {
			continue
		}
		seen[adapter.Key()] = true
		adapters = append(adapters, adapter)
	}
	return NewRegistry(adapters...)
}
