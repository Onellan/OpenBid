package main

import (
	"log"
	"tenderhub-za/internal/app"
)

func main() {
	a, err := app.New()
	if err != nil {
		log.Fatal(err)
	}
	log.Fatal(a.Server.ListenAndServe())
}
