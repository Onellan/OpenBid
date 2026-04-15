package tenderstate

import (
	"testing"
	"time"

	"openbid/internal/models"
)

func TestIsExpiredUsesSharedClosingDateRule(t *testing.T) {
	now := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		closing string
		expired bool
	}{
		{name: "past date", closing: "2026-04-10", expired: true},
		{name: "past time", closing: "2026-04-11 11:59", expired: true},
		{name: "same date only stays open until end of day", closing: "2026-04-11", expired: false},
		{name: "future date", closing: "2026-04-12", expired: false},
		{name: "missing date", closing: "", expired: false},
		{name: "unparseable date", closing: "not a date", expired: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsExpired(models.Tender{ClosingDate: tc.closing}, now); got != tc.expired {
				t.Fatalf("IsExpired(%q) = %v, want %v", tc.closing, got, tc.expired)
			}
		})
	}
}

func TestMarkSkippedMetadata(t *testing.T) {
	now := time.Date(2026, 4, 11, 12, 0, 0, 0, time.UTC)
	tender := models.Tender{ID: "expired"}
	MarkExtractionSkipped(&tender, now)
	if tender.DocumentStatus != models.ExtractionSkipped || tender.ExtractionSkippedReason != ExpiredSkipReason || tender.ExtractionSkippedSource != ExpiredEvaluationSource || !tender.ExtractionSkippedAt.Equal(now) {
		t.Fatalf("unexpected skipped tender metadata: %#v", tender)
	}
	job := models.ExtractionJob{ID: "job", State: models.ExtractionQueued, NextAttemptAt: now.Add(time.Hour)}
	MarkJobSkipped(&job, now)
	if job.State != models.ExtractionSkipped || job.SkipReason != ExpiredSkipReason || job.SkipSource != ExpiredEvaluationSource || !job.NextAttemptAt.IsZero() || !job.SkippedAt.Equal(now) {
		t.Fatalf("unexpected skipped job metadata: %#v", job)
	}
}
