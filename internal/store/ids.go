package store

import "github.com/google/uuid"

func newid() string {
	return uuid.NewString()
}
