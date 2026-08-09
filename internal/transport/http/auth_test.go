package httptransport_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/hprotzek/einkaufsliste/internal/auth"
	"github.com/hprotzek/einkaufsliste/internal/auth/oidctest"
	"github.com/hprotzek/einkaufsliste/internal/dbtest"
	"github.com/hprotzek/einkaufsliste/internal/store"
	httptransport "github.com/hprotzek/einkaufsliste/internal/transport/http"
)

const (
	clientID     = "test-client-id"
	provider     = "google"
	nonce        = "a-nonce"
	codeVerifier = "a-pkce-verifier-long-enough-to-be-realistic"
	redirectURI  = "https://example.test/callback"
)

// stack is the whole thing: real router, real database, fake provider.
type stack struct {
	server *httptest.Server
	issuer *oidctest.Issuer
	client *http.Client
}

func newStack(t *testing.T) *stack {
	t.Helper()

	ctx := t.Context()
	log := slog.New(slog.NewJSONHandler(io.Discard, nil))

	pool, err := store.NewPool(ctx, dbtest.New(t))
	if err != nil {
		t.Fatalf("opening pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if migrateErr := store.Migrate(ctx, pool, log); migrateErr != nil {
		t.Fatalf("migrating: %v", migrateErr)
	}

	issuer := oidctest.New(t)

	// The exchanger keeps the client it discovered with, which is how it
	// reaches the fake issuer for the token endpoint and JWKS later.
	exchanger, err := auth.NewExchanger(
		oidc.ClientContext(ctx, issuer.Client()),
		[]auth.ExchangeConfig{{
			ProviderConfig: auth.ProviderConfig{Name: provider, Issuer: issuer.URL(), ClientID: clientID},
			ClientSecret:   "test-client-secret",
		}},
	)
	if err != nil {
		t.Fatalf("building exchanger: %v", err)
	}

	tokens, err := auth.NewTokenIssuer(bytes.Repeat([]byte("k"), 32), nil)
	if err != nil {
		t.Fatalf("building token issuer: %v", err)
	}

	srv := httptest.NewServer(httptransport.NewRouter(httptransport.Deps{
		Log:       log,
		Exchanger: exchanger,
		Accounts:  auth.NewAccounts(pool, log),
		Sessions:  auth.NewSessions(pool, tokens),
		Tokens:    tokens,
		// The test server speaks plain HTTP, so a Secure cookie would never
		// come back. Production defaults to true.
		SecureCookies: false,
	}))
	t.Cleanup(srv.Close)

	// A cookie jar, so the test behaves like a browser rather than juggling
	// headers by hand — which is the point of testing the cookie flow at all.
	return &stack{server: srv, issuer: issuer, client: srv.Client()}
}

type sessionBody struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
	User        struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	} `json:"user"`
}

// signIn drives the whole callback: the fake provider issues a code, the
// client posts it, the server exchanges and verifies it.
func (s *stack) signIn(t *testing.T, mutate func(*oidctest.Token)) (*http.Response, sessionBody) {
	t.Helper()

	tok := s.issuer.ValidToken(clientID)
	tok.Nonce = nonce
	if mutate != nil {
		mutate(&tok)
	}

	code := s.issuer.IssueCode(t, tok, codeVerifier)

	body, err := json.Marshal(map[string]string{
		"code":          code,
		"code_verifier": codeVerifier,
		"redirect_uri":  redirectURI,
		"nonce":         nonce,
	})
	if err != nil {
		t.Fatalf("encoding request: %v", err)
	}

	res, err := s.client.Post(
		s.server.URL+"/api/v1/auth/oidc/"+provider+"/callback",
		"application/json",
		bytes.NewReader(body),
	)
	if err != nil {
		t.Fatalf("posting callback: %v", err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })

	var decoded sessionBody
	if res.StatusCode == http.StatusOK {
		if err := json.NewDecoder(res.Body).Decode(&decoded); err != nil {
			t.Fatalf("decoding session: %v", err)
		}
	}

	return res, decoded
}

