package store_test

import (
	"io"
	"io/fs"
	"log/slog"
	"testing"

	"github.com/hprotzek/einkaufsliste/internal/dbtest"
	"github.com/hprotzek/einkaufsliste/internal/store"
	"github.com/hprotzek/einkaufsliste/migrations"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}

func TestMigrateAppliesSchema(t *testing.T) {
	ctx := t.Context()

	pool, err := store.NewPool(ctx, dbtest.New(t))
	if err != nil {
		t.Fatalf("opening pool: %v", err)
	}
	defer pool.Close()

	if err := store.Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	// Derived from the embedded files rather than hardcoded, so adding a
	// migration does not break a test that has nothing to do with it.
	want := countMigrations(t)

	var version int64
	if err := pool.QueryRow(ctx, "SELECT max(version_id) FROM goose_db_version").Scan(&version); err != nil {
		t.Fatalf("reading schema version: %v", err)
	}
	if version != want {
		t.Errorf("schema version = %d, want %d (one per file in migrations/)", version, want)
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

	pool, err := store.NewPool(ctx, dbtest.New(t))
	if err != nil {
		t.Fatalf("opening pool: %v", err)
	}
	defer pool.Close()

	countApplied := func(when string) int {
		var applied int
		if err := pool.QueryRow(ctx,
			"SELECT count(*) FROM goose_db_version WHERE version_id > 0").Scan(&applied); err != nil {
			t.Fatalf("counting applied migrations %s: %v", when, err)
		}
		return applied
	}

	if err := store.Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("first migrate: %v", err)
	}
	first := countApplied("after the first run")

	if err := store.Migrate(ctx, pool, discardLogger()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	second := countApplied("after the second run")

	// Idempotency is "the second run changed nothing", not any particular
	// count — which also keeps this honest as migrations are added.
	if second != first {
		t.Errorf("applied migrations went from %d to %d; the second run re-applied something", first, second)
	}
	if first != int(countMigrations(t)) {
		t.Errorf("applied %d migrations, but migrations/ holds %d", first, countMigrations(t))
	}
}

// countMigrations counts the embedded .sql files, which is what goose applies.
func countMigrations(t *testing.T) int64 {
	t.Helper()

	entries, err := fs.Glob(migrations.FS, "*.sql")
	if err != nil {
		t.Fatalf("listing migrations: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no migrations found; the embed pattern is wrong")
	}

	return int64(len(entries))
}

func TestNewPoolRejectsMalformedDSN(t *testing.T) {
	if _, err := store.NewPool(t.Context(), "://not a dsn"); err == nil {
		t.Error("expected an error for a malformed DSN, got nil")
	}
}
