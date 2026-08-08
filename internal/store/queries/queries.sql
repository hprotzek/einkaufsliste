-- Queries live here, one `-- name:` annotation each, and sqlc turns them into
-- type-safe Go in the parent package. Hand-written SQL only: there is no ORM
-- in this project (non-negotiable 3).
--
-- sqlc copies the comment above a query into the generated Go doc comment, so
-- write it as documentation for the caller.

-- Ping proves the sqlc pipeline end to end: hand-written SQL in, type-safe Go
-- out. It touches no table, so it stays valid whatever the schema does, and it
-- exists because sqlc rejects a file of pure comments with "no queries
-- contained in paths" — plan task 0.5's "empty query file" cannot be taken
-- literally. Real queries arrive with the tables at M2.

-- name: Ping :one
SELECT 1 AS ok;
