-- +goose Up

-- One row per refresh token ever issued, including spent ones. Spent rows
-- are the point: without them a replayed token would simply look unknown,
-- and reuse detection needs to tell "never existed" from "already used"
-- (spec §9).
CREATE TABLE refresh_tokens (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Every token descended from one sign-in shares a family. Rotation keeps
    -- the family and replaces the token; detecting reuse kills the family,
    -- which logs out the thief and the victim together — deliberately, since
    -- there is no way to tell which is which.
    family_id  uuid NOT NULL,
    -- Only ever the hash. A database dump must contain nothing usable as a
    -- credential.
    token_hash text NOT NULL,
    issued_at  timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    -- Set when the token is exchanged. A second exchange is the replay.
    used_at    timestamptz,
    -- Set when the family is revoked, by reuse detection or by logout.
    revoked_at timestamptz
);

-- Lookup is always by hash, and it must be unique or a collision would let
-- one token consume another's row.
CREATE UNIQUE INDEX refresh_tokens_token_hash_key ON refresh_tokens (token_hash);

-- Revoking a family touches every row in it.
CREATE INDEX refresh_tokens_family_id_idx ON refresh_tokens (family_id);

-- Logging out everywhere, and cascading when an account is removed.
CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);

-- +goose Down
DROP TABLE refresh_tokens;
