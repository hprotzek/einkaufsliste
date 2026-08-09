package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/hprotzek/einkaufsliste/internal/auth"
	"github.com/hprotzek/einkaufsliste/internal/auth/oidctest"
)

const (
	clientID = "test-client-id"
	provider = "google"
	nonce    = "test-nonce"
)

// newVerifier wires a Verifier to a fake issuer. The context carries the
// issuer's HTTP client, which is how go-oidc reaches it for discovery and
// JWKS — and how it keeps reaching it later, since the verifier caches it.
func newVerifier(t *testing.T, iss *oidctest.Issuer, name string) *auth.Verifier {
	t.Helper()

	ctx := oidc.ClientContext(t.Context(), iss.Client())

	v, err := auth.NewVerifier(ctx, []auth.ProviderConfig{{
		Name:     name,
		Issuer:   iss.URL(),
		ClientID: clientID,
	}})
	if err != nil {
		t.Fatalf("building verifier: %v", err)
	}

	return v
}

func TestVerifyAcceptsAValidToken(t *testing.T) {
	iss := oidctest.New(t)
	v := newVerifier(t, iss, provider)

	tok := iss.ValidToken(clientID)
	tok.Nonce = nonce

	got, err := v.Verify(t.Context(), provider, iss.Sign(t, tok), nonce)
	if err != nil {
		t.Fatalf("a valid token was rejected: %v", err)
	}

	if got.Provider != provider {
		t.Errorf("provider = %q, want %q", got.Provider, provider)
	}
	if got.Subject != tok.Subject {
		t.Errorf("subject = %q, want %q", got.Subject, tok.Subject)
	}
	if got.Email != tok.Email {
		t.Errorf("email = %q, want %q", got.Email, tok.Email)
	}
	if !got.EmailVerified {
		t.Error("email_verified = false, want true")
	}
	if got.Name != tok.Name {
		t.Errorf("name = %q, want %q", got.Name, tok.Name)
	}
}

// Spec §11.4: "Expired, wrong-aud, wrong-iss, and unsigned ID tokens → all
// rejected." One row each, so adding a case is adding a row.
func TestVerifyRejectsMalformedTokens(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T, iss *oidctest.Issuer) string
		want  error
	}{
		{
			name: "expired",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken(clientID)
				tok.Nonce = nonce
				tok.IssuedAt = time.Now().Add(-2 * time.Hour)
				tok.Expiry = time.Now().Add(-time.Hour)
				return iss.Sign(t, tok)
			},
			want: auth.ErrInvalidToken,
		},
		{
			name: "wrong audience",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken("a-different-client")
				tok.Nonce = nonce
				return iss.Sign(t, tok)
			},
			want: auth.ErrInvalidToken,
		},
		{
			name: "wrong issuer",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken(clientID)
				tok.Nonce = nonce
				tok.Issuer = "https://accounts.example.invalid"
				return iss.Sign(t, tok)
			},
			want: auth.ErrInvalidToken,
		},
		{
			name: "signed by an unpublished key",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken(clientID)
				tok.Nonce = nonce
				return iss.SignWithForeignKey(t, tok)
			},
			want: auth.ErrInvalidToken,
		},
		{
			name: "unsigned (alg=none)",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken(clientID)
				tok.Nonce = nonce
				return iss.SignUnsecured(t, tok)
			},
			want: auth.ErrInvalidToken,
		},
		{
			name:  "not a JWT",
			build: func(_ *testing.T, _ *oidctest.Issuer) string { return "nonsense" },
			want:  auth.ErrInvalidToken,
		},
		{
			// The token is perfectly valid; it just belongs to a different
			// login attempt. Without this check, an ID token captured
			// elsewhere can be replayed into someone else's session.
			name: "nonce from another login attempt",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken(clientID)
				tok.Nonce = "some-other-attempt"
				return iss.Sign(t, tok)
			},
			want: auth.ErrNonceMismatch,
		},
		{
			name: "no nonce in the token",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken(clientID)
				tok.Nonce = ""
				return iss.Sign(t, tok)
			},
			want: auth.ErrNonceMismatch,
		},
		{
			name: "no subject",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken(clientID)
				tok.Nonce = nonce
				tok.Subject = ""
				return iss.Sign(t, tok)
			},
			want: auth.ErrMissingSubject,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			iss := oidctest.New(t)
			v := newVerifier(t, iss, provider)

			_, err := v.Verify(t.Context(), provider, tc.build(t, iss), nonce)
			if err == nil {
				t.Fatalf("%s was accepted; it must be rejected", tc.name)
			}
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want one matching %v", err, tc.want)
			}
		})
	}
}

