package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/endorses/psst.zip/backend/internal/adminsecuritycli"
	"github.com/endorses/psst.zip/backend/internal/api"
	"github.com/endorses/psst.zip/backend/internal/cleanup"
	"github.com/endorses/psst.zip/backend/internal/config"
	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/incidentcli"
	"github.com/endorses/psst.zip/backend/internal/store"
)

func main() {
	cfg := config.Load()
	if len(os.Args) > 1 {
		if os.Args[1] == "admin-factor-reset" {
			if err := adminsecuritycli.Run(cfg.DBPath, os.Args[2:], os.Stdout); err != nil {
				fmt.Fprintln(os.Stderr, err)
				fmt.Fprint(os.Stderr, adminsecuritycli.Usage)
				os.Exit(1)
			}
			return
		}
		if len(os.Args) == 2 && (os.Args[1] == "--help" || os.Args[1] == "-h") {
			fmt.Fprint(os.Stdout, incidentcli.Usage)
			fmt.Fprint(os.Stdout, adminsecuritycli.Usage)
			return
		}
		if len(os.Args) != 2 {
			fmt.Fprint(os.Stderr, incidentcli.Usage)
			os.Exit(2)
		}
		if err := incidentcli.Run(cfg.DBPath, os.Args[1], os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			fmt.Fprint(os.Stderr, incidentcli.Usage)
			os.Exit(1)
		}
		return
	}

	// Ensure data directories exist.
	if err := os.MkdirAll(filepath.Dir(cfg.DBPath), 0o700); err != nil {
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
	if err := queries.RecoverTrafficLeases(); err != nil {
		log.Fatalf("recover traffic accounting: %v", err)
	}
	srv := api.NewServer(cfg, queries, fs)
	if err := srv.BootstrapAdmin(); err != nil {
		log.Fatalf("initialize accounts: %v", err)
	}

	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		MaxHeaderBytes:    32 * 1024,
		ReadTimeout:       0, // Per-IO deadlines permit large, progressing streams.
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go srv.RunIncidentMonitor(ctx)

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
