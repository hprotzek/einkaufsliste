// Command api is the shopping list HTTP service. This file is wiring only:
// config, dependencies, serve (spec §5.3).
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/hprotzek/einkaufsliste/internal/auth"
	"github.com/hprotzek/einkaufsliste/internal/store"
	httptransport "github.com/hprotzek/einkaufsliste/internal/transport/http"
)

const (
	defaultAddr = ":8080"

	readHeaderTimeout = 5 * time.Second
	readTimeout       = 15 * time.Second
	writeTimeout      = 15 * time.Second
	idleTimeout       = 60 * time.Second

	// shutdownTimeout bounds how long in-flight requests get to finish. Long
	// enough for any request this service makes, short enough that a container
	// restart is not a wait.
	shutdownTimeout = 10 * time.Second

	// startupTimeout bounds connecting to Postgres and applying migrations.
	// Generous, because a migration is allowed to take a while; bounded, so a
	// wedged database surfaces as a failed start rather than a silent hang.
	startupTimeout = 2 * time.Minute
)

// config is everything the process reads from its environment. It stays in
// main deliberately — no config library, and internal/ never reads env vars.
type config struct {
	addr        string
	logLevel    slog.Level
	databaseURL string

	// signingKey signs this server's own access tokens. Required, with no
	// default: a shipped default is a key everybody has.
	signingKey []byte

	// providers is the OIDC configuration. R1 supplies Google; nothing in
	// the code assumes it (non-negotiable 10).
	providers []auth.ExchangeConfig

	// secureCookies marks the refresh cookie Secure. Only a local stack on
	// plain HTTP has any reason to turn it off.
	secureCookies bool
}

func loadConfig() (config, error) {
	// Required, with no fallback. A default pointing at localhost would let a
	// misconfigured deploy start up and quietly serve the wrong database.
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return config{}, errors.New("DATABASE_URL is required")
	}

	signingKey := os.Getenv("AUTH_SIGNING_KEY")
	if signingKey == "" {
		return config{}, errors.New("AUTH_SIGNING_KEY is required")
	}

	providers, err := oidcProviders()
	if err != nil {
		return config{}, err
	}

	return config{
		addr:          envOr("ADDR", defaultAddr),
		logLevel:      parseLevel(os.Getenv("LOG_LEVEL")),
		databaseURL:   databaseURL,
		signingKey:    []byte(signingKey),
		providers:     providers,
		secureCookies: envOr("COOKIE_SECURE", "true") != "false",
	}, nil
}

// oidcProviders reads provider configuration from the environment. Adding a
// second provider means another block here and nothing else.
func oidcProviders() ([]auth.ExchangeConfig, error) {
	clientID := os.Getenv("GOOGLE_CLIENT_ID")
	clientSecret := os.Getenv("GOOGLE_CLIENT_SECRET")

	if clientID == "" || clientSecret == "" {
		return nil, errors.New("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are required")
	}

	return []auth.ExchangeConfig{{
		ProviderConfig: auth.ProviderConfig{
			Name:     "google",
			Issuer:   envOr("GOOGLE_ISSUER", "https://accounts.google.com"),
			ClientID: clientID,
		},
		ClientSecret: clientSecret,
	}}, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// parseLevel maps LOG_LEVEL onto a slog level, defaulting to info for anything
// empty or unrecognised — a typo in an env var should not silence the logs.
func parseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func main() {
	// Probe mode, before any config is read: the health probe needs neither a
	// database nor a logger, and must not fail because one is misconfigured.
	if len(os.Args) > 1 && os.Args[1] == healthcheckArg {
		os.Exit(runHealthcheck())
	}

	cfg, err := loadConfig()
	if err != nil {
		// The logger is not built yet, and its level comes from the config we
		// just failed to read.
		slog.Error("invalid configuration", slog.Any("error", err))
		os.Exit(1)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.logLevel}))
	slog.SetDefault(log)

	if err := run(cfg, log); err != nil {
		log.Error("server stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(cfg config, log *slog.Logger) error {
	startupCtx, cancelStartup := context.WithTimeout(context.Background(), startupTimeout)
	defer cancelStartup()

	pool, err := store.NewPool(startupCtx, cfg.databaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	// Before the listener opens, so the process never serves traffic against a
	// schema it has not migrated (spec §12.3).
	if migrateErr := store.Migrate(startupCtx, pool, log); migrateErr != nil {
		return migrateErr
	}

	tokens, err := auth.NewTokenIssuer(cfg.signingKey, nil)
	if err != nil {
		return err
	}

	// Discovery reaches the provider, so a misconfigured deployment fails
	// here rather than on the first person trying to sign in.
	exchanger, err := auth.NewExchanger(startupCtx, cfg.providers)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr: cfg.addr,
		Handler: httptransport.NewRouter(httptransport.Deps{
			Log:           log,
			Exchanger:     exchanger,
			Accounts:      auth.NewAccounts(pool, log),
			Sessions:      auth.NewSessions(pool, tokens),
			SecureCookies: cfg.secureCookies,
		}),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	// SIGTERM is what the container runtime sends; SIGINT is Ctrl-C in dev.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		log.Info("listening", slog.String("addr", cfg.addr))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		log.Info("shutdown signal received", slog.Duration("grace", shutdownTimeout))
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}

	log.Info("shutdown complete")
	return nil
}
