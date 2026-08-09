package auth_test

import (
	"errors"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/hprotzek/einkaufsliste/internal/auth"
	"github.com/hprotzek/einkaufsliste/internal/auth/oidctest"
)

const codeVerifier = "a-pkce-verifier-long-enough-to-be-realistic"

func newExchanger(t *testing.T, iss *oidctest.Issuer) *auth.Exchanger {
	t.Helper()

	ctx := oidc.ClientContext(t.Context(), iss.Client())

	e, err := auth.NewExchanger(ctx, []auth.ExchangeConfig{{
		ProviderConfig: auth.ProviderConfig{
			Name:     provider,
			Issuer:   iss.URL(),
			ClientID: clientID,
		},
		ClientSecret: "test-client-secret",
	}})
	if err != nil {
		t.Fatalf("building exchanger: %v", err)
	}

	return e
}

// The whole of §9 step 4 in one test: a code goes in, a verified identity
// comes out, and the provider's own tokens never leave this server.
func TestExchangeReturnsAVerifiedIdentity(t *testing.T) {
	iss := oidctest.New(t)
	e := newExchanger(t, iss)

	want := iss.ValidToken(clientID)
	want.Nonce = nonce
	code := iss.IssueCode(t, want, codeVerifier)

	ctx := oidc.ClientContext(t.Context(), iss.Client())

	got, err := e.Exchange(ctx, auth.CodeExchange{
		Provider:     provider,
		Code:         code,
		CodeVerifier: codeVerifier,
		RedirectURI:  "https://example.test/callback",
		Nonce:        nonce,
	})
	if err != nil {
		t.Fatalf("exchanging: %v", err)
	}

	if got.Subject != want.Subject {
		t.Errorf("subject = %q, want %q", got.Subject, want.Subject)
	}
	if got.Email != want.Email {
		t.Errorf("email = %q, want %q", got.Email, want.Email)
	}
	if got.Provider != provider {
		t.Errorf("provider = %q, want %q", got.Provider, provider)
	}
	if !got.EmailVerified {
		t.Error("email_verified = false, want true")
	}
}

func TestExchangeRejects(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(iss *oidctest.Issuer, req *auth.CodeExchange)
		wantErr error
	}{
		{
			// PKCE's entire purpose: a code intercepted without the verifier
			// is worthless.
			name: "wrong PKCE verifier",
			mutate: func(_ *oidctest.Issuer, req *auth.CodeExchange) {
				req.CodeVerifier = "not-the-verifier-that-was-used"
			},
			wantErr: auth.ErrExchangeFailed,
		},
		{
			name: "unknown code",
			mutate: func(_ *oidctest.Issuer, req *auth.CodeExchange) {
				req.Code = "a-code-nobody-issued"
			},
			wantErr: auth.ErrExchangeFailed,
		},
		{
			name: "unknown provider",
			mutate: func(_ *oidctest.Issuer, req *auth.CodeExchange) {
				req.Provider = "apple"
			},
			wantErr: auth.ErrUnknownProvider,
		},
		{
			// The code exchanges fine; the ID token inside belongs to another
			// login attempt.
			name: "nonce from another attempt",
			mutate: func(_ *oidctest.Issuer, req *auth.CodeExchange) {
				req.Nonce = "a-different-nonce"
			},
			wantErr: auth.ErrNonceMismatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			iss := oidctest.New(t)
			e := newExchanger(t, iss)

			tok := iss.ValidToken(clientID)
			tok.Nonce = nonce

			req := auth.CodeExchange{
				Provider:     provider,
				Code:         iss.IssueCode(t, tok, codeVerifier),
				CodeVerifier: codeVerifier,
				RedirectURI:  "https://example.test/callback",
				Nonce:        nonce,
			}
			tc.mutate(iss, &req)

			ctx := oidc.ClientContext(t.Context(), iss.Client())

			if _, err := e.Exchange(ctx, req); !errors.Is(err, tc.wantErr) {
				t.Errorf("error = %v, want one matching %v", err, tc.wantErr)
			}
		})
	}
}

// A real provider burns the code on first use, and so must anything relying
// on one: replaying a code must not mint a second session.
func TestAuthorisationCodeIsSingleUse(t *testing.T) {
	iss := oidctest.New(t)
	e := newExchanger(t, iss)

	tok := iss.ValidToken(clientID)
	tok.Nonce = nonce
	code := iss.IssueCode(t, tok, codeVerifier)

	ctx := oidc.ClientContext(t.Context(), iss.Client())
	req := auth.CodeExchange{
		Provider:     provider,
		Code:         code,
		CodeVerifier: codeVerifier,
		RedirectURI:  "https://example.test/callback",
		Nonce:        nonce,
	}

	if _, err := e.Exchange(ctx, req); err != nil {
		t.Fatalf("first exchange: %v", err)
	}
	if _, err := e.Exchange(ctx, req); !errors.Is(err, auth.ErrExchangeFailed) {
		t.Errorf("second exchange of the same code: err = %v, want ErrExchangeFailed", err)
	}
}

// A token minted for a different audience must not become a session here,
// even though the code exchange itself succeeds.
func TestExchangeRejectsTokenForAnotherAudience(t *testing.T) {
	iss := oidctest.New(t)
	e := newExchanger(t, iss)

	tok := iss.ValidToken("somebody-elses-client-id")
	tok.Nonce = nonce

	ctx := oidc.ClientContext(t.Context(), iss.Client())

	_, err := e.Exchange(ctx, auth.CodeExchange{
		Provider:     provider,
		Code:         iss.IssueCode(t, tok, codeVerifier),
		CodeVerifier: codeVerifier,
		RedirectURI:  "https://example.test/callback",
		Nonce:        nonce,
	})
	if !errors.Is(err, auth.ErrInvalidToken) {
		t.Errorf("error = %v, want ErrInvalidToken", err)
	}
}