func refreshCookieFrom(t *testing.T, res *http.Response) *http.Cookie {
	t.Helper()

	for _, c := range res.Cookies() {
		if c.Name == "refresh_token" {
			return c
		}
	}
	t.Fatal("no refresh_token cookie in the response")
	return nil
}

func (s *stack) post(t *testing.T, path string, cookie *http.Cookie) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, s.server.URL+path, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}

	res, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("posting %s: %v", path, err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}

// Task 1.6's done-when: the whole flow, end to end, against the fake issuer.
func TestSignInRefreshLogout(t *testing.T) {
	s := newStack(t)

	// 1. Sign in.
	res, session := s.signIn(t, nil)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("callback status = %d, want 200", res.StatusCode)
	}
	if session.AccessToken == "" {
		t.Error("no access token in the response")
	}
	if session.User.Email != "kari.nordmann@example.no" {
		t.Errorf("email = %q, want the address from the ID token", session.User.Email)
	}
	if session.ExpiresIn <= 0 || session.ExpiresIn > int(auth.AccessTokenTTL.Seconds()) {
		t.Errorf("expires_in = %d, want up to %d", session.ExpiresIn, int(auth.AccessTokenTTL.Seconds()))
	}

	// The refresh token is a cookie and nothing else. If it appeared in the
	// body, JavaScript could read it (non-negotiable 6).
	cookie := refreshCookieFrom(t, res)
	if !cookie.HttpOnly {
		t.Error("the refresh cookie is not HttpOnly")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}
	if cookie.Path != "/api/v1/auth" {
		t.Errorf("cookie path = %q, want /api/v1/auth", cookie.Path)
	}

	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("re-encoding: %v", err)
	}
	if bytes.Contains(raw, []byte(cookie.Value)) {
		t.Error("the refresh token appears in the response body")
	}

	// 2. Refresh.
	refreshed := s.post(t, "/api/v1/auth/refresh", cookie)
	if refreshed.StatusCode != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200", refreshed.StatusCode)
	}
	rotated := refreshCookieFrom(t, refreshed)
	if rotated.Value == cookie.Value {
		t.Error("refresh returned the same token")
	}

	// 3. Log out.
	out := s.post(t, "/api/v1/auth/logout", rotated)
	if out.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", out.StatusCode)
	}
	if cleared := refreshCookieFrom(t, out); cleared.Value != "" || cleared.MaxAge >= 0 {
		t.Errorf("logout did not clear the cookie: value=%q maxage=%d", cleared.Value, cleared.MaxAge)
	}

	// 4. And the token is dead.
	after := s.post(t, "/api/v1/auth/refresh", rotated)
	if after.StatusCode != http.StatusUnauthorized {
		t.Errorf("refresh after logout = %d, want 401", after.StatusCode)
	}
}

// Replaying a spent cookie must end the session, and must not say why.
func TestReplayedRefreshCookieEndsTheSession(t *testing.T) {
	s := newStack(t)

	res, _ := s.signIn(t, nil)
	first := refreshCookieFrom(t, res)

	rotatedRes := s.post(t, "/api/v1/auth/refresh", first)
	if rotatedRes.StatusCode != http.StatusOK {
		t.Fatalf("first refresh = %d, want 200", rotatedRes.StatusCode)
	}
	current := refreshCookieFrom(t, rotatedRes)

	// The attacker replays the older cookie.
	replay := s.post(t, "/api/v1/auth/refresh", first)
	if replay.StatusCode != http.StatusUnauthorized {
		t.Errorf("replay = %d, want 401", replay.StatusCode)
	}

	body, err := io.ReadAll(replay.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	// "Already used" would confirm to an attacker that the token was real.
	if bytes.Contains(bytes.ToLower(body), []byte("reus")) ||
		bytes.Contains(bytes.ToLower(body), []byte("already")) {
		t.Errorf("the response says why it failed: %s", body)
	}

	// The honest client is signed out too, because nothing can tell them
	// apart (§9).
	honest := s.post(t, "/api/v1/auth/refresh", current)
	if honest.StatusCode != http.StatusUnauthorized {
		t.Errorf("the honest client's cookie still worked: %d", honest.StatusCode)
	}
}

func TestCallbackRejects(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*oidctest.Token)
		want   int
	}{
		{
			name:   "expired id token",
			mutate: func(tok *oidctest.Token) { tok.Expiry = tok.IssuedAt.Add(-time.Hour) },
			want:   http.StatusUnauthorized,
		},
		{
			name:   "id token for another audience",
			mutate: func(tok *oidctest.Token) { tok.Audience = "somebody-else" },
			want:   http.StatusUnauthorized,
		},
		{
			name:   "nonce from another attempt",
			mutate: func(tok *oidctest.Token) { tok.Nonce = "a-different-nonce" },
			want:   http.StatusUnauthorized,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStack(t)

			res, _ := s.signIn(t, tc.mutate)
			if res.StatusCode != tc.want {
				t.Errorf("status = %d, want %d", res.StatusCode, tc.want)
			}
			if ct := res.Header.Get("Content-Type"); ct != "application/problem+json" {
				t.Errorf("Content-Type = %q, want application/problem+json", ct)
			}
			for _, c := range res.Cookies() {
				if c.Name == "refresh_token" && c.Value != "" {
					t.Error("a failed sign-in set a refresh cookie")
				}
			}
		})
	}
}

