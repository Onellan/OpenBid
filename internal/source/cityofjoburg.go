package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"openbid/internal/models"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	TypeCityOfJoburgPortal       = "city_of_joburg_portal"
	DefaultCityOfJoburgPageURL   = "https://joburg.org.za/work_/Pages/Work%20in%20Joburg/Tenders%20and%20Quotations/2022%20Tenders%20and%20Quotations/2022%20TENDERS/BID%20OPENING%20REGISTERS/Invitation%20to%20Bid.aspx"
	cityOfJoburgDefaultUserAgent = "OpenBid City of Joburg importer/1.0"
)

var cityOfJoburgTenderNumberPattern = regexp.MustCompile(`(?i)\b(?:COJ[-_][A-Z0-9]+|[A-Z]{2,10}[0-9]{1,4})[-_][0-9]{2}[-_][0-9]{2}\b`)

type CityOfJoburgAdapter struct {
	SourceKey string
	PageURL   string
	Client    *http.Client
	Now       func() time.Time
}

type cityOfJoburgPage struct {
	URL      string
	Role     string
	Required bool
}

func NewCityOfJoburgAdapter(sourceKey, pageURL string) *CityOfJoburgAdapter {
	return &CityOfJoburgAdapter{
		SourceKey: NormalizeKey(sourceKey),
		PageURL:   strings.TrimSpace(pageURL),
		Client:    &http.Client{Timeout: 30 * time.Second},
		Now:       time.Now,
	}
}

func (a *CityOfJoburgAdapter) Key() string {
	if a.SourceKey == "" {
		return "city-of-joburg"
	}
	return a.SourceKey
}

func (a *CityOfJoburgAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	pageURL := strings.TrimSpace(a.PageURL)
	if pageURL == "" {
		pageURL = DefaultCityOfJoburgPageURL
	}
	baseURL, err := url.Parse(pageURL)
	if err != nil {
		return nil, "", err
	}
	now := time.Now()
	if a.Now != nil {
		now = a.Now()
	}
	pages := cityOfJoburgPages(baseURL, now)
	out := []models.Tender{}
	seen := map[string]bool{}
	fetched := 0
	for _, page := range pages {
		items, ok, err := a.fetchPage(ctx, page)
		if err != nil {
			return nil, "", err
		}
		if !ok {
			continue
		}
		fetched++
		for _, item := range items {
			item = NormalizeTenderIdentity(item)
			if item.ID == "" || seen[item.ID] {
				continue
			}
			seen[item.ID] = true
			out = append(out, item)
		}
	}
	return out, fmt.Sprintf("loaded %d City of Johannesburg listings from %d pages", len(out), fetched), nil
}

