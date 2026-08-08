-- +goose Up

-- One row per person. Only the fields spec §4 permits are stored: email,
-- display name and avatar URL. The OIDC subject lives on identities.
CREATE TABLE users (
    id             uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    display_name   text NOT NULL,
    -- citext (migration 00001) so uniqueness is case-insensitive without
    -- lower()-ing at every call site. Never lowercase or ASCII-fold this by
    -- hand: §4 requires æ/ø/å survive intact.
    email          citext NOT NULL,
    -- Whether the provider asserted email_verified when this account was
    -- created. Spec §9 requires it: linking a second provider is permitted
    -- only when the incoming provider says verified *and* "the existing
    -- account's email was itself verified at signup". Without this column that
    -- second half cannot be evaluated, so the rule could not be enforced.
    -- §6.2's column list omits it; §9's linking rules need it.
    email_verified boolean NOT NULL DEFAULT false,
    avatar_url     text,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    -- Soft delete. Deleting an account anonymises it rather than cascading
    -- (§14, decision 6), so items keep their attribution.
    deleted_at     timestamptz
);

CREATE UNIQUE INDEX users_email_key ON users (email);

-- One row per provider link. A person with both Google and Apple has two
-- rows here and one row in users.
CREATE TABLE identities (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- Deliberately unconstrained text, with no CHECK listing the providers we
    -- know about: nothing in this system may assume 'google' (non-negotiable
    -- 10), and a CHECK would mean a migration before a second one could ship.
    provider   text NOT NULL,
    -- The provider's stable subject claim. This, with provider, is the
    -- identity key — never the email, which can be an Apple private relay
    -- address or simply change (§9).
    subject    text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT identities_provider_subject_key UNIQUE (provider, subject)
);

-- Sign-in looks up by (provider, subject), which the unique constraint
-- already indexes. This one covers the other direction: listing a user's
-- linked providers, and the cascade on delete.
CREATE INDEX identities_user_id_idx ON identities (user_id);

-- +goose Down
DROP TABLE identities;
DROP TABLE users;
