package source

import (
	"context"
	"tenderhub-za/internal/models"
)

type StubAdapter struct{ Source string }

func (s StubAdapter) Key() string { return s.Source }
func (s StubAdapter) Fetch(ctx context.Context) ([]models.Tender, string, error) {
	return []models.Tender{}, "stub adapter configured", nil
}
