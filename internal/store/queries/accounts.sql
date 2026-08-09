-- GetUserByIdentity is the normal sign-in path: (provider, subject) is the
-- identity key, never the email (spec §9).
-- name: GetUserByIdentity :one
SELECT users.* FROM users
JOIN identities ON identities.user_id = users.id
WHERE identities.provider = $1
  AND identities.subject = $2
  AND users.deleted_at IS NULL;

-- GetVerifiedUserByEmail finds a linkable account. Only verified addresses
-- are considered: linking to an unverified one would let anybody claim
-- somebody else's address and inherit their lists (§9).
-- name: GetVerifiedUserByEmail :one
SELECT * FROM users
WHERE email = $1
  AND email_verified
  AND deleted_at IS NULL;

-- name: CreateUser :one
INSERT INTO users (display_name, email, email_verified, avatar_url)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: CreateIdentity :one
INSERT INTO identities (user_id, provider, subject)
VALUES ($1, $2, $3)
RETURNING *;

-- UpdateUserProfile refreshes the display name and avatar on each sign-in,
-- since the provider is authoritative for both. The email is deliberately
-- not touched here: changing it would move an account between the verified
-- and unverified sides of the unique index.
-- name: UpdateUserProfile :one
UPDATE users
SET display_name = $2,
    avatar_url = $3,
    updated_at = now()
WHERE id = $1
RETURNING *;

-- ListIdentitySubjectsForUser backs the audit line §9 asks for: every link
-- event logged with both provider subjects.
-- name: ListIdentitySubjectsForUser :many
SELECT provider, subject FROM identities
WHERE user_id = $1
ORDER BY provider;

-- name: GetUserByID :one
SELECT * FROM users
WHERE id = $1
  AND deleted_at IS NULL;
