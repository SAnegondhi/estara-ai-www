package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/estara-ai/www/internal/api"
	"github.com/estara-ai/www/internal/config"
	"github.com/estara-ai/www/internal/db/postgres"
	redisClient "github.com/estara-ai/www/internal/db/redis"
)

func main() {
	// Set up structured logging
	logLevel := slog.LevelInfo
	if os.Getenv("ENV") == "development" {
		logLevel = slog.LevelDebug
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: logLevel,
	})
	slog.SetDefault(slog.New(handler))

	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Create context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect to databases
	db, err := postgres.NewDB(ctx, cfg)
	if err != nil {
		slog.Error("failed to connect to database", "error", err)
		os.Exit(1)
	}
	defer db.Close()

	// Connect to Redis (optional - continue if unavailable)
	var redis *redisClient.Client
	if cfg.Redis.URL != "" {
		redis, err = redisClient.NewClient(ctx, cfg.Redis)
		if err != nil {
			slog.Warn("failed to connect to Redis, continuing without caching", "error", err)
		} else {
			defer redis.Close()
		}
	}

	// Create application services
	services, err := api.NewServices(ctx, api.ServiceConfig{
		DB:     db,
		Redis:  redis,
		Config: cfg,
	})
	if err != nil {
		slog.Error("failed to initialize services", "error", err)
		os.Exit(1)
	}
	defer services.Close()

	// Create HTTP router
	router := api.NewRouter(ctx, api.RouterConfig{
		Config:   cfg,
		DB:       db,
		Redis:    redis,
		Services: services,
	})

	// Create HTTP server
	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Server.Port),
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
	}

	// Start server in goroutine
	go func() {
		slog.Info("starting server",
			"port", cfg.Server.Port,
			"env", cfg.Server.Env,
		)

		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			cancel()
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		slog.Info("received shutdown signal", "signal", sig)
	case <-ctx.Done():
		slog.Info("context cancelled")
	}

	// Graceful shutdown
	slog.Info("shutting down server...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("server shutdown error", "error", err)
	}

	slog.Info("server shutdown complete")
}
