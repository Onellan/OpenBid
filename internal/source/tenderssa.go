package source

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"openbid/internal/models"
	"openbid/internal/tenderstate"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultTendersSAPageURL = "https://www.tenders-sa.org/sa-tenders/tenders"
	tendersSAUserAgent      = "OpenBid Tenders-SA importer/1.0"
)

var (
	tendersSAArticlePattern  = regexp.MustCompile(`(?is)<(?:article|li)[^>]*>(.*?)</(?:article|li)>`)
	tendersSALinkPattern     = regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']*/sa-tenders/tender/[^"']+)["'][^>]*>\s*<h[1-6][^>]*>(.*?)</h[1-6]>`)
	tendersSAIDPattern       = regexp.MustCompile(`(?is)<(?:span|div|p)[^>]*class=["'][^"']*(?:reference|tender-number|tender-id)[^"']*["'][^>]*>(.*?)</(?:span|div|p)>`)
	tendersSAIssuerPattern   = regexp.MustCompile(`(?is)<(?:span|div|p)[^>]*class=["'][^"']*(?:issuer|organisation|organization)[^"']*["'][^>]*>(.*?)</(?:span|div|p)>`)
	tendersSAProvincePattern = regexp.MustCompile(`(?is)<(?:span|div|p)[^>]*class=["'][^"']*province[^"']*["'][^>]*>(.*?)</(?:span|div|p)>`)
	tendersSACategoryPattern = regexp.MustCompile(`(?is)<(?:span|div|p)[^>]*class=["'][^"']*categor(?:y|ies)[^"']*["'][^>]*>(.*?)</(?:span|div|p)>`)
	tendersSAClosingPattern  = regexp.MustCompile(`(?is)clos(?:es|ing date)?\s*:\s*(?:<[^>]+>\s*)*([^<]{4,40})`)
)

// TendersSAAdapter imports the publicly listed tender cards. It deliberately
// does not accept a website username/password: customer passwords are for the
// upstream administration UI and are not a safe substitute for an API token.
type TendersSAAdapter struct {
	SourceKey string
	PageURL   string
	Client    *http.Client
}

func NewTendersSAAdapter(sourceKey, pageURL string) *TendersSAAdapter {
	return &TendersSAAdapter{SourceKey: NormalizeKey(sourceKey), PageURL: strings.TrimSpace(pageURL), Client: &http.Client{Timeout: 30 * time.Second}}
}

func (a *TendersSAAdapter) Key() string {
	if a.SourceKey == "" {
		return "tenders-sa"
	}
	return a.SourceKey
}

func (a *TendersSAAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	if a.PageURL == "" {
		return nil, "", fmt.Errorf("tenders-sa page url is required")
	}
	pageURL, err := url.Parse(a.PageURL)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", tendersSAUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, "Tenders SA blocks this automated request. Configure an upstream API/feed access method; do not add an admin password to OpenBid.", nil
	}
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("tenders-sa returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	items := a.parsePage(pageURL, string(body))
	return items, fmt.Sprintf("loaded %d Tenders SA listings", len(items)), nil
}

func (a *TendersSAAdapter) parsePage(pageURL *url.URL, body string) []models.Tender {
	blocks := tendersSAArticlePattern.FindAllStringSubmatch(body, -1)
	if len(blocks) == 0 {
		blocks = [][]string{{body}}
	}
	items := make([]models.Tender, 0, len(blocks))
	seen := map[string]bool{}
	for _, blockMatch := range blocks {
		block := blockMatch[1]
		link := tendersSALinkPattern.FindStringSubmatch(block)
		if len(link) < 3 {
			continue
		}
		detailURL, err := url.Parse(html.UnescapeString(link[1]))
		if err != nil {
			continue
		}
		detailURL = pageURL.ResolveReference(detailURL)
		if seen[detailURL.String()] {
			continue
		}
		seen[detailURL.String()] = true
		title := tendersSAText(link[2])
		if title == "" {
			continue
		}
		number := tendersSAField(block, tendersSAIDPattern)
		if number == "" {
			number = tendersSAExternalID(detailURL)
		}
		closing := tendersSAClosingDate(block)
		issuer := tendersSAField(block, tendersSAIssuerPattern)
		if issuer == "" {
			issuer = "Tenders SA"
		}
		category := tendersSAField(block, tendersSACategoryPattern)
		if category == "" {
			category = "Tenders SA listing"
		}
		province := tendersSAField(block, tendersSAProvincePattern)
		facts := map[string]string{"source_page": pageURL.String(), "detail_access": "public_listing"}
		items = append(items, NormalizeTenderIdentity(models.Tender{SourceKey: a.Key(), ExternalID: number, Title: title, Issuer: issuer, Province: province, Category: category, TenderNumber: number, ClosingDate: closing, Status: tendersSAStatus(closing), Scope: title, Summary: title, OriginalURL: detailURL.String(), EngineeringRelevant: score(title+" "+category) > 0.5, RelevanceScore: score(title + " " + category), PageFacts: cloneFacts(facts), ExtractedFacts: cloneFacts(facts), SourceMetadata: map[string]string{"public_listing": "true"}}))
	}
	return items
}

func tendersSAField(block string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(block)
	if len(match) < 2 {
		return ""
	}
	return tendersSAText(match[1])
}
func tendersSAText(raw string) string { return webpageText(html.UnescapeString(raw)) }
func tendersSAExternalID(detailURL *url.URL) string {
	if detailURL == nil {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(detailURL.Path, "/sa-tenders/tender/"))
}
func tendersSAClosingDate(block string) string {
	match := tendersSAClosingPattern.FindStringSubmatch(block)
	if len(match) < 2 {
		return ""
	}
	raw := match[1]
	raw = tendersSAText(raw)
	raw = strings.ReplaceAll(raw, "Sept", "Sep")
	for _, layout := range []string{"02 Jan 2006", "2 Jan 2006", "02 January 2006", "2006-01-02"} {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	return raw
}
func tendersSAStatus(closing string) string {
	if tenderstate.IsExpired(models.Tender{ClosingDate: closing}, time.Now().UTC()) {
		return "closed"
	}
	return "open"
}
