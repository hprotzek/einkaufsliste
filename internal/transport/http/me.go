package httptransport

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/hprotzek/einkaufsliste/internal/auth"
)

// bearerPrefix is the only authorization scheme this API accepts.
const bearerPrefix = "Bearer "

// authenticate resolves the caller from the Authorization header.
//
// This is a helper rather than middleware because exactly one endpoint needs
// it today. It becomes middleware at M2, where the two-hop authorisation
// check (§6.1) has to run on every list route and doing it per-handler would
// be the kind of repetition that eventually gets forgotten on one of them.
func (s server) authenticate(r *http.Request) (uuid.UUID, error) {
	header := r.Header.Get("Authorization")
	if !strings.HasPrefix(header, bearerPrefix) {
		return uuid.Nil, auth.ErrInvalidAccessToken
	}

	subject, err := s.tokens.ParseAccessToken(strings.TrimPrefix(header, bearerPrefix))
	if err != nil {
		return uuid.Nil, err
	}

	// The subject is a uuid this server put there, so a parse failure means
	// something is badly wrong rather than merely unauthorised.
	id, err := uuid.Parse(subject)
	if err != nil {
		return uuid.Nil, auth.ErrInvalidAccessToken
	}

	return id, nil
}

// GetMe returns the signed-in user.
func (s server) GetMe(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, err := s.authenticate(r)
	if err != nil {
		// Expiry is the one case the client acts on differently: it means
		// "refresh and retry", not "sign in again". The header says so; the
		// body still does not elaborate.
		if errors.Is(err, auth.ErrExpiredAccessToken) {
			w.Header().Set("WWW-Authenticate", `Bearer error="invalid_token", error_description="expired"`)
		} else {
			w.Header().Set("WWW-Authenticate", "Bearer")
		}

		s.writeProblem(w, r, http.StatusUnauthorized, "not signed in")
		return
	}

	user, err := s.accounts.ByID(ctx, userID)
	if err != nil {
		// A valid token for a user that no longer exists is a deleted
		// account, not a server fault.
		s.log.WarnContext(ctx, "no user for a valid access token",
			slog.String("user_id", userID.String()),
			slog.Any("error", err),
		)
		s.writeProblem(w, r, http.StatusUnauthorized, "not signed in")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(apiUser(user)); err != nil {
		s.log.ErrorContext(ctx, "writing /me response", slog.Any("error", err))
	}
}
