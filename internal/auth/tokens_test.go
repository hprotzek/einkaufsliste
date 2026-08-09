package auth_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/hprotzek/einkaufsliste/internal/auth"
)

const userID = "11111111-2222-3333-4444-555555555555"

// A key of the minimum acceptable length, so tests exercise the same shape
// production will use.
func testKey(seed byte) []byte {
	key := make([]byte, 32)
	for i := range key {
		key[i] = seed + byte(i)
	}
	return key
}

func newIssuer(t *testing.T, now auth.Clock) *auth.TokenIssuer {
	t.Helper()

	issuer, err := auth.NewTokenIssuer(testKey(1), now)
	if err != nil {
		t.Fatalf("building token issuer: %v", err)
	}
	return issuer
}

func TestAccessTokenRoundTrip(t *testing.T) {
	issuer := newIssuer(t, nil)

	token, err := issuer.NewAccessToken(userID)
	if err != nil {
		t.Fatalf("minting: %v", err)
	}

	got, err := issuer.ParseAccessToken(token.Token)
	if err != nil {
		t.Fatalf("parsing a token we just minted: %v", err)
	}
	if got != userID {
		t.Errorf("subject = %q, want %q", got, userID)
	}

	// §9 fixes this at fifteen minutes, and the value is a security control
	// rather than a preference: it bounds how long a stolen access token,
	// which cannot be revoked, remains useful.
	within := time.Until(token.ExpiresAt)
	if within > auth.AccessTokenTTL || within < auth.AccessTokenTTL-time.Minute {
		t.Errorf("expiry in %v, want about %v", within, auth.AccessTokenTTL)
	}
}

func TestAccessTokenExpires(t *testing.T) {
	base := time.Now()
	issuer := newIssuer(t, func() time.Time { return base })

	token, err := issuer.NewAccessToken(userID)
	if err != nil {
		t.Fatalf("minting: %v", err)
	}

	// Still valid a minute before expiry.
	early := newIssuer(t, func() time.Time { return base.Add(auth.AccessTokenTTL - time.Minute) })
	if _, earlyErr := early.ParseAccessToken(token.Token); earlyErr != nil {
		t.Errorf("token rejected before it expired: %v", earlyErr)
	}

	// And refused a minute after.
	late := newIssuer(t, func() time.Time { return base.Add(auth.AccessTokenTTL + time.Minute) })
	_, err = late.ParseAccessToken(token.Token)
	if !errors.Is(err, auth.ErrExpiredAccessToken) {
		t.Errorf("error = %v, want ErrExpiredAccessToken", err)
	}
}

func TestAccessTokenFromAnotherKeyIsRejected(t *testing.T) {
	mine := newIssuer(t, nil)

	theirs, err := auth.NewTokenIssuer(testKey(200), nil)
	if err != nil {
		t.Fatalf("building second issuer: %v", err)
	}

	token, err := theirs.NewAccessToken(userID)
	if err != nil {
		t.Fatalf("minting: %v", err)
	}

	if _, err := mine.ParseAccessToken(token.Token); !errors.Is(err, auth.ErrInvalidAccessToken) {
		t.Errorf("error = %v, want ErrInvalidAccessToken", err)
	}
}

