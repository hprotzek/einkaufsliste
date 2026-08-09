package auth_test

import (
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hprotzek/einkaufsliste/internal/auth"
	"github.com/hprotzek/einkaufsliste/internal/dbtest"
	"github.com/hprotzek/einkaufsliste/internal/store"
)

func newAccounts(t *testing.T) (*auth.Accounts, *pgxpool.Pool) {
	t.Helper()

	ctx := t.Context()

	pool, err := store.NewPool(ctx, dbtest.New(t))
	if err != nil {
		t.Fatalf("opening pool: %v", err)
	}
	t.Cleanup(pool.Close)

	log := slog.New(slog.NewJSONHandler(io.Discard, nil))
	if migrateErr := store.Migrate(ctx, pool, log); migrateErr != nil {
		t.Fatalf("migrating: %v", migrateErr)
	}

	return auth.NewAccounts(pool, log), pool
}

func googleIdentity(subject, email string, verified bool) auth.Identity {
	return auth.Identity{
		Provider:      "google",
		Subject:       subject,
		Email:         email,
		EmailVerified: verified,
		Name:          "Kari Nordmann",
		Picture:       "https://example.invalid/kari.png",
	}
}

func countUsers(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()

	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM users`).Scan(&n); err != nil {
		t.Fatalf("counting users: %v", err)
	}
	return n
}

func TestProvisionCreatesThenRecognisesAUser(t *testing.T) {
	accounts, pool := newAccounts(t)
	ctx := t.Context()

	identity := googleIdentity("sub-1", "kari@example.no", true)

	created, err := accounts.Provision(ctx, identity)
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}
	if created.Email != identity.Email || !created.EmailVerified {
		t.Errorf("stored %q verified=%v, want %q verified=true", created.Email, created.EmailVerified, identity.Email)
	}

	again, err := accounts.Provision(ctx, identity)
	if err != nil {
		t.Fatalf("second sign-in: %v", err)
	}
	if again.ID != created.ID {
		t.Error("a returning user got a second account")
	}
	if n := countUsers(t, pool); n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
}

// The identity key is (provider, subject). A provider changing the email on
// an existing account must not create a second one.
func TestEmailChangeDoesNotCreateASecondAccount(t *testing.T) {
	accounts, pool := newAccounts(t)
	ctx := t.Context()

	first, err := accounts.Provision(ctx, googleIdentity("sub-1", "old@example.no", true))
	if err != nil {
		t.Fatalf("first sign-in: %v", err)
	}

	second, err := accounts.Provision(ctx, googleIdentity("sub-1", "new@example.no", true))
	if err != nil {
		t.Fatalf("second sign-in: %v", err)
	}

	if second.ID != first.ID {
		t.Error("changing the provider's email created a new account")
	}
	if n := countUsers(t, pool); n != 1 {
		t.Errorf("users = %d, want 1", n)
	}
}

func TestProfileIsRefreshedOnEachSignIn(t *testing.T) {
	accounts, _ := newAccounts(t)
	ctx := t.Context()

	if _, err := accounts.Provision(ctx, googleIdentity("sub-1", "kari@example.no", true)); err != nil {
		t.Fatalf("first sign-in: %v", err)
	}

	renamed := googleIdentity("sub-1", "kari@example.no", true)
	renamed.Name = "Kari Nordmann-Hansen"
	renamed.Picture = "https://example.invalid/new.png"

	updated, err := accounts.Provision(ctx, renamed)
	if err != nil {
		t.Fatalf("second sign-in: %v", err)
	}

	if updated.DisplayName != renamed.Name {
		t.Errorf("display name = %q, want %q", updated.DisplayName, renamed.Name)
	}
	if updated.AvatarUrl == nil || *updated.AvatarUrl != renamed.Picture {
		t.Errorf("avatar = %v, want %q", updated.AvatarUrl, renamed.Picture)
	}
}

// §9's linking rule: link only when the incoming provider says verified AND
// the existing account's email was verified at signup.
func TestLinkingRules(t *testing.T) {
	tests := []struct {
		name string
		// existing is provisioned first, incoming second.
		existing auth.Identity
		incoming auth.Identity
		wantLink bool
		why      string
	}{
		{
			name:     "both verified, same email",
			existing: googleIdentity("google-sub", "kari@example.no", true),
			incoming: auth.Identity{Provider: "hypothetical", Subject: "other-sub", Email: "kari@example.no", EmailVerified: true},
			wantLink: true,
			why:      "both sides verified, so this is the same person",
		},
		{
			name:     "incoming unverified",
			existing: googleIdentity("google-sub", "kari@example.no", true),
			incoming: auth.Identity{Provider: "hypothetical", Subject: "other-sub", Email: "kari@example.no", EmailVerified: false},
			wantLink: false,
			why:      "an unverified claim on somebody's address must not inherit their lists",
		},
		{
			name:     "existing account unverified",
			existing: googleIdentity("google-sub", "kari@example.no", false),
			incoming: auth.Identity{Provider: "hypothetical", Subject: "other-sub", Email: "kari@example.no", EmailVerified: true},
			wantLink: false,
			why:      "the existing account never proved the address either",
		},
		{
			name:     "private relay address",
			existing: googleIdentity("google-sub", "kari@example.no", true),
			incoming: auth.Identity{Provider: "hypothetical", Subject: "other-sub", Email: "kari@example.no", EmailVerified: true, Picture: ""},
			wantLink: true,
			why:      "control case for the relay test below",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			accounts, pool := newAccounts(t)
			ctx := t.Context()

			first, err := accounts.Provision(ctx, tc.existing)
			if err != nil {
				t.Fatalf("provisioning the existing account: %v", err)
			}

			second, err := accounts.Provision(ctx, tc.incoming)
			if err != nil {
				t.Fatalf("provisioning the incoming identity: %v", err)
			}

			linked := second.ID == first.ID
			if linked != tc.wantLink {
				t.Errorf("linked = %v, want %v — %s", linked, tc.wantLink, tc.why)
			}

			wantUsers := 2
			if tc.wantLink {
				wantUsers = 1
			}
			if n := countUsers(t, pool); n != wantUsers {
				t.Errorf("users = %d, want %d", n, wantUsers)
			}
		})
	}
}

// §9: "Never link on an Apple private-relay address — it is per-app and
// per-user, so it can never legitimately match an existing Google email."
func TestPrivateRelayAddressIsNeverLinked(t *testing.T) {
	accounts, pool := newAccounts(t)
	ctx := t.Context()

	const relay = "abc123xyz@privaterelay.appleid.com"

	first, err := accounts.Provision(ctx, googleIdentity("google-sub", relay, true))
	if err != nil {
		t.Fatalf("provisioning the existing account: %v", err)
	}

	// Same address, verified on both sides — and still not linkable.
	second, err := accounts.Provision(ctx, auth.Identity{
		Provider:      "hypothetical",
		Subject:       "apple-sub",
		Email:         relay,
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("provisioning the relay identity: %v", err)
	}

	if second.ID == first.ID {
		t.Error("a private-relay address was linked to an existing account")
	}
	if n := countUsers(t, pool); n != 2 {
		t.Errorf("users = %d, want 2", n)
	}
}

// §11.4: "Two existing users, one per provider, same email → no merge."
// Linking only ever adds an identity row; it never combines two user rows,
// because merging two people's lists cannot be undone.
func TestTwoExistingAccountsAreNeverMerged(t *testing.T) {
	accounts, pool := newAccounts(t)
	ctx := t.Context()

	// Two accounts on one address, which the partial unique index allows
	// only because at most one of them is verified.
	verified, err := accounts.Provision(ctx, googleIdentity("google-sub", "kari@example.no", true))
	if err != nil {
		t.Fatalf("provisioning the verified account: %v", err)
	}
	unverified, err := accounts.Provision(ctx, auth.Identity{
		Provider:      "hypothetical",
		Subject:       "other-sub",
		Email:         "kari@example.no",
		EmailVerified: false,
	})
	if err != nil {
		t.Fatalf("provisioning the unverified account: %v", err)
	}
	if verified.ID == unverified.ID {
		t.Fatal("the unverified identity was linked; the test needs two accounts")
	}

	// Signing in again with either must land on the same account it made,
	// not merge them.
	backAsVerified, err := accounts.Provision(ctx, googleIdentity("google-sub", "kari@example.no", true))
	if err != nil {
		t.Fatalf("signing in again: %v", err)
	}
	if backAsVerified.ID != verified.ID {
		t.Error("the verified account moved")
	}

	if n := countUsers(t, pool); n != 2 {
		t.Errorf("users = %d, want 2 — the accounts were merged", n)
	}
}

// The invariant the partial unique index exists to hold: two *verified*
// accounts can never share an address, whatever the linking code does.
func TestTwoVerifiedAccountsCannotShareAnEmail(t *testing.T) {
	_, pool := newAccounts(t)
	ctx := t.Context()

	if _, err := pool.Exec(ctx,
		`INSERT INTO users (display_name, email, email_verified) VALUES ('A', 'shared@example.no', true)`,
	); err != nil {
		t.Fatalf("inserting the first user: %v", err)
	}

	_, err := pool.Exec(ctx,
		`INSERT INTO users (display_name, email, email_verified) VALUES ('B', 'shared@example.no', true)`)
	if err == nil {
		t.Fatal("a second verified account on the same address was allowed")
	}

	// An unverified one on the same address is fine, which is what makes
	// §9's "create a separate account" possible at all.
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (display_name, email, email_verified) VALUES ('C', 'shared@example.no', false)`,
	); err != nil {
		t.Errorf("an unverified duplicate was rejected: %v", err)
	}
}

func TestDisplayNameFallsBackWhenTheProviderOmitsIt(t *testing.T) {
	accounts, _ := newAccounts(t)
	ctx := t.Context()

	user, err := accounts.Provision(ctx, auth.Identity{
		Provider:      "google",
		Subject:       "sub-nameless",
		Email:         "nameless@example.no",
		EmailVerified: true,
	})
	if err != nil {
		t.Fatalf("provisioning: %v", err)
	}

	// display_name is NOT NULL, and some providers simply do not send one.
	if user.DisplayName != "nameless" {
		t.Errorf("display name = %q, want %q", user.DisplayName, "nameless")
	}
}
