package source

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"tenderhub-za/internal/models"
	"time"
)

type FeedAdapter struct {
	SourceKey string
	FeedURL   string
	Client    *http.Client
}

func NewFeedAdapter(sourceKey, feedURL string) *FeedAdapter {
	return &FeedAdapter{SourceKey: NormalizeKey(sourceKey), FeedURL: strings.TrimSpace(feedURL), Client: &http.Client{Timeout: 30 * time.Second}}
}
func NewTreasuryAdapter(feedURL string) *FeedAdapter { return NewFeedAdapter("treasury", feedURL) }
func (a *FeedAdapter) Key() string {
	if a.SourceKey == "" {
		return "treasury"
	}
	return a.SourceKey
}
func score(s string) float64 {
	s = strings.ToLower(s)
	hits := 0
	for _, w := range []string{"engineering", "civil", "electrical", "mechanical", "roads", "water", "substation", "stormwater", "building"} {
		if strings.Contains(s, w) {
			hits++
		}
	}
	if hits == 0 {
		return 0.1
	}
	if hits > 9 {
		hits = 9
	}
	return float64(hits) / 9
}
func stringy(v any) string { return strings.TrimSpace(fmt.Sprint(v)) }
func (a *FeedAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	if a.FeedURL == "" {
		return []models.Tender{
			{SourceKey: a.Key(), ExternalID: "demo-001", Title: "Civil engineering maintenance services", Issuer: "City Infrastructure Unit", Province: "Gauteng", Category: "Civil Engineering", TenderNumber: "THZA-001", PublishedDate: "2026-04-01", ClosingDate: "2026-04-18", Status: "open", CIDBGrading: "6CE", Summary: "Term tender for civil maintenance and stormwater works.", OriginalURL: "https://example.org/tenders/1", DocumentURL: "https://example.org/docs/1.pdf", EngineeringRelevant: true, RelevanceScore: 0.94, DocumentStatus: models.ExtractionQueued, ExtractedFacts: map[string]string{}},
			{SourceKey: a.Key(), ExternalID: "demo-002", Title: "Electrical substation upgrade", Issuer: "Provincial Works", Province: "KwaZulu-Natal", Category: "Electrical Engineering", TenderNumber: "THZA-002", PublishedDate: "2026-03-28", ClosingDate: "2026-04-22", Status: "open", CIDBGrading: "7EP", Summary: "Upgrade and commissioning of distribution assets.", OriginalURL: "https://example.org/tenders/2", DocumentURL: "https://example.org/docs/2.html", EngineeringRelevant: true, RelevanceScore: 0.97, DocumentStatus: models.ExtractionQueued, ExtractedFacts: map[string]string{}},
		}, "loaded embedded sample feed", nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.FeedURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := a.Client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("feed returned %d", resp.StatusCode)
	}
	var payload struct {
		Releases []map[string]any `json:"releases"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, "", err
	}
	out := []models.Tender{}
	for _, r := range payload.Releases {
		rel := score(stringy(r["title"]) + " " + stringy(r["category"]) + " " + stringy(r["issuer"]))
		out = append(out, models.Tender{SourceKey: a.Key(), ExternalID: stringy(r["ocid"]), Title: stringy(r["title"]), Issuer: stringy(r["issuer"]), Province: stringy(r["province"]), Category: stringy(r["category"]), TenderNumber: stringy(r["tender_number"]), PublishedDate: stringy(r["published_date"]), ClosingDate: stringy(r["closing_date"]), Status: stringy(r["status"]), CIDBGrading: stringy(r["cidb_grading"]), Summary: stringy(r["summary"]), OriginalURL: stringy(r["original_url"]), DocumentURL: stringy(r["document_url"]), EngineeringRelevant: rel > 0.5, RelevanceScore: rel, DocumentStatus: models.ExtractionQueued, ExtractedFacts: map[string]string{}})
	}
	return out, "loaded remote feed", nil
}
