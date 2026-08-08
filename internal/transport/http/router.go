// Package httptransport builds the HTTP router and its middleware. It
// translates between HTTP and the business packages under internal/ and makes
// no decisions of its own (spec §5.3).
//
// The package is named httptransport rather than http so that callers — and
// every file in this package — can import net/http without an alias.
package httptransport

import (
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/hprotzek/einkaufsliste/internal/transport/http/openapi"
)

// server implements the generated openapi.ServerInterface.
//
// Embedding openapi.Unimplemented means an operation added to openapi.yaml
// compiles immediately and answers 501 until someone writes its handler,
// rather than the contract and the router silently disagreeing. Remove the
// embed once every operation is implemented, and the compiler starts
// enforcing completeness instead.
type server struct {
	openapi.Unimplemented

	log *slog.Logger
}

// NewRouter returns the API handler, with its routes built from
// api/openapi.yaml — the contract of record (non-negotiable 4). Paths are not
// written out here, so they cannot drift from the spec.
func NewRouter(log *slog.Logger) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	return openapi.HandlerFromMux(server{log: log}, r)
}
