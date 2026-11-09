package main

import (
	"log"

	"atop-companion/storage"
	"atop-companion/ui"
)

func main() {
	err := storage.InitializeStorage()
	if err != nil {
		log.Fatalf("Critical Error setting up local bbolt DB: %v", err)
	}
	defer storage.CloseStorage()
	_ = storage.PruneOldLogs()

	ui.Run()
}
