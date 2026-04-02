package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"tenderhub-za/internal/models"
	"time"
)

const (
	eTendersDefaultStatus   = 1
	eTendersDefaultPageSize = 100
)

type ETendersAdapter struct {
	SourceKey string
	PageURL   string
	PageSize  int
	Client    *http.Client
}

type eTendersResponse struct {
	RecordsFiltered int                   `json:"recordsFiltered"`
	Data            []eTendersOpportunity `json:"data"`
}

type eTendersOpportunity struct {
	ID                        int                       `json:"id"`
	TenderNo                  string                    `json:"tender_No"`
	Description               string                    `json:"description"`
	Category                  string                    `json:"category"`
	Type                      string                    `json:"type"`
	Department                string                    `json:"department"`
	OrganOfState              string                    `json:"organ_of_State"`
	Status                    string                    `json:"status"`
	ClosingDate               string                    `json:"closing_Date"`
	DatePublished             string                    `json:"date_Published"`
	CompulsoryBriefingSession string                    `json:"compulsory_briefing_session"`
	BriefingVenue             string                    `json:"briefingVenue"`
	Conditions                string                    `json:"conditions"`
	ContactPerson             string                    `json:"contactPerson"`
	Email                     string                    `json:"email"`
	Telephone                 string                    `json:"telephone"`
	Fax                       string                    `json:"fax"`
	Province                  string                    `json:"province"`
	Delivery                  string                    `json:"delivery"`
	BriefingSession           bool                      `json:"briefingSession"`
	BriefingCompulsory        bool                      `json:"briefingCompulsory"`
	ESubmission               bool                      `json:"eSubmission"`
	SupportDocuments          []eTendersSupportDocument `json:"supportDocument"`
}

type eTendersSupportDocument struct {
	SupportDocumentID string `json:"supportDocumentID"`
	FileName          string `json:"fileName"`
	Extension         string `json:"extension"`
}

func NewETendersAdapter(sourceKey, pageURL string) *ETendersAdapter {
	return &ETendersAdapter{
		SourceKey: NormalizeKey(sourceKey),
		PageURL:   strings.TrimSpace(pageURL),
		PageSize:  eTendersDefaultPageSize,
		Client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (a *ETendersAdapter) Key() string {
	if a.SourceKey == "" {
		return "etenders"
	}
	return a.SourceKey
}

func (a *ETendersAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	if a.PageURL == "" {
		return nil, "", fmt.Errorf("etenders page url is required")
	}
	pageURL, err := url.Parse(a.PageURL)
	if err != nil {
		return nil, "", err
	}
	status := eTendersStatus(pageURL)
	pageSize := a.PageSize
	if pageSize <= 0 {
		pageSize = eTendersDefaultPageSize
	}

	out := []models.Tender{}
	draw := 1
	for start := 0; ; start += pageSize {
		payload, err := a.fetchPage(ctx, pageURL, status, draw, start, pageSize)
		if err != nil {
			return nil, "", err
		}
		if len(payload.Data) == 0 {
			break
		}
		for _, item := range payload.Data {
			out = append(out, a.mapOpportunity(pageURL, item))
		}
		if start+len(payload.Data) >= payload.RecordsFiltered {
			break
		}
		draw++
	}

	return out, fmt.Sprintf("loaded %d eTenders opportunities", len(out)), nil
}

func (a *ETendersAdapter) fetchPage(ctx context.Context, pageURL *url.URL, status, draw, start, length int) (eTendersResponse, error) {
	endpoint := *pageURL
	endpoint.Path = "/Home/PaginatedTenderOpportunities"
	query := url.Values{}
	query.Set("draw", strconv.Itoa(draw))
	query.Set("start", strconv.Itoa(start))
	query.Set("length", strconv.Itoa(length))
	query.Set("status", strconv.Itoa(status))
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return eTendersResponse{}, err
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return eTendersResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return eTendersResponse{}, fmt.Errorf("etenders returned %d", resp.StatusCode)
	}

	var payload eTendersResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return eTendersResponse{}, err
	}
	return payload, nil
}

