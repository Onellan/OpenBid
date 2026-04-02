package source

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"tenderhub-za/internal/models"
	"time"
)

const publicWorksDefaultRelevance = 0.6

var (
	publicWorksTenderPattern = regexp.MustCompile(`(?is)<li>\s*<span class="bid-num">.*?<a href="([^"]+)"[^>]*>([^<]+)</a>\s*</span>\s*<span class="closing-date">\[Closing:\s*(.*?)\]</span>\s*<span class="posted-date">(.*?)</span>(?:\s*<span class="status">(.*?)</span>)?\s*</li>`)
	publicWorksSpacePattern  = regexp.MustCompile(`\s+`)
	publicWorksClockPattern  = regexp.MustCompile(`(?i)\b(\d{1,2})H(\d{2})\b`)
)

type PublicWorksAdapter struct {
	SourceKey string
	PageURL   string
	Client    *http.Client
}

func NewPublicWorksAdapter(sourceKey, pageURL string) *PublicWorksAdapter {
	return &PublicWorksAdapter{
		SourceKey: NormalizeKey(sourceKey),
		PageURL:   strings.TrimSpace(pageURL),
		Client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *PublicWorksAdapter) Key() string {
	if a.SourceKey == "" {
		return "public-works"
	}
	return a.SourceKey
}

func (a *PublicWorksAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	if a.PageURL == "" {
		return nil, "", fmt.Errorf("public works page url is required")
	}
	pageURL, err := url.Parse(a.PageURL)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("public works returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	items := a.parsePage(pageURL, string(body))
	return items, fmt.Sprintf("loaded %d Public Works tenders", len(items)), nil
}

func (a *PublicWorksAdapter) parsePage(pageURL *url.URL, content string) []models.Tender {
	block := publicWorksActiveBlock(content)
	matches := publicWorksTenderPattern.FindAllStringSubmatch(block, -1)
	out := make([]models.Tender, 0, len(matches))
	for _, match := range matches {
		documentURL := publicWorksDocumentURL(pageURL, match[1])
		tenderNumber := publicWorksText(match[2])
		status := strings.ToLower(publicWorksText(match[5]))
		if status == "" {
			status = "active"
		}
		publishedDate := publicWorksDate(match[4])
		closingDate := publicWorksClosingDate(match[3])
		pageFacts := map[string]string{
			"closing_details": closingDate,
			"posted_date":     publishedDate,
			"listing_section": "active_tenders",
			"document_name":   publicWorksDocumentName(documentURL),
		}
		out = append(out, models.Tender{
			ID:                  publicWorksStableID(a.Key(), documentURL, tenderNumber),
			SourceKey:           a.Key(),
			ExternalID:          publicWorksExternalID(documentURL, tenderNumber),
			Title:               "DPWI tender " + tenderNumber,
			Issuer:              "Department of Public Works and Infrastructure",
			Category:            "Public works tender",
			TenderNumber:        tenderNumber,
			PublishedDate:       publishedDate,
			ClosingDate:         closingDate,
			Status:              status,
			Summary:             "Tender notice listed on the DPWI active tenders page. Open the linked PDF for scope, submission requirements, and supporting details.",
			OriginalURL:         strings.TrimSpace(pageURL.String()),
			DocumentURL:         documentURL,
			EngineeringRelevant: true,
			RelevanceScore:      publicWorksDefaultRelevance,
			DocumentStatus:      models.ExtractionQueued,
			ExtractedFacts:      cloneFacts(pageFacts),
			PageFacts:           pageFacts,
			SourceMetadata: map[string]string{
				"listing_section": "active_tenders",
				"listing_status":  status,
			},
			Location: models.TenderLocation{
				Province: "",
			},
			Submission: models.TenderSubmission{
				Method:            "physical",
				Instructions:      "See the linked tender bulletin PDF for submission address, briefing details, and bid collection requirements.",
				ElectronicAllowed: false,
				PhysicalAllowed:   true,
			},
			Documents: []models.TenderDocument{{
				URL:      documentURL,
				FileName: publicWorksDocumentName(documentURL),
				MIMEType: "application/pdf",
				Role:     "notice",
				Source:   "listing",
			}},
		})
	}
	return out
}

func publicWorksActiveBlock(content string) string {
	lower := strings.ToLower(content)
	start := strings.Index(lower, `id="pills-advert"`)
	if start == -1 {
		start = strings.Index(lower, "<h4>active tenders</h4>")
	}
	if start == -1 {
		return content
	}
	restLower := lower[start:]
	if end := strings.Index(restLower, `<div class="tab-pane fade"`); end > 0 {
		return content[start : start+end]
	}
	if end := strings.Index(restLower, "<h4>archived tenders</h4>"); end > 0 {
		return content[start : start+end]
	}
	return content[start:]
}

func publicWorksDocumentURL(pageURL *url.URL, href string) string {
	ref, err := url.Parse(strings.TrimSpace(href))
	if err != nil {
		return strings.TrimSpace(href)
	}
	return pageURL.ResolveReference(ref).String()
}

func publicWorksStableID(sourceKey, documentURL, tenderNumber string) string {
	key := NormalizeKey(sourceKey)
	if key == "" {
		key = "public-works"
	}
	externalID := NormalizeKey(publicWorksExternalID(documentURL, tenderNumber))
	if externalID == "" {
		externalID = "listing"
	}
	return key + "-" + externalID
}

func publicWorksExternalID(documentURL, tenderNumber string) string {
	if documentURL != "" {
		if parsed, err := url.Parse(documentURL); err == nil {
			name := strings.TrimSuffix(path.Base(parsed.Path), path.Ext(parsed.Path))
			if name != "" && name != "." && name != "/" {
				return name
			}
		}
	}
	return publicWorksText(tenderNumber)
}

func publicWorksDocumentName(documentURL string) string {
	parsed, err := url.Parse(documentURL)
	if err != nil {
		return ""
	}
	name := path.Base(parsed.Path)
	if name == "." || name == "/" {
		return ""
	}
	return name
}

func publicWorksDate(raw string) string {
	raw = publicWorksText(raw)
	for _, layout := range []string{"2 January 2006", "02 January 2006"} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	return raw
}

func publicWorksClosingDate(raw string) string {
	raw = publicWorksText(raw)
	raw = publicWorksClockPattern.ReplaceAllString(raw, "$1:$2")
	for _, layout := range []string{"2 January 2006 at 15:04", "02 January 2006 at 15:04"} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed.Format("2006-01-02 15:04")
		}
	}
	return raw
}

func publicWorksText(raw string) string {
	raw = html.UnescapeString(raw)
	raw = publicWorksSpacePattern.ReplaceAllString(raw, " ")
	return strings.TrimSpace(raw)
}
