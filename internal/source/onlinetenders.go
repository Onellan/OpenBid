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
	"strconv"
	"strings"
	"time"
)

const (
	onlinetendersDefaultUserAgent = "OpenBid OnlineTenders importer/1.0"

	DefaultOnlineTendersPageURL = "https://www.onlinetenders.co.za/tenders/south-africa?tcs=civil%23engineering%20consultants"
)

var (
	onlinetendersCountPattern      = regexp.MustCompile(`(?is)Showing\s+(\d+)\s+to\s+(\d+)\s+of\s+(\d+)\s+Tenders`)
	onlinetendersPageLinkPattern   = regexp.MustCompile(`(?i)[?&]page=(\d+)`)
	onlinetendersTenderStart       = regexp.MustCompile(`(?is)<div class="tender"[^>]*data-tid='([^']+)'[^>]*data-closed='([^']*)'[^>]*>`)
	onlinetendersContractPattern   = regexp.MustCompile(`(?is)<div class="tender-cn[^"]*"[^>]*>.*?<a[^>]*>(.*?)</a>`)
	onlinetendersDescPattern       = regexp.MustCompile(`(?is)<div class="tender-desc">(.*?)</div>\s*<div class="tender-si">`)
	onlinetendersSitePattern       = regexp.MustCompile(`(?is)<div class="tender-si">(.*?)</div>\s*<div class="tender-cd">`)
	onlinetendersClosingPattern    = regexp.MustCompile(`(?is)<div class="tender-cd">(.*?)</div>\s*<div class="tender-attb`)
	onlinetendersShowMorePattern   = regexp.MustCompile(`(?is)<a[^>]*class="show-more-tender-details"[^>]*>.*?</a>`)
	onlinetendersTagPattern        = regexp.MustCompile(`(?is)<br\s*/?>`)
	onlinetendersHTMLTagPattern    = regexp.MustCompile(`(?is)<[^>]+>`)
	onlinetendersWhitespacePattern = regexp.MustCompile(`\s+`)
)

type OnlineTendersAdapter struct {
	SourceKey string
	PageURL   string
	Client    *http.Client
	MaxPages  int
}

func NewOnlineTendersAdapter(sourceKey, pageURL string) *OnlineTendersAdapter {
	return &OnlineTendersAdapter{
		SourceKey: NormalizeKey(sourceKey),
		PageURL:   strings.TrimSpace(pageURL),
		Client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *OnlineTendersAdapter) Key() string {
	if a.SourceKey == "" {
		return "onlinetenders"
	}
	return a.SourceKey
}

func (a *OnlineTendersAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	if strings.TrimSpace(a.PageURL) == "" {
		return nil, "", fmt.Errorf("onlinetenders page url is required")
	}
	pageURL, err := url.Parse(a.PageURL)
	if err != nil {
		return nil, "", err
	}

	firstHTML, err := a.fetchPage(ctx, pageURL, 1)
	if err != nil {
		return nil, "", err
	}
	items, totalPages := a.parsePage(pageURL, 1, firstHTML)
	if totalPages <= 0 {
		totalPages = 1
	}
	if a.MaxPages > 0 && totalPages > a.MaxPages {
		totalPages = a.MaxPages
	}
	for page := 2; page <= totalPages; page++ {
		body, err := a.fetchPage(ctx, pageURL, page)
		if err != nil {
			return nil, "", err
		}
		pageItems, _ := a.parsePage(pageURL, page, body)
		if len(pageItems) == 0 {
			break
		}
		items = append(items, pageItems...)
	}
	return items, fmt.Sprintf("loaded %d OnlineTenders listings", len(items)), nil
}

func (a *OnlineTendersAdapter) fetchPage(ctx context.Context, pageURL *url.URL, page int) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, onlinetendersPageURL(pageURL, page, "").String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", onlinetendersDefaultUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := a.Client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("onlinetenders returned %d", resp.StatusCode)
	}
	payload, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (a *OnlineTendersAdapter) parsePage(pageURL *url.URL, page int, body string) ([]models.Tender, int) {
	matches := onlinetendersTenderStart.FindAllStringSubmatchIndex(body, -1)
	items := make([]models.Tender, 0, len(matches))
	category := onlinetendersCategory(pageURL)
	for i, match := range matches {
		start := match[0]
		end := len(body)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		block := body[start:end]
		tenderID := body[match[2]:match[3]]
		closedFlag := body[match[4]:match[5]]
		contractNumber := onlinetendersText(onlinetendersExtract(block, onlinetendersContractPattern), false)
		description := onlinetendersDescription(block)
		closingRaw := onlinetendersExtract(block, onlinetendersClosingPattern)
		siteInspection := onlinetendersSiteInspection(block)
		title := onlinetendersTitle(contractNumber, description)
		status := onlinetendersStatus(closedFlag, closingRaw)
		closingSortable := onlinetendersSortableDateTime(closingRaw)
		pageFacts := map[string]string{
			"closing_details": closingRaw,
			"detail_access":   "subscription_required",
		}
		if siteInspection != "" {
			pageFacts["site_inspection"] = siteInspection
		}
		sourceMetadata := map[string]string{
			"category_filter": category,
			"detail_access":   "subscription_required",
			"page_number":     strconv.Itoa(page),
			"public_listing":  "true",
		}
		relevanceInput := strings.Join([]string{title, description, category}, " ")
		items = append(items, NormalizeTenderIdentity(models.Tender{
			SourceKey:           a.Key(),
			ExternalID:          strings.TrimSpace(tenderID),
			Title:               title,
			Issuer:              "OnlineTenders",
			Category:            category,
			TenderNumber:        contractNumber,
			ClosingDate:         closingSortable,
			Status:              status,
			Scope:               description,
			Summary:             description,
			OriginalURL:         onlinetendersPageURL(pageURL, page, tenderID).String(),
			EngineeringRelevant: score(relevanceInput) > 0.5,
			RelevanceScore:      score(relevanceInput),
			PageFacts:           pageFacts,
			ExtractedFacts:      cloneFacts(pageFacts),
			SourceMetadata:      sourceMetadata,
		}))
	}
	return items, onlinetendersTotalPages(body, len(items))
}

