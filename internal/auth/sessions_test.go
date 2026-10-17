package auth_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/hprotzek/einkaufsliste/internal/auth"
	"github.com/hprotzek/einkaufsliste/internal/dbtest"
	"github.com/hprotzek/einkaufsliste/internal/store"
)

// newSessions gives a Sessions wired to a real, migrated database, plus a
// user to hang tokens off.
func newSessions(t *testing.T) (*auth.Sessions, *pgxpool.Pool, uuid.UUID) {
	t.Helper()

	ctx := t.Context()

	pool, err := store.NewPool(ctx, dbtest.New(t))
	if err != nil {
		t.Fatalf("opening pool: %v", err)
	}
	t.Cleanup(pool.Close)

	if migrateErr := store.Migrate(ctx, pool, slog.New(slog.NewJSONHandler(io.Discard, nil))); migrateErr != nil {
		t.Fatalf("migrating: %v", migrateErr)
	}

	var id uuid.UUID
	if userErr := pool.QueryRow(ctx,
		`INSERT INTO users (display_name, email) VALUES ('Kari', 'kari@example.no') RETURNING id`,
	).Scan(&id); userErr != nil {
		t.Fatalf("creating user: %v", userErr)
	}

	issuer, err := auth.NewTokenIssuer(testKey(7), nil)
	if err != nil {
		t.Fatalf("building token issuer: %v", err)
	}

	return auth.NewSessions(pool, issuer), pool, id
}

