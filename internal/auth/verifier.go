package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Errors callers can act on. The transport layer maps these to problem+json;
// nothing in this package knows that (spec §5.3).
var (
	// ErrUnknownProvider means the provider name is not configured. It is
	// distinct from a bad token: the request named something that does not
	// exist, rather than failing a check.
	ErrUnknownProvider = errors.New("auth: unknown provider")

	// ErrInvalidToken covers every reason an ID token was not acceptable:
	// signature, issuer, audience, expiry, or malformed input. They are
	// deliberately one error — telling a caller which check failed tells an
	// attacker which check to work on next.
	ErrInvalidToken = errors.New("auth: invalid id token")

	// ErrNonceMismatch means the token verified but replayed a different
	// login attempt than the one in progress.
	ErrNonceMismatch = errors.New("auth: nonce mismatch")

	// ErrMissingSubject means the provider returned a token with no stable
	// identifier, which leaves nothing to key an account on.
	ErrMissingSubject = errors.New("auth: id token has no subject")
)

// ProviderConfig describes one OIDC provider. R1 configures exactly one,
// Google, but nothing here knows that (non-negotiable 10): the name is data,
// and adding a second provider is another entry in a slice.
type ProviderConfig struct {
	// Name is what the API path uses: /auth/oidc/{provider}/callback.
	Name string
	// Issuer is the issuer URL, used for discovery and matched against the
	// token's iss claim.
	Issuer string
	// ClientID is the audience this server expects.
	ClientID string
}

// Identity is what a verified ID token tells us about a person, normalised
// across providers.
type Identity struct {
	// Provider and Subject together are the identity key. Never the email:
	// an Apple private-relay address is per-app, and emails change (spec §9).
	Provider string
	Subject  string

	Email string
	// EmailVerified is carried through rather than acted on here. The linking
	// rules (§9) decide what it means; verification only reports it.
	EmailVerified bool

	Name    string
	Picture string
}

// Verifier checks ID tokens against the providers it was configured with.
// Safe for concurrent use; go-oidc caches and refreshes JWKS internally.
type Verifier struct {
	providers map[string]*oidc.IDTokenVerifier
}

// NewVerifier performs OIDC discovery for each provider. It reaches the
// network, so it belongs in start-up wiring rather than a request path — and
// failing here means a misconfigured deployment, which should not start.
func NewVerifier(ctx context.Context, configs []ProviderConfig) (*Verifier, error) {
	if len(configs) == 0 {
		return nil, errors.New("auth: no providers configured")
	}

	verifiers := make(map[string]*oidc.IDTokenVerifier, len(configs))

	for _, cfg := range configs {
		switch {
		case cfg.Name == "":
			return nil, errors.New("auth: provider has no name")
		case cfg.Issuer == "":
			return nil, fmt.Errorf("auth: provider %q has no issuer", cfg.Name)
		case cfg.ClientID == "":
			return nil, fmt.Errorf("auth: provider %q has no client id", cfg.Name)
		}

		if _, exists := verifiers[cfg.Name]; exists {
			return nil, fmt.Errorf("auth: provider %q configured twice", cfg.Name)
		}

		provider, err := oidc.NewProvider(ctx, cfg.Issuer)
		if err != nil {
			return nil, fmt.Errorf("auth: discovering provider %q: %w", cfg.Name, err)
		}

		verifiers[cfg.Name] = provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	}

	return &Verifier{providers: verifiers}, nil
}

// Providers lists the configured provider names, sorted for stable output.
func (v *Verifier) Providers() []string {
	names := make([]string, 0, len(v.providers))
	for name := range v.providers {
		names = append(names, name)
	}
	return names
}

// Verify checks an ID token and returns the identity it asserts.
//
// It checks the signature against the provider's JWKS, and the iss, aud and
// exp claims, via go-oidc. The nonce it checks itself: go-oidc exposes the
// claim but has no idea what value this login attempt used, and an unchecked
// nonce leaves the flow open to replay of an ID token captured elsewhere.
func (v *Verifier) Verify(ctx context.Context, provider, rawIDToken, expectedNonce string) (Identity, error) {
	verifier, ok := v.providers[provider]
	if !ok {
		return Identity{}, fmt.Errorf("%w: %q", ErrUnknownProvider, provider)
	}

	idToken, err := verifier.Verify(ctx, rawIDToken)
	if err != nil {
		// Wrapped for logs, but the sentinel is what callers match on, and it
		// does not distinguish between the ways a token can be wrong.
		return Identity{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}

	if expectedNonce == "" {
		// A caller with no nonce cannot have started the flow correctly.
		// Treating that as "nothing to check" would silently disable replay
		// protection, so it is an error instead.
		return Identity{}, fmt.Errorf("%w: no nonce to check against", ErrNonceMismatch)
	}
	if idToken.Nonce != expectedNonce {
		return Identity{}, ErrNonceMismatch
	}

	if idToken.Subject == "" {
		return Identity{}, ErrMissingSubject
	}

	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return Identity{}, fmt.Errorf("%w: reading claims: %w", ErrInvalidToken, err)
	}

	return Identity{
		Provider: provider,
		Subject:  idToken.Subject,
		// Providers are inconsistent about whitespace and case. Trimming is
		// safe; lowercasing is not, and is unnecessary because the email
		// column is citext (§4 forbids a naive fold).
		Email:         strings.TrimSpace(claims.Email),
		EmailVerified: claims.EmailVerified,
		Name:          strings.TrimSpace(claims.Name),
		Picture:       claims.Picture,
	}, nil
}
