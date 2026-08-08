package store_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/hprotzek/einkaufsliste/internal/store"
)

// baseDSN points at a real Postgres for the whole package. Never a mock — see
// non-negotiable 9.
var baseDSN string

// TestMain resolves that Postgres once. DATABASE_URL wins when it is set, so
// CI reuses the service container it already started; otherwise a container is
// started here, which is what makes `go test ./...` work on a laptop with no
// setup beyond a running Docker.
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		baseDSN = dsn
		return m.Run()
	}

	ctx := context.Background()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("einkaufsliste_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			// Postgres logs readiness twice: once for the init scripts, once
			// for the real listener. Waiting for the first is a race.
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(2*time.Minute),
		),
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "starting postgres container: %v\n", err)
		return 1
	}
	defer func() {
		if terr := testcontainers.TerminateContainer(container); terr != nil {
			fmt.Fprintf(os.Stderr, "terminating postgres container: %v\n", terr)
		}
	}()

	baseDSN, err = container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reading container connection string: %v\n", err)
		return 1
	}

	return m.Run()
}

// newTestDB creates an empty database of its own and returns its DSN, so each
// test migrates from nothing and no test can see another's schema.
func newTestDB(t *testing.T) string {
	t.Helper()

	ctx := t.Context()

	admin, err := pgxpool.New(ctx, baseDSN)
	if err != nil {
		t.Fatalf("connecting to base database: %v", err)
	}
	defer admin.Close()

	name := fmt.Sprintf("test_%d", time.Now().UnixNano())

	// An identifier cannot be a bind parameter. The name is generated here,
	// never taken from input.
	if _, err := admin.Exec(ctx, fmt.Sprintf("CREATE DATABASE %q", name)); err != nil {
		t.Fatalf("creating test database: %v", err)
	}

	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cleaner, err := pgxpool.New(cleanupCtx, baseDSN)
		if err != nil {
			t.Logf("dropping test database %s: %v", name, err)
			return
		}
		defer cleaner.Close()

		if _, err := cleaner.Exec(cleanupCtx, fmt.Sprintf("DROP DATABASE IF EXISTS %q (FORCE)", name)); err != nil {
			t.Logf("dropping test database %s: %v", name, err)
		}
	})

	return dsnForDatabase(t, baseDSN, name)
}

// dsnForDatabase rewrites the database name in a URL-style DSN.
func dsnForDatabase(t *testing.T, dsn, database string) string {
	t.Helper()

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("DATABASE_URL must be a URL-style DSN, got %q: %v", dsn, err)
	}
	u.Path = "/" + database

	return u.String()
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestMigrateAppliesSchema(t *testing.T) {
	ctx := t.Context()

	pool, err := store.NewPool(ctx, newTestDB(t))
	if err != nil {
		t.Fatalf("opening pool: %v", err)
	}
	defer pool.Close()

	if err := store.Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	var version int64
	if err := pool.QueryRow(ctx, "SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatalf("reading schema version: %v", err)
	}
	if version != 1 {
		t.Errorf("schema version = %d, want 1", version)
	}

	// The migration's actual effect, not just its bookkeeping.
	var hasCitext bool
	if err := pool.QueryRow(ctx,
		"SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'citext')").Scan(&hasCitext); err != nil {
		t.Fatalf("checking citext extension: %v", err)
	}
	if !hasCitext {
		t.Error("citext extension is not installed")
	}
}

// Every container start runs migrations (spec §12.3), so applying them to an
// already-current database must be a no-op rather than an error. This is the
// property that makes run-on-start safe.
func TestMigrateIsIdempotent(t *testing.T) {
	ctx := t.Context()

	pool, err := store.NewPool(ctx, newTestDB(t))
	if err != nil {
		t.Fatalf("opening pool: %v", err)
	}
	defer pool.Close()

	if err := store.Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	if err := store.Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}

	var applied int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM goose_db_version WHERE version_id > 0").Scan(&applied); err != nil {
		t.Fatalf("counting applied migrations: %v", err)
	}
	if applied != 1 {
		t.Errorf("applied migrations = %d, want 1 — the second run re-applied something", applied)
	}
}

func TestNewPoolRejectsMalformedDSN(t *testing.T) {
	if _, err := store.NewPool(t.Context(), "://not a dsn"); err == nil {
		t.Error("expected an error for a malformed DSN, got nil")
	}
}
