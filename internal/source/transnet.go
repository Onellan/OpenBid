package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"openbid/internal/models"
	"openbid/internal/tenderstate"
	"path"
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

type transnetSupplierPortalResponse struct {
	Draw            int                             `json:"draw"`
	RecordsTotal    int                             `json:"recordsTotal"`
	RecordsFiltered int                             `json:"recordsFiltered"`
	Data            []transnetSupplierPortalListing `json:"data"`
}

type transnetSupplierPortalListing struct {
	ID                      int                          `json:"id"`
	Status                  transnetSupplierPortalStatus `json:"status"`
	TenderReference         string                       `json:"tender_reference"`
	TenderTitle             string                       `json:"tender_title"`
	TenderDescription       string                       `json:"tender_description"`
	OpenDate                string                       `json:"open_date"`
	OpenTime                string                       `json:"open_time"`
	IssueDate               string                       `json:"issue_date"`
	ClosingDate             string                       `json:"closing_date"`
	ClosingTime             string                       `json:"closing_time"`
	IsThereABriefingSession bool                         `json:"is_there_a_briefing_session"`
	BriefingDescription     string                       `json:"briefing_description"`
	BriefingSessionDate     string                       `json:"briefing_session_date"`
	BriefingSessionTime     string                       `json:"briefing_session_time"`
	LocationOfService       string                       `json:"location_of_service"`
	ValidityEndDate         string                       `json:"validity_end_date"`
	RequirementsDocument    string                       `json:"requirements_document"`
	AllowManualSubmission   bool                         `json:"allow_manual_submission"`
	TenderDisclaimer        string                       `json:"tender_disclaimer"`
	CreatedAt               string                       `json:"created_at"`
	UpdatedAt               string                       `json:"updated_at"`
	Tactical                int                          `json:"tactical"`
	Sourcing                int                          `json:"sourcing"`
	Stage                   int                          `json:"stage"`
	Mechanism               int                          `json:"mechanism"`
	TenderType              int                          `json:"tender_type"`
	TenderCategory          int                          `json:"tender_category"`
	ContactPerson           int                          `json:"contact_person"`
	SecondContactPerson     int                          `json:"second_contact_person"`
	TenderCorridorType      int                          `json:"tender_corridor_type"`
	OperatingDivision       int                          `json:"operating_division"`
	BidValidityPeriod       int                          `json:"bid_validity_period"`
	CancellationCount       int                          `json:"cancellation_count"`
	CancellationReason      string                       `json:"cancellation_reason"`
}

type transnetSupplierPortalStatus struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Code string `json:"code"`
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
	if transnetUsesSupplierPortal(pageURL) {
		return a.fetchSupplierPortal(ctx, pageURL)
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

func (a *TransnetAdapter) fetchSupplierPortal(ctx context.Context, pageURL *url.URL) ([]models.Tender, string, error) {
	endpoint := *pageURL
	endpoint.Path = "/portal/supplier_relationship_management/tenders"
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
		return nil, "", fmt.Errorf("transnet supplier portal returned %d", resp.StatusCode)
	}
	var payload transnetSupplierPortalResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", err
	}
	out := make([]models.Tender, 0, len(payload.Data))
	for _, item := range payload.Data {
		out = append(out, NormalizeTenderIdentity(a.mapSupplierPortalListing(pageURL, item)))
	}
	return out, fmt.Sprintf("loaded %d Transnet eSupplier advertised tenders", len(out)), nil
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

