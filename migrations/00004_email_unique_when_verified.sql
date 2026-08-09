-- +goose Up

-- §6.2 asked for a plain unique email, but §9 requires that an unverified
-- claim on an address create a *separate* account rather than linking. Both
-- cannot hold: a second account cannot be created for an address that is
-- already taken.
--
-- Uniqueness now applies only to verified addresses. That keeps the state
-- §9's linking rules exist to prevent — two verified accounts on one address
-- — impossible in the data, not merely unlikely in the code, while allowing
-- the unverified duplicate §9 and §11.4 call for.
--
-- The identity key remains (provider, subject). Email is never it (§9).
DROP INDEX users_email_key;

CREATE UNIQUE INDEX users_email_verified_key ON users (email) WHERE email_verified;

-- Unverified lookups are still by address, just without the guarantee.
CREATE INDEX users_email_idx ON users (email);

-- +goose Down
DROP INDEX users_email_idx;
DROP INDEX users_email_verified_key;
CREATE UNIQUE INDEX users_email_key ON users (email);
