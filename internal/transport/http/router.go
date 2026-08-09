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

	"github.com/hprotzek/einkaufsliste/internal/auth"
	"github.com/hprotzek/einkaufsliste/internal/transport/http/openapi"
)

// Deps are the things the handlers translate between. Every one of them is
// an interface-free concrete type from internal/, because the transport
// layer has no business abstracting over its own application.
type Deps struct {
	Log       *slog.Logger
	Exchanger *auth.Exchanger
	Accounts  *auth.Accounts
	Sessions  *auth.Sessions

	// SecureCookies marks the refresh cookie Secure. True everywhere real:
	// only a local stack served over plain HTTP has any reason to turn it
	// off, and doing so in production would put the refresh token on the
	// wire in the clear.
	SecureCookies bool
}

// server implements the generated openapi.ServerInterface.
//
// Embedding openapi.Unimplemented means an operation added to openapi.yaml
// compiles immediately and answers 501 until someone writes its handler,
// rather than the contract and the router silently disagreeing. Remove the
// embed once every operation is implemented, and the compiler starts
// enforcing completeness instead.
type server struct {
	openapi.Unimplemented

	log           *slog.Logger
	exchanger     *auth.Exchanger
	accounts      *auth.Accounts
	sessions      *auth.Sessions
	secureCookies bool
}

// NewRouter returns the API handler, with its routes built from
// api/openapi.yaml — the contract of record (non-negotiable 4). Paths are not
// written out here, so they cannot drift from the spec.
func NewRouter(deps Deps) http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)

	return openapi.HandlerFromMux(server{
		log:           deps.Log,
		exchanger:     deps.Exchanger,
		accounts:      deps.Accounts,
		sessions:      deps.Sessions,
		secureCookies: deps.SecureCookies,
	}, r)
}
