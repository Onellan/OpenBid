package source

import (
	"context"
	"fmt"
	"html"
	"io"
	"mime"
	"net/http"
	"net/url"
	"openbid/internal/models"
	"path"
	"regexp"
	"strings"
	"time"
)

const webpageDefaultUserAgent = "OpenBid webpage importer/1.0"

var (
	webpageTitlePattern         = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	webpageAnchorPattern        = regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	webpageTagPattern           = regexp.MustCompile(`(?is)<[^>]+>`)
	webpageSpacePattern         = regexp.MustCompile(`\s+`)
	webpageTenderNumberPattern  = regexp.MustCompile(`(?i)\b(?:rfp|rfq|bid|tender|quote|quotation)[\s:/#-]*[a-z0-9][a-z0-9/._-]*|\b[a-z]{1,8}/[0-9]{4}/[0-9]{2}/[0-9]{3,6}/[0-9]{3,6}/(?:rfp|rfq)\b`)
	webpageUploadDatePattern    = regexp.MustCompile(`/(20[0-9]{2})[-/]([0-9]{2})[-/]([0-9]{2})(?:/|[-_])`)
	webpageDocumentExts         = map[string]bool{".pdf": true, ".doc": true, ".docx": true, ".xls": true, ".xlsx": true, ".zip": true}
	webpageIgnoredLinkFragments = []string{"privacy", "cookie", "terms", "facebook", "twitter", "linkedin", "instagram", "youtube", "tiktok", "mailto:", "tel:", "javascript:"}
	webpageRelevantTerms        = []string{"tender", "rfq", "rfp", "bid", "quotation", "quote", "sourcing", "download"}
)

type WebPageAdapter struct {
	SourceKey string
	PageURL   string
	Client    *http.Client
}

