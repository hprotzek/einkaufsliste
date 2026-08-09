package oidctest_test

import (
	"context"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/hprotzek/einkaufsliste/internal/auth/oidctest"
)

const audience = "test-client-id"

// The point of these tests is not the fake issuer for its own sake. It is
// that go-oidc — the library that will verify real Google tokens at task 1.3
// — accepts what this issuer mints and rejects what it deliberately breaks.
// A fake issuer the real verifier disagrees with would be worse than none.
func verifier(t *testing.T, iss *oidctest.Issuer) (context.Context, *oidc.IDTokenVerifier) {
	t.Helper()

	ctx := oidc.ClientContext(t.Context(), iss.Client())

	provider, err := oidc.NewProvider(ctx, iss.URL())
	if err != nil {
		t.Fatalf("discovering the issuer: %v", err)
	}

	return ctx, provider.Verifier(&oidc.Config{ClientID: audience})
}

func TestDiscoveryAndVerificationOfAValidToken(t *testing.T) {
	iss := oidctest.New(t)
	ctx, v := verifier(t, iss)

	want := iss.ValidToken(audience)
	raw := iss.Sign(t, want)

	got, err := v.Verify(ctx, raw)
	if err != nil {
		t.Fatalf("a valid token was rejected: %v", err)
	}

	if got.Subject != want.Subject {
		t.Errorf("subject = %q, want %q", got.Subject, want.Subject)
	}
	if got.Issuer != iss.URL() {
		t.Errorf("issuer = %q, want %q", got.Issuer, iss.URL())
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Nonce         string `json:"nonce"`
	}
	if err := got.Claims(&claims); err != nil {
		t.Fatalf("decoding claims: %v", err)
	}
	if claims.Email != want.Email {
		t.Errorf("email = %q, want %q", claims.Email, want.Email)
	}
	if !claims.EmailVerified {
		t.Error("email_verified = false, want true")
	}
	if claims.Nonce != want.Nonce {
		t.Errorf("nonce = %q, want %q", claims.Nonce, want.Nonce)
	}
}

// Each of these is a control from spec §11.4. They are table-driven because
// the list only grows: every new malformation is one more row.
func TestMalformedTokensAreRejected(t *testing.T) {
	tests := []struct {
		name  string
		build func(t *testing.T, iss *oidctest.Issuer) string
	}{
		{
			name: "expired",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken(audience)
				tok.IssuedAt = time.Now().Add(-2 * time.Hour)
				tok.Expiry = time.Now().Add(-time.Hour)
				return iss.Sign(t, tok)
			},
		},
		{
			name: "wrong audience",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken("someone-elses-client-id")
				return iss.Sign(t, tok)
			},
		},
		{
			name: "wrong issuer",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				tok := iss.ValidToken(audience)
				tok.Issuer = "https://accounts.example.invalid"
				return iss.Sign(t, tok)
			},
		},
		{
			// Structurally perfect, claims perfect, signed by a key that is
			// not in the JWKS. This is what a forgery looks like.
			name: "signed by an unpublished key",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				return iss.SignWithForeignKey(t, iss.ValidToken(audience))
			},
		},
		{
			// The classic JWT vulnerability: accept alg=none and anyone can
			// mint any identity they like.
			name: "unsigned (alg=none)",
			build: func(t *testing.T, iss *oidctest.Issuer) string {
				return iss.SignUnsecured(t, iss.ValidToken(audience))
			},
		},
		{
			name: "not a JWT at all",
			build: func(_ *testing.T, _ *oidctest.Issuer) string {
				return "this is not a token"
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			iss := oidctest.New(t)
			ctx, v := verifier(t, iss)

			if _, err := v.Verify(ctx, tc.build(t, iss)); err == nil {
				t.Errorf("%s was accepted; it must be rejected", tc.name)
			}
		})
	}
}

// An unverified email is not a malformed token — it verifies fine. It is the
// linking rules at §9 that must refuse to act on it, which is task 1.7. This
// test exists so the issuer can express the case, and to pin that the claim
// survives verification rather than being swallowed.
func TestUnverifiedEmailVerifiesButIsVisible(t *testing.T) {
	iss := oidctest.New(t)
	ctx, v := verifier(t, iss)

	tok := iss.ValidToken(audience)
	tok.EmailVerified = false

	idToken, err := v.Verify(ctx, iss.Sign(t, tok))
	if err != nil {
		t.Fatalf("token with email_verified=false should still verify: %v", err)
	}

	var claims struct {
		EmailVerified bool `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		t.Fatalf("decoding claims: %v", err)
	}
	if claims.EmailVerified {
		t.Error("email_verified came back true; the claim was not carried through")
	}
}

// Apple's private-relay addresses must never be treated as a linkable email
// (§9). The issuer needs to be able to mint one so that rule is testable at
// 1.7 without an Apple developer account.
func TestPrivateRelayAddressCanBeMinted(t *testing.T) {
	iss := oidctest.New(t)
	ctx, v := verifier(t, iss)

	tok := iss.ValidToken(audience)
	tok.Email = "abc123xyz@privaterelay.appleid.com"

	idToken, err := v.Verify(ctx, iss.Sign(t, tok))
	if err != nil {
		t.Fatalf("verifying: %v", err)
	}

	var claims struct {
		Email string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		t.Fatalf("decoding claims: %v", err)
	}
	if claims.Email != tok.Email {
		t.Errorf("email = %q, want %q", claims.Email, tok.Email)
	}
}

// Two issuers must not share a key, or a test could pass because another
// test's issuer happened to vouch for the token.
func TestIssuersAreIndependent(t *testing.T) {
	first := oidctest.New(t)
	second := oidctest.New(t)

	if first.URL() == second.URL() {
		t.Fatal("two issuers share a URL")
	}

	ctx, v := verifier(t, first)

	// Minted by the second issuer but claiming to be the first.
	tok := second.ValidToken(audience)
	tok.Issuer = first.URL()

	if _, err := v.Verify(ctx, second.Sign(t, tok)); err == nil {
		t.Error("a token signed by a different issuer's key was accepted")
	}
}
