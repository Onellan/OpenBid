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
	"sync"
	"time"
)

const (
	eskomDefaultConcurrency = 8
	eskomDefaultUserAgent   = "OpenBid Eskom importer/1.0"
)

type EskomAdapter struct {
	SourceKey   string
	PageURL     string
	Client      *http.Client
	Concurrency int
}

type eskomTender struct {
	TenderID       int    `json:"TENDER_ID"`
	GroupID        int    `json:"GROUP_ID"`
	Description    string `json:"DESCRIPTION"`
	ContractID     int    `json:"CONTRACT_ID"`
	Audience       string `json:"Audience"`
	HeaderDesc     string `json:"HEADER_DESC"`
	Reference      string `json:"REFERENCE"`
	ScopeDetails   string `json:"SCOPE_DETAILS"`
	Summary        string `json:"SUMMARY"`
	OfficeLocation string `json:"OFFICE_LOCATION"`
	Docs           int    `json:"DOCS"`
	ClosingDate    string `json:"CLOSING_DATE"`
	PublishedDate  string `json:"PUBLISHEDDATE"`
	DateModified   string `json:"DATEMODIFIED"`
	Fax            string `json:"FAX"`
	Email          string `json:"EMAIL"`
	Address        string `json:"ADDRESS"`
	Publish        string `json:"PUBLISH"`
	ProvinceID     int    `json:"PROVINCE_ID"`
	Province       string `json:"Province"`
	ETendering     bool   `json:"E_TENDERING"`
}

type eskomLookupItem struct {
	Description string `json:"DESCRIPTION"`
	GroupID     int    `json:"GROUP_ID"`
	ContractID  int    `json:"CONTRACT_ID"`
	ProvinceID  int    `json:"PROVINCE_ID"`
}

type eskomDocument struct {
	ID          int    `json:"ID"`
	Name        string `json:"NAME"`
	Size        int64  `json:"SIZE"`
	Sequence    int    `json:"SEQUENCE"`
	CreatedDate string `json:"CDATE"`
	ContentType string `json:"ContentType"`
}

func NewEskomAdapter(sourceKey, pageURL string) *EskomAdapter {
	return &EskomAdapter{
		SourceKey:   NormalizeKey(sourceKey),
		PageURL:     strings.TrimSpace(pageURL),
		Client:      &http.Client{Timeout: 30 * time.Second},
		Concurrency: eskomDefaultConcurrency,
	}
}

func (a *EskomAdapter) Key() string {
	if a.SourceKey == "" {
		return "eskom"
	}
	return a.SourceKey
}

func (a *EskomAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	apiBase, publicBase, err := eskomBaseURLs(a.PageURL)
	if err != nil {
		return nil, "", err
	}
	listings, err := a.fetchTenders(ctx, apiBase)
	if err != nil {
		return nil, "", err
	}
	contracts, _ := a.fetchContractTypes(ctx, apiBase)
	divisions, _ := a.fetchDivisions(ctx, apiBase)
	provinces, _ := a.fetchProvinces(ctx, apiBase)
	documents := a.fetchDocuments(ctx, apiBase, listings)

	out := make([]models.Tender, 0, len(listings))
	for _, item := range listings {
		if !strings.EqualFold(strings.TrimSpace(item.Publish), "Y") {
			continue
		}
		docs := documents[item.TenderID]
		out = append(out, NormalizeTenderIdentity(a.mapTender(publicBase, item, contracts, divisions, provinces, docs)))
	}
	return out, fmt.Sprintf("loaded %d Eskom tenders", len(out)), nil
}

func (a *EskomAdapter) fetchTenders(ctx context.Context, apiBase string) ([]eskomTender, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/Lookup/GetTender?TENDER_ID=", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", eskomDefaultUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("eskom returned %d", resp.StatusCode)
	}
	var payload []eskomTender
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (a *EskomAdapter) fetchContractTypes(ctx context.Context, apiBase string) (map[int]string, error) {
	var payload []eskomLookupItem
	if err := a.fetchLookup(ctx, apiBase+"/Lookup/GetContract?CONTRACT_ID=", &payload); err != nil {
		return nil, err
	}
	out := map[int]string{}
	for _, item := range payload {
		if item.ContractID > 0 && strings.TrimSpace(item.Description) != "" {
			out[item.ContractID] = strings.TrimSpace(item.Description)
		}
	}
	return out, nil
}