func (a *CityOfJoburgAdapter) fetchPage(ctx context.Context, page cityOfJoburgPage) ([]models.Tender, bool, error) {
	pageURL, err := url.Parse(page.URL)
	if err != nil {
		if page.Required {
			return nil, false, err
		}
		return nil, false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return nil, false, err
	}
	req.Header.Set("User-Agent", cityOfJoburgDefaultUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := a.Client.Do(req)
	if err != nil {
		if page.Required {
			return nil, false, err
		}
		return nil, false, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, false, nil
	}
	if resp.StatusCode >= 300 {
		if page.Required {
			return nil, false, fmt.Errorf("city of joburg returned %d for %s", resp.StatusCode, page.URL)
		}
		return nil, false, nil
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, false, err
	}
	parser := NewWebPageAdapter(a.Key(), page.URL)
	items := parser.parsePage(pageURL, string(body))
	for i := range items {
		items[i] = a.normalizeTender(items[i], page)
	}
	return items, true, nil
}

func (a *CityOfJoburgAdapter) normalizeTender(item models.Tender, page cityOfJoburgPage) models.Tender {
	documentName := cityOfJoburgDocumentName(item.DocumentURL)
	if documentName != "" {
		item.Title = cityOfJoburgTitle(documentName)
	}
	if tenderNumber := cityOfJoburgTenderNumber(item.Title, documentName, item.TenderNumber); tenderNumber != "" {
		item.TenderNumber = tenderNumber
		item.ExternalID = tenderNumber
		item.ID = ""
	}
	item.SourceKey = a.Key()
	item.Issuer = "City of Johannesburg"
	item.Province = "Gauteng"
	item.Category = "City of Johannesburg " + cityOfJoburgRoleLabel(page.Role)
	item.Scope = strings.TrimSpace(item.Title)
	item.Summary = strings.TrimSpace(item.Title)
	item.OriginalURL = strings.TrimSpace(page.URL)
	item.Location = models.TenderLocation{
		Town:     "Johannesburg",
		Province: "Gauteng",
	}
	if item.PageFacts == nil {
		item.PageFacts = map[string]string{}
	}
	item.PageFacts["coj_page_role"] = page.Role
	item.PageFacts["coj_page_url"] = page.URL
	item.ExtractedFacts = cloneFacts(item.PageFacts)
	if item.SourceMetadata == nil {
		item.SourceMetadata = map[string]string{}
	}
	item.SourceMetadata["coj_page_role"] = page.Role
	if page.Role == "bid_opening_register" {
		item.Status = "closed"
		for i := range item.Documents {
			item.Documents[i].Role = "bid_opening_register"
		}
	} else if strings.TrimSpace(item.Status) == "" {
		item.Status = "open"
	}
	return item
}

func cityOfJoburgPages(baseURL *url.URL, now time.Time) []cityOfJoburgPage {
	pages := []cityOfJoburgPage{}
	seen := map[string]bool{}
	add := func(rawURL, role string, required bool) {
		rawURL = strings.TrimSpace(rawURL)
		if rawURL == "" || seen[rawURL] {
			return
		}
		seen[rawURL] = true
		pages = append(pages, cityOfJoburgPage{URL: rawURL, Role: role, Required: required})
	}
	add(baseURL.String(), cityOfJoburgPageRole(baseURL), true)
	invitationURL := cityOfJoburgURL(baseURL, "/work_/Pages/Work in Joburg/Tenders and Quotations/2022 Tenders and Quotations/2022 TENDERS/BID OPENING REGISTERS/Invitation to Bid.aspx")
	add(invitationURL, "current_bid_proposals", false)
	for year := now.Year(); year >= now.Year()-1; year-- {
		add(cityOfJoburgURL(baseURL, fmt.Sprintf("/work_/Pages/%d-Tenders/Bid-Opening-Registers-%d.aspx", year, year)), "bid_opening_register", false)
		add(cityOfJoburgURL(baseURL, fmt.Sprintf("/work_/Pages/%d-Tenders-and-Quotations/Bid-Opening-Registers.aspx", year)), "bid_opening_register", false)
	}
	return pages
}

func cityOfJoburgURL(baseURL *url.URL, pathValue string) string {
	u := *baseURL
	u.Path = pathValue
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func cityOfJoburgPageRole(pageURL *url.URL) string {
	lower := strings.ToLower(pageURL.Path)
	if strings.Contains(lower, "bid-opening-register") {
		return "bid_opening_register"
	}
	return "current_bid_proposals"
}

func cityOfJoburgRoleLabel(role string) string {
	if role == "bid_opening_register" {
		return "Bid Opening Register"
	}
	return "Current Bid Proposal"
}

func cityOfJoburgDocumentName(documentURL string) string {
	if strings.TrimSpace(documentURL) == "" {
		return ""
	}
	parsed, err := url.Parse(documentURL)
	if err != nil {
		return ""
	}
	name := path.Base(parsed.Path)
	if decoded, err := url.PathUnescape(name); err == nil {
		name = decoded
	}
	if name == "." || name == "/" {
		return ""
	}
	return name
}

func cityOfJoburgTitle(documentName string) string {
	title := strings.TrimSuffix(documentName, path.Ext(documentName))
	title = strings.NewReplacer("_", " ", "-", " ").Replace(title)
	title = webpageSpacePattern.ReplaceAllString(strings.TrimSpace(title), " ")
	if title == "" {
		return "City of Johannesburg tender document"
	}
	return title
}

func cityOfJoburgTenderNumber(candidates ...string) string {
	for _, candidate := range candidates {
		candidate = strings.NewReplacer("_", "-", "%20", " ").Replace(candidate)
		if match := cityOfJoburgTenderNumberPattern.FindString(candidate); match != "" {
			return strings.ToUpper(strings.ReplaceAll(match, "_", "-"))
		}
	}
	return ""
}
