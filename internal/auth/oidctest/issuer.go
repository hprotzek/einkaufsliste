// Package oidctest runs a throwaway OIDC provider for tests: a keypair, a
// discovery document, a JWKS endpoint, and the ability to mint arbitrary ID
// tokens — including deliberately broken ones.
//
// Spec §11.4 asks for this even though R1 ships a single real provider,
// because the cases that matter cannot be produced with Google: an
// unverified email, a private-relay address, an expired token, a token
// signed by the wrong key, a token not signed at all. Each of those is a
// security control, and a control that is not tested is not a control.
//
// It is deliberately importable rather than a _test.go file: the auth
// verification tests, the service tests and the end-to-end handler tests all
// need the same issuer.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// keyID is the kid published in the JWKS and set in every token header. A
// fixed value keeps failures readable.
const keyID = "oidctest-key-1"

// Issuer is a running fake provider. Create one with New; it stops itself
// when the test ends.
type Issuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey

	// foreign is never published in the JWKS. Tokens signed with it look
	// structurally perfect and must still be rejected.
	foreign *rsa.PrivateKey

	// codes are the authorisation codes handed out but not yet exchanged.
	// The token endpoint is served concurrently with the test goroutine, so
	// this needs the mutex.
	mu    sync.Mutex
	codes map[string]pendingCode
}

// New starts an issuer and registers its shutdown with t.
func New(t *testing.T) *Issuer {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating issuer key: %v", err)
	}
	foreign, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating foreign key: %v", err)
	}

	iss := &Issuer{key: key, foreign: foreign, codes: map[string]pendingCode{}}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", iss.handleDiscovery)
	mux.HandleFunc("/jwks.json", iss.handleJWKS)
	mux.HandleFunc("/token", iss.handleToken)

	iss.server = httptest.NewServer(mux)
	t.Cleanup(iss.server.Close)

	return iss
}

// URL is the issuer identifier, which is also the base for discovery.
func (i *Issuer) URL() string { return i.server.URL }

// Client returns an HTTP client that reaches this issuer, with no proxy in
// the way. Pass it to go-oidc through oidc.ClientContext.
func (i *Issuer) Client() *http.Client {
	return &http.Client{Timeout: 5 * time.Second}
}

func (i *Issuer) handleDiscovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                i.server.URL,
		"authorization_endpoint":                i.server.URL + "/authorize",
		"token_endpoint":                        i.server.URL + "/token",
		"jwks_uri":                              i.server.URL + "/jwks.json",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"scopes_supported":                      []string{"openid", "email", "profile"},
	})
}

func (i *Issuer) handleJWKS(w http.ResponseWriter, _ *http.Request) {
	// The embedded field rather than Public().(*rsa.PublicKey): no assertion
	// to get wrong, and no way for it to fail at runtime.
	pub := i.key.PublicKey

	writeJSON(w, map[string]any{
		"keys": []map[string]any{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": keyID,
			"n":   base64url(pub.N.Bytes()),
			"e":   base64url(big.NewInt(int64(pub.E)).Bytes()),
		}},
	})
}

func writeJSON(w http.ResponseWriter, body any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(body)
}

func base64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}

// Token is the claim set of an ID token, as a plain struct so a test can
// break exactly one field and leave the rest valid.
type Token struct {
	Issuer        string
	Audience      string
	Subject       string
	Email         string
	EmailVerified bool
	Name          string
	Picture       string
	Nonce         string
	IssuedAt      time.Time
	Expiry        time.Time
}

// ValidToken returns a token that should pass every check, for the given
// audience. Mutate one field to build the case you actually want to test.
func (i *Issuer) ValidToken(audience string) Token {
	now := time.Now()

	return Token{
		Issuer:        i.server.URL,
		Audience:      audience,
		Subject:       "oidctest-subject-1",
		Email:         "kari.nordmann@example.no",
		EmailVerified: true,
		Name:          "Kari Nordmann",
		Picture:       "https://example.invalid/avatar.png",
		Nonce:         "test-nonce",
		IssuedAt:      now,
		Expiry:        now.Add(time.Hour),
	}
}