func (a *EskomAdapter) fetchDivisions(ctx context.Context, apiBase string) (map[int]string, error) {
	var payload []eskomLookupItem
	if err := a.fetchLookup(ctx, apiBase+"/Lookup/GetDivision?GROUP_ID=", &payload); err != nil {
		return nil, err
	}
	out := map[int]string{}
	for _, item := range payload {
		if item.GroupID > 0 && strings.TrimSpace(item.Description) != "" {
			out[item.GroupID] = strings.TrimSpace(item.Description)
		}
	}
	return out, nil
}

func (a *EskomAdapter) fetchProvinces(ctx context.Context, apiBase string) (map[int]string, error) {
	var payload []eskomLookupItem
	if err := a.fetchLookup(ctx, apiBase+"/Lookup/GetProvince?PROVINCE_ID=", &payload); err != nil {
		return nil, err
	}
	out := map[int]string{}
	for _, item := range payload {
		if strings.TrimSpace(item.Description) != "" {
			out[item.ProvinceID] = strings.TrimSpace(item.Description)
		}
	}
	return out, nil
}

func (a *EskomAdapter) fetchLookup(ctx context.Context, endpoint string, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", eskomDefaultUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := a.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("eskom lookup returned %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(target)
}

func (a *EskomAdapter) fetchDocuments(ctx context.Context, apiBase string, tenders []eskomTender) map[int][]eskomDocument {
	type result struct {
		tenderID int
		docs     []eskomDocument
	}
	workers := a.Concurrency
	if workers <= 0 {
		workers = eskomDefaultConcurrency
	}
	sem := make(chan struct{}, workers)
	results := make(chan result, len(tenders))
	var wg sync.WaitGroup
	for _, item := range tenders {
		if item.TenderID <= 0 || item.Docs <= 0 {
			continue
		}
		wg.Add(1)
		go func(item eskomTender) {
			defer wg.Done()
			sem <- struct{}{}
			docs, err := a.fetchTenderDocuments(ctx, apiBase, item.TenderID)
			<-sem
			if err != nil || len(docs) == 0 {
				return
			}
			results <- result{tenderID: item.TenderID, docs: docs}
		}(item)
	}
	wg.Wait()
	close(results)
	out := map[int][]eskomDocument{}
	for item := range results {
		out[item.tenderID] = item.docs
	}
	return out
}

func (a *EskomAdapter) fetchTenderDocuments(ctx context.Context, apiBase string, tenderID int) ([]eskomDocument, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBase+"/Files/GetDocs?TENDER_ID="+strconv.Itoa(tenderID), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", eskomDefaultUserAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("eskom docs returned %d", resp.StatusCode)
	}
	var payload []eskomDocument
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func (a *EskomAdapter) mapTender(publicBase string, item eskomTender, contracts, divisions, provinces map[int]string, docs []eskomDocument) models.Tender {
	title := strings.TrimSpace(item.HeaderDesc)
	if title == "" {
		title = strings.TrimSpace(item.Description)
	}
	summary := strings.TrimSpace(item.Summary)
	if summary == "" {
		summary = strings.TrimSpace(item.ScopeDetails)
	}
	if summary == "" {
		summary = title
	}
	division := strings.TrimSpace(item.Description)
	if division == "" {
		division = strings.TrimSpace(divisions[item.GroupID])
	}
	province := strings.TrimSpace(item.Province)
	if province == "" {
		province = strings.TrimSpace(provinces[item.ProvinceID])
	}
	contractType := strings.TrimSpace(contracts[item.ContractID])
	mappedDocs := eskomDocuments(publicBase, docs)
	documentURL := ""
	if len(mappedDocs) > 0 {
		documentURL = mappedDocs[0].URL
	}
	facts := map[string]string{
		"closing_details":    eskomDateTime(item.ClosingDate),
		"submission_details": strings.TrimSpace(item.OfficeLocation),
		"contact_details":    strings.TrimSpace(item.Email),
		"audience":           strings.TrimSpace(item.Audience),
		"contract_type":      contractType,
	}
	sourceMetadata := map[string]string{
		"division":        division,
		"audience":        strings.TrimSpace(item.Audience),
		"office_location": strings.TrimSpace(item.OfficeLocation),
		"publish_flag":    strings.TrimSpace(item.Publish),
		"date_modified":   eskomDateTime(item.DateModified),
		"docs_count":      strconv.Itoa(item.Docs),
	}
	status := eskomStatus(item)
	relevanceInput := strings.Join([]string{title, summary, division, contractType}, " ")
	return models.Tender{
		SourceKey:           a.Key(),
		ExternalID:          strconv.Itoa(item.TenderID),
		Title:               title,
		Issuer:              division,
		Province:            province,
		Category:            division,
		TenderNumber:        strings.TrimSpace(item.Reference),
		PublishedDate:       eskomDate(item.PublishedDate),
		ClosingDate:         eskomSortableDateTime(item.ClosingDate),
		Status:              status,
		TenderType:          contractType,
		Scope:               strings.TrimSpace(item.ScopeDetails),
		Summary:             summary,
		OriginalURL:         fmt.Sprintf("%s/tender/%d", publicBase, item.TenderID),
		DocumentURL:         documentURL,
		EngineeringRelevant: score(relevanceInput) > 0.5,
		RelevanceScore:      score(relevanceInput),
		DocumentStatus:      models.ExtractionQueued,
		ExtractedFacts:      cloneFacts(facts),
		PageFacts:           facts,
		SourceMetadata:      sourceMetadata,
		Location: models.TenderLocation{
			DeliveryLocation: strings.TrimSpace(item.OfficeLocation),
			Province:         province,
		},
		Submission: models.TenderSubmission{
			Method:            eskomSubmissionMethod(item),
			Address:           strings.TrimSpace(item.Address),
			DeliveryLocation:  strings.TrimSpace(item.OfficeLocation),
			Instructions:      "Refer to the Eskom tender bulletin and tender documents for final submission requirements.",
			ElectronicAllowed: item.ETendering,
			PhysicalAllowed:   true,
		},
		Contacts:  eskomContacts(item),
		Documents: mappedDocs,
	}
}