func TestRefreshWithoutACookieIs401(t *testing.T) {
	s := newStack(t)

	if res := s.post(t, "/api/v1/auth/refresh", nil); res.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", res.StatusCode)
	}
}

// Logout must work from any state, or a client can get stuck unable to reach
// a signed-out state.
func TestLogoutWithoutASessionSucceeds(t *testing.T) {
	s := newStack(t)

	if res := s.post(t, "/api/v1/auth/logout", nil); res.StatusCode != http.StatusNoContent {
		t.Errorf("status = %d, want 204", res.StatusCode)
	}
}

// Signing in twice is one account and two independent sessions.
func TestSecondSignInIsTheSameAccount(t *testing.T) {
	s := newStack(t)

	_, first := s.signIn(t, nil)
	_, second := s.signIn(t, nil)

	if first.User.ID != second.User.ID {
		t.Errorf("user id changed between sign-ins: %s then %s", first.User.ID, second.User.ID)
	}
}

// get is a GET with an optional bearer token.
func (s *stack) get(t *testing.T, path, accessToken string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, s.server.URL+path, nil)
	if err != nil {
		t.Fatalf("building request: %v", err)
	}
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}

	res, err := s.client.Do(req)
	if err != nil {
		t.Fatalf("getting %s: %v", path, err)
	}
	t.Cleanup(func() { _ = res.Body.Close() })

	return res
}

// The visible half of 1.9's done-when: sign in, then see your own name.
func TestMeReturnsTheSignedInUser(t *testing.T) {
	s := newStack(t)

	_, session := s.signIn(t, nil)

	res := s.get(t, "/api/v1/me", session.AccessToken)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}

	var user struct {
		ID          string `json:"id"`
		DisplayName string `json:"display_name"`
		Email       string `json:"email"`
	}
	if err := json.NewDecoder(res.Body).Decode(&user); err != nil {
		t.Fatalf("decoding: %v", err)
	}

	if user.DisplayName != "Kari Nordmann" {
		t.Errorf("display_name = %q, want the name from the ID token", user.DisplayName)
	}
	if user.ID != session.User.ID {
		t.Errorf("id = %q, want %q", user.ID, session.User.ID)
	}
}

func TestMeRejectsBadCredentials(t *testing.T) {
	s := newStack(t)
	_, session := s.signIn(t, nil)

	tests := []struct{ name, token string }{
		{"no token", ""},
		{"nonsense", "not-a-token"},
		{"the refresh token, not the access token", "some-other-value"},
		// A valid token with a byte flipped: the signature must fail.
		{"tampered", session.AccessToken[:len(session.AccessToken)-1] + "x"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := s.get(t, "/api/v1/me", tc.token)
			if res.StatusCode != http.StatusUnauthorized {
				t.Errorf("status = %d, want 401", res.StatusCode)
			}
			if got := res.Header.Get("WWW-Authenticate"); got == "" {
				t.Error("no WWW-Authenticate header on a 401")
			}
		})
	}
}
