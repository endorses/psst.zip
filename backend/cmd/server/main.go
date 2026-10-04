package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/cleanup"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func main() {
	cfg := config.Load()

	// Ensure data directories exist.
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o755); err != nil {
		log.Fatalf("create db directory: %v", err)
	}

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	fs, err := store.NewDiskStore(cfg.StoragePath)
	if err != nil {
		log.Fatalf("create file store: %v", err)
	}

	queries := database.NewQueries(db)
	srv := api.NewServer(cfg, queries, fs)
	if err := srv.BootstrapAdmin(); err != nil {
		log.Fatalf("initialize accounts: %v", err)
	}

	httpSrv := &http.Server{
		Addr:         cfg.ListenAddr,
		Handler:      srv.Router(),
		ReadTimeout:  0, // tus uploads can be large
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start cleanup worker.
	worker := cleanup.NewWorker(queries, fs, cfg.CleanupInterval)
	go worker.Run(ctx)

	// Graceful shutdown.
	shutdownDone := make(chan struct{})
	go func() {
		defer close(shutdownDone)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
		log.Println("shutting down...")
		cancel()

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown error: %v", err)
			_ = httpSrv.Close()
		}
		srv.WaitForRequests()
	}()

	log.Printf("listening on %s", cfg.ListenAddr)
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
	<-shutdownDone
}
