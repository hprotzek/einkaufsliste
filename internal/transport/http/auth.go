package httptransport

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"

	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/hprotzek/einkaufsliste/internal/auth"
	"github.com/hprotzek/einkaufsliste/internal/store"
	"github.com/hprotzek/einkaufsliste/internal/transport/http/openapi"
)

// maxAuthBody bounds what an unauthenticated caller can make this server
// read. The real bodies here are a few hundred bytes.
const maxAuthBody = 8 << 10

// AuthCallback exchanges an authorisation code for a session.
func (s server) AuthCallback(w http.ResponseWriter, r *http.Request, provider string) {
	ctx := r.Context()

	var body openapi.AuthCallbackRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAuthBody)).Decode(&body); err != nil {
		s.writeProblem(w, r, http.StatusBadRequest, "the request body is not valid JSON")
		return
	}

	if body.Code == "" || body.CodeVerifier == "" || body.RedirectUri == "" || body.Nonce == "" {
		s.writeProblem(w, r, http.StatusBadRequest,
			"code, code_verifier, redirect_uri and nonce are all required")
		return
	}

	identity, err := s.exchanger.Exchange(ctx, auth.CodeExchange{
		Provider:     provider,
		Code:         body.Code,
		CodeVerifier: body.CodeVerifier,
		RedirectURI:  body.RedirectUri,
		Nonce:        body.Nonce,
	})
	if err != nil {
		// Logged in full, reported in one word. Which check failed is useful
		// to us and useful to an attacker, so it goes to the log only.
		s.log.WarnContext(ctx, "sign-in failed",
			slog.String("provider", provider),
			slog.Any("error", err),
		)

		if errors.Is(err, auth.ErrUnknownProvider) {
			s.writeProblem(w, r, http.StatusBadRequest, "unknown provider")
			return
		}
		s.writeProblem(w, r, http.StatusUnauthorized, "sign-in failed")
		return
	}

	user, err := s.accounts.Provision(ctx, identity)
	if err != nil {
		s.log.ErrorContext(ctx, "provisioning an account", slog.Any("error", err))
		s.writeProblem(w, r, http.StatusInternalServerError, "could not complete sign-in")
		return
	}

	session, err := s.sessions.Issue(ctx, user.ID.Bytes)
	if err != nil {
		s.log.ErrorContext(ctx, "issuing a session", slog.Any("error", err))
		s.writeProblem(w, r, http.StatusInternalServerError, "could not complete sign-in")
		return
	}

	s.log.InfoContext(ctx, "signed in",
		slog.String("provider", provider),
		slog.String("user_id", session.UserID.String()),
	)

	s.writeSession(w, r, session, &user)
}

// AuthRefresh rotates the refresh cookie.
func (s server) AuthRefresh(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	presented := refreshCookie(r)
	if presented == "" {
		s.writeProblem(w, r, http.StatusUnauthorized, "not signed in")
		return
	}

	session, err := s.sessions.Rotate(ctx, presented)
	if err != nil {
		// A replay is worth an alarm in the log. The response is the same
		// either way: telling the caller their token was recognised but spent
		// confirms it was genuine.
		if errors.Is(err, auth.ErrRefreshTokenReused) {
			s.log.WarnContext(ctx, "refresh token reused; the session family was revoked")
		}

		s.clearRefreshCookie(w)
		s.writeProblem(w, r, http.StatusUnauthorized, "not signed in")
		return
	}

	s.writeSession(w, r, session, nil)
}

// AuthLogout ends the session.
//
// It succeeds whether or not there was one, so a client can always reach a
// signed-out state — a logout that can fail leaves people stuck.
func (s server) AuthLogout(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if presented := refreshCookie(r); presented != "" {
		if err := s.sessions.RevokeByToken(ctx, presented); err != nil {
			// Logged, not reported: the cookie is cleared regardless, so the
			// client is signed out either way.
			s.log.WarnContext(ctx, "revoking on logout", slog.Any("error", err))
		}
	}

	s.clearRefreshCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

// writeSession returns the access token and sets the refresh cookie. The
// refresh token itself never appears in the body.
func (s server) writeSession(w http.ResponseWriter, r *http.Request, session auth.Session, user *store.User) {
	s.setRefreshCookie(w, session.Refresh)

	body := openapi.Session{
		AccessToken: session.Access.Token,
		ExpiresIn:   int(time.Until(session.Access.ExpiresAt).Round(time.Second).Seconds()),
	}

	if user != nil {
		body.User = apiUser(*user)
	} else {
		loaded, err := s.accounts.ByID(r.Context(), session.UserID)
		if err != nil {
			s.log.ErrorContext(r.Context(), "loading the user for a session", slog.Any("error", err))
			s.writeProblem(w, r, http.StatusInternalServerError, "could not complete sign-in")
			return
		}
		body.User = apiUser(loaded)
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.log.ErrorContext(r.Context(), "writing session response", slog.Any("error", err))
	}
}

// apiUser maps a stored user onto the contract's shape. Only the fields §4
// permits are exposed, and nothing else leaks by accident.
func apiUser(u store.User) openapi.User {
	return openapi.User{
		Id:          u.ID.Bytes,
		DisplayName: u.DisplayName,
		Email:       openapi_types.Email(u.Email),
		AvatarUrl:   u.AvatarUrl,
		CreatedAt:   u.CreatedAt.Time,
	}
}
