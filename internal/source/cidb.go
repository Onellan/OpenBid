package source

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"mime"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"tenderhub-za/internal/models"
	"time"
)

var (
	cidbAnchorPattern = regexp.MustCompile(`(?is)<a[^>]+href="([^"]+)"[^>]*>(.*?)</a>`)
	cidbTagPattern    = regexp.MustCompile(`(?is)<[^>]+>`)
	cidbSpacePattern  = regexp.MustCompile(`\s+`)
)

const (
	cidbUserAgent      = "OpenBid CIDB importer/1.0"
	cidbMaxAttempts    = 3
	cidbBaseRetryDelay = time.Second
	cidbMaxRetryDelay  = 5 * time.Second
)

type CIDBAdapter struct {
	SourceKey string
	PageURL   string
	Client    *http.Client
}

type cidbResponse struct {
	TenderCount string        `json:"tender_count"`
	Tenders     []cidbListing `json:"tm_tenders"`
}

type cidbListing struct {
	TenderID                string `json:"tender_ID"`
	UserID                  string `json:"user_ID"`
	Region                  string `json:"region"`
	RegionName              string `json:"region_name"`
	Description             string `json:"description"`
	BidNumber               string `json:"bid_number"`
	AdvertFile              string `json:"tender_advert_file"`
	AdvertDateTime          string `json:"tender_advert_date_time"`
	SpecificationFile       string `json:"tender_specification_file"`
	SpecificationDateTime   string `json:"tender_specification_date_time"`
	AwardsFile              string `json:"tender_awards_file"`
	AwardsDateTime          string `json:"tender_awards_date_time"`
	BriefingFile            string `json:"tender_briefing_file"`
	Status                  string `json:"status"`
	RealStatus              string `json:"realstatus"`
	RowNum                  int    `json:"row_num"`
	TenderAdvertFileLink    string `json:"tender_advert_file_link"`
	TenderSpecificationLink string `json:"tender_specification_file_link"`
	TenderAwardsLink        string `json:"tender_awards_file_link"`
	TenderBriefingLink      string `json:"tender_briefing_file_link"`
}

func NewCIDBAdapter(sourceKey, pageURL string) *CIDBAdapter {
	return &CIDBAdapter{
		SourceKey: NormalizeKey(sourceKey),
		PageURL:   strings.TrimSpace(pageURL),
		Client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *CIDBAdapter) Key() string {
	if a.SourceKey == "" {
		return "cidb"
	}
	return a.SourceKey
}

func (a *CIDBAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	if a.PageURL == "" {
		return nil, "", fmt.Errorf("cidb page url is required")
	}
	endpoint, pageURL, err := cidbEndpointURLs(a.PageURL)
	if err != nil {
		return nil, "", err
	}
	for attempt := 1; attempt <= cidbMaxAttempts; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("Accept", "application/json, text/plain, */*")
		req.Header.Set("User-Agent", cidbUserAgent)
		req.Header.Set("Referer", pageURL.String())

		resp, err := a.Client.Do(req)
		if err != nil {
			return nil, "", err
		}
		if resp.StatusCode >= 300 {
			delay := cidbRetryDelay(resp.Header.Get("Retry-After"), attempt)
			retryable := cidbRetryableStatus(resp.StatusCode)
			resp.Body.Close()
			if retryable && attempt < cidbMaxAttempts {
				if err := cidbSleep(ctx, delay); err != nil {
					return nil, "", err
				}
				continue
			}
			return nil, "", fmt.Errorf("cidb returned %d", resp.StatusCode)
		}

		var payload cidbResponse
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			resp.Body.Close()
			return nil, "", err
		}
		resp.Body.Close()

		out := make([]models.Tender, 0, len(payload.Tenders))
		for _, item := range payload.Tenders {
			out = append(out, a.mapListing(pageURL, item))
		}
		return out, fmt.Sprintf("loaded %d CIDB tenders", len(out)), nil
	}
	return nil, "", fmt.Errorf("cidb returned 429")
}