func eskomBaseURLs(pageURL string) (string, string, error) {
	raw := strings.TrimSpace(pageURL)
	if raw == "" {
		return "", "", fmt.Errorf("eskom page url is required")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", err
	}
	if u.Scheme == "" || u.Host == "" {
		return "", "", fmt.Errorf("eskom page url must include scheme and host")
	}
	publicBase := u.Scheme + "://" + u.Host
	return publicBase + "/webapi/api", publicBase, nil
}

func eskomDocuments(publicBase string, docs []eskomDocument) []models.TenderDocument {
	if len(docs) == 0 {
		return nil
	}
	out := make([]models.TenderDocument, 0, len(docs))
	for _, doc := range docs {
		out = append(out, models.TenderDocument{
			URL:          fmt.Sprintf("%s/webapi/api/Files/GetFile?FileID=%d", publicBase, doc.ID),
			FileName:     strings.TrimSpace(doc.Name),
			MIMEType:     strings.TrimSpace(doc.ContentType),
			Role:         "support_document",
			Source:       "listing",
			LastModified: eskomDateTime(doc.CreatedDate),
			SizeBytes:    doc.Size,
		})
	}
	return out
}

func eskomContacts(item eskomTender) []models.TenderContact {
	if strings.TrimSpace(item.Email) == "" && strings.TrimSpace(item.Fax) == "" {
		return nil
	}
	return []models.TenderContact{{
		Role:  "listing_contact",
		Email: strings.TrimSpace(item.Email),
		Fax:   strings.TrimSpace(item.Fax),
	}}
}

func eskomSubmissionMethod(item eskomTender) string {
	if item.ETendering {
		return "electronic"
	}
	return "physical"
}

func eskomStatus(item eskomTender) string {
	text := strings.ToLower(strings.TrimSpace(item.HeaderDesc + " " + item.ScopeDetails + " " + item.Summary))
	if strings.Contains(text, "cancel") {
		return "cancelled"
	}
	if tenderstate.IsExpired(models.Tender{ClosingDate: eskomSortableDateTime(item.ClosingDate)}, time.Now().UTC()) {
		return "closed"
	}
	return "open"
}

func eskomDate(raw string) string {
	if parsed, ok := parseEskomTime(raw); ok {
		return parsed.Format("2006-01-02")
	}
	return strings.TrimSpace(raw)
}

func eskomSortableDateTime(raw string) string {
	if parsed, ok := parseEskomTime(raw); ok {
		return parsed.Format("2006-01-02 15:04")
	}
	return strings.TrimSpace(raw)
}

func eskomDateTime(raw string) string {
	if parsed, ok := parseEskomTime(raw); ok {
		return parsed.Format("2006-01-02 15:04")
	}
	return strings.TrimSpace(raw)
}

func parseEskomTime(raw string) (time.Time, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, false
	}
	layouts := []string{
		time.RFC3339Nano,
		"2006-01-02T15:04:05",
		"2006-01-02T15:04:05.000",
		"2006-01-02T15:04:05.00",
	}
	for _, layout := range layouts {
		if parsed, err := time.Parse(layout, raw); err == nil {
			return parsed.UTC(), true
		}
	}
	return time.Time{}, false
}
