package smartkeywords

import (
	"sort"
	"strings"

	"openbid/internal/models"
)

func NormalizeTerm(value string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(value)), " "))
}

func DisplayTerm(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func TenderSearchText(t models.Tender) string {
	parts := []string{
		t.Title, t.Summary, t.Excerpt, t.Scope, t.Issuer, t.Category, t.Province,
		t.TenderNumber, t.SourceKey, t.CIDBGrading, t.TenderType,
		t.Submission.Method, t.Submission.Address, t.Submission.DeliveryLocation, t.Submission.Instructions,
		t.Location.Site, t.Location.DeliveryLocation, t.Location.Street, t.Location.Suburb, t.Location.Town, t.Location.PostalCode, t.Location.Province,
		t.Evaluation.Method, t.Evaluation.Notes,
	}
	for _, contact := range t.Contacts {
		parts = append(parts, contact.Role, contact.Name, contact.Email, contact.Telephone, contact.Fax, contact.Mobile)
	}
	for _, briefing := range t.Briefings {
		parts = append(parts, briefing.Label, briefing.DateTime, briefing.Venue, briefing.Address, briefing.Notes)
	}
	for _, document := range t.Documents {
		parts = append(parts, document.FileName, document.Role, document.Source, document.URL)
	}
	for _, requirement := range t.Requirements {
		parts = append(parts, requirement.Category, requirement.Description)
	}
	appendFacts := func(facts map[string]string) {
		for key, value := range facts {
			parts = append(parts, key, value)
		}
	}
	appendFacts(t.PageFacts)
	appendFacts(t.DocumentFacts)
	appendFacts(t.ExtractedFacts)
	appendFacts(t.SourceMetadata)
	return NormalizeTerm(strings.Join(parts, "\n"))
}

func Evaluate(t models.Tender, enabled bool, groups []models.SmartKeywordGroup, keywords []models.SmartKeyword) models.SmartKeywordEvaluation {
	result := models.SmartKeywordEvaluation{Enabled: enabled}
	if !enabled {
		result.Accepted = true
		result.Reasons = []string{"No Filter mode is selected for source extraction."}
		return result
	}
	text := TenderSearchText(t)
	if strings.TrimSpace(text) == "" {
		result.Reasons = []string{"Tender has no searchable text."}
		return result
	}

	groupByID := map[string]models.SmartKeywordGroup{}
	for _, group := range groups {
		groupByID[group.ID] = normalizeGroup(group)
	}
	groupKeywords := map[string][]models.SmartKeyword{}
	standalone := []models.SmartKeyword{}
	for _, keyword := range keywords {
		keyword.Value = DisplayTerm(keyword.Value)
		if keyword.NormalizedValue == "" {
			keyword.NormalizedValue = NormalizeTerm(keyword.Value)
		}
		if keyword.NormalizedValue == "" || !keyword.Enabled {
			continue
		}
		if strings.TrimSpace(keyword.GroupID) == "" {
			standalone = append(standalone, keyword)
			continue
		}
		groupKeywords[keyword.GroupID] = append(groupKeywords[keyword.GroupID], keyword)
	}

	seenStandalone := map[string]bool{}
	for _, keyword := range standalone {
		result.ActiveKeywordCount++
		if strings.Contains(text, keyword.NormalizedValue) && !seenStandalone[keyword.NormalizedValue] {
			seenStandalone[keyword.NormalizedValue] = true
			result.StandaloneMatches = append(result.StandaloneMatches, keyword.Value)
			result.MatchedKeywords = append(result.MatchedKeywords, keyword.Value)
		}
	}

	for _, group := range groups {
		group = groupByID[group.ID]
		if !group.Enabled {
			continue
		}
		active := groupKeywords[group.ID]
		result.ActiveKeywordCount += len(active)
		evaluation := evaluateGroup(text, group, active)
		result.GroupMatches = append(result.GroupMatches, evaluation)
		if !evaluation.Accepted {
			continue
		}
		result.GroupTags = append(result.GroupTags, evaluation.TagName)
		for _, keyword := range evaluation.MatchedKeywords {
			result.MatchedKeywords = append(result.MatchedKeywords, keyword)
		}
	}

	result.StandaloneMatches = stableUnique(result.StandaloneMatches)
	result.MatchedKeywords = stableUnique(result.MatchedKeywords)
	result.GroupTags = stableUnique(result.GroupTags)
	result.Accepted = len(result.StandaloneMatches) > 0 || len(result.GroupTags) > 0
	if result.ActiveKeywordCount == 0 {
		result.Reasons = append(result.Reasons, "No active keywords are configured.")
	} else if result.Accepted {
		result.Reasons = append(result.Reasons, "Matched active Smart Keyword Extraction configuration.")
	} else {
		result.Reasons = append(result.Reasons, "No active keyword or group rule matched.")
	}
	return result
}

func normalizeGroup(group models.SmartKeywordGroup) models.SmartKeywordGroup {
	group.Name = strings.TrimSpace(group.Name)
	group.TagName = strings.TrimSpace(group.TagName)
	if group.TagName == "" {
		group.TagName = group.Name
	}
	if group.MatchMode != models.SmartMatchModeAll {
		group.MatchMode = models.SmartMatchModeAny
	}
	if group.MinMatchCount <= 0 {
		group.MinMatchCount = 1
	}
	return group
}

func evaluateGroup(text string, group models.SmartKeywordGroup, keywords []models.SmartKeyword) models.SmartGroupEvaluation {
	out := models.SmartGroupEvaluation{
		GroupID:       group.ID,
		GroupName:     group.Name,
		TagName:       group.TagName,
		MatchMode:     group.MatchMode,
		MinMatchCount: group.MinMatchCount,
		Priority:      group.Priority,
	}
	for _, raw := range group.ExcludeTerms {
		normalized := NormalizeTerm(raw)
		if normalized != "" && strings.Contains(text, normalized) {
			out.ExcludeMatches = append(out.ExcludeMatches, DisplayTerm(raw))
		}
	}
	if len(out.ExcludeMatches) > 0 {
		out.ExcludeMatches = stableUnique(out.ExcludeMatches)
		out.Reason = "Excluded by group exclude term."
		return out
	}
	seen := map[string]bool{}
	for _, keyword := range keywords {
		if keyword.NormalizedValue == "" {
			keyword.NormalizedValue = NormalizeTerm(keyword.Value)
		}
		if keyword.NormalizedValue == "" || seen[keyword.NormalizedValue] {
			continue
		}
		if strings.Contains(text, keyword.NormalizedValue) {
			seen[keyword.NormalizedValue] = true
			out.MatchedKeywords = append(out.MatchedKeywords, DisplayTerm(keyword.Value))
		}
	}
	out.MatchedKeywords = stableUnique(out.MatchedKeywords)
	switch group.MatchMode {
	case models.SmartMatchModeAll:
		out.Accepted = len(keywords) > 0 && len(out.MatchedKeywords) >= len(uniqueActiveKeywords(keywords))
	default:
		out.Accepted = len(out.MatchedKeywords) > 0
	}
	if group.MinMatchCount > 1 && len(out.MatchedKeywords) < group.MinMatchCount {
		out.Accepted = false
	}
	if out.Accepted {
		out.Reason = "Group rule matched."
		return out
	}
	if len(keywords) == 0 {
		out.Reason = "Group has no enabled keywords."
	} else {
		out.Reason = "Group rule did not meet match mode or minimum count."
	}
	return out
}

func uniqueActiveKeywords(keywords []models.SmartKeyword) []string {
	values := []string{}
	seen := map[string]bool{}
	for _, keyword := range keywords {
		key := keyword.NormalizedValue
		if key == "" {
			key = NormalizeTerm(keyword.Value)
		}
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		values = append(values, key)
	}
	return values
}

func stableUnique(values []string) []string {
	out := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		display := DisplayTerm(value)
		key := NormalizeTerm(display)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, display)
	}
	sort.Slice(out, func(i, j int) bool {
		if strings.EqualFold(out[i], out[j]) {
			return out[i] < out[j]
		}
		return strings.ToLower(out[i]) < strings.ToLower(out[j])
	})
	return out
}