func (a *TransnetAdapter) mapSupplierPortalListing(pageURL *url.URL, item transnetSupplierPortalListing) models.Tender {
	title := strings.TrimSpace(item.TenderTitle)
	if title == "" {
		title = strings.TrimSpace(item.TenderDescription)
	}
	summary := strings.TrimSpace(item.TenderDescription)
	if summary == "" {
		summary = title
	}
	detailURL := *pageURL
	detailURL.Path = fmt.Sprintf("/portal/procurement/supplier_relationship_management/tenders/detailed/%d", item.ID)
	detailURL.RawQuery = ""
	detailURL.Fragment = ""
	closingDate := transnetCombineDateTime(item.ClosingDate, item.ClosingTime)
	status := transnetSupplierStatus(item.Status.Name)
	if tenderstate.IsExpired(models.Tender{ClosingDate: closingDate}, time.Now().UTC()) && status == "open" {
		status = "closed"
	}
	briefingDate := transnetCombineDateTime(item.BriefingSessionDate, item.BriefingSessionTime)
	documentURL := transnetResolveURL(pageURL, item.RequirementsDocument)
	pageFacts := map[string]string{
		"source_page":         pageURL.String(),
		"open_date":           transnetCombineDateTime(item.OpenDate, item.OpenTime),
		"issue_date":          strings.TrimSpace(item.IssueDate),
		"closing_details":     closingDate,
		"briefing_details":    strings.TrimSpace(item.BriefingDescription),
		"briefing_date":       briefingDate,
		"location_of_service": strings.TrimSpace(item.LocationOfService),
		"validity_end_date":   strings.TrimSpace(item.ValidityEndDate),
		"allow_manual_submit": strconv.FormatBool(item.AllowManualSubmission),
		"tender_disclaimer":   strings.TrimSpace(item.TenderDisclaimer),
		"cancellation_reason": strings.TrimSpace(item.CancellationReason),
		"status_display_name": strings.TrimSpace(item.Status.Name),
		"status_display_code": strings.TrimSpace(item.Status.Code),
	}
	sourceMetadata := map[string]string{
		"portal_id":             strconv.Itoa(item.ID),
		"tactical_id":           transnetIntString(item.Tactical),
		"sourcing_id":           transnetIntString(item.Sourcing),
		"stage_id":              transnetIntString(item.Stage),
		"mechanism_id":          transnetIntString(item.Mechanism),
		"tender_type_id":        transnetIntString(item.TenderType),
		"tender_category_id":    transnetIntString(item.TenderCategory),
		"contact_person_id":     transnetIntString(item.ContactPerson),
		"second_contact_id":     transnetIntString(item.SecondContactPerson),
		"corridor_type_id":      transnetIntString(item.TenderCorridorType),
		"operating_division_id": transnetIntString(item.OperatingDivision),
		"bid_validity_period":   transnetIntString(item.BidValidityPeriod),
	}
	documents := []models.TenderDocument{}
	if documentURL != "" {
		documents = append(documents, models.TenderDocument{
			URL:      documentURL,
			FileName: transnetDocumentName(documentURL, strconv.Itoa(item.ID)),
			Role:     "requirements_document",
			Source:   "listing",
		})
	}
	var documentStatus models.ExtractionState
	if documentURL != "" {
		documentStatus = models.ExtractionQueued
	}
	briefings := []models.TenderBriefing{}
	if item.IsThereABriefingSession || strings.TrimSpace(item.BriefingDescription) != "" || strings.TrimSpace(briefingDate) != "" {
		briefings = append(briefings, models.TenderBriefing{
			Label:    "briefing_session",
			DateTime: briefingDate,
			Notes:    strings.TrimSpace(item.BriefingDescription),
		})
	}
	return models.Tender{
		SourceKey:           a.Key(),
		ExternalID:          strconv.Itoa(item.ID),
		Title:               title,
		Issuer:              "Transnet",
		Category:            "Transnet eSupplier tender",
		TenderNumber:        strings.TrimSpace(item.TenderReference),
		PublishedDate:       transnetDate(strings.TrimSpace(firstNonEmpty(item.IssueDate, item.OpenDate))),
		ClosingDate:         closingDate,
		Status:              status,
		TenderType:          transnetTenderType(item.TenderReference),
		Scope:               summary,
		Summary:             summary,
		OriginalURL:         detailURL.String(),
		DocumentURL:         documentURL,
		EngineeringRelevant: score(strings.Join([]string{title, summary, item.LocationOfService}, " ")) > 0.5,
		RelevanceScore:      score(strings.Join([]string{title, summary, item.LocationOfService}, " ")),
		DocumentStatus:      documentStatus,
		ExtractedFacts:      cloneFacts(pageFacts),
		PageFacts:           pageFacts,
		SourceMetadata:      sourceMetadata,
		Location: models.TenderLocation{
			DeliveryLocation: strings.TrimSpace(item.LocationOfService),
		},
		Submission: models.TenderSubmission{
			Method:       "electronic",
			Instructions: "Use the Transnet eSupplier portal and tender documents for final submission requirements.",
		},
		Briefings: briefings,
		Documents: documents,
	}
}

func transnetUsesSupplierPortal(pageURL *url.URL) bool {
	host := strings.ToLower(pageURL.Hostname())
	pagePath := strings.ToLower(strings.TrimSpace(pageURL.Path))
	return strings.Contains(host, "esupplierportal.transnet.net") || strings.Contains(pagePath, "/portal/advertisedtenders")
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
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
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

func transnetCombineDateTime(datePart, timePart string) string {
	datePart = strings.TrimSpace(datePart)
	timePart = strings.TrimSpace(timePart)
	if datePart == "" {
		return ""
	}
	if timePart == "" {
		return transnetDate(datePart)
	}
	if parsed, ok := parseTransnetTime(datePart + " " + timePart); ok {
		return parsed.Format("2006-01-02 15:04")
	}
	if parsed, ok := parseTransnetTime(datePart); ok {
		return parsed.Format("2006-01-02") + " " + strings.TrimSuffix(timePart, ":00")
	}
	return strings.TrimSpace(datePart + " " + timePart)
}

func transnetSupplierStatus(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "published":
		return "open"
	case "award", "awarded":
		return "awarded"
	case "evaluation", "tender closed", "closed":
		return "closed"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return strings.ToLower(strings.TrimSpace(raw))
	}
}

func transnetResolveURL(baseURL *url.URL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	ref, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	return baseURL.ResolveReference(ref).String()
}

func transnetDocumentName(documentURL, fallback string) string {
	parsed, err := url.Parse(strings.TrimSpace(documentURL))
	if err != nil {
		return fallback
	}
	name := path.Base(parsed.Path)
	if name == "." || name == "/" || name == "" {
		return fallback
	}
	return name
}

func transnetIntString(value int) string {
	if value == 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func transnetTenderType(reference string) string {
	parts := strings.Split(strings.TrimSpace(reference), "/")
	if len(parts) == 0 {
		return ""
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	if last == "" {
		return ""
	}
	return strings.ToUpper(last)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
