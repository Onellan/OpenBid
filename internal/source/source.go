package source

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"openbid/internal/models"
	"openbid/internal/netguard"
	"sort"
	"strings"
)

const (
	TypeJSONFeed       = "json_feed"
	TypeETendersPortal = "etenders_portal"
	TypePublicWorks    = "publicworks_portal"
	TypeCIDBPortal     = "cidb_portal"
	TypeEskomPortal    = "eskom_portal"
	TypeOnlineTenders  = "onlinetenders_portal"
	TypeDurbanPortal   = "durban_procurement_portal"
	TypeTransnetPortal = "transnet_portal"
	TypeWebPagePortal  = "webpage_portal"

	DefaultEskomPageURL = "https://tenderbulletin.eskom.co.za/?pageSize=5&pageNumber=1"
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

func NormalizeTenderIdentity(t models.Tender) models.Tender {
	t.SourceKey = NormalizeKey(t.SourceKey)
	t.ExternalID = strings.TrimSpace(t.ExternalID)
	t.TenderNumber = strings.TrimSpace(t.TenderNumber)
	t.DocumentURL = strings.TrimSpace(t.DocumentURL)
	t.OriginalURL = strings.TrimSpace(t.OriginalURL)
	t.Title = strings.TrimSpace(t.Title)
	t.Issuer = strings.TrimSpace(t.Issuer)
	t.ClosingDate = strings.TrimSpace(t.ClosingDate)
	if strings.TrimSpace(t.ID) != "" {
		return t
	}
	sourceKey := t.SourceKey
	if sourceKey == "" {
		sourceKey = "source"
	}
	candidates := []string{}
	if t.ExternalID != "" {
		candidates = append(candidates, "external:"+t.ExternalID)
	}
	if t.DocumentURL != "" {
		candidates = append(candidates, "document:"+t.DocumentURL)
	}
	if t.OriginalURL != "" && t.TenderNumber != "" {
		candidates = append(candidates, "listing:"+t.OriginalURL+"|"+t.TenderNumber)
	}
	if t.Title != "" {
		candidates = append(candidates, "title:"+t.Title+"|"+t.Issuer+"|"+t.ClosingDate)
	}
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		sum := sha1.Sum([]byte(candidate))
		t.ID = sourceKey + "-" + hex.EncodeToString(sum[:8])
		break
	}
	return t
}

func DefaultConfigs(feedURL string) []models.SourceConfig {
	return []models.SourceConfig{
		defaultSourceConfig("treasury", "National Treasury", TypeJSONFeed, strings.TrimSpace(feedURL)),
		defaultSourceConfig("etenders", "eTenders Portal", TypeETendersPortal, "https://www.etenders.gov.za/"),
		defaultSourceConfig("eskom", "Eskom Tender Bulletin", TypeEskomPortal, DefaultEskomPageURL),
		defaultSourceConfig("transnet", "Transnet e-Tenders", TypeTransnetPortal, "https://transnetetenders.azurewebsites.net/Home/AdvertisedTenders"),
		defaultSourceConfig("transnet-esupplier", "Transnet eSupplier Portal", TypeTransnetPortal, "https://esupplierportal.transnet.net/portal/advertisedTenders"),
		defaultSourceConfig("onlinetenders", "OnlineTenders South Africa", TypeOnlineTenders, DefaultOnlineTendersPageURL),
		defaultSourceConfig("durban", "eThekwini Municipality Procurement", TypeDurbanPortal, DefaultDurbanProcurementURL),
		defaultSourceConfig("jhb-property-rfqs", "Joburg Property Company RFQs", TypeWebPagePortal, "https://jhbproperty.co.za/supply-chain-management-scm/rfqs/"),
		defaultSourceConfig("rand-water", "Rand Water Available Tenders", TypeWebPagePortal, "https://www.randwater.co.za/availabletenders.php"),
		defaultSourceConfig("hda", "Housing Development Agency Tenders", TypeWebPagePortal, "https://thehda.co.za/index.php/tenders"),
		defaultSourceConfig("city-of-joburg", "City of Johannesburg Tenders", TypeCityOfJoburgPortal, DefaultCityOfJoburgPageURL),
		defaultSourceConfig("tshipi", "Tshipi Tenders", TypeWebPagePortal, "https://www.tshipi.co.za/opportunities/tenders"),
		defaultSourceConfig("ecdc", "Eastern Cape Development Corporation Open Tenders", TypeWebPagePortal, "https://www.ecdc.co.za/open-tenders"),
		defaultSourceConfig("jda", "Johannesburg Development Agency Current Tenders", TypeWebPagePortal, "https://www.jda.org.za/procurement/current-tenders/"),
		defaultSourceConfig("gtac", "GTAC Advertised Tenders", TypeWebPagePortal, "https://www.gtac.gov.za/tenders/advertised-tenders/"),
		defaultSourceConfig("freeport-saldanha", "Freeport Saldanha Tenders", TypeWebPagePortal, "https://freeportsaldanha.com/tenders/how-to-tender/"),
		defaultSourceConfig("dbsa", "Development Bank of Southern Africa Procurement", TypeWebPagePortal, "https://www.dbsa.org/procurement"),
		defaultSourceConfig("ppp-kenya", "PPP Kenya Procurement of Goods and Services", TypeWebPagePortal, "https://pppkenya.go.ke/procurement-of-goods-services/"),
		defaultSourceConfig("csir", "CSIR Tenders", TypeWebPagePortal, "https://www.csir.co.za/work-with-us/tenders"),
	}
}

func defaultSourceConfig(key, name, sourceType, feedURL string) models.SourceConfig {
	return models.SourceConfig{
		Key:                 key,
		Name:                name,
		Type:                sourceType,
		FeedURL:             strings.TrimSpace(feedURL),
		Enabled:             true,
		ManualChecksEnabled: true,
		AutoCheckEnabled:    true,
	}
}

func IsSupportedType(sourceType string) bool {
	switch strings.TrimSpace(sourceType) {
	case "", TypeJSONFeed, TypeETendersPortal, TypePublicWorks, TypeCIDBPortal, TypeEskomPortal, TypeOnlineTenders, TypeDurbanPortal, TypeTransnetPortal, TypeCityOfJoburgPortal, TypeWebPagePortal:
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
	case TypeEskomPortal:
		return NewEskomAdapter(key, cfg.FeedURL), nil
	case TypeOnlineTenders:
		return NewOnlineTendersAdapter(key, cfg.FeedURL), nil
	case TypeDurbanPortal:
		return NewDurbanAdapter(key, cfg.FeedURL), nil
	case TypeTransnetPortal:
		return NewTransnetAdapter(key, cfg.FeedURL), nil
	case TypeCityOfJoburgPortal:
		return NewCityOfJoburgAdapter(key, cfg.FeedURL), nil
	case TypeWebPagePortal:
		return NewWebPageAdapter(key, cfg.FeedURL), nil
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
