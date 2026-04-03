package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	path := "/tmp/tenderhub-worker-heartbeat"
	if len(os.Args) > 1 && strings.TrimSpace(os.Args[1]) != "" {
		path = strings.TrimSpace(os.Args[1])
	}
	maxAgeSeconds := 180
	if len(os.Args) > 2 && strings.TrimSpace(os.Args[2]) != "" {
		parsed, err := strconv.Atoi(strings.TrimSpace(os.Args[2]))
		if err != nil || parsed <= 0 {
			log.Fatalf("invalid max age seconds: %q", os.Args[2])
		}
		maxAgeSeconds = parsed
	}

	info, err := os.Stat(path)
	if err != nil {
		log.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Duration(maxAgeSeconds)*time.Second {
		log.Fatalf("worker heartbeat stale: %s", path)
	}
	log.Printf("worker heartbeat healthy: %s", path)
}
