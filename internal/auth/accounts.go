package auth

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hprotzek/einkaufsliste/internal/store"
)

// privateRelaySuffix marks Apple's per-app, per-user relay addresses. They can
// never legitimately match an existing account's email, so they are never
// linkable (spec §9).
const privateRelaySuffix = "@privaterelay.appleid.com"

// Accounts turns a verified identity into a user, creating or linking as
// spec §9 allows.
type Accounts struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

func NewAccounts(pool *pgxpool.Pool, log *slog.Logger) *Accounts {
	return &Accounts{pool: pool, log: log}
}

// Provision returns the user this identity belongs to.
//
// The rules, in order, all from §9:
//
//  1. A known (provider, subject) is that user. Email is never consulted —
//     it can change, and at Apple it can be a relay address.
//  2. Otherwise this provider is new for this person. Linking to an existing
//     account is permitted only when the incoming provider says the email is
//     verified AND the existing account's email was verified at signup. If
//     either is unverified, a separate account is created rather than
//     guessing, because loose email matching is an account-takeover vector.
//  3. A private-relay address is never linked.
//  4. Linking only ever adds a row to identities. Two existing user rows are
//     never merged: silently combining two people's lists is unrecoverable.
func (a *Accounts) Provision(ctx context.Context, identity Identity) (store.User, error) {
	tx, err := a.pool.Begin(ctx)
	if err != nil {
		return store.User{}, fmt.Errorf("auth: beginning provisioning: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	q := store.New(tx)

	user, err := a.provision(ctx, q, identity)
	if err != nil {
		return store.User{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return store.User{}, fmt.Errorf("auth: committing provisioning: %w", err)
	}

	return user, nil
}

func (a *Accounts) provision(ctx context.Context, q *store.Queries, identity Identity) (store.User, error) {
	// 1. Known identity: this is a returning user.
	existing, err := q.GetUserByIdentity(ctx, store.GetUserByIdentityParams{
		Provider: identity.Provider,
		Subject:  identity.Subject,
	})
	switch {
	case err == nil:
		return a.refreshProfile(ctx, q, existing, identity)
	case !errors.Is(err, pgx.ErrNoRows):
		return store.User{}, fmt.Errorf("auth: looking up identity: %w", err)
	}

	// 2. New identity. Decide whether it may join an existing account.
	if linkTo, ok, err := a.linkTarget(ctx, q, identity); err != nil {
		return store.User{}, err
	} else if ok {
		return a.link(ctx, q, linkTo, identity)
	}

	return a.create(ctx, q, identity)
}

// linkTarget reports the account this identity may attach to, if any.
func (a *Accounts) linkTarget(ctx context.Context, q *store.Queries, identity Identity) (store.User, bool, error) {
	if !identity.EmailVerified {
		a.log.InfoContext(ctx, "not linking: incoming email is unverified",
			slog.String("provider", identity.Provider))
		return store.User{}, false, nil
	}

	if isPrivateRelay(identity.Email) {
		a.log.InfoContext(ctx, "not linking: private relay address",
			slog.String("provider", identity.Provider))
		return store.User{}, false, nil
	}

	// Only verified accounts are candidates, which is the other half of §9's
	// condition: the existing account's email must have been verified too.
	candidate, err := q.GetVerifiedUserByEmail(ctx, identity.Email)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return store.User{}, false, nil
		}
		return store.User{}, false, fmt.Errorf("auth: looking for a linkable account: %w", err)
	}

	return candidate, true, nil
}

// link adds an identity to an existing user. It never merges user rows.
func (a *Accounts) link(ctx context.Context, q *store.Queries, user store.User, identity Identity) (store.User, error) {
	if _, err := q.CreateIdentity(ctx, store.CreateIdentityParams{
		UserID:   user.ID,
		Provider: identity.Provider,
		Subject:  identity.Subject,
	}); err != nil {
		return store.User{}, fmt.Errorf("auth: linking identity: %w", err)
	}

	// §9: "Log every link event with both provider subjects."
	subjects, err := q.ListIdentitySubjectsForUser(ctx, user.ID)
	if err != nil {
		return store.User{}, fmt.Errorf("auth: reading linked identities: %w", err)
	}

	linked := make([]string, 0, len(subjects))
	for _, s := range subjects {
		linked = append(linked, s.Provider+":"+s.Subject)
	}

	a.log.WarnContext(ctx, "linked a new provider to an existing account",
		slog.String("user_id", uuidFrom(user.ID).String()),
		slog.String("added", identity.Provider+":"+identity.Subject),
		slog.Any("identities", linked),
	)

	return a.refreshProfile(ctx, q, user, identity)
}

// create makes a new account and its first identity.
func (a *Accounts) create(ctx context.Context, q *store.Queries, identity Identity) (store.User, error) {
	// A private relay address is recorded as unverified however loudly the
	// provider asserts it. email_verified exists in this schema for exactly
	// one purpose — gating whether a future provider may link to this
	// account — and a per-app, per-user address must never gate that. Storing
	// it as verified would also claim the unique index for an address that is
	// not a durable identifier for anybody.
	verified := identity.EmailVerified && !isPrivateRelay(identity.Email)

	user, err := q.CreateUser(ctx, store.CreateUserParams{
		DisplayName:   displayName(identity),
		Email:         identity.Email,
		EmailVerified: verified,
		AvatarUrl:     optional(identity.Picture),
	})
	if err != nil {
		return store.User{}, fmt.Errorf("auth: creating user: %w", err)
	}

	if _, err := q.CreateIdentity(ctx, store.CreateIdentityParams{
		UserID:   user.ID,
		Provider: identity.Provider,
		Subject:  identity.Subject,
	}); err != nil {
		return store.User{}, fmt.Errorf("auth: creating identity: %w", err)
	}

	a.log.InfoContext(ctx, "created an account",
		slog.String("user_id", uuidFrom(user.ID).String()),
		slog.String("provider", identity.Provider),
		slog.Bool("email_verified", verified),
	)

	return user, nil
}

// refreshProfile keeps the display name and avatar current, since the
// provider is authoritative for both and people change them.
func (a *Accounts) refreshProfile(ctx context.Context, q *store.Queries, user store.User, identity Identity) (store.User, error) {
	name := displayName(identity)
	avatar := optional(identity.Picture)

	if name == user.DisplayName && sameOptional(avatar, user.AvatarUrl) {
		return user, nil
	}

	updated, err := q.UpdateUserProfile(ctx, store.UpdateUserProfileParams{
		ID:          user.ID,
		DisplayName: name,
		AvatarUrl:   avatar,
	})
	if err != nil {
		return store.User{}, fmt.Errorf("auth: updating profile: %w", err)
	}

	return updated, nil
}

// displayName falls back to the local part of the email, because a display
// name is NOT NULL and some providers omit the claim.
func displayName(identity Identity) string {
	if identity.Name != "" {
		return identity.Name
	}
	if local, _, found := strings.Cut(identity.Email, "@"); found && local != "" {
		return local
	}
	return identity.Subject
}

func isPrivateRelay(email string) bool {
	return strings.HasSuffix(strings.ToLower(email), privateRelaySuffix)
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func sameOptional(a, b *string) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return *a == *b
	}
}
