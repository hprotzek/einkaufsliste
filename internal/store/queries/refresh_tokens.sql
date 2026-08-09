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