func onlinetendersCategory(pageURL *url.URL) string {
	if pageURL == nil {
		return "Online tenders"
	}
	filter := strings.TrimSpace(pageURL.Query().Get("tcs"))
	if filter == "" {
		filter = strings.Trim(strings.TrimSpace(pageURL.Path), "/")
	}
	filter = strings.ReplaceAll(filter, "#", " / ")
	filter = strings.ReplaceAll(filter, "-", " ")
	filter = onlinetendersWhitespacePattern.ReplaceAllString(strings.TrimSpace(filter), " ")
	if filter == "" {
		return "Online tenders"
	}
	return filter
}

func onlinetendersTotalPages(body string, pageItems int) int {
	if match := onlinetendersCountPattern.FindStringSubmatch(body); len(match) == 4 {
		from, _ := strconv.Atoi(match[1])
		to, _ := strconv.Atoi(match[2])
		total, _ := strconv.Atoi(match[3])
		pageSize := to - from + 1
		if pageSize <= 0 {
			pageSize = pageItems
		}
		if total > 0 && pageSize > 0 {
			return (total + pageSize - 1) / pageSize
		}
	}
	maxPage := 1
	for _, match := range onlinetendersPageLinkPattern.FindAllStringSubmatch(body, -1) {
		if len(match) < 2 {
			continue
		}
		page, err := strconv.Atoi(match[1])
		if err == nil && page > maxPage {
			maxPage = page
		}
	}
	return maxPage
}

func onlinetendersDescription(block string) string {
	raw := onlinetendersExtract(block, onlinetendersDescPattern)
	raw = onlinetendersShowMorePattern.ReplaceAllString(raw, "")
	return onlinetendersText(raw, true)
}

func onlinetendersSiteInspection(block string) string {
	value := onlinetendersText(onlinetendersExtract(block, onlinetendersSitePattern), false)
	lower := strings.ToLower(value)
	if value == "" || strings.Contains(lower, "subscribe now") || strings.Contains(lower, "to view details") {
		return ""
	}
	return value
}

func onlinetendersTitle(contractNumber, description string) string {
	for _, line := range strings.Split(description, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) > 140 {
			line = strings.TrimSpace(line[:140])
		}
		return line
	}
	return strings.TrimSpace(contractNumber)
}

func onlinetendersStatus(closedFlag, closingRaw string) string {
	if strings.TrimSpace(closedFlag) == "1" {
		return "closed"
	}
	if tenderstate.IsExpired(models.Tender{ClosingDate: onlinetendersSortableDateTime(closingRaw)}, time.Now().UTC()) {
		return "closed"
	}
	return "open"
}

func onlinetendersSortableDateTime(raw string) string {
	value := strings.TrimSpace(onlinetendersText(raw, false))
	if value == "" {
		return ""
	}
	parts := strings.Fields(value)
	if len(parts) == 0 {
		return ""
	}
	date := parts[0]
	if len(parts) == 1 {
		return date
	}
	timePart := strings.ToUpper(parts[1])
	timePart = strings.ReplaceAll(timePart, "H", ":")
	if len(strings.Split(timePart, ":")) == 2 {
		return date + " " + timePart
	}
	return date
}

func onlinetendersExtract(block string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(block)
	if len(match) < 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func onlinetendersText(raw string, preserveBreaks bool) string {
	if preserveBreaks {
		raw = onlinetendersTagPattern.ReplaceAllString(raw, "\n")
	} else {
		raw = onlinetendersTagPattern.ReplaceAllString(raw, " ")
	}
	raw = onlinetendersHTMLTagPattern.ReplaceAllString(raw, " ")
	raw = html.UnescapeString(raw)
	if preserveBreaks {
		lines := []string{}
		for _, line := range strings.Split(raw, "\n") {
			line = onlinetendersWhitespacePattern.ReplaceAllString(strings.TrimSpace(line), " ")
			if line != "" {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}
	return onlinetendersWhitespacePattern.ReplaceAllString(strings.TrimSpace(raw), " ")
}

func onlinetendersPageURL(pageURL *url.URL, page int, tenderID string) *url.URL {
	cloned := *pageURL
	query := cloned.Query()
	if page > 0 {
		query.Set("page", strconv.Itoa(page))
	}
	cloned.RawQuery = query.Encode()
	if strings.TrimSpace(tenderID) != "" {
		cloned.Fragment = "tender-" + strings.TrimSpace(tenderID)
	}
	return &cloned
}
