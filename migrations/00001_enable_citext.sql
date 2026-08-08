-- +goose Up
-- citext backs users.email, which must be unique case-insensitively (spec §6.2).
-- Extensions are cluster-level infrastructure rather than schema, so enabling
-- it first keeps the M2 table migrations free of setup concerns.
CREATE EXTENSION IF NOT EXISTS citext;

-- +goose Down
-- Never run on a deployed environment: deploys are forward-only (§12.3). This
-- exists so a local database can be reset cleanly, and it will refuse to run
-- once any column depends on the type — which is the correct behaviour.
DROP EXTENSION IF EXISTS citext;