// Verification must never be skipped because the caller forgot a nonce.
// Treating an empty expectation as "nothing to compare" would turn replay
// protection off for anyone who omits it.
func TestVerifyRefusesWhenNoNonceIsExpected(t *testing.T) {
	iss := oidctest.New(t)
	v := newVerifier(t, iss, provider)

	tok := iss.ValidToken(clientID)
	tok.Nonce = nonce

	_, err := v.Verify(t.Context(), provider, iss.Sign(t, tok), "")
	if !errors.Is(err, auth.ErrNonceMismatch) {
		t.Errorf("error = %v, want ErrNonceMismatch", err)
	}
}

func TestVerifyRejectsUnknownProvider(t *testing.T) {
	iss := oidctest.New(t)
	v := newVerifier(t, iss, provider)

	tok := iss.ValidToken(clientID)
	tok.Nonce = nonce

	_, err := v.Verify(t.Context(), "apple", iss.Sign(t, tok), nonce)
	if !errors.Is(err, auth.ErrUnknownProvider) {
		t.Errorf("error = %v, want ErrUnknownProvider", err)
	}
}

// Non-negotiable 10: nothing may assume provider == "google". Two providers
// are configured here under names neither of which is special, and a token
// from one must not be accepted under the other's name.
func TestProvidersAreIndependentAndNotSpecialCased(t *testing.T) {
	first := oidctest.New(t)
	second := oidctest.New(t)

	ctx := oidc.ClientContext(t.Context(), first.Client())

	v, err := auth.NewVerifier(ctx, []auth.ProviderConfig{
		{Name: "google", Issuer: first.URL(), ClientID: clientID},
		{Name: "hypothetical", Issuer: second.URL(), ClientID: clientID},
	})
	if err != nil {
		t.Fatalf("building verifier: %v", err)
	}

	if len(v.Providers()) != 2 {
		t.Errorf("providers = %v, want two", v.Providers())
	}

	// Each token verifies under its own provider...
	for name, iss := range map[string]*oidctest.Issuer{"google": first, "hypothetical": second} {
		tok := iss.ValidToken(clientID)
		tok.Nonce = nonce
		tok.Subject = "subject-" + name

		got, err := v.Verify(t.Context(), name, iss.Sign(t, tok), nonce)
		if err != nil {
			t.Fatalf("token from %s was rejected: %v", name, err)
		}
		if got.Provider != name {
			t.Errorf("provider = %q, want %q", got.Provider, name)
		}
	}

	// ...and not under the other's, even though both are structurally sound.
	tok := second.ValidToken(clientID)
	tok.Nonce = nonce
	if _, err := v.Verify(t.Context(), "google", second.Sign(t, tok), nonce); err == nil {
		t.Error("a token from one provider was accepted under another's name")
	}
}

func TestNewVerifierRejectsBadConfiguration(t *testing.T) {
	iss := oidctest.New(t)
	ctx := oidc.ClientContext(t.Context(), iss.Client())

	tests := []struct {
		name    string
		configs []auth.ProviderConfig
	}{
		{name: "no providers", configs: nil},
		{name: "no name", configs: []auth.ProviderConfig{{Issuer: iss.URL(), ClientID: clientID}}},
		{name: "no issuer", configs: []auth.ProviderConfig{{Name: provider, ClientID: clientID}}},
		{name: "no client id", configs: []auth.ProviderConfig{{Name: provider, Issuer: iss.URL()}}},
		{
			name: "duplicate provider",
			configs: []auth.ProviderConfig{
				{Name: provider, Issuer: iss.URL(), ClientID: clientID},
				{Name: provider, Issuer: iss.URL(), ClientID: clientID},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := auth.NewVerifier(ctx, tc.configs); err == nil {
				t.Errorf("%s was accepted; it must be rejected", tc.name)
			}
		})
	}
}

func TestNewVerifierFailsWhenDiscoveryFails(t *testing.T) {
	// A misconfigured deployment must fail at start-up, not on the first
	// person trying to sign in.
	_, err := auth.NewVerifier(context.Background(), []auth.ProviderConfig{{
		Name:     provider,
		Issuer:   "http://127.0.0.1:1/nothing-here",
		ClientID: clientID,
	}})
	if err == nil {
		t.Error("discovery against a dead issuer succeeded")
	}
}