func (a *CIDBAdapter) mapListing(pageURL *url.URL, item cidbListing) models.Tender {
	descriptionText := cidbText(item.Description)
	extraDocuments := cidbDescriptionDocuments(pageURL, item.Description)
	documents := cidbDocuments(pageURL, item, extraDocuments)
	documentURL := ""
	if len(documents) > 0 {
		documentURL = documents[0].URL
	}
	pageFacts := map[string]string{
		"listing_status":  cidbStatus(item),
		"advertised_at":   cidbDateTime(item.AdvertDateTime),
		"region_name":     strings.TrimSpace(item.RegionName),
		"extra_documents": strconv.Itoa(len(extraDocuments)),
	}
	sourceMetadata := map[string]string{
		"tender_id":                 strings.TrimSpace(item.TenderID),
		"user_id":                   strings.TrimSpace(item.UserID),
		"region":                    strings.TrimSpace(item.Region),
		"region_name":               strings.TrimSpace(item.RegionName),
		"real_status":               strings.TrimSpace(item.RealStatus),
		"advert_date_time":          strings.TrimSpace(item.AdvertDateTime),
		"specification_date_time":   strings.TrimSpace(item.SpecificationDateTime),
		"awards_date_time":          strings.TrimSpace(item.AwardsDateTime),
		"advert_file_link_markup":   strings.TrimSpace(item.TenderAdvertFileLink),
		"specification_file_markup": strings.TrimSpace(item.TenderSpecificationLink),
		"awards_file_markup":        strings.TrimSpace(item.TenderAwardsLink),
		"briefing_file_link_markup": strings.TrimSpace(item.TenderBriefingLink),
		"description_html":          strings.TrimSpace(item.Description),
	}
	if item.AdvertDateTime != "" {
		pageFacts["advertised_date"] = cidbDate(item.AdvertDateTime)
	}

	return models.Tender{
		ID:                  cidbStableID(a.Key(), item),
		SourceKey:           a.Key(),
		ExternalID:          strings.TrimSpace(item.TenderID),
		Title:               descriptionText,
		Issuer:              "Construction Industry Development Board",
		Category:            "CIDB tender",
		TenderNumber:        cidbBidNumber(item.BidNumber),
		PublishedDate:       cidbDate(item.AdvertDateTime),
		ClosingDate:         "",
		Status:              cidbStatus(item),
		Scope:               descriptionText,
		Summary:             descriptionText,
		OriginalURL:         pageURL.String(),
		DocumentURL:         documentURL,
		EngineeringRelevant: score(descriptionText) > 0.5,
		RelevanceScore:      score(descriptionText),
		DocumentStatus:      models.ExtractionQueued,
		ExtractedFacts:      cloneFacts(pageFacts),
		PageFacts:           pageFacts,
		SourceMetadata:      sourceMetadata,
		Documents:           documents,
	}
}

func cidbEndpointURLs(raw string) (*url.URL, *url.URL, error) {
	pageURL, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, nil, err
	}
	endpoint := *pageURL
	if strings.HasSuffix(strings.ToLower(endpoint.Path), ".json") {
		return &endpoint, pageURL, nil
	}
	endpoint.Path = "/tenders.json"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	return &endpoint, pageURL, nil
}

func cidbStableID(sourceKey string, item cidbListing) string {
	base := NormalizeKey(strings.TrimSpace(item.TenderID))
	if base == "" {
		base = NormalizeKey(cidbBidNumber(item.BidNumber))
	}
	if base == "" {
		base = "listing"
	}
	return NormalizeKey(sourceKey) + "-" + base
}

func cidbBidNumber(raw string) string {
	return strings.ToUpper(strings.TrimSpace(raw))
}

func cidbStatus(item cidbListing) string {
	if value := strings.TrimSpace(item.RealStatus); value != "" {
		return strings.ToLower(value)
	}
	if value := strings.TrimSpace(item.Status); value != "" {
		return strings.ToLower(value)
	}
	return "unknown"
}