func liveTokens(t *testing.T, pool *pgxpool.Pool, family uuid.UUID) int {
	t.Helper()

	var live int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM refresh_tokens WHERE family_id = $1 AND revoked_at IS NULL`,
		family,
	).Scan(&live); err != nil {
		t.Fatalf("counting live tokens: %v", err)
	}
	return live
}

func TestIssueThenRotate(t *testing.T) {
	sessions, _, userID := newSessions(t)
	ctx := t.Context()

	first, err := sessions.Issue(ctx, userID)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	if first.Refresh.Token == "" || first.Access.Token == "" {
		t.Fatal("issued an empty token")
	}

	second, err := sessions.Rotate(ctx, first.Refresh.Token)
	if err != nil {
		t.Fatalf("rotating: %v", err)
	}

	if second.Refresh.Token == first.Refresh.Token {
		t.Error("rotation returned the same refresh token")
	}
	// Rotation continues a session rather than starting one, so the family
	// carries over; that is what makes reuse detectable across the chain.
	if second.FamilyID != first.FamilyID {
		t.Errorf("family changed on rotation: %s -> %s", first.FamilyID, second.FamilyID)
	}
	if second.UserID != userID {
		t.Errorf("user = %s, want %s", second.UserID, userID)
	}
}

func TestRotationChainsManyTimes(t *testing.T) {
	sessions, _, userID := newSessions(t)
	ctx := t.Context()

	session, err := sessions.Issue(ctx, userID)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}

	family := session.FamilyID
	for i := range 10 {
		session, err = sessions.Rotate(ctx, session.Refresh.Token)
		if err != nil {
			t.Fatalf("rotation %d: %v", i+1, err)
		}
		if session.FamilyID != family {
			t.Fatalf("family changed at rotation %d", i+1)
		}
	}
}

// The test the plan names: "replayed token kills the family".
func TestReplayedTokenRevokesTheWholeFamily(t *testing.T) {
	sessions, pool, userID := newSessions(t)
	ctx := t.Context()

	// A session, rotated a few times, as a real client would.
	first, err := sessions.Issue(ctx, userID)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	stolen := first.Refresh.Token

	current, err := sessions.Rotate(ctx, stolen)
	if err != nil {
		t.Fatalf("rotating: %v", err)
	}
	current, err = sessions.Rotate(ctx, current.Refresh.Token)
	if err != nil {
		t.Fatalf("rotating again: %v", err)
	}

	if live := liveTokens(t, pool, first.FamilyID); live == 0 {
		t.Fatal("no live tokens before the replay; the test proves nothing")
	}

	// The attacker replays the token they captured earlier.
	ageUsedTokens(t, pool)
	_, err = sessions.Rotate(ctx, stolen)
	if !errors.Is(err, auth.ErrRefreshTokenReused) {
		t.Fatalf("error = %v, want ErrRefreshTokenReused", err)
	}

	// Every token in the family is now dead, including the one the honest
	// client is holding. That is deliberate: nothing here can tell the thief
	// from the victim, and leaving the victim signed in leaves the thief
	// signed in too.
	if live := liveTokens(t, pool, first.FamilyID); live != 0 {
		t.Errorf("live tokens after replay = %d, want 0", live)
	}

	if _, err := sessions.Rotate(ctx, current.Refresh.Token); !errors.Is(err, auth.ErrInvalidRefreshToken) {
		t.Errorf("the honest client's token still worked: err = %v", err)
	}
}

// One family being compromised must not sign out the user's other devices.
func TestRevocationIsScopedToOneFamily(t *testing.T) {
	sessions, pool, userID := newSessions(t)
	ctx := t.Context()

	phone, err := sessions.Issue(ctx, userID)
	if err != nil {
		t.Fatalf("issuing phone session: %v", err)
	}
	laptop, err := sessions.Issue(ctx, userID)
	if err != nil {
		t.Fatalf("issuing laptop session: %v", err)
	}
	if phone.FamilyID == laptop.FamilyID {
		t.Fatal("two sign-ins shared a family")
	}

	rotated, err := sessions.Rotate(ctx, phone.Refresh.Token)
	if err != nil {
		t.Fatalf("rotating phone: %v", err)
	}
	ageUsedTokens(t, pool)
	if _, err := sessions.Rotate(ctx, phone.Refresh.Token); !errors.Is(err, auth.ErrRefreshTokenReused) {
		t.Fatalf("replay not detected: %v", err)
	}

	// The phone is out.
	if _, err := sessions.Rotate(ctx, rotated.Refresh.Token); !errors.Is(err, auth.ErrInvalidRefreshToken) {
		t.Errorf("phone token survived: %v", err)
	}
	// The laptop is untouched.
	if _, err := sessions.Rotate(ctx, laptop.Refresh.Token); err != nil {
		t.Errorf("laptop session was revoked too: %v", err)
	}
}

func TestUnknownTokenIsRejectedWithoutRevokingAnything(t *testing.T) {
	sessions, pool, userID := newSessions(t)
	ctx := t.Context()

	session, err := sessions.Issue(ctx, userID)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}

	if _, err := sessions.Rotate(ctx, "a-token-that-was-never-issued"); !errors.Is(err, auth.ErrInvalidRefreshToken) {
		t.Errorf("error = %v, want ErrInvalidRefreshToken", err)
	}

	// A guess must not be able to log anybody out.
	if live := liveTokens(t, pool, session.FamilyID); live != 1 {
		t.Errorf("live tokens = %d, want 1 — an unknown token disturbed a real session", live)
	}
}

func TestRevokeFamilyEndsTheSession(t *testing.T) {
	sessions, _, userID := newSessions(t)
	ctx := t.Context()

	session, err := sessions.Issue(ctx, userID)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}

	if err := sessions.RevokeFamily(ctx, session.FamilyID); err != nil {
		t.Fatalf("revoking: %v", err)
	}

	// A revoked token must not be reported as reuse: it was never spent, and
	// calling it an attack would raise a false alarm every time somebody logs
	// out and their client retries.
	if _, err := sessions.Rotate(ctx, session.Refresh.Token); !errors.Is(err, auth.ErrInvalidRefreshToken) {
		t.Errorf("error = %v, want ErrInvalidRefreshToken", err)
	}
}

func TestRevokeAllForUserEndsEverySession(t *testing.T) {
	sessions, _, userID := newSessions(t)
	ctx := t.Context()

	var issued []auth.Session
	for range 3 {
		session, err := sessions.Issue(ctx, userID)
		if err != nil {
			t.Fatalf("issuing: %v", err)
		}
		issued = append(issued, session)
	}

	if err := sessions.RevokeAllForUser(ctx, userID); err != nil {
		t.Fatalf("revoking all: %v", err)
	}

	for i, session := range issued {
		if _, err := sessions.Rotate(ctx, session.Refresh.Token); !errors.Is(err, auth.ErrInvalidRefreshToken) {
			t.Errorf("session %d survived: %v", i, err)
		}
	}
}

// ageUsedTokens moves every spent token's use further into the past, past
// the reuse grace window. Tests that model a thief replaying a token captured
// earlier use it: a replay seconds after the rotation is the benign race the
// grace window exists for, and is covered by its own tests below.
func ageUsedTokens(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()

	if _, err := pool.Exec(t.Context(),
		`UPDATE refresh_tokens SET used_at = used_at - interval '1 minute' WHERE used_at IS NOT NULL`,
	); err != nil {
		t.Fatalf("ageing used tokens: %v", err)
	}
}

// Two tabs (or the installed app and a browser tab) share one refresh
// cookie. When both refresh at the same moment, exactly one rotation wins.
// The loser must not take the winner down with it: before the grace window,
// the loser was read as a replay, the family was revoked, and the token the
// winner had just received died with it — both tabs signed out.
func TestConcurrentRefreshKeepsTheWinnerSignedIn(t *testing.T) {
	sessions, pool, userID := newSessions(t)
	ctx := context.Background()

	session, err := sessions.Issue(t.Context(), userID)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		winner string
		losers []error
	)
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			s, err := sessions.Rotate(ctx, session.Refresh.Token)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				winner = s.Refresh.Token
				return
			}
			losers = append(losers, err)
		}()
	}
	wg.Wait()

	if winner == "" {
		t.Fatal("neither rotation won")
	}
	for _, err := range losers {
		if !errors.Is(err, auth.ErrRefreshTokenRaced) {
			t.Errorf("loser error = %v, want ErrRefreshTokenRaced", err)
		}
	}
	var revoked int
	if err := pool.QueryRow(t.Context(),
		`SELECT count(*) FROM refresh_tokens WHERE family_id = $1 AND revoked_at IS NOT NULL`,
		session.FamilyID,
	).Scan(&revoked); err != nil {
		t.Fatalf("counting revoked tokens: %v", err)
	}
	if revoked != 0 {
		t.Errorf("revoked tokens after the race = %d, want 0", revoked)
	}
	if _, err := sessions.Rotate(ctx, winner); err != nil {
		t.Errorf("the winning tab's new refresh token no longer works: %v", err)
	}
}

// The grace window must not become a way around reuse detection. Once the
// successor has itself been used, the old token is history, and presenting
// it again is a replay however quickly it happens.
func TestReuseAfterTheSuccessorWasUsedIsAReplay(t *testing.T) {
	sessions, pool, userID := newSessions(t)
	ctx := t.Context()

	first, err := sessions.Issue(ctx, userID)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	second, err := sessions.Rotate(ctx, first.Refresh.Token)
	if err != nil {
		t.Fatalf("rotating: %v", err)
	}
	if _, err := sessions.Rotate(ctx, second.Refresh.Token); err != nil {
		t.Fatalf("rotating again: %v", err)
	}

	// Well inside the grace window, but the chain has moved on.
	if _, err := sessions.Rotate(ctx, first.Refresh.Token); !errors.Is(err, auth.ErrRefreshTokenReused) {
		t.Fatalf("error = %v, want ErrRefreshTokenReused", err)
	}
	if live := liveTokens(t, pool, first.FamilyID); live != 0 {
		t.Errorf("live tokens after replay = %d, want 0", live)
	}
}

// After the grace window, a token whose successor is still unused is a
// replay too: no honest client takes that long to lose a race.
func TestReuseAfterTheGraceWindowIsAReplay(t *testing.T) {
	sessions, pool, userID := newSessions(t)
	ctx := t.Context()

	first, err := sessions.Issue(ctx, userID)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}
	if _, err := sessions.Rotate(ctx, first.Refresh.Token); err != nil {
		t.Fatalf("rotating: %v", err)
	}

	ageUsedTokens(t, pool)

	if _, err := sessions.Rotate(ctx, first.Refresh.Token); !errors.Is(err, auth.ErrRefreshTokenReused) {
		t.Fatalf("error = %v, want ErrRefreshTokenReused", err)
	}
	if live := liveTokens(t, pool, first.FamilyID); live != 0 {
		t.Errorf("live tokens after replay = %d, want 0", live)
	}
}

// Many requests racing with the same token: exactly one wins, and every
// loser is a benign race rather than a replay, because they all arrived
// while the winner's successor was fresh and unused.
func TestConcurrentRotationLetsExactlyOneWin(t *testing.T) {
	sessions, _, userID := newSessions(t)
	ctx := context.Background()

	session, err := sessions.Issue(t.Context(), userID)
	if err != nil {
		t.Fatalf("issuing: %v", err)
	}

	const racers = 8

	var (
		wg        sync.WaitGroup
		mu        sync.Mutex
		succeeded int
		raced     int
		other     []error
	)

	wg.Add(racers)
	for range racers {
		go func() {
			defer wg.Done()

			_, err := sessions.Rotate(ctx, session.Refresh.Token)

			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				succeeded++
			case errors.Is(err, auth.ErrRefreshTokenRaced):
				raced++
			default:
				other = append(other, err)
			}
		}()
	}
	wg.Wait()

	if succeeded != 1 {
		t.Errorf("rotations that succeeded = %d, want exactly 1", succeeded)
	}
	if len(other) > 0 {
		t.Errorf("unexpected errors: %v", other)
	}
	if raced != racers-1 {
		t.Errorf("benign races = %d, want %d", raced, racers-1)
	}
}
