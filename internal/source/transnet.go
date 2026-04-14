package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"openbid/internal/models"
	"openbid/internal/tenderstate"
	"strconv"
	"strings"
	"time"
)

const transnetDefaultUserAgent = "OpenBid Transnet importer/1.0"

type TransnetAdapter struct {
	SourceKey string
	PageURL   string
	Client    *http.Client
}

type transnetResponse struct {
	Success         bool              `json:"success"`
	Result          []transnetListing `json:"result"`
	RecordsFiltered int               `json:"recordsFiltered"`
	TotalCount      int               `json:"totalCount"`
}

type transnetListing struct {
	NameOfTender              string `json:"nameOfTender"`
	DescriptionOfTender       string `json:"descriptionOfTender"`
	TenderNumber              string `json:"tenderNumber"`
	BriefingDate              string `json:"briefingDate"`
	BriefingDetails           string `json:"briefingDetails"`
	ClosingDate               string `json:"closingDate"`
	ContactPersonEmailAddress string `json:"contactPersonEmailAddress"`
	ContactPersonName         string `json:"contactPersonName"`
	PublishedDate             string `json:"publishedDate"`
	Attachment                string `json:"attachment"`
	TenderType                string `json:"tenderType"`
	LocationOfService         string `json:"locationOfService"`
	NameOfInstitution         string `json:"nameOfInstitution"`
	TenderCategory            string `json:"tenderCategory"`
	TenderStatus              string `json:"tenderStatus"`
	RowKey                    string `json:"rowKey"`
	TenderAccessType          string `json:"tenderAccessType"`
	SelectedSuppliers         string `json:"selectedSuppliers"`
}

func NewTransnetAdapter(sourceKey, pageURL string) *TransnetAdapter {
	return &TransnetAdapter{
		SourceKey: NormalizeKey(sourceKey),
		PageURL:   strings.TrimSpace(pageURL),
		Client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *TransnetAdapter) Key() string {
	if a.SourceKey == "" {
		return "transnet"
	}
	return a.SourceKey
}

func (a *TransnetAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	if a.PageURL == "" {
		return nil, "", fmt.Errorf("transnet page url is required")
	}
	pageURL, err := url.Parse(a.PageURL)
	if err != nil {
		return nil, "", err
	}
	endpoint := *pageURL
	endpoint.Path = "/Home/GetAdvertisedTenders"
	endpoint.RawQuery = ""
	endpoint.Fragment = ""
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", transnetDefaultUserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Referer", pageURL.String())
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("transnet returned %d", resp.StatusCode)
	}
	var payload transnetResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", err
	}
	if !payload.Success {
		return nil, "", fmt.Errorf("transnet response was not successful")
	}
	out := make([]models.Tender, 0, len(payload.Result))
	for _, item := range payload.Result {
		out = append(out, NormalizeTenderIdentity(a.mapListing(pageURL, item)))
	}
	return out, fmt.Sprintf("loaded %d Transnet advertised tenders", len(out)), nil
}

