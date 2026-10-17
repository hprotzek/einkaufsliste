-- InsertRefreshToken records a newly issued token. The caller supplies the
-- family: a fresh uuid for a new sign-in, or the existing one when rotating.
-- name: InsertRefreshToken :one
INSERT INTO refresh_tokens (user_id, family_id, token_hash, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- ConsumeRefreshToken marks a token spent and returns it, in one statement.
--
-- The conditions are the whole reason this is a single UPDATE rather than a
-- SELECT followed by an UPDATE: two requests arriving together with the same
-- token both pass a SELECT, and only one can win this. A caller that gets no
-- row must then ask why — missing, already spent, revoked or expired are
-- very different answers, and only one of them is an attack.
-- name: ConsumeRefreshToken :one
UPDATE refresh_tokens
SET used_at = now()
WHERE token_hash = $1
  AND used_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > now()
RETURNING *;

-- GetRefreshTokenByHash reads a token whatever its state, which is how a
-- failed consume is diagnosed.
-- name: GetRefreshTokenByHash :one
SELECT * FROM refresh_tokens
WHERE token_hash = $1;

-- RevokeRefreshTokenFamily kills every live token descended from one
-- sign-in. Already-revoked rows are left alone so revoked_at keeps the time
-- of the first revocation.
-- name: RevokeRefreshTokenFamily :execrows
UPDATE refresh_tokens
SET revoked_at = now()
WHERE family_id = $1
  AND revoked_at IS NULL;

-- RevokeRefreshTokensForUser logs a user out everywhere.
-- name: RevokeRefreshTokensForUser :execrows
UPDATE refresh_tokens
SET revoked_at = now()
WHERE user_id = $1
  AND revoked_at IS NULL;

-- IsBenignRefreshRace decides whether a token that was already spent is
-- being presented by an honest client that lost a race, rather than replayed
-- by somebody who stole it.
--
-- Two tabs share one refresh cookie. When both refresh at the same moment,
-- one rotation wins and the other finds the token spent. Reading that as a
-- replay revokes the family, which takes the winner's brand-new token with
-- it and signs out both tabs (spec §9.2).
--
-- It is benign only when all three hold:
--   * the token was spent within the grace window;
--   * its successor exists and is still live — unused, unrevoked, unexpired;
--   * nothing descended from that rotation has been used since.
-- The last condition is what keeps the window from being a way around reuse
-- detection: once the chain has moved on, the old token is history.
--
-- "Descended from the rotation" is issued_at >= the token's used_at. Both are
-- now() inside the rotation's transaction, which Postgres fixes for the whole
-- transaction, so the successor's issued_at equals the spent token's used_at
-- exactly and every later token is issued after it.
-- name: IsBenignRefreshRace :one
SELECT
    @used_at::timestamptz > now() - make_interval(secs => @grace_seconds::float8)
    AND EXISTS (
        SELECT 1 FROM refresh_tokens successor
        WHERE successor.family_id = @family_id::uuid
          AND successor.issued_at >= @used_at::timestamptz
          AND successor.used_at IS NULL
          AND successor.revoked_at IS NULL
          AND successor.expires_at > now()
    )
    AND NOT EXISTS (
        SELECT 1 FROM refresh_tokens descendant
        WHERE descendant.family_id = @family_id::uuid
          AND descendant.issued_at >= @used_at::timestamptz
          AND descendant.used_at IS NOT NULL
    ) AS benign;
