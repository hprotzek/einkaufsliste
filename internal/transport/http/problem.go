package httptransport

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/hprotzek/einkaufsliste/internal/transport/http/openapi"
)

// problemContentType is RFC 9457, which spec §8 makes the error format for
// the whole API.
const problemContentType = "application/problem+json"

// writeProblem sends an RFC 9457 problem document.
//
// The detail is written for a person reading a client, not for whoever sent
// the request: auth failures in particular must not explain which check
// failed, because that tells an attacker what to change.
func (s server) writeProblem(w http.ResponseWriter, r *http.Request, status int, detail string) {
	problem := openapi.Problem{
		Title:  http.StatusText(status),
		Status: status,
	}
	if detail != "" {
		problem.Detail = &detail
	}

	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(problem); err != nil {
		s.log.ErrorContext(r.Context(), "writing problem response",
			slog.Int("status", status),
			slog.Any("error", err),
		)
	}
}
