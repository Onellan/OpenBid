package main

import (
	"context"
	"log"
	"openbid/internal/store"
	"os"
)

func main() {
	path := os.Getenv("DATA_PATH")
	if path == "" {
		path = "./data/store.db"
	}
	s, err := store.NewSQLiteStore(path)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()
	if err := s.ValidateRuntime(context.Background()); err != nil {
		log.Fatal(err)
	}
	log.Printf("sqlite runtime validation passed: %s", path)
}
