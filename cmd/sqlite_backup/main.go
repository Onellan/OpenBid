package main

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"time"

	"tenderhub-za/internal/store"
)

func main() {
	dataPath := os.Getenv("DATA_PATH")
	if dataPath == "" {
		dataPath = "./data/store.db"
	}
	outputPath := ""
	if len(os.Args) > 1 {
		outputPath = os.Args[1]
	}
	if outputPath == "" {
		backupDir := os.Getenv("BACKUP_DIR")
		if backupDir == "" {
			backupDir = "./backups"
		}
		outputPath = filepath.Join(backupDir, "store-"+time.Now().UTC().Format("20060102-150405")+".db")
	}

	s, err := store.NewSQLiteStore(dataPath)
	if err != nil {
		log.Fatal(err)
	}
	defer s.Close()

	if err := s.BackupTo(context.Background(), outputPath); err != nil {
		log.Fatal(err)
	}
	log.Printf("sqlite backup created: %s", outputPath)
}
