package main

import (
	"context"
	"errors"
	"log"
	"openbid/internal/app"
	"os/signal"
	"syscall"
)

func main() {
	a, err := app.New()
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := a.Close(); err != nil {
			log.Printf("worker store close failed: %v", err)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("worker starting")
	if err := a.RunWorkerContext(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
	log.Printf("worker stopped")
}
