package tenderstate

import (
	"strings"
	"time"

	"openbid/internal/models"
)

const (
	ExpiredSkipReason       = "skipped_due_to_expired_tender"
	ExpiredEvaluationSource = "closing_date"
)

func ParseClosingTime(value string, now time.Time) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	location := now.Location()
	formats := []struct {
		layout   string
		dateOnly bool
	}{
		{time.RFC3339Nano, false},
		{time.RFC3339, false},
		{"2006-01-02 15:04:05", false},
		{"2006-01-02 15:04", false},
		{"2006/01/02 15:04:05", false},
		{"2006/01/02 15:04", false},
		{"02/01/2006 15:04:05", false},
		{"02/01/2006 15:04", false},
		{"2006-01-02", true},
		{"2006/01/02", true},
		{"02/01/2006", true},
		{"02 Jan 2006", true},
		{"02 January 2006", true},
	}
	for _, format := range formats {
		parsed, err := time.ParseInLocation(format.layout, value, location)
		if err != nil {
			continue
		}
		if format.dateOnly {
			parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, location)
		}
		return parsed, true
	}
	return time.Time{}, false
}

func IsExpired(tender models.Tender, now time.Time) bool {
	closingAt, ok := ParseClosingTime(tender.ClosingDate, now)
	if !ok {
		return false
	}
	return !closingAt.After(now)
}

func MarkExtractionSkipped(tender *models.Tender, now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	tender.DocumentStatus = models.ExtractionSkipped
	tender.ExtractionSkippedReason = ExpiredSkipReason
	tender.ExtractionSkippedSource = ExpiredEvaluationSource
	tender.ExtractionSkippedAt = now.UTC()
}

func MarkJobSkipped(job *models.ExtractionJob, now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	job.State = models.ExtractionSkipped
	job.LastError = "Skipped because the tender closing date/time has passed."
	job.SkipReason = ExpiredSkipReason
	job.SkipSource = ExpiredEvaluationSource
	job.SkippedAt = now.UTC()
	job.NextAttemptAt = time.Time{}
}
