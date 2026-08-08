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
)

// config is everything the process reads from its environment. It stays in
// main deliberately — no config library, and internal/ never reads env vars.
type config struct {
	addr     string
	logLevel slog.Level
}

func loadConfig() config {
	return config{
		addr:     envOr("ADDR", defaultAddr),
		logLevel: parseLevel(os.Getenv("LOG_LEVEL")),
	}
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
	cfg := loadConfig()

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.logLevel}))
	slog.SetDefault(log)

	if err := run(cfg, log); err != nil {
		log.Error("server stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func run(cfg config, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              cfg.addr,
		Handler:           httptransport.NewRouter(log),
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
