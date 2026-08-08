package httptransport

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/hprotzek/einkaufsliste/internal/transport/http/openapi"
)

// healthOK is the only value the schema allows, so it is built once rather
// than per request.
var healthOK = openapi.Health{Status: openapi.Ok}

// GetHealth reports process liveness only. It deliberately checks no
// dependencies: a database blip must not turn into a restart loop, and the
// uptime ping (spec §12.6) asks "is the process up", not "is everything well".
func (s server) GetHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(healthOK); err != nil {
		s.log.ErrorContext(r.Context(), "writing healthz response", slog.Any("error", err))
	}
}
