package httptransport

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// healthResponse is the body of GET /healthz.
type healthResponse struct {
	Status string `json:"status"`
}

// handleHealthz reports process liveness only. It deliberately checks no
// dependencies: once Postgres is wired in (task 0.4) a database blip must not
// turn into a restart loop, and the uptime ping (spec §12.6) asks "is the
// process up", not "is everything well".
func handleHealthz(log *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		if err := json.NewEncoder(w).Encode(healthResponse{Status: "ok"}); err != nil {
			log.ErrorContext(r.Context(), "writing healthz response", slog.Any("error", err))
		}
	}
}
