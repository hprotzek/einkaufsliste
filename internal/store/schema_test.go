package store_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/hprotzek/einkaufsliste/internal/dbtest"
	"github.com/hprotzek/einkaufsliste/internal/store"
	"github.com/hprotzek/einkaufsliste/migrations"
)

// Postgres error codes we assert on, rather than matching message text.
const (
	codeUniqueViolation     = "23505"
	codeForeignKeyViolation = "23503"
	codeNotNullViolation    = "23502"
)

// migratedPool returns a pool against a database with every migration applied.
func migratedPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	pool, err := store.NewPool(t.Context(), dbtest.New(t))
	if err != nil {
		t.Fatalf("opening pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if err := store.Migrate(t.Context(), pool, discardLogger()); err != nil {
		t.Fatalf("migrating: %v", err)
	}

	return pool
}

// pgxPoolAsSQL bridges the pool to database/sql, which is what goose takes.
// Closing the bridge does not close the pool.
func pgxPoolAsSQL(t *testing.T, pool *pgxpool.Pool) *sql.DB {
	t.Helper()

	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })

	return db
}

// assertPGCode fails unless err is a Postgres error with the given SQLSTATE.
func assertPGCode(t *testing.T, err error, want string) {
	t.Helper()

	if err == nil {
		t.Fatalf("expected a %s error, got nil", want)
	}

	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("expected a Postgres error, got %T: %v", err, err)
	}
	if pgErr.Code != want {
		t.Errorf("SQLSTATE = %s (%s), want %s", pgErr.Code, pgErr.Message, want)
	}
}

func insertUser(ctx context.Context, pool *pgxpool.Pool, email string) (string, error) {
	var id string
	err := pool.QueryRow(ctx,
		`INSERT INTO users (display_name, email) VALUES ($1, $2) RETURNING id`,
		"Test Person", email,
	).Scan(&id)
	return id, err
}

// Norwegian users have addresses with æ/ø/å in them, and Google will happily
// send the same address capitalised differently on different days. citext is
// what makes those the same account; a naive lower() would not be safe here.
func TestUserEmailIsUniqueCaseInsensitively(t *testing.T) {
	ctx := t.Context()
	pool := migratedPool(t)

	if _, err := insertUser(ctx, pool, "Kari.Nordmann@Example.NO"); err != nil {
		t.Fatalf("inserting first user: %v", err)
	}

	_, err := insertUser(ctx, pool, "kari.nordmann@example.no")
	assertPGCode(t, err, codeUniqueViolation)
}

func TestUserEmailPreservesNorwegianCharacters(t *testing.T) {
	ctx := t.Context()
	pool := migratedPool(t)

	const email = "SMØR.LØK@Example.no"
	if _, err := insertUser(ctx, pool, email); err != nil {
		t.Fatalf("inserting user: %v", err)
	}

	var stored string
	if err := pool.QueryRow(ctx, `SELECT email::text FROM users`).Scan(&stored); err != nil {
		t.Fatalf("reading back: %v", err)
	}

	// citext compares case-insensitively but must not mangle what was stored.
	if stored != email {
		t.Errorf("stored email = %q, want %q — the address was altered on write", stored, email)
	}
}

func TestUserEmailIsRequired(t *testing.T) {
	ctx := t.Context()
	pool := migratedPool(t)

	_, err := pool.Exec(ctx, `INSERT INTO users (display_name) VALUES ($1)`, "No Email")
	assertPGCode(t, err, codeNotNullViolation)
}

func TestUserEmailVerifiedDefaultsToFalse(t *testing.T) {
	ctx := t.Context()
	pool := migratedPool(t)

	if _, err := insertUser(ctx, pool, "unverified@example.com"); err != nil {
		t.Fatalf("inserting user: %v", err)
	}

	var verified bool
	if err := pool.QueryRow(ctx, `SELECT email_verified FROM users`).Scan(&verified); err != nil {
		t.Fatalf("reading back: %v", err)
	}

	// Defaulting to true would silently satisfy half of §9's linking rule for
	// every account ever created, which is an account-takeover vector.
	if verified {
		t.Error("email_verified defaulted to true; it must default to false")
	}
}

