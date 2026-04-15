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
	"openbid/internal/tenderstate"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultDurbanProcurementURL = "https://www.durban.gov.za/pages/business/procurement"

	durbanDefaultUserAgent = "OpenBid Durban importer/1.0"
)

var (
	durbanTenderPairPattern = regexp.MustCompile(`(?is)<tr[^>]*font-weight:\s*380[^>]*>\s*<td[^>]*>(.*?)</td>\s*<td[^>]*>(.*?)</td>\s*<td[^>]*>(.*?)</td>\s*<td[^>]*>(.*?)</td>\s*<td[^>]*>(.*?)</td>\s*</tr>\s*<tr[^>]*>\s*<td[^>]*colspan\s*=\s*["']?6["']?[^>]*>(.*?)</td>\s*</tr>`)
	durbanInfoPattern       = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*list-group-item[^"]*"[^>]*>\s*<span[^>]*>\s*<strong>(.*?)</strong>\s*</span>(.*?)</a>`)
	durbanListItemPattern   = regexp.MustCompile(`(?is)<li[^>]*>(.*?)</li>`)
	durbanAnchorPattern     = regexp.MustCompile(`(?is)<a[^>]+href=["']([^"']+)["'][^>]*>(.*?)</a>`)
	durbanSpanPattern       = regexp.MustCompile(`(?is)<span[^>]*>(.*?)</span>`)
	durbanBreakPattern      = regexp.MustCompile(`(?is)<br\s*/?>`)
	durbanTagPattern        = regexp.MustCompile(`(?is)<[^>]+>`)
	durbanSpacePattern      = regexp.MustCompile(`\s+`)
	durbanUploadDatePattern = regexp.MustCompile(`/([0-9]{4})/([0-9]{2})/([0-9]{2})/`)
)

type DurbanAdapter struct {
	SourceKey string
	PageURL   string
	Client    *http.Client
}

func NewDurbanAdapter(sourceKey, pageURL string) *DurbanAdapter {
	return &DurbanAdapter{
		SourceKey: NormalizeKey(sourceKey),
		PageURL:   strings.TrimSpace(pageURL),
		Client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *DurbanAdapter) Key() string {
	if a.SourceKey == "" {
		return "durban"
	}
	return a.SourceKey
}

func (a *DurbanAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	if a.PageURL == "" {
		return nil, "", fmt.Errorf("durban procurement page url is required")
	}
	pageURL, err := url.Parse(a.PageURL)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", durbanDefaultUserAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml")
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("durban procurement returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	items := a.parsePage(pageURL, string(body))
	return items, fmt.Sprintf("loaded %d Durban procurement tenders", len(items)), nil
}

func (a *DurbanAdapter) parsePage(pageURL *url.URL, content string) []models.Tender {
	matches := durbanTenderPairPattern.FindAllStringSubmatch(content, -1)
	out := make([]models.Tender, 0, len(matches))
	for _, match := range matches {
		category := durbanText(match[1], false)
		tenderType := durbanText(match[2], false)
		title := durbanText(match[3], false)
		tenderNumber := durbanText(match[4], false)
		closingDate := durbanDateTime(match[5])
		detailBlock := match[6]
		info := durbanInfoFacts(detailBlock)
		if closingDate == "" {
			closingDate = durbanDateTime(info["closing_date"])
		}
		documents := durbanDocuments(pageURL, detailBlock)
		documentURL := ""
		if len(documents) > 0 {
			documentURL = documents[0].URL
		}
		issuer := strings.TrimSpace(info["enquiries_procuring_entity"])
		if issuer == "" {
			issuer = "eThekwini Municipality"
		}
		pageFacts := map[string]string{
			"tender_category":  category,
			"tender_type":      tenderType,
			"closing_details":  closingDate,
			"procuring_entity": issuer,
			"document_count":   fmt.Sprintf("%d", len(documents)),
		}
		for key, value := range info {
			if strings.TrimSpace(value) != "" {
				pageFacts[key] = value
			}
		}
		sourceMetadata := map[string]string{
			"listing_section": "current_tenders",
			"source_page":     strings.TrimSpace(pageURL.String()),
		}
		relevanceInput := strings.Join([]string{title, category, tenderType, issuer}, " ")
		status := "open"
		if tenderstate.IsExpired(models.Tender{ClosingDate: closingDate}, time.Now().UTC()) {
			status = "closed"
		}
		item := models.Tender{
			ID:                  durbanStableID(a.Key(), tenderNumber, documentURL, title),
			SourceKey:           a.Key(),
			ExternalID:          tenderNumber,
			Title:               title,
			Issuer:              issuer,
			Province:            "KwaZulu-Natal",
			Category:            category,
			TenderNumber:        tenderNumber,
			PublishedDate:       durbanPublishedDate(documents),
			ClosingDate:         closingDate,
			Status:              status,
			TenderType:          tenderType,
			Scope:               title,
			Summary:             title,
			OriginalURL:         strings.TrimSpace(pageURL.String()),
			DocumentURL:         documentURL,
			EngineeringRelevant: score(relevanceInput) > 0.5,
			RelevanceScore:      score(relevanceInput),
			ExtractedFacts:      cloneFacts(pageFacts),
			PageFacts:           pageFacts,
			SourceMetadata:      sourceMetadata,
			Location: models.TenderLocation{
				Town:     "Durban",
				Province: "KwaZulu-Natal",
			},
			Submission: models.TenderSubmission{
				Method:            "physical",
				Instructions:      "Refer to the eThekwini Municipality tender documents for submission address, briefing details, and final requirements.",
				ElectronicAllowed: false,
				PhysicalAllowed:   true,
			},
			Contacts:  durbanContacts(info),
			Documents: documents,
		}
		if documentURL != "" {
			item.DocumentStatus = models.ExtractionQueued
		}
		out = append(out, item)
	}
	return out
}

func durbanStableID(sourceKey, tenderNumber, documentURL, title string) string {
	key := NormalizeKey(sourceKey)
	if key == "" {
		key = "durban"
	}
	base := NormalizeKey(tenderNumber)
	if base == "" {
		base = NormalizeKey(durbanDocumentName(documentURL))
	}
	if base == "" {
		base = NormalizeKey(title)
	}
	if base == "" {
		base = "listing"
	}
	return key + "-" + base
}

func durbanInfoFacts(block string) map[string]string {
	out := map[string]string{}
	for _, match := range durbanInfoPattern.FindAllStringSubmatch(block, -1) {
		key := durbanInfoKey(match[1])
		value := durbanText(match[2], false)
		if key != "" {
			out[key] = value
		}
	}
	return out
}

func durbanInfoKey(raw string) string {
	raw = durbanText(raw, false)
	raw = strings.Trim(raw, ": ")
	raw = strings.ToLower(raw)
	raw = strings.ReplaceAll(raw, "&", "and")
	return strings.ReplaceAll(NormalizeKey(raw), "-", "_")
}

func durbanDocuments(pageURL *url.URL, block string) []models.TenderDocument {
	out := []models.TenderDocument{}
	seen := map[string]bool{}
	for _, item := range durbanListItemPattern.FindAllStringSubmatch(block, -1) {
		anchor := durbanAnchorPattern.FindStringSubmatch(item[1])
		if len(anchor) < 3 {
			continue
		}
		ref, err := url.Parse(strings.TrimSpace(anchor[1]))
		if err != nil {
			continue
		}
		link := pageURL.ResolveReference(ref).String()
		if seen[link] {
			continue
		}
		seen[link] = true
		roleLabel := durbanDocumentRoleLabel(item[1])
		fileName := durbanText(anchor[2], false)
		if fileName == "" {
			fileName = path.Base(ref.Path)
		}
		out = append(out, models.TenderDocument{
			URL:      link,
			FileName: fileName,
			MIMEType: durbanMIMEType(ref.Path),
			Role:     durbanDocumentRole(roleLabel),
			Source:   "listing",
		})
	}
	return out
}

func durbanDocumentRoleLabel(block string) string {
	match := durbanSpanPattern.FindStringSubmatch(block)
	if len(match) < 2 {
		return ""
	}
	return strings.Trim(durbanText(match[1], false), "| ")
}

func durbanDocumentRole(label string) string {
	normalized := NormalizeKey(label)
	switch normalized {
	case "main-tender-document":
		return "notice"
	case "additional-tender-document":
		return "support_document"
	case "":
		return "attachment"
	default:
		return normalized
	}
}

func durbanContacts(info map[string]string) []models.TenderContact {
	name := strings.TrimSpace(info["enquiries_contact_person"])
	email := strings.Trim(strings.TrimSpace(info["enquiries-email"]), ".")
	if email == "" {
		email = strings.Trim(strings.TrimSpace(info["enquiries_email"]), ".")
	}
	telephone := strings.TrimSpace(info["enquiries_contact_number"])
	fax := strings.TrimSpace(info["enquiries_fax"])
	if name == "" && email == "" && telephone == "" && fax == "" {
		return nil
	}
	return []models.TenderContact{{
		Role:      "enquiries",
		Name:      name,
		Email:     email,
		Telephone: telephone,
		Fax:       fax,
	}}
}

func durbanPublishedDate(documents []models.TenderDocument) string {
	for _, doc := range documents {
		if match := durbanUploadDatePattern.FindStringSubmatch(doc.URL); len(match) == 4 {
			return match[1] + "-" + match[2] + "-" + match[3]
		}
	}
	return ""
}

func durbanDateTime(raw string) string {
	raw = durbanText(raw, false)
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"2 January 2006 15:04",
		"02 January 2006 15:04",
	} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			if layout == "2006-01-02" {
				return parsed.Format("2006-01-02")
			}
			return parsed.Format("2006-01-02 15:04")
		}
	}
	return raw
}

func durbanDocumentName(documentURL string) string {
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

func durbanMIMEType(p string) string {
	if mimeType := mime.TypeByExtension(strings.ToLower(path.Ext(p))); mimeType != "" {
		return mimeType
	}
	return ""
}

func durbanText(raw string, preserveBreaks bool) string {
	if preserveBreaks {
		raw = durbanBreakPattern.ReplaceAllString(raw, "\n")
	} else {
		raw = durbanBreakPattern.ReplaceAllString(raw, " ")
	}
	raw = durbanTagPattern.ReplaceAllString(raw, " ")
	raw = html.UnescapeString(raw)
	if preserveBreaks {
		lines := []string{}
		for _, line := range strings.Split(raw, "\n") {
			line = durbanSpacePattern.ReplaceAllString(strings.TrimSpace(line), " ")
			if line != "" {
				lines = append(lines, line)
			}
		}
		return strings.Join(lines, "\n")
	}
	return durbanSpacePattern.ReplaceAllString(strings.TrimSpace(raw), " ")
}
