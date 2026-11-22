// Command server runs the DropPulse API.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/auth"
	"github.com/ondrej-li/DropPulse/backend/internal/config"
	"github.com/ondrej-li/DropPulse/backend/internal/db"
	"github.com/ondrej-li/DropPulse/backend/internal/httpapi"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
)

func main() {
	if err := run(); err != nil {
		slog.Error("backend stopped", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load configuration: %w", err)
	}
	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel})
	logger := slog.New(handler).With("service", "droppulse-backend", "env", cfg.Environment)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.RunMigrations {
		if err := db.Migrate(ctx, pool, logger); err != nil {
			return fmt.Errorf("migrate database: %w", err)
		}
	} else {
		logger.Warn("database migrations are disabled")
	}

	st := store.New(pool)
	oauthClient := &http.Client{Timeout: 10 * time.Second}
	providers, err := auth.NewProviders(cfg, oauthClient)
	if err != nil {
		return fmt.Errorf("configure OAuth providers: %w", err)
	}
	if len(providers) == 0 {
		logger.Warn("no OAuth providers configured; all sign-in attempts will fail")
	}
	authService := auth.NewService(cfg, st, providers)
	api := httpapi.New(cfg, st, authService, logger)

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.BindAddr, cfg.Port),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(handler, slog.LevelError),
	}

	go purgeExpiredFlows(ctx, st, logger)
	errCh := make(chan error, 1)
	go func() {
		logger.Info("DropPulse backend listening", "addr", srv.Addr, "providers", cfg.ProviderNames())
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown requested")
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP: %w", err)
		}
		return nil
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}

func purgeExpiredFlows(ctx context.Context, st *store.Store, logger *slog.Logger) {
	purge := func() {
		n, err := st.DeleteExpiredOAuthFlows(ctx)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Warn("purge expired OAuth flows", "err", err)
			}
			return
		}
		if n > 0 {
			logger.Info("purged expired OAuth flows", "count", n)
		}
	}
	purge()
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			purge()
		}
	}
}
