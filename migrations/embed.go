// Package migrations embeds the goose SQL files so they travel inside the
// binary.
//
// This is not a convenience. The production image is FROM scratch with only
// the binary and CA certs (spec §12.6), so there is no goose binary, no shell
// and no filesystem to read .sql files from. Embedding is therefore the only
// way migrations can run on container start as §12.3 requires.
//
// The Go file lives here, next to the migrations, because go:embed cannot
// reach outside its own directory. The directory itself is unchanged from the
// layout in spec §5.3.
package migrations

import "embed"

// FS holds every migration in this directory, applied in filename order.
//
//go:embed *.sql
var FS embed.FS