func NewWebPageAdapter(sourceKey, pageURL string) *WebPageAdapter {
	return &WebPageAdapter{
		SourceKey: NormalizeKey(sourceKey),
		PageURL:   strings.TrimSpace(pageURL),
		Client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *WebPageAdapter) Key() string {
	if a.SourceKey == "" {
		return "webpage"
	}
	return a.SourceKey
}

func (a *WebPageAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	if a.PageURL == "" {
		return nil, "", fmt.Errorf("webpage url is required")
	}
	pageURL, err := url.Parse(a.PageURL)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", webpageDefaultUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, fmt.Sprintf("webpage source %s requires access or blocks automated reads", a.Key()), nil
	}
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("webpage returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	items := a.parsePage(pageURL, string(body))
	return items, fmt.Sprintf("loaded %d webpage listings", len(items)), nil
}

func (a *WebPageAdapter) parsePage(pageURL *url.URL, body string) []models.Tender {
	pageTitle := webpageText(webpageExtract(body, webpageTitlePattern))
	issuer := webpageIssuer(pageURL, pageTitle)
	matches := webpageAnchorPattern.FindAllStringSubmatchIndex(body, -1)
	out := []models.Tender{}
	seen := map[string]bool{}
	for _, match := range matches {
		href := body[match[2]:match[3]]
		linkText := webpageText(body[match[4]:match[5]])
		resolved, ok := webpageResolveLink(pageURL, href)
		if !ok || seen[resolved] {
			continue
		}
		contextText := webpageText(webpageAnchorContext(body, match[0], match[1]))
		if !webpageRelevantLink(resolved, linkText, contextText) {
			continue
		}
		seen[resolved] = true
		document := webpageDocumentURL(resolved)
		documentName := webpageDocumentName(resolved)
		title := webpageTenderTitle(linkText, contextText, documentName)
		tenderNumber := webpageTenderNumber(title, documentName, resolved)
		publishedDate := webpagePublishedDate(resolved)
		pageFacts := map[string]string{
			"source_page":    pageURL.String(),
			"link_text":      linkText,
			"listing_text":   webpageLimit(contextText, 600),
			"document_name":  documentName,
			"published_hint": publishedDate,
		}
		item := models.Tender{
			SourceKey:           a.Key(),
			ExternalID:          webpageExternalID(resolved, tenderNumber),
			Title:               title,
			Issuer:              issuer,
			Category:            "Web tender listing",
			TenderNumber:        tenderNumber,
			PublishedDate:       publishedDate,
			Status:              "open",
			Scope:               title,
			Summary:             title,
			OriginalURL:         resolved,
			DocumentURL:         document,
			EngineeringRelevant: score(strings.Join([]string{title, contextText}, " ")) > 0.5,
			RelevanceScore:      score(strings.Join([]string{title, contextText}, " ")),
			ExtractedFacts:      cloneFacts(pageFacts),
			PageFacts:           pageFacts,
			SourceMetadata: map[string]string{
				"page_title": pageTitle,
				"link_href":  strings.TrimSpace(href),
			},
		}
		if document != "" {
			item.DocumentStatus = models.ExtractionQueued
			item.Documents = []models.TenderDocument{{
				URL:      document,
				FileName: documentName,
				MIMEType: webpageMIMEType(documentName),
				Role:     webpageDocumentRole(documentName, title),
				Source:   "listing",
			}}
		}
		out = append(out, NormalizeTenderIdentity(item))
	}
	return out
}

func webpageResolveLink(pageURL *url.URL, href string) (string, bool) {
	href = strings.TrimSpace(html.UnescapeString(href))
	lower := strings.ToLower(href)
	if href == "" || strings.HasPrefix(href, "#") {
		return "", false
	}
	for _, ignored := range webpageIgnoredLinkFragments {
		if strings.Contains(lower, ignored) {
			return "", false
		}
	}
	ref, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	resolved := pageURL.ResolveReference(ref)
	if resolved.Scheme != "http" && resolved.Scheme != "https" {
		return "", false
	}
	if webpageSamePage(pageURL, resolved) {
		return "", false
	}
	return resolved.String(), true
}

func webpageSamePage(pageURL, resolved *url.URL) bool {
	left := *pageURL
	right := *resolved
	left.Fragment = ""
	right.Fragment = ""
	return strings.EqualFold(left.String(), right.String())
}

func webpageRelevantLink(link, text, context string) bool {
	if webpageLooksLikePagination(link, text) {
		return false
	}
	if webpageDocumentURL(link) != "" {
		return true
	}
	combined := strings.ToLower(strings.Join([]string{link, text}, " "))
	for _, term := range webpageRelevantTerms {
		if strings.Contains(combined, term) {
			return true
		}
	}
	lowerText := strings.ToLower(text)
	context = strings.ToLower(context)
	if (strings.Contains(lowerText, "view") || strings.Contains(lowerText, "detail") || strings.Contains(lowerText, "download")) && (strings.Contains(context, "rfq") || strings.Contains(context, "rfp")) {
		return true
	}
	return false
}

func webpageLooksLikePagination(link, text string) bool {
	if webpageDocumentURL(link) != "" {
		return false
	}
	parsed, err := url.Parse(link)
	if err != nil {
		return false
	}
	if parsed.Query().Get("page") == "" {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func webpageAnchorContext(body string, start, end int) string {
	for _, tag := range []string{"tr", "li", "article", "p"} {
		open := strings.LastIndex(strings.ToLower(body[:start]), "<"+tag)
		close := strings.Index(strings.ToLower(body[end:]), "</"+tag+">")
		if open >= 0 && close >= 0 {
			return body[open : end+close+len(tag)+3]
		}
	}
	left := start - 220
	if left < 0 {
		left = 0
	}
	right := end + 220
	if right > len(body) {
		right = len(body)
	}
	return body[left:right]
}

func webpageTenderTitle(linkText, contextText, documentName string) string {
	for _, candidate := range []string{contextText, linkText, strings.TrimSuffix(documentName, path.Ext(documentName))} {
		candidate = webpageLimit(candidate, 180)
		if candidate != "" {
			return candidate
		}
	}
	return "Web tender listing"
}

func webpageTenderNumber(title, documentName, link string) string {
	for _, candidate := range []string{title, documentName, link} {
		if match := webpageTenderNumberPattern.FindString(candidate); match != "" {
			return strings.ToUpper(strings.TrimSpace(match))
		}
	}
	return ""
}

func webpageExternalID(link, tenderNumber string) string {
	if strings.TrimSpace(tenderNumber) != "" {
		return strings.TrimSpace(tenderNumber)
	}
	if parsed, err := url.Parse(link); err == nil {
		name := strings.TrimSuffix(path.Base(parsed.Path), path.Ext(parsed.Path))
		if name != "" && name != "." && name != "/" {
			return name
		}
	}
	return link
}

func webpageIssuer(pageURL *url.URL, title string) string {
	title = strings.TrimSpace(title)
	if title != "" {
		parts := strings.Split(title, "-")
		if len(parts) > 1 {
			return strings.TrimSpace(parts[len(parts)-1])
		}
		return title
	}
	if pageURL != nil {
		return pageURL.Hostname()
	}
	return "Web tender source"
}

func webpageDocumentURL(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	if webpageDocumentExts[strings.ToLower(path.Ext(parsed.Path))] {
		return link
	}
	return ""
}

func webpageDocumentName(link string) string {
	parsed, err := url.Parse(link)
	if err != nil {
		return ""
	}
	name := path.Base(parsed.Path)
	if name == "." || name == "/" {
		return ""
	}
	return name
}

func webpageDocumentRole(documentName, title string) string {
	lower := strings.ToLower(documentName + " " + title)
	switch {
	case strings.Contains(lower, "brief"):
		return "briefing_note"
	case strings.Contains(lower, "addendum") || strings.Contains(lower, "annex") || strings.Contains(lower, "appendix"):
		return "addendum"
	case strings.Contains(lower, "boq") || strings.Contains(lower, "pricing"):
		return "pricing_schedule"
	default:
		return "support_document"
	}
}

func webpagePublishedDate(link string) string {
	if match := webpageUploadDatePattern.FindStringSubmatch(link); len(match) == 4 {
		return match[1] + "-" + match[2] + "-" + match[3]
	}
	return ""
}

func webpageMIMEType(name string) string {
	if mimeType := mime.TypeByExtension(strings.ToLower(path.Ext(name))); mimeType != "" {
		return mimeType
	}
	return ""
}

func webpageExtract(body string, pattern *regexp.Regexp) string {
	match := pattern.FindStringSubmatch(body)
	if len(match) < 2 {
		return ""
	}
	return match[1]
}

func webpageText(raw string) string {
	raw = webpageTagPattern.ReplaceAllString(raw, " ")
	raw = html.UnescapeString(raw)
	raw = webpageSpacePattern.ReplaceAllString(strings.TrimSpace(raw), " ")
	return raw
}

func webpageLimit(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if limit <= 0 || len(raw) <= limit {
		return raw
	}
	return strings.TrimSpace(raw[:limit])
}
