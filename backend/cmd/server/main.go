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
	"strings"
	"syscall"
	"time"

	"github.com/ondrej-li/DropPulse/backend/internal/auth"
	"github.com/ondrej-li/DropPulse/backend/internal/config"
	"github.com/ondrej-li/DropPulse/backend/internal/db"
	"github.com/ondrej-li/DropPulse/backend/internal/domain"
	"github.com/ondrej-li/DropPulse/backend/internal/events"
	"github.com/ondrej-li/DropPulse/backend/internal/httpapi"
	"github.com/ondrej-li/DropPulse/backend/internal/memstore"
	"github.com/ondrej-li/DropPulse/backend/internal/notify"
	"github.com/ondrej-li/DropPulse/backend/internal/store"
	"github.com/ondrej-li/DropPulse/backend/internal/watch"
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

	var (
		st        *store.Store
		repo      events.Repository
		adminSt   httpapi.AdminStore
		testUsers httpapi.TestUserStore
	)
	if cfg.UsesMemoryStore() {
		mem := memstore.New()
		repo = mem
		adminSt = mem
		testUsers = mem
		logger.Warn("using ephemeral in-memory store for local headless testing")
	} else {
		pool, openErr := db.Open(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
		if openErr != nil {
			return openErr
		}
		defer pool.Close()

		if cfg.RunMigrations {
			if err := db.Migrate(ctx, pool, logger); err != nil {
				return fmt.Errorf("migrate database: %w", err)
			}
		} else {
			logger.Warn("database migrations are disabled")
		}
		st = store.New(pool)
		repo = st
		adminSt = st
		testUsers = st
	}

	oauthClient := &http.Client{Timeout: 10 * time.Second}
	providers, err := auth.NewProviders(cfg, oauthClient)
	if err != nil {
		return fmt.Errorf("configure OAuth providers: %w", err)
	}
	if len(providers) == 0 && !cfg.SingleUserMode && !cfg.LocalTestAuth {
		logger.Warn("no OAuth providers configured; all sign-in attempts will fail")
	}
	authService := auth.NewService(cfg, st, providers)

	var admin domain.User
	if cfg.SingleUserMode {
		if len(providers) > 0 {
			logger.Warn("OAuth providers are configured but ignored in single-user mode")
		}
		admin, err = st.EnsureLocalAdmin(ctx, cfg.SingleUserName)
		if err != nil {
			return fmt.Errorf("ensure local admin: %w", err)
		}
	}
	if cfg.LocalTestAuth {
		logger.Warn("LOCAL_TEST_AUTH is enabled; loopback requests may skip OAuth and impersonate any user/role")
	}

	// A fixed recipient is a single-user convenience. Multi-user notices go to
	// the verified address on the account that owns the event.
	var mailer events.Mailer
	newMailer := func(recipient string) (*notify.Mailer, error) {
		if cfg.SMTPHost != "" {
			return notify.NewSMTP(notify.SMTPConfig{
				Host:     cfg.SMTPHost,
				Port:     cfg.SMTPPort,
				Username: cfg.SMTPUsername,
				Password: cfg.SMTPPassword,
				TLSMode:  cfg.SMTPTLS,
				Helo:     cfg.SMTPHelo,
			}, cfg.SMTPFrom, recipient, 10*time.Second)
		}
		return notify.New(cfg.SendmailPath, cfg.SMTPFrom, recipient, 10*time.Second)
	}
	switch {
	case cfg.SingleUserMode && cfg.NotificationEmail != "":
		mailer, err = newMailer(cfg.NotificationEmail)
		if err != nil {
			return fmt.Errorf("configure notifications: %w", err)
		}
	case !cfg.SingleUserMode && cfg.SMTPFrom != "":
		mailer, err = newMailer("")
		if err != nil {
			return fmt.Errorf("configure notifications: %w", err)
		}
	case cfg.SingleUserMode:
		logger.Warn("email notifications are disabled; set NOTIFICATION_EMAIL and SMTP_FROM (plus SMTP_HOST for sandboxed deployments) to enable them")
	default:
		logger.Warn("email notifications are disabled; set SMTP_FROM (and SMTP_HOST for sandboxed deployments) to email verified account addresses")
	}

	uiURL := cfg.WebAppURL.String()
	if !cfg.SingleUserMode && cfg.FrontendFile != "" {
		uiURL = strings.TrimRight(uiURL, "/") + "/app/"
	}

	eventsSvc := events.NewService(
		repo,
		watch.NewChecker(10*time.Second),
		mailer,
		logger,
		admin,
		uiURL,
	)
	mailTransport := "disabled"
	if mailer != nil {
		if cfg.SMTPHost != "" {
			mailTransport = "smtp"
		} else {
			mailTransport = "sendmail"
		}
	}
	if cfg.SingleUserMode {
		logger.Info("single-user mode enabled",
			"username", admin.Username,
			"notifications", mailer != nil,
			"mail_transport", mailTransport,
		)
	} else {
		logger.Info("multi-user mode enabled",
			"notifications", mailer != nil,
			"mail_transport", mailTransport,
		)
	}

	api, err := httpapi.New(cfg, st, authService, eventsSvc, logger)
	if err != nil {
		return err
	}
	api.SetStores(adminSt, testUsers)

	srv := &http.Server{
		Addr:              net.JoinHostPort(cfg.BindAddr, cfg.Port),
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      45 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
		ErrorLog:          slog.NewLogLogger(handler, slog.LevelError),
	}

	if st != nil {
		go purgeExpiredFlows(ctx, st, logger)
	}
	go eventsSvc.Run(ctx)
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
		} else if n > 0 {
			logger.Info("purged expired OAuth flows", "count", n)
		}

		pruned, err := st.PurgeOldEventChecks(ctx, 90*24*time.Hour)
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				logger.Warn("purge old event checks", "err", err)
			}
		} else if pruned > 0 {
			logger.Info("purged old event checks", "count", pruned)
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