func (a *ETendersAdapter) mapOpportunity(pageURL *url.URL, item eTendersOpportunity) models.Tender {
	issuer := strings.TrimSpace(item.Department)
	if issuer == "" {
		issuer = strings.TrimSpace(item.OrganOfState)
	}
	documentURL := ""
	documentNames := make([]string, 0, len(item.SupportDocuments))
	if len(item.SupportDocuments) > 0 {
		documentURL = eTendersDownloadURL(pageURL, item.SupportDocuments[0])
		for _, doc := range item.SupportDocuments {
			if name := strings.TrimSpace(doc.FileName); name != "" {
				documentNames = append(documentNames, name)
			}
		}
	}

	relevanceInput := strings.Join([]string{item.Description, item.Category, issuer}, " ")
	facts := map[string]string{
		"closing_details":    eTendersDateTime(item.ClosingDate),
		"briefing_details":   eTendersBriefingDetails(item),
		"submission_details": eTendersSubmissionDetails(item),
		"contact_details":    eTendersContactDetails(item),
		"tender_type":        strings.TrimSpace(item.Type),
		"special_conditions": strings.TrimSpace(item.Conditions),
		"delivery_location":  strings.TrimSpace(item.Delivery),
		"document_names":     strings.Join(documentNames, "; "),
	}

	return models.Tender{
		SourceKey:           a.Key(),
		ExternalID:          strconv.Itoa(item.ID),
		Title:               strings.TrimSpace(item.Description),
		Issuer:              issuer,
		Province:            strings.TrimSpace(item.Province),
		Category:            strings.TrimSpace(item.Category),
		TenderNumber:        strings.TrimSpace(item.TenderNo),
		PublishedDate:       eTendersDate(item.DatePublished),
		ClosingDate:         eTendersSortableDateTime(item.ClosingDate),
		Status:              strings.ToLower(strings.TrimSpace(item.Status)),
		Summary:             strings.TrimSpace(item.Description),
		OriginalURL:         strings.TrimSpace(pageURL.String()),
		DocumentURL:         documentURL,
		EngineeringRelevant: score(relevanceInput) > 0.5,
		RelevanceScore:      score(relevanceInput),
		DocumentStatus:      models.ExtractionQueued,
		ExtractedFacts:      facts,
	}
}

func eTendersStatus(pageURL *url.URL) int {
	status := eTendersDefaultStatus
	if raw := strings.TrimSpace(pageURL.Query().Get("id")); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 {
			status = parsed
		}
	}
	return status
}

func eTendersDownloadURL(pageURL *url.URL, doc eTendersSupportDocument) string {
	if doc.SupportDocumentID == "" {
		return ""
	}
	downloadURL := *pageURL
	downloadURL.Path = "/home/Download/"
	query := url.Values{}
	query.Set("blobName", doc.SupportDocumentID+doc.Extension)
	query.Set("downloadedFileName", doc.FileName)
	downloadURL.RawQuery = query.Encode()
	return downloadURL.String()
}

func eTendersDate(raw string) string {
	if parsed, ok := parseETendersTime(raw); ok {
		return parsed.Format("2006-01-02")
	}
	return strings.TrimSpace(raw)
}

func eTendersSortableDateTime(raw string) string {
	if parsed, ok := parseETendersTime(raw); ok {
		return parsed.Format("2006-01-02 15:04")
	}
	return strings.TrimSpace(raw)
}

func eTendersDateTime(raw string) string {
	if parsed, ok := parseETendersTime(raw); ok {
		return parsed.Format("2006-01-02 15:04")
	}
	return "N/A"
}

func eTendersBriefingDetails(item eTendersOpportunity) string {
	parts := []string{
		"session=" + boolText(item.BriefingSession),
		"compulsory=" + boolText(item.BriefingCompulsory),
	}
	if when := eTendersDateTime(item.CompulsoryBriefingSession); when != "N/A" {
		parts = append(parts, "when="+when)
	}
	if venue := strings.TrimSpace(item.BriefingVenue); venue != "" {
		parts = append(parts, "venue="+venue)
	}
	return strings.Join(parts, "; ")
}

func eTendersSubmissionDetails(item eTendersOpportunity) string {
	parts := []string{"e_submission=" + boolText(item.ESubmission)}
	if delivery := strings.TrimSpace(item.Delivery); delivery != "" {
		parts = append(parts, "delivery="+delivery)
	}
	return strings.Join(parts, "; ")
}

func eTendersContactDetails(item eTendersOpportunity) string {
	parts := []string{}
	if value := strings.TrimSpace(item.ContactPerson); value != "" {
		parts = append(parts, "person="+value)
	}
	if value := strings.TrimSpace(item.Email); value != "" {
		parts = append(parts, "email="+value)
	}
	if value := strings.TrimSpace(item.Telephone); value != "" {
		parts = append(parts, "telephone="+value)
	}
	if value := strings.TrimSpace(item.Fax); value != "" {
		parts = append(parts, "fax="+value)
	}
	return strings.Join(parts, "; ")
}

func parseETendersTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "0001-01-01") {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05.9999999",
		"2006-01-02T15:04:05",
	}
	for _, layout := range layouts {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			return parsed, true
		}
	}
	return time.Time{}, false
}

func boolText(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}
