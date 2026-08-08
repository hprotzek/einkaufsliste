# Contributing

This is a family shopping list, self-hosted and not commercial. It is not
looking for contributors, but the repository is public, the licence is MIT, and
patches are welcome if something here is useful to you.

## Getting set up

```bash
cp .env.example .env          # then edit DATABASE_URL
make run                      # migrations run automatically at start-up
make test                     # needs a reachable Postgres, or Docker
```

Tests use a real Postgres rather than a mock. They use `DATABASE_URL` when it
is set, and otherwise start a container, so `make test` works either way.

## Before opening a pull request

```bash
make generate   # if you touched api/openapi.yaml, SQL, or migrations
make lint
make test
```

CI runs `go vet`, golangci-lint, `go test -race` against a real Postgres, and a
drift check that regenerates every generated file and fails if the tree
changes. All of it must be green.

## Things that will get a patch sent back

Not to be unwelcoming — these are settled decisions with reasons recorded in
the spec's decision log (§14). If one looks wrong, say so and we will change
the spec first, rather than working around it in code.

- **Editing generated files by hand.** `internal/store/db.go`, `models.go`,
  `*.sql.go` and `internal/transport/http/openapi/openapi.gen.go` come from
  `make generate`. Change the source and regenerate.
- **Adding a route without adding it to `api/openapi.yaml` first.** The router
  is built from the spec; the spec leads.
- **Mutating an item without writing its `item_events` row in the same
  transaction.** The log cannot be backfilled.
- **Adding Redis**, or any service the household does not need. At roughly ten
  users, removing infrastructure beats adding it.
- **Adding a server-state cache library** for list data. The sync engine owns
  server state, and a second cache will disagree with it.
- **Mocking the database in a test.**

## Style

- Conventional commits (`feat:`, `fix:`, `chore:`).
- One task per branch.
- Business logic in `internal/`, ignorant of HTTP.
- Structured logging with `log/slog`; no `fmt.Println` in committed code.
- UTC internally, formatted to local only at the UI edge.
- Norwegian is a first-class target: fold æ/ø/å properly and accept the decimal
  comma. Never use a naive ASCII fold — it silently merges distinct words.