// The algorithm-confusion attack: hand the parser a token that says it needs
// no signature. Without an explicit method allow-list, some parsers oblige.
func TestUnsignedAccessTokenIsRejected(t *testing.T) {
	issuer := newIssuer(t, nil)

	unsigned := jwt.NewWithClaims(jwt.SigningMethodNone, jwt.MapClaims{
		"iss": "einkaufsliste",
		"sub": userID,
		"typ": "access",
		"iat": jwt.NewNumericDate(time.Now()),
		"exp": jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	raw, err := unsigned.SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("building unsigned token: %v", err)
	}

	if _, err := issuer.ParseAccessToken(raw); !errors.Is(err, auth.ErrInvalidAccessToken) {
		t.Errorf("an alg=none token was accepted: err = %v", err)
	}
}

// The guard that jwt.WithValidMethods actually earns its place for.
//
// golang-jwt already refuses alg=none by itself, so the test above passes
// with or without the allow-list — it does not pin that option. Algorithm
// substitution does: HS512 signed with the same key is a perfectly valid
// HMAC token, and without an explicit allow-list the parser accepts it,
// letting a caller choose the algorithm. Removing WithValidMethods makes
// this test fail, which is the point of it.
func TestAccessTokenSignedWithAnotherAlgorithmIsRejected(t *testing.T) {
	issuer := newIssuer(t, nil)

	substituted := jwt.NewWithClaims(jwt.SigningMethodHS512, jwt.MapClaims{
		"iss": "einkaufsliste",
		"sub": userID,
		"typ": "access",
		"iat": jwt.NewNumericDate(time.Now()),
		"exp": jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	raw, err := substituted.SignedString(testKey(1))
	if err != nil {
		t.Fatalf("signing with HS512: %v", err)
	}

	if _, err := issuer.ParseAccessToken(raw); !errors.Is(err, auth.ErrInvalidAccessToken) {
		t.Errorf("an HS512 token was accepted where only HS256 is allowed: err = %v", err)
	}
}

// A token of a different type, signed with the same key, must not pass as an
// access token — otherwise any future token this server signs becomes a
// credential.
func TestTokenOfAnotherTypeIsRejected(t *testing.T) {
	issuer := newIssuer(t, nil)

	other := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": "einkaufsliste",
		"sub": userID,
		"typ": "invite",
		"iat": jwt.NewNumericDate(time.Now()),
		"exp": jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	raw, err := other.SignedString(testKey(1))
	if err != nil {
		t.Fatalf("signing: %v", err)
	}

	if _, err := issuer.ParseAccessToken(raw); !errors.Is(err, auth.ErrInvalidAccessToken) {
		t.Errorf("a token of type \"invite\" was accepted as an access token: err = %v", err)
	}
}

func TestAccessTokenRejectsJunk(t *testing.T) {
	issuer := newIssuer(t, nil)

	for _, raw := range []string{"", "nonsense", "a.b.c", strings.Repeat("x", 500)} {
		if _, err := issuer.ParseAccessToken(raw); err == nil {
			t.Errorf("%q was accepted", raw)
		}
	}
}

func TestAccessTokenNeedsAUser(t *testing.T) {
	issuer := newIssuer(t, nil)

	if _, err := issuer.NewAccessToken(""); err == nil {
		t.Error("minted an access token with no user id")
	}
}

// A short signing key produces tokens that verify perfectly and forge
// easily, so it has to fail loudly at construction.
func TestWeakSigningKeyIsRejected(t *testing.T) {
	for _, size := range []int{0, 1, 16, 31} {
		if _, err := auth.NewTokenIssuer(make([]byte, size), nil); err == nil {
			t.Errorf("a %d-byte signing key was accepted", size)
		}
	}

	if _, err := auth.NewTokenIssuer(make([]byte, 32), nil); err != nil {
		t.Errorf("a 32-byte key was rejected: %v", err)
	}
}

func TestRefreshTokensAreUnpredictable(t *testing.T) {
	issuer := newIssuer(t, nil)

	const count = 500
	seenTokens := make(map[string]struct{}, count)
	seenHashes := make(map[string]struct{}, count)

	for range count {
		token, err := issuer.NewRefreshToken()
		if err != nil {
			t.Fatalf("minting: %v", err)
		}

		if _, dup := seenTokens[token.Token]; dup {
			t.Fatal("the same refresh token was issued twice")
		}
		if _, dup := seenHashes[token.Hash]; dup {
			t.Fatal("two refresh tokens hashed to the same value")
		}
		seenTokens[token.Token] = struct{}{}
		seenHashes[token.Hash] = struct{}{}

		// 32 random bytes, base64url without padding.
		if len(token.Token) < 40 {
			t.Fatalf("refresh token is %d characters, which is too short to be 256 bits", len(token.Token))
		}
	}
}

func TestRefreshTokenHashIsDeterministicAndOneWay(t *testing.T) {
	issuer := newIssuer(t, nil)

	token, err := issuer.NewRefreshToken()
	if err != nil {
		t.Fatalf("minting: %v", err)
	}

	// Lookup depends on the same input producing the same hash.
	if got := auth.HashRefreshToken(token.Token); got != token.Hash {
		t.Errorf("hash = %q, want %q", got, token.Hash)
	}

	// The stored form must not contain the secret. If a database dump leaks,
	// nothing in it should be usable as a credential.
	if strings.Contains(token.Hash, token.Token) {
		t.Error("the hash contains the token itself")
	}
	if auth.HashRefreshToken(token.Token+"x") == token.Hash {
		t.Error("a different token hashed to the same value")
	}
}

func TestRefreshTokenExpiryIsSixtyDays(t *testing.T) {
	base := time.Now()
	issuer := newIssuer(t, func() time.Time { return base })

	token, err := issuer.NewRefreshToken()
	if err != nil {
		t.Fatalf("minting: %v", err)
	}

	if got := token.ExpiresAt.Sub(base); got != auth.RefreshTokenTTL {
		t.Errorf("expiry in %v, want %v", got, auth.RefreshTokenTTL)
	}
}

func TestEqualHashes(t *testing.T) {
	hash := auth.HashRefreshToken("a-token")

	if !auth.EqualHashes(hash, hash) {
		t.Error("a hash did not equal itself")
	}
	if auth.EqualHashes(hash, auth.HashRefreshToken("another-token")) {
		t.Error("two different hashes compared equal")
	}
	if auth.EqualHashes(hash, "") {
		t.Error("a hash compared equal to the empty string")
	}
	// A prefix must not match: a comparison that stops at the shorter length
	// would accept a truncated value.
	if auth.EqualHashes(hash, hash[:len(hash)-1]) {
		t.Error("a truncated hash compared equal")
	}
}
