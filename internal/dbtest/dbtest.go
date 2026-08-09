// Package dbtest hands tests a real, empty Postgres database.
//
// Never a mock (non-negotiable 9). DATABASE_URL wins when it is set, so CI
// reuses the service container it already started; otherwise a container is
// started once per test binary, which is what makes `go test ./...` work on a
// laptop with nothing but Docker running.
//
// It lives in its own package because more than one package needs it: the
// store tests, the auth tests, and every service test from M2 onwards.
package dbtest

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	// postgresImage matches the version production runs (spec §11.1).
	postgresImage = "postgres:16-alpine"

	startupTimeout = 2 * time.Minute
	cleanupTimeout = 30 * time.Second
)

var (
	once    sync.Once
	baseDSN string
	initErr error
)

// base resolves the Postgres every test database is created on, once per
// test binary.
func base(t *testing.T) string {
	t.Helper()

	once.Do(func() {
		if dsn := envDatabaseURL(); dsn != "" {
			baseDSN = dsn
			return
		}
		baseDSN, initErr = startContainer()
	})

	if initErr != nil {
		t.Fatalf("no Postgres available for tests: %v", initErr)
	}

	return baseDSN
}

func startContainer() (string, error) {
	ctx := context.Background()

	container, err := postgres.Run(ctx, postgresImage,
		postgres.WithDatabase("einkaufsliste_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			// Postgres announces readiness twice: once for its init scripts,
			// once for the real listener. Waiting for the first is a race.
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(startupTimeout),
		),
	)
	if err != nil {
		return "", fmt.Errorf("starting postgres: %w", err)
	}

	// The container deliberately outlives the test that triggered it: later
	// tests in the same binary reuse it. testcontainers' reaper removes it
	// when the binary exits, so nothing is left running.
	return container.ConnectionString(ctx, "sslmode=disable")
}

// New creates an empty database of its own and returns its DSN, so each test
// starts from nothing and no test can see another's schema. The database is
// dropped when the test ends.
func New(t *testing.T) string {
	t.Helper()

	ctx := t.Context()
	dsn := base(t)

	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connecting to the base database: %v", err)
	}
	defer admin.Close()

	name := fmt.Sprintf("test_%d", time.Now().UnixNano())

	// An identifier cannot be a bind parameter. The name is generated here
	// and never taken from input.
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		t.Fatalf("creating test database: %v", err)
	}

	t.Cleanup(func() { drop(t, dsn, name) })

	return forDatabase(t, dsn, name)
}

func drop(t *testing.T, baseDSN, name string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
	defer cancel()

	cleaner, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Logf("dropping test database %s: %v", name, err)
		return
	}
	defer cleaner.Close()

	if _, err := cleaner.Exec(ctx, fmt.Sprintf("DROP DATABASE IF EXISTS %q (FORCE)", name)); err != nil {
		t.Logf("dropping test database %s: %v", name, err)
	}
}

// forDatabase rewrites the database name in a URL-style DSN.
func forDatabase(t *testing.T, dsn, database string) string {
	t.Helper()

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DATABASE_URL must be a URL-style DSN, got %q: %v", dsn, err)
	}
	u.Path = "/" + database

	return u.String()
}

// envDatabaseURL is a function so the lookup happens when tests run rather
// than at package initialisation.
func envDatabaseURL() string { return os.Getenv("DATABASE_URL") }