func (a *TransnetAdapter) mapListing(pageURL *url.URL, item transnetListing) models.Tender {
	title := strings.TrimSpace(item.NameOfTender)
	if title == "" {
		title = strings.TrimSpace(item.DescriptionOfTender)
	}
	summary := strings.TrimSpace(item.DescriptionOfTender)
	if summary == "" {
		summary = title
	}
	detailURL := *pageURL
	detailURL.Path = "/Home/TenderDetails"
	query := url.Values{}
	query.Set("Id", strings.TrimSpace(item.RowKey))
	detailURL.RawQuery = query.Encode()
	detailURL.Fragment = ""
	status := strings.ToLower(strings.TrimSpace(item.TenderStatus))
	if status == "" {
		status = "open"
	}
	closingDate := transnetDateTime(item.ClosingDate)
	if tenderstate.IsExpired(models.Tender{ClosingDate: closingDate}, time.Now().UTC()) && status == "open" {
		status = "closed"
	}
	documentURL := strings.TrimSpace(item.Attachment)
	pageFacts := map[string]string{
		"closing_details":     closingDate,
		"briefing_details":    strings.TrimSpace(item.BriefingDetails),
		"briefing_date":       transnetDateTime(item.BriefingDate),
		"contact_details":     strings.TrimSpace(item.ContactPersonName + " " + item.ContactPersonEmailAddress),
		"location_of_service": strings.TrimSpace(item.LocationOfService),
		"access_type":         strings.TrimSpace(item.TenderAccessType),
	}
	sourceMetadata := map[string]string{
		"row_key":            strings.TrimSpace(item.RowKey),
		"institution":        strings.TrimSpace(item.NameOfInstitution),
		"selected_suppliers": strings.TrimSpace(item.SelectedSuppliers),
	}
	documents := []models.TenderDocument{}
	if documentURL != "" {
		documents = append(documents, models.TenderDocument{
			URL:      documentURL,
			FileName: strings.TrimSpace(item.RowKey),
			Role:     "support_document",
			Source:   "listing",
		})
	}
	var documentStatus models.ExtractionState
	if documentURL != "" {
		documentStatus = models.ExtractionQueued
	}
	contacts := []models.TenderContact{}
	if strings.TrimSpace(item.ContactPersonName) != "" || strings.TrimSpace(item.ContactPersonEmailAddress) != "" {
		contacts = append(contacts, models.TenderContact{
			Role:  "listing_contact",
			Name:  strings.TrimSpace(item.ContactPersonName),
			Email: strings.TrimSpace(item.ContactPersonEmailAddress),
		})
	}
	briefings := []models.TenderBriefing{}
	if strings.TrimSpace(item.BriefingDate) != "" || strings.TrimSpace(item.BriefingDetails) != "" {
		briefings = append(briefings, models.TenderBriefing{
			Label:    "briefing_session",
			DateTime: transnetDateTime(item.BriefingDate),
			Notes:    strings.TrimSpace(item.BriefingDetails),
		})
	}
	return models.Tender{
		SourceKey:           a.Key(),
		ExternalID:          strings.TrimSpace(item.RowKey),
		Title:               title,
		Issuer:              strings.TrimSpace(item.NameOfInstitution),
		Category:            strings.TrimSpace(item.TenderCategory),
		TenderNumber:        strings.TrimSpace(item.TenderNumber),
		PublishedDate:       transnetDate(item.PublishedDate),
		ClosingDate:         closingDate,
		Status:              status,
		TenderType:          strings.TrimSpace(item.TenderType),
		Scope:               summary,
		Summary:             summary,
		OriginalURL:         detailURL.String(),
		DocumentURL:         documentURL,
		EngineeringRelevant: score(strings.Join([]string{title, summary, item.TenderCategory}, " ")) > 0.5,
		RelevanceScore:      score(strings.Join([]string{title, summary, item.TenderCategory}, " ")),
		DocumentStatus:      documentStatus,
		ExtractedFacts:      cloneFacts(pageFacts),
		PageFacts:           pageFacts,
		SourceMetadata:      sourceMetadata,
		Location: models.TenderLocation{
			DeliveryLocation: strings.TrimSpace(item.LocationOfService),
		},
		Submission: models.TenderSubmission{
			Method:       "electronic",
			Instructions: "Use the Transnet e-Tenders portal and tender documents for final submission requirements.",
		},
		Contacts:  contacts,
		Briefings: briefings,
		Documents: documents,
	}
}

func transnetDate(raw string) string {
	if parsed, ok := parseTransnetTime(raw); ok {
		return parsed.Format("2006-01-02")
	}
	return strings.TrimSpace(raw)
}

func transnetDateTime(raw string) string {
	if parsed, ok := parseTransnetTime(raw); ok {
		return parsed.Format("2006-01-02 15:04")
	}
	return strings.TrimSpace(raw)
}

func parseTransnetTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		"1/2/2006 3:04:05 PM",
		"1/2/2006 3:04 PM",
		time.RFC3339Nano,
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed, true
		}
	}
	if unix, err := strconv.ParseInt(raw, 10, 64); err == nil && unix > 0 {
		return time.Unix(unix, 0), true
	}
	return time.Time{}, false
}