// (provider, subject) is the identity key, not the email (§9).
func TestIdentityIsUniquePerProviderAndSubject(t *testing.T) {
	ctx := t.Context()
	pool := migratedPool(t)

	userID, err := insertUser(ctx, pool, "linked@example.com")
	if err != nil {
		t.Fatalf("inserting user: %v", err)
	}

	insert := func(provider, subject string) error {
		_, err := pool.Exec(ctx,
			`INSERT INTO identities (user_id, provider, subject) VALUES ($1, $2, $3)`,
			userID, provider, subject)
		return err
	}

	if err := insert("google", "sub-123"); err != nil {
		t.Fatalf("inserting first identity: %v", err)
	}

	assertPGCode(t, insert("google", "sub-123"), codeUniqueViolation)

	// The same subject string from a different provider is a different person
	// as far as this table is concerned, and must be allowed.
	if err := insert("apple", "sub-123"); err != nil {
		t.Errorf("same subject under a different provider was rejected: %v", err)
	}
}

func TestIdentityRequiresAnExistingUser(t *testing.T) {
	ctx := t.Context()
	pool := migratedPool(t)

	_, err := pool.Exec(ctx,
		`INSERT INTO identities (user_id, provider, subject)
		 VALUES ('00000000-0000-0000-0000-000000000000', 'google', 'orphan')`)
	assertPGCode(t, err, codeForeignKeyViolation)
}

func TestDeletingAUserRemovesItsIdentities(t *testing.T) {
	ctx := t.Context()
	pool := migratedPool(t)

	userID, err := insertUser(ctx, pool, "cascade@example.com")
	if err != nil {
		t.Fatalf("inserting user: %v", err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO identities (user_id, provider, subject) VALUES ($1, 'google', 'sub-cascade')`,
		userID); err != nil {
		t.Fatalf("inserting identity: %v", err)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, userID); err != nil {
		t.Fatalf("deleting user: %v", err)
	}

	var remaining int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM identities`).Scan(&remaining); err != nil {
		t.Fatalf("counting identities: %v", err)
	}
	if remaining != 0 {
		t.Errorf("identities left behind = %d, want 0", remaining)
	}
}

// Task 1.1's done-when is "migration applies and rolls back". Production has
// no Down path on purpose — deploys are forward-only (non-negotiable 3) — so
// the rollback is exercised here, against the same embedded files.
func TestMigrationsRollBack(t *testing.T) {
	ctx := t.Context()
	pool := migratedPool(t)

	tableExists := func(name string) bool {
		var exists bool
		if err := pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables
			                WHERE table_schema = 'public' AND table_name = $1)`,
			name).Scan(&exists); err != nil {
			t.Fatalf("checking for table %s: %v", name, err)
		}
		return exists
	}

	for _, name := range []string{"users", "identities", "refresh_tokens"} {
		if !tableExists(name) {
			t.Fatalf("table %s missing after migrating up", name)
		}
	}

	db := pgxPoolAsSQL(t, pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatalf("preparing migrations: %v", err)
	}

	if _, err := provider.DownTo(ctx, 0); err != nil {
		t.Fatalf("rolling back: %v", err)
	}

	for _, name := range []string{"users", "identities", "refresh_tokens"} {
		if tableExists(name) {
			t.Errorf("table %s survived the rollback", name)
		}
	}

	// And forward again, so a down/up cycle is proven rather than assumed.
	if _, err := provider.Up(ctx); err != nil {
		t.Fatalf("re-applying: %v", err)
	}
	for _, name := range []string{"users", "identities", "refresh_tokens"} {
		if !tableExists(name) {
			t.Errorf("table %s missing after re-applying", name)
		}
	}
}
