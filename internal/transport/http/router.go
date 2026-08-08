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
)

// NewRouter returns the API handler. Routes under /api/v1 (spec §8) are added
// as the milestones that own them land; /healthz sits at the root because the
// uptime ping targets it directly (spec §12.6).
func NewRouter(log *slog.Logger) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	r.Get("/healthz", handleHealthz(log))

	return r
}
