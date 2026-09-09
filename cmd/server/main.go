package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"tracker-proxy/config"
	"tracker-proxy/pkg/aggregator"
	"tracker-proxy/pkg/api"
	"tracker-proxy/pkg/auth"
	"tracker-proxy/pkg/cache"
	"tracker-proxy/pkg/flaresolverr"
	"tracker-proxy/pkg/tracker"
	"tracker-proxy/pkg/tracker/nnmclub"
	"tracker-proxy/pkg/tracker/rutor"
	"tracker-proxy/pkg/tracker/rutracker"
)

func main() {
	configPath := os.Getenv("CONFIG_PATH")
	if configPath == "" {
		configPath = "config.yaml"
	}

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Printf("Warning: failed to load config from %s: %v (using defaults)", configPath, err)
		cfg = config.DefaultConfig()
	}

	// Initialize Trackers
	var trackers []tracker.Tracker

	// 1. RuTor
	rutorTracker := rutor.New(cfg.Trackers.Rutor.BaseURL)
	rutorTracker.SetEnabled(cfg.Trackers.Rutor.Enabled)
	trackers = append(trackers, rutorTracker)
	log.Printf("Loaded tracker: %s (enabled: %v)", rutorTracker.Name(), rutorTracker.IsEnabled())

	// 2. NNM-Club
	nnmTracker := nnmclub.New(
		cfg.Trackers.NNMClub.BaseURL,
		cfg.Trackers.NNMClub.Username,
		cfg.Trackers.NNMClub.Password,
		cfg.Trackers.NNMClub.Cookie,
	)
	nnmTracker.SetEnabled(cfg.Trackers.NNMClub.Enabled)
	trackers = append(trackers, nnmTracker)
	log.Printf("Loaded tracker: %s (enabled: %v)", nnmTracker.Name(), nnmTracker.IsEnabled())

	// FlareSolverr Client
	var fsClient *flaresolverr.Client
	if cfg.FlareSolverr.Enabled {
		fsClient = flaresolverr.NewClient(cfg.FlareSolverr.URL, cfg.FlareSolverr.TimeoutSeconds)
		log.Printf("FlareSolverr client enabled at %s", cfg.FlareSolverr.URL)
	}

	// 3. RuTracker
	rutrackerTracker := rutracker.New(
		cfg.Trackers.RuTracker.BaseURL,
		cfg.Trackers.RuTracker.Username,
		cfg.Trackers.RuTracker.Password,
		cfg.Trackers.RuTracker.Cookie,
		cfg.Trackers.RuTracker.UserAgent,
		fsClient,
	)
	rutrackerTracker.SetEnabled(cfg.Trackers.RuTracker.Enabled)
	trackers = append(trackers, rutrackerTracker)
	log.Printf("Loaded tracker: %s (enabled: %v)", rutrackerTracker.Name(), rutrackerTracker.IsEnabled())

	// Prefetch RuTracker clearance in background if FlareSolverr is enabled
	if fsClient != nil && rutrackerTracker.IsEnabled() {
		go func() {
			log.Printf("Prefetching RuTracker Cloudflare clearance via FlareSolverr in background...")
			bgCtx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
			defer cancel()
			if err := rutrackerTracker.EnsureClearance(bgCtx); err != nil {
				log.Printf("Initial clearance prefetch notice: %v", err)
			}
		}()
	}

	// bbolt Cache Store
	var cacheStore *cache.Store
	if cfg.Cache.Enabled {
		ttl := time.Duration(cfg.Cache.TTLHours) * time.Hour
		cs, err := cache.New(cfg.Cache.Path, ttl)
		if err != nil {
			log.Printf("Warning: failed to initialize bbolt cache at %s: %v (caching disabled)", cfg.Cache.Path, err)
		} else {
			cacheStore = cs
			log.Printf("bbolt disk cache enabled at %s (TTL: %d hours)", cfg.Cache.Path, cfg.Cache.TTLHours)
		}
	}

	// Aggregator
	timeout := time.Duration(cfg.Server.TimeoutSeconds) * time.Second
	agg := aggregator.New(trackers, cacheStore, timeout)

	// Auth Manager
	authMgr := auth.NewManager(cfg.Auth.Enabled, cfg.Auth.Username, cfg.Auth.Password, cfg.Auth.Secret)
	if cfg.Auth.Enabled {
		log.Printf("Authentication enabled (username: %s)", cfg.Auth.Username)
	} else {
		log.Println("Authentication disabled")
	}

	// HTTP Server
	mux := http.NewServeMux()
	handler := api.NewHandler(agg, cacheStore, authMgr)
	handler.RegisterRoutes(mux)

	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Server.Port)
	server := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 20 * time.Second,
	}

	// Graceful shutdown handling
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		log.Printf("tracker-proxy listening on %s", addr)
		log.Printf("  REST API:    http://localhost:%d/api/search?q=...", cfg.Server.Port)
		log.Printf("  Torznab API: http://localhost:%d/api/v2.0/indexers/all/results/torznab/api?t=search&q=...", cfg.Server.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Server error: %v", err)
		}
	}()

	<-stop
	log.Println("Shutting down server gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}

	if cacheStore != nil {
		if err := cacheStore.Close(); err != nil {
			log.Printf("Error closing cache store: %v", err)
		}
	}

	log.Println("Server stopped.")
}
