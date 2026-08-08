package store

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/hprotzek/einkaufsliste/migrations"
)

// Migrate applies every pending migration in order and never rolls one back.
// Deploys are forward-only (non-negotiable 3, spec §12.3), so this package
// deliberately exposes no Down path — reverting is a new migration, not an
// undo.
//
// It runs at start-up, before the server accepts traffic, so a process that
// cannot reach its schema dies loudly instead of serving requests against a
// database it does not understand.
func Migrate(ctx context.Context, pool *pgxpool.Pool, log *slog.Logger) error {
	// Borrows the existing pool rather than opening a second connection.
	// Closing this handle does not close the pool — see stdlib.OpenDBFromPool.
	db := stdlib.OpenDBFromPool(pool)
	// Nothing to recover from: this only releases the bridge, and the pool it
	// borrows from outlives this call and is closed by the caller.
	defer func() { _ = db.Close() }()

	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		return fmt.Errorf("preparing migrations: %w", err)
	}

	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("applying migrations: %w", err)
	}

	for _, r := range results {
		log.InfoContext(ctx, "migration applied",
			slog.Int64("version", r.Source.Version),
			slog.String("source", r.Source.Path),
			slog.Duration("took", r.Duration),
		)
	}

	version, err := provider.GetDBVersion(ctx)
	if err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}

	log.InfoContext(ctx, "schema up to date",
		slog.Int64("version", version),
		slog.Int("applied", len(results)),
	)

	return nil
}