func (t Token) claims() jwt.MapClaims {
	claims := jwt.MapClaims{
		"iss":            t.Issuer,
		"aud":            t.Audience,
		"sub":            t.Subject,
		"email":          t.Email,
		"email_verified": t.EmailVerified,
		"iat":            t.IssuedAt.Unix(),
		"exp":            t.Expiry.Unix(),
	}

	// Optional claims are omitted rather than sent empty, matching how a real
	// provider behaves when it has nothing to say.
	if t.Name != "" {
		claims["name"] = t.Name
	}
	if t.Picture != "" {
		claims["picture"] = t.Picture
	}
	if t.Nonce != "" {
		claims["nonce"] = t.Nonce
	}

	return claims
}

// Sign returns a correctly signed ID token for these claims.
func (i *Issuer) Sign(t *testing.T, tok Token) string {
	t.Helper()
	return i.signWith(t, tok, i.key)
}

// SignWithForeignKey signs with a key that is not in the JWKS. The token is
// well-formed and its claims are perfect; only the signature is
// unverifiable. This is the shape of a forged token.
func (i *Issuer) SignWithForeignKey(t *testing.T, tok Token) string {
	t.Helper()
	return i.signWith(t, tok, i.foreign)
}

func (i *Issuer) signWith(t *testing.T, tok Token, key *rsa.PrivateKey) string {
	t.Helper()

	signed, err := i.signToken(tok, key)
	if err != nil {
		t.Fatalf("signing token: %v", err)
	}

	return signed
}

// signToken is the same work without a *testing.T, for the token endpoint,
// which runs on the server goroutine and cannot fail a test.
func (i *Issuer) signToken(tok Token, key *rsa.PrivateKey) (string, error) {
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodRS256, tok.claims())
	jwtToken.Header["kid"] = keyID

	return jwtToken.SignedString(key)
}

// SignUnsecured returns an alg=none token: valid claims, no signature at
// all. A verifier that accepts this accepts anything anyone cares to write.
func (i *Issuer) SignUnsecured(t *testing.T, tok Token) string {
	t.Helper()

	jwtToken := jwt.NewWithClaims(jwt.SigningMethodNone, tok.claims())

	signed, err := jwtToken.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("signing unsecured token: %v", err)
	}

	return signed
}

// --- Authorization code exchange ---------------------------------------------
//
// Spec §9 has the client complete PKCE with the provider and post the code to
// our server, which exchanges it. Testing that end to end needs a token
// endpoint, so the issuer keeps a small table of codes it has handed out.

// IssueCode registers an authorisation code that will exchange for an ID
// token built from tok. The PKCE challenge is the S256 hash of verifier.
func (i *Issuer) IssueCode(t *testing.T, tok Token, verifier string) string {
	t.Helper()

	code := "code-" + base64url(randomBytes(t, 16))

	i.mu.Lock()
	defer i.mu.Unlock()
	i.codes[code] = pendingCode{token: tok, challenge: pkceChallenge(verifier)}

	return code
}

type pendingCode struct {
	token     Token
	challenge string
}

// pkceChallenge is the S256 transform: base64url(sha256(verifier)).
func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64url(sum[:])
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()

	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("reading random bytes: %v", err)
	}
	return b
}

// handleToken implements the bits of RFC 6749 + RFC 7636 this project uses:
// grant_type=authorization_code with a PKCE verifier.
func (i *Issuer) handleToken(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		writeTokenError(w, http.StatusBadRequest, "invalid_request")
		return
	}

	if r.PostFormValue("grant_type") != "authorization_code" {
		writeTokenError(w, http.StatusBadRequest, "unsupported_grant_type")
		return
	}

	i.mu.Lock()
	pending, ok := i.codes[r.PostFormValue("code")]
	// A code is single-use, exactly as a real provider treats it.
	delete(i.codes, r.PostFormValue("code"))
	i.mu.Unlock()

	if !ok {
		writeTokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}

	// The whole point of PKCE: without the verifier, a stolen code is useless.
	if pkceChallenge(r.PostFormValue("code_verifier")) != pending.challenge {
		writeTokenError(w, http.StatusBadRequest, "invalid_grant")
		return
	}

	signed, err := i.signToken(pending.token, i.key)
	if err != nil {
		writeTokenError(w, http.StatusInternalServerError, "server_error")
		return
	}

	writeJSON(w, map[string]any{
		"access_token": "fake-provider-access-token",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     signed,
	})
}

func writeTokenError(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": code})
}
