package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hprotzek/einkaufsliste/internal/store"
)

var (
	// ErrInvalidRefreshToken means the token is unknown, expired, or belongs
	// to a family that has been revoked. The client's only move is to sign in
	// again.
	ErrInvalidRefreshToken = errors.New("auth: invalid refresh token")

	// ErrRefreshTokenReused means a token was presented twice. The family is
	// revoked by the time this is returned.
	//
	// It is distinct from ErrInvalidRefreshToken for logging and alerting, not
	// for the client: both end the session, and the API must not tell a caller
	// which of the two happened. Knowing "that one was already used" tells an
	// attacker their stolen token was genuine.
	ErrRefreshTokenReused = errors.New("auth: refresh token reused")
)

// Session is a freshly issued pair. The refresh token's plaintext appears
// here once and is never recoverable afterwards.
type Session struct {
	Access  AccessToken
	Refresh RefreshToken
	// FamilyID ties this refresh token to the sign-in it descends from.
	FamilyID uuid.UUID
	UserID   uuid.UUID
}

// Sessions issues and rotates refresh tokens.
type Sessions struct {
	pool   *pgxpool.Pool
	tokens *TokenIssuer
}

// NewSessions wires rotation to a database.
func NewSessions(pool *pgxpool.Pool, tokens *TokenIssuer) *Sessions {
	return &Sessions{pool: pool, tokens: tokens}
}

// Issue starts a new session: a new family, a new refresh token, and an
// access token. Called after a successful sign-in.
func (s *Sessions) Issue(ctx context.Context, userID uuid.UUID) (Session, error) {
	return s.mint(ctx, s.pool, userID, uuid.New())
}

// Rotate exchanges a refresh token for a new pair.
//
// The exchange is a single conditional UPDATE, so two requests arriving
// together with the same token cannot both succeed: exactly one marks it
// used. Whichever loses is then indistinguishable from a replay, which is
// the correct outcome — a token really was presented twice.
//
// When the token turns out to have been spent already, the entire family is
// revoked. Both the thief and the legitimate client are logged out, because
// nothing in the request says which one this is, and leaving the real user
// signed in would mean leaving the attacker signed in too.
func (s *Sessions) Rotate(ctx context.Context, refreshToken string) (Session, error) {
	hash := HashRefreshToken(refreshToken)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("auth: beginning rotation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := store.New(tx)

	consumed, err := q.ConsumeRefreshToken(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// Nothing was consumable. Roll back before diagnosing, so the
			// revocation below is not tangled up in this transaction.
			_ = tx.Rollback(ctx)
			return Session{}, s.diagnoseFailedConsume(ctx, hash)
		}
		return Session{}, fmt.Errorf("auth: consuming refresh token: %w", err)
	}

	session, err := s.mint(ctx, tx, uuidFrom(consumed.UserID), uuidFrom(consumed.FamilyID))
	if err != nil {
		return Session{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("auth: committing rotation: %w", err)
	}

	return session, nil
}

// diagnoseFailedConsume works out why a token could not be consumed, and
// revokes the family if the answer is that it had already been used.
func (s *Sessions) diagnoseFailedConsume(ctx context.Context, hash string) error {
	q := store.New(s.pool)

	existing, err := q.GetRefreshTokenByHash(ctx, hash)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			// No such token ever existed. Nothing to revoke.
			return ErrInvalidRefreshToken
		}
		return fmt.Errorf("auth: looking up refresh token: %w", err)
	}

	// Already spent: this is a replay. Kill the family.
	if existing.UsedAt.Valid {
		if _, err := q.RevokeRefreshTokenFamily(ctx, existing.FamilyID); err != nil {
			return fmt.Errorf("auth: revoking reused token family: %w", err)
		}
		return ErrRefreshTokenReused
	}

	// Revoked or expired. Both mean sign in again, and neither is an attack
	// worth distinguishing to the caller.
	return ErrInvalidRefreshToken
}

// RevokeFamily ends one session, which is what logout does.
func (s *Sessions) RevokeFamily(ctx context.Context, familyID uuid.UUID) error {
	q := store.New(s.pool)

	if _, err := q.RevokeRefreshTokenFamily(ctx, pgUUID(familyID)); err != nil {
		return fmt.Errorf("auth: revoking family: %w", err)
	}
	return nil
}

// RevokeAllForUser ends every session a user has, everywhere.
func (s *Sessions) RevokeAllForUser(ctx context.Context, userID uuid.UUID) error {
	q := store.New(s.pool)

	if _, err := q.RevokeRefreshTokensForUser(ctx, pgUUID(userID)); err != nil {
		return fmt.Errorf("auth: revoking user sessions: %w", err)
	}
	return nil
}

// mint writes a new refresh token and pairs it with an access token.
func (s *Sessions) mint(ctx context.Context, db store.DBTX, userID, familyID uuid.UUID) (Session, error) {
	refresh, err := s.tokens.NewRefreshToken()
	if err != nil {
		return Session{}, err
	}

	if _, insertErr := store.New(db).InsertRefreshToken(ctx, store.InsertRefreshTokenParams{
		UserID:    pgUUID(userID),
		FamilyID:  pgUUID(familyID),
		TokenHash: refresh.Hash,
		ExpiresAt: pgtype.Timestamptz{Time: refresh.ExpiresAt, Valid: true},
	}); insertErr != nil {
		return Session{}, fmt.Errorf("auth: storing refresh token: %w", insertErr)
	}

	access, err := s.tokens.NewAccessToken(userID.String())
	if err != nil {
		return Session{}, err
	}

	return Session{
		Access:   access,
		Refresh:  refresh,
		FamilyID: familyID,
		UserID:   userID,
	}, nil
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func uuidFrom(id pgtype.UUID) uuid.UUID {
	return id.Bytes
}
