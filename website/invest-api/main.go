package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/debros/orama-website/invest-api/config"
	"github.com/debros/orama-website/invest-api/db"
	"github.com/debros/orama-website/invest-api/helius"
	"github.com/debros/orama-website/invest-api/router"
	"github.com/debros/orama-website/invest-api/verifier"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("database error: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatalf("migration error: %v", err)
	}

	if err := db.Seed(database); err != nil {
		log.Fatalf("seed error: %v", err)
	}

	heliusClient := helius.NewClient(cfg.HeliusAPIKey, cfg.HeliusRPCURL)
	handler := router.New(database, cfg.JWTSecret, heliusClient)

	// Start background verifier
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	v := verifier.New(database, heliusClient)
	go v.Run(ctx)

	// HTTP server with graceful shutdown
	srv := &http.Server{
		Addr:    ":" + cfg.Port,
		Handler: handler,
	}

	go func() {
		log.Printf("invest-api listening on :%s", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	cancel() // Stop verifier

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("shutdown error: %v", err)
	}

	log.Println("invest-api stopped")
}
