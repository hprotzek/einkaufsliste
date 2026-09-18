# CLAUDE.md

Shared shopping list for one family. Self-hosted, not commercial. Go + chi backend, React PWA, Postgres, behind a Cloudflare Tunnel.

## Project docs

- `docs/shared-shopping-list-spec.md` — the spec: requirements, data model, sync protocol, auth, UI decisions
- `docs/implementation-plan.md` — ordered tasks with "done when" criteria

**Read the relevant section on demand — do not load either file wholesale.** The spec is long; pull in the section that covers the task at hand (they are numbered) and leave the rest.

When I say "do task 3.4", that means the numbered task in the implementation plan. Read the plan entry, read the spec sections it references, then propose an approach before writing code.

## Architecture

- **`cmd/api`** — wiring only: reads env config, opens the pgx pool, runs migrations, builds the router, serves with graceful shutdown. No business logic.
- **`internal/transport/http`** (package `httptransport`) — the chi router is built from `api/openapi.yaml` by oapi-codegen (`openapi/openapi.gen.go`, generated — never hand-edit). The `server` struct embeds `openapi.Unimplemented`, so adding an operation to the spec compiles immediately and answers 501 until someone writes its handler — the contract and router cannot silently disagree. Handlers translate HTTP ↔ `internal/` types; they hold no decisions.
- **`internal/store`** — the pgx pool plus sqlc-generated queries. Only `store.go` and `migrate.go` are hand-written; `db.go`, `models.go`, and `*.sql.go` come from `make generate` (sqlc reads `migrations/` as the schema and `internal/store/queries/*.sql`). Migrations are goose `.sql` files embedded in the binary (`migrations/embed.go`) and applied automatically at start-up, before the listener opens — no separate migrate step in production.
- **`internal/auth`** — OIDC token exchange/verification plus this server's own access/refresh token issuance and rotation (spec §9). Provider-agnostic even though only Google is wired up (non-negotiable 10).
- **`internal/items`, `internal/lists`, `internal/sync`** (package `syncsvc`) — domain logic, currently stubs (`doc.go` only). Each fills in at the milestone named in its doc comment (M2/M3/M5 in `docs/implementation-plan.md`) — an empty package here is expected, not a gap to fill speculatively.
- **`internal/dbtest`** — shared test helper: reuses `DATABASE_URL` when set (CI's service container), otherwise starts one testcontainer per test binary. This is what makes non-negotiable 9 practical everywhere.
- **`web/`** — the React PWA. `src/api/schema.d.ts` is generated from `api/openapi.yaml` via `openapi-typescript`.
- **Codegen**: `api/openapi.yaml` is the one contract, feeding both the Go server/models (oapi-codegen) and the TS types (openapi-typescript); `migrations/` + `internal/store/queries/*.sql` feed sqlc. `make generate` regenerates all three; CI's `generate` job fails on any drift.

## Non-negotiables

These are settled decisions with reasons recorded in the spec's decision log (§14). Do not quietly undo them; if one looks wrong, say so and we will change the spec first.

1. **`item_events` is written in the same transaction as every item mutation.** No mutation without its event. This log cannot be backfilled.
2. **No Redis.** In-process pub/sub behind a `Broadcaster` interface. Idempotency keys live in Postgres.
3. **No ORM.** sqlc generates from hand-written SQL. Migrations are goose `.sql` files, forward-only.
4. **`api/openapi.yaml` is the contract of record.** Change it first, regenerate both sides, then write code. CI fails on drift.
5. **Free text always wins in the combobox.** No pre-highlighted first row, no autocomplete on blur, always an explicit "Add …" row. See spec §10.4.
6. **No `localStorage` for tokens.** Access token in memory, refresh token in an `HttpOnly` cookie.
7. **The WebSocket is an optimisation.** Correctness lives in `GET /changes?since=`. Never let a feature depend on the socket being connected.
8. **Every entry has four fields populated** (item, count, added-by, store) — enforced `NOT NULL`, defaulted server-side, never prompted for in the UI. Spec §6.0 and §6.4.
9. **Tests ship with the code**, not after. Real Postgres via testcontainers; never mock the database.
10. **Google is the only auth provider**, but nothing may assume `provider == 'google'`.

## Commands

```bash
make up / make down           # build+start / stop the compose stack: postgres + api + caddy (needs .env)
make run                      # the API from source against DATABASE_URL; migrations apply at start-up
make migrate                  # goose up (manual; production applies migrations at start-up)
make migrate-status           # which migrations are applied (dev convenience only)
make generate                 # sqlc + oapi-codegen + openapi-typescript — run after touching
                              # api/openapi.yaml, migrations/, or internal/store/queries
make test                     # go test -race ./... (DATABASE_URL or a testcontainer)
go test -race ./internal/auth/... -run TestName   # a single test
make lint                     # golangci-lint at the pinned version
make vet                      # go vet ./...
make web / make web-dev       # build the app into web/dist / Vite dev server (proxies /api)
make web-typecheck            # tsc --noEmit
```

`make help` lists everything. Vitest and Playwright arrive with M4 (plan tasks 4.7 and 5.9); until then the web app is gated by typecheck and build only — don't assume `npm test` or `npx playwright test` work.

## Conventions

- Conventional commits (`feat:`, `fix:`, `chore:`).
- One task per branch, PR into `main`. CI must be green before merge.
- Business logic in `internal/`, ignorant of HTTP. Handlers translate, they don't decide.
- Structured logging with `log/slog`. No `fmt.Println` in committed code.
- UTC everywhere internally; format to local only at the UI edge.
- Norwegian matters: normalisation folds æ/ø/å, quantity parsing accepts the decimal comma.

## Things to get right that are easy to get wrong

- **Don't reach for `pull_request_target`** in workflows. This is a public repo.
- **Don't add a server-state cache library** (TanStack Query etc.) for list data — the sync engine owns server state, and a second cache will disagree with it.
- **Don't reorder the suggestion chip row** in response to a tap. Ranking freezes when the screen opens. Spec §7.6.
- **Don't surface conflict resolution to the user.** Last-write-wins is silent by design. Only a rejected local write surfaces.
- **Don't suggest scaling work.** Target is ~10 users. Prefer removing infrastructure to adding it.

## Working style

Propose before implementing anything non-trivial. Push back if a request conflicts with the spec — quote the section. If we settle something new, update the spec in the same PR; a decision that only exists in a chat log is lost.
