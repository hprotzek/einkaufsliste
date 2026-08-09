package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// ErrExchangeFailed means the provider refused the authorisation code. Wrong
// code, wrong PKCE verifier, wrong redirect URI, or a code already spent —
// all of which mean "start again", and none of which is worth telling a
// caller apart.
var ErrExchangeFailed = errors.New("auth: authorisation code exchange failed")

// ErrNoIDToken means the provider returned tokens without an id_token, which
// makes the response useless for signing anybody in.
var ErrNoIDToken = errors.New("auth: provider returned no id_token")

// Exchanger turns an authorisation code into a verified identity.
//
// The exchange happens server-side, so the client never handles the
// provider's tokens and this server's own tokens are the only session
// currency (spec §9).
type Exchanger struct {
	verifier *Verifier
	configs  map[string]exchangeConfig
}

type exchangeConfig struct {
	oauth  *oauth2.Config
	issuer string
}

// ExchangeConfig adds the provider secrets that verification alone does not
// need.
type ExchangeConfig struct {
	ProviderConfig
	// ClientSecret is issued by the provider. Google requires it even with
	// PKCE for a confidential client.
	ClientSecret string
}

// NewExchanger discovers each provider's endpoints. Like NewVerifier it
// reaches the network, so it belongs in start-up wiring.
func NewExchanger(ctx context.Context, configs []ExchangeConfig) (*Exchanger, error) {
	// No providers is a valid state, not a misconfiguration: until task 1.8
	// creates the Google OAuth client there is nothing to configure, and the
	// service still has a health endpoint, a schema and static files to
	// serve. Sign-in reports the provider as unknown, which is true.
	if len(configs) == 0 {
		return &Exchanger{
			verifier: &Verifier{providers: map[string]*oidc.IDTokenVerifier{}},
			configs:  map[string]exchangeConfig{},
		}, nil
	}

	providerConfigs := make([]ProviderConfig, 0, len(configs))
	for _, cfg := range configs {
		providerConfigs = append(providerConfigs, cfg.ProviderConfig)
	}

	verifier, err := NewVerifier(ctx, providerConfigs)
	if err != nil {
		return nil, err
	}

	exchanges := make(map[string]exchangeConfig, len(configs))
	for _, cfg := range configs {
		provider, err := oidc.NewProvider(ctx, cfg.Issuer)
		if err != nil {
			return nil, fmt.Errorf("auth: discovering provider %q: %w", cfg.Name, err)
		}

		exchanges[cfg.Name] = exchangeConfig{
			oauth: &oauth2.Config{
				ClientID:     cfg.ClientID,
				ClientSecret: cfg.ClientSecret,
				Endpoint:     provider.Endpoint(),
				Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
			},
			issuer: cfg.Issuer,
		}
	}

	return &Exchanger{verifier: verifier, configs: exchanges}, nil
}

// CodeExchange is what the client posts after completing PKCE.
type CodeExchange struct {
	Provider     string
	Code         string
	CodeVerifier string
	RedirectURI  string
	// Nonce is the value the client put in the authorize request. The ID
	// token must carry the same one.
	Nonce string
}

// Exchange redeems the code and returns the verified identity.
func (e *Exchanger) Exchange(ctx context.Context, req CodeExchange) (Identity, error) {
	cfg, ok := e.configs[req.Provider]
	if !ok {
		return Identity{}, fmt.Errorf("%w: %q", ErrUnknownProvider, req.Provider)
	}

	// A copy, because RedirectURL varies per request and the shared config
	// must not be mutated under concurrent sign-ins.
	oauthCfg := *cfg.oauth
	oauthCfg.RedirectURL = req.RedirectURI

	token, err := oauthCfg.Exchange(ctx, req.Code,
		oauth2.SetAuthURLParam("code_verifier", req.CodeVerifier),
	)
	if err != nil {
		return Identity{}, fmt.Errorf("%w: %w", ErrExchangeFailed, err)
	}

	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return Identity{}, ErrNoIDToken
	}

	// The provider's access token is deliberately dropped here. This server
	// asks the provider nothing further, so keeping it would be a credential
	// held for no reason.
	return e.verifier.Verify(ctx, req.Provider, rawIDToken, req.Nonce)
}

// Providers lists the configured provider names.
func (e *Exchanger) Providers() []string { return e.verifier.Providers() }