func cidbDate(raw string) string {
	if parsed, ok := parseCIDBTime(raw); ok {
		return parsed.Format("2006-01-02")
	}
	return strings.TrimSpace(raw)
}

func cidbDateTime(raw string) string {
	if parsed, ok := parseCIDBTime(raw); ok {
		return parsed.Format("2006-01-02 15:04")
	}
	return strings.TrimSpace(raw)
}

func parseCIDBTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "0000-00-00") {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func cidbDescriptionDocuments(pageURL *url.URL, description string) []models.TenderDocument {
	matches := cidbAnchorPattern.FindAllStringSubmatch(description, -1)
	out := []models.TenderDocument{}
	for _, match := range matches {
		ref, err := url.Parse(strings.TrimSpace(match[1]))
		if err != nil {
			continue
		}
		link := pageURL.ResolveReference(ref).String()
		name := cidbText(match[2])
		if name == "" {
			name = path.Base(ref.Path)
		}
		out = append(out, models.TenderDocument{
			URL:      link,
			FileName: path.Base(ref.Path),
			MIMEType: cidbMIMEType(ref.Path),
			Role:     "attachment",
			Source:   "listing",
		})
		if name != "" && name != path.Base(ref.Path) {
			out[len(out)-1].Role = "attachment:" + NormalizeKey(name)
		}
	}
	return out
}

func cidbDocuments(pageURL *url.URL, item cidbListing, extra []models.TenderDocument) []models.TenderDocument {
	out := []models.TenderDocument{}
	seen := map[string]bool{}
	appendDoc := func(link, role, modified string) {
		link = strings.TrimSpace(link)
		if link == "" || seen[link] {
			return
		}
		ref, err := url.Parse(link)
		if err != nil {
			return
		}
		seen[link] = true
		out = append(out, models.TenderDocument{
			URL:          pageURL.ResolveReference(ref).String(),
			FileName:     path.Base(ref.Path),
			MIMEType:     cidbMIMEType(ref.Path),
			Role:         role,
			Source:       "listing",
			LastModified: cidbDateTime(modified),
		})
	}
	appendDoc(item.AdvertFile, "advert", item.AdvertDateTime)
	appendDoc(item.SpecificationFile, "addendum", item.SpecificationDateTime)
	appendDoc(item.AwardsFile, "opening_register", item.AwardsDateTime)
	appendDoc(item.BriefingFile, "briefing_note", "")
	for _, doc := range extra {
		if seen[doc.URL] {
			continue
		}
		seen[doc.URL] = true
		out = append(out, doc)
	}
	return out
}

func cidbText(raw string) string {
	raw = cidbAnchorPattern.ReplaceAllString(raw, "$2")
	raw = cidbTagPattern.ReplaceAllString(raw, " ")
	raw = html.UnescapeString(raw)
	raw = cidbSpacePattern.ReplaceAllString(raw, " ")
	return strings.TrimSpace(raw)
}

func cidbMIMEType(p string) string {
	if mimeType := mime.TypeByExtension(strings.ToLower(path.Ext(p))); mimeType != "" {
		return mimeType
	}
	return ""
}

func cidbRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func cidbRetryDelay(raw string, attempt int) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds >= 0 {
			return cidbClampRetryDelay(time.Duration(seconds) * time.Second)
		}
		if retryAt, err := http.ParseTime(raw); err == nil {
			return cidbClampRetryDelay(time.Until(retryAt))
		}
	}
	return cidbClampRetryDelay(time.Duration(attempt) * cidbBaseRetryDelay)
}

func cidbClampRetryDelay(delay time.Duration) time.Duration {
	if delay < 0 {
		return 0
	}
	if delay > cidbMaxRetryDelay {
		return cidbMaxRetryDelay
	}
	return delay
}

func cidbSleep(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
