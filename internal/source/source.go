package source

import (
	"context"
	"tenderhub-za/internal/models"
)

type Adapter interface {
	Key() string
	Fetch(context.Context) ([]models.Tender, string, error)
}
type Registry struct{ Adapters []Adapter }

func NewRegistry(adapters ...Adapter) Registry { return Registry{Adapters: adapters} }
