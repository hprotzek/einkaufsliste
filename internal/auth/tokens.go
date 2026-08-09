package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	// AccessTokenTTL is short because an access token cannot be revoked: it
	// is verified by signature alone, with no database lookup. Fifteen
	// minutes bounds how long a stolen one is useful (spec §9).
	AccessTokenTTL = 15 * time.Minute

	// RefreshTokenTTL is long because the refresh token *can* be revoked —
	// it is stored, and rotation with reuse detection (task 1.5) is what
	// makes a long life safe.
	RefreshTokenTTL = 60 * 24 * time.Hour

	// issuerName identifies tokens this server minted. Ours are our own; a
	// provider's tokens are never passed around (§9).
	issuerName = "einkaufsliste"

	// accessTokenType is carried in the token so an access token can never
	// be mistaken for some other JWT this system might sign later.
	accessTokenType = "access"

	// refreshTokenBytes is the entropy in an opaque refresh token. 256 bits
	// is far past guessable, which is what lets the stored form be a fast
	// hash rather than a slow one.
	refreshTokenBytes = 32

	// minSigningKeyBytes rejects a key shorter than the HMAC output it
	// feeds. A short key is the failure that looks like it works.
	minSigningKeyBytes = 32
)

var (
	// ErrInvalidAccessToken covers every reason an access token was not
	// acceptable. As with ID tokens, the reason is not reported back.
	ErrInvalidAccessToken = errors.New("auth: invalid access token")

	// ErrExpiredAccessToken is separate because the client acts on it: it
	// means "refresh and retry", not "sign in again".
	ErrExpiredAccessToken = errors.New("auth: access token expired")
)

// Clock lets tests move time without sleeping. Production passes nil and
// gets time.Now.
type Clock func() time.Time

// TokenIssuer mints and checks this server's own tokens.
type TokenIssuer struct {
	signingKey []byte
	now        Clock
}

// NewTokenIssuer fails on a weak key rather than accepting one, because a
// short signing key produces tokens that verify perfectly and forge easily.
func NewTokenIssuer(signingKey []byte, now Clock) (*TokenIssuer, error) {
	if len(signingKey) < minSigningKeyBytes {
		return nil, fmt.Errorf("auth: signing key is %d bytes, need at least %d",
			len(signingKey), minSigningKeyBytes)
	}
	if now == nil {
		now = time.Now
	}

	return &TokenIssuer{signingKey: signingKey, now: now}, nil
}

// AccessToken is a freshly minted access token and when it stops working.
type AccessToken struct {
	Token     string
	ExpiresAt time.Time
}

// NewAccessToken mints a JWT identifying the user.
//
// HS256 rather than RS256: one process signs these and the same process
// verifies them, so an asymmetric pair would add key distribution and buy
// nothing. Revisit if anything else ever needs to verify a token without
// holding the secret.
func (t *TokenIssuer) NewAccessToken(userID string) (AccessToken, error) {
	if userID == "" {
		return AccessToken{}, errors.New("auth: cannot mint an access token without a user id")
	}

	now := t.now()
	expiresAt := now.Add(AccessTokenTTL)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"iss": issuerName,
		"sub": userID,
		"iat": jwt.NewNumericDate(now),
		"exp": jwt.NewNumericDate(expiresAt),
		"typ": accessTokenType,
	})

	signed, err := token.SignedString(t.signingKey)
	if err != nil {
		return AccessToken{}, fmt.Errorf("auth: signing access token: %w", err)
	}

	return AccessToken{Token: signed, ExpiresAt: expiresAt}, nil
}

// ParseAccessToken verifies an access token and returns the user id it
// carries.
func (t *TokenIssuer) ParseAccessToken(raw string) (string, error) {
	parsed, err := jwt.Parse(raw,
		func(*jwt.Token) (any, error) { return t.signingKey, nil },
		// Without this, a token claiming alg=none — or any algorithm the
		// attacker prefers — would be handed to the keyfunc. Pinning the
		// method is what closes the algorithm-confusion hole.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuerName),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(t.now),
	)
	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return "", ErrExpiredAccessToken
		}
		return "", fmt.Errorf("%w: %w", ErrInvalidAccessToken, err)
	}

	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return "", fmt.Errorf("%w: unexpected claims type", ErrInvalidAccessToken)
	}

	// A refresh or invite token signed with the same key must not be usable
	// here, however well-formed it is.
	if typ, _ := claims["typ"].(string); typ != accessTokenType {
		return "", fmt.Errorf("%w: token type %q", ErrInvalidAccessToken, typ)
	}

	subject, _ := claims["sub"].(string)
	if subject == "" {
		return "", fmt.Errorf("%w: no subject", ErrInvalidAccessToken)
	}

	return subject, nil
}

// RefreshToken is a newly minted refresh token. Only Hash is ever stored;
// Token is shown to the client once and never again.
type RefreshToken struct {
	// Token is the opaque secret handed to the client.
	Token string
	// Hash is what goes in the database.
	Hash string
	// ExpiresAt is when it stops being accepted.
	ExpiresAt time.Time
}

// NewRefreshToken mints an opaque token and its stored hash.
//
// Opaque rather than a JWT because a refresh token must be revocable, and
// revocation means a database lookup — at which point the claims a JWT
// carries are dead weight, and a stolen one that has not yet expired would
// be impossible to stop.
func (t *TokenIssuer) NewRefreshToken() (RefreshToken, error) {
	raw := make([]byte, refreshTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return RefreshToken{}, fmt.Errorf("auth: generating refresh token: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(raw)

	return RefreshToken{
		Token:     token,
		Hash:      HashRefreshToken(token),
		ExpiresAt: t.now().Add(RefreshTokenTTL),
	}, nil
}

// HashRefreshToken is how a presented token is looked up.
//
// SHA-256, not bcrypt or argon2. Those exist to make guessing a low-entropy
// secret slow; this token is 256 bits from crypto/rand, so guessing is not
// the threat. A slow hash here would only make every refresh slower, and
// refresh happens on every session.
func HashRefreshToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// EqualHashes compares two token hashes in constant time.
//
// The hashes are not secrets in the usual sense, but a lookup that leaks
// where two values start to differ leaks how close a guess was, and this
// costs nothing.
func EqualHashes(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
