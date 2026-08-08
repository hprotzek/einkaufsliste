# Implementation Plan — Shared Shopping List

Companion to `shared-shopping-list-spec.md`. The spec says *what* and *why*; this says *in what order*, *when a thing is done*, and *where to stop*.

**Honest sizing up front.** R1 is roughly 35–45 working sessions of 2–3 hours — call it 90–135 hours, or 3–4 months of evenings at a realistic pace. That is not a discouragement, it is the number that makes the sequencing below matter. The plan is built so that you have something working on your own phone at session ~25, not at session ~50.

---

## 0. Before you write any code

### 0.1 Accounts to set up

| Thing | Cost | Lead time |
|---|---|---|
| **Domain name** | ~€10–15/yr | Minutes, though DNS and the naming decision can dawdle |
| **Cloudflare account + Tunnel** | Free | An hour to set up `cloudflared` |
| **Google Cloud project + OAuth client** | Free | An hour — the consent screen is fiddlier than expected |
| **GitHub public repo** | Free | Minutes |

**Not needed: the Apple Developer Program.** R1 is Google-only (spec §9.1). Apple's requirement to offer Sign in with Apple applies to App Store apps, not websites, so a Google-only PWA is fully compliant on iPhones. The $99/year becomes relevant only at R4, and only if native iOS actually happens.

**Total running cost: a domain.** Roughly €10–15/year, since the server is already yours and the tunnel is free.

Google's OAuth consent screen may ask for brand verification (confirming you own the domain) — free, 2–3 business days, and only relevant when you publish rather than during development. Do it once M0 has the domain live.

### 0.2 Decisions to lock before M0

Four things from the spec's open questions block the schema, so settle them now rather than mid-migration:

1. **The word for "household"** (spec §14b, 8b) — it goes in table names, API paths and every UI string.
2. **Invite link lifetime and reusability** (§14b, 1–2) — shapes the `household_invites` table.
3. **The hostname on the tunnel** — it goes in the Google OAuth redirect URI, which is painful to change later.
4. **Repo name and licence** — MIT unless you have a reason.

The rest of §14b can genuinely wait.

### 0.3 Working rhythm

- **One task per branch, PR into `main`, even solo.** Branch protection makes CI the gate. It feels like ceremony for one person; it is the thing that stops a broken `main` at 11pm.
- **Every task ends green.** Never leave a session with failing tests — you will not remember the context.
- **Conventional commits** (`feat:`, `fix:`, `chore:`), because release notes fall out of them for free.
- **Write the test with the code, not after.** For this project specifically, testing is a stated priority (spec §11) and retrofitting tests to sync code is miserable.

---

## 1. M0 — Foundation

**Goal: an empty service that builds, tests, containerises and deploys.** No features. This is the phase people skip and regret.

| # | Task | Done when |
|---|---|---|
| 0.1 | Public GitHub repo; LICENSE, README, `.gitignore`, `.env.example`; **push protection and secret scanning enabled before the first commit** | Repo exists, protections on |
| 0.2 | Go module, `/cmd/api/main.go` serving `/healthz`, layout per spec §5.3 | `go run ./cmd/api` returns 200 |
| 0.3 | `docker-compose.yml`: postgres, api, caddy. No Redis | `docker compose up` gives a healthy stack |
| 0.4 | goose migrations, one trivial migration, run-on-start | `goose status` clean against the container |
| 0.5 | sqlc configured, generating from an empty query file | `sqlc generate` runs in CI |
| 0.6 | `ci.yml`: `go vet`, `golangci-lint`, `go test -race`, with a Postgres service container | Green on a PR |
| 0.7 | Multi-stage Dockerfile → `FROM scratch`; multi-arch (amd64 + arm64) push to GHCR | Image < 20 MB, pulls anonymously, runs on your server's architecture |
| 0.8 | Home server: `cloudflared` tunnel → Caddy → api, compose stack, systemd timer pulling `:latest`. **No router ports opened** | `https://yourdomain/healthz` returns 200 from mobile data, with the home firewall untouched |
| 0.9 | `api/openapi.yaml` skeleton; `oapi-codegen` + `openapi-typescript` wired; CI fails on drift | Editing the spec without regenerating breaks CI |
| 0.10 | Vite + React + TS app in `/web` served by Caddy at the same origin | Blank page loads over HTTPS |

**Checkpoint:** you can push a commit and see it live within minutes, with no manual steps. Do not proceed until this is true — every later phase depends on the loop being fast.

*≈ 4–6 sessions.*

---

## 2. M1 — Auth

**Goal: sign in with Google, your own tokens, `/me` works.** Front-loaded because it is the riskiest external dependency and the least reversible. Google only — see spec §9.1 for why, and for what a second method would cost if someone ever needs one.

| # | Task | Done when |
|---|---|---|
| 1.1 | `users`, `identities` migrations | Migration applies and rolls back |
| 1.2 | **Fake OIDC issuer for tests** — keypair, JWKS endpoint, self-signed ID tokens. Build this even though there is only one real provider: it is how you test malformed tokens, unverified emails and relay addresses without touching Google | Test helper can mint arbitrary ID tokens |
| 1.3 | ID-token verification via `coreos/go-oidc`: `iss`, `aud`, `exp`, `nonce`, signature | Table-driven tests reject each malformation |
| 1.4 | Access token (JWT, 15 min) + opaque refresh token, hashed, 60 days | Unit tested |
| 1.5 | Refresh rotation with **reuse detection revoking the family** | Test: replayed token kills the family |
| 1.6 | `/auth/oidc/{provider}/callback`, `/auth/refresh`, `/auth/logout`; refresh in `HttpOnly` cookie | Integration test end-to-end against the fake issuer |
| 1.7 | Account-linking rules per spec §9 — **written and tested now, dormant in production** with one provider. Building them later against live user data is far riskier than building them now against fakes | All four cases from spec §11.4 pass against the fake issuer |
| 1.8 | Google wired for real. Nothing in the code assumes `provider == 'google'` | Sign in works on the deployed site |
| 1.9 | Web: sign-in screen, token handling, single-flight refresh, `/me` | Can sign in on your phone and see your own name |

**Checkpoint:** Google sign-in works on the real deployed site, from a phone. Auth is where solo projects stall — getting past this is the biggest single de-risking step, and dropping the second provider takes roughly a third of the work out of this milestone.

*≈ 4–6 sessions.*

---

## 3. M2 — Households, lists, and the full schema

**Goal: the complete data model exists, even though most of it has no UI.**

| # | Task | Done when |
|---|---|---|
| 2.1 | Migrations for `households`, `household_members`, `household_invites`, `lists`, `stores`, `items`, `catalog_items`, `catalog_aliases`, `item_events` — **the whole spec §6.2 schema** | Applied; indexes from §6.2 present |
| 2.2 | Personal household + default "Any store" auto-created at signup | New user has both, tested |
| 2.3 | Two-hop authorisation middleware (user → household → list), cached per request | **Cross-household isolation test passes**: A cannot read anything of B's |
| 2.4 | Lists CRUD + archive + reorder | API tests |
| 2.5 | Roles: owner/member, permission matrix from §6.1 enforced | Member gets 403 on every owner-only path, tested directly against the API |
| 2.6 | Last-owner protection: cannot leave or self-delete | Tested |

Invites are built here at the API level but **not exposed in the UI until R2** — the endpoints are cheap while you are in this code.

*≈ 4–5 sessions.*

---

## 4. M3 — Items and the event log

**Goal: items work, and every action is logged from the very first one.**

| # | Task | Done when |
|---|---|---|
| 3.1 | Item create/update/delete with the four mandatory fields, client-supplied UUIDs | API tests incl. `NOT NULL` enforcement |
| 3.2 | **Default resolution chain** (§6.4): explicit → catalog default → filtered store → list default | Unit tested as a pure function |
| 3.3 | `item_events` written on every add/check/uncheck/delete-unchecked — **in the same transaction as the mutation** | Test: no mutation exists without its event |
| 3.4 | Catalog upsert on add: normalise, match alias, bump `add_count`, update defaults | Tested, incl. Norwegian æ/ø/å folding |
| 3.5 | Name normalisation + quantity parsing (`2 melk`, `0,5 kg smør`) | Table-driven tests with decimal comma |
| 3.6 | Snapshot endpoint `GET /lists/{id}/items` (live items only) | API test |
| 3.7 | `clear checked` + 12-hour auto-clear job | Tested with a fake clock |

**The event log in 3.3 is the one thing in this plan that must not slip.** Everything else can be added later; a year of missing history cannot.

*≈ 4–5 sessions.*

---

## 5. M4 — Web UI, online only

**Goal: an app you would actually use, if you never lose signal.** Deliberately before sync, so you have something real in your hands early.

| # | Task | Done when |
|---|---|---|
| 4.1 | Read the frontend-design skill; settle typography, colour, spacing. Design tokens, not ad-hoc CSS | A styled empty screen you like |
| 4.2 | Dexie schema mirroring the API; API client generated from OpenAPI | Types flow end to end |
| 4.3 | List screen: rows, tick control (whole row), checked section at the bottom | Works on a phone |
| 4.4 | Add field (plain input for now), optimistic add | Adding feels instant |
| 4.5 | Count steppers on the row; avatars for added-by/ticked-by | Per §10.3 |
| 4.6 | Empty states and the three connection states (§10.5) | All states reachable in dev |
| 4.7 | Playwright set up, first E2E: sign in, add, tick, clear | Green in CI |

**Checkpoint — the important one.** Put it on your phone's home screen and use it for a real shop. Everything after this should be shaped by that experience, not by this document.

*≈ 7–9 sessions.*

---

## 6. M4b — The conformance suite (before any sync code)

**Goal: the sync protocol specified as executable tests.** Spec §11.2.

| # | Task | Done when |
|---|---|---|
| 4b.1 | Harness driving the HTTP API as N independent simulated clients, with controllable clocks and connectivity | Harness runs, no scenarios yet |
| 4b.2 | Encode all §11.2 scenarios — concurrent tick, tick-vs-delete, offline batch, replayed `mutation_id`, out-of-order outbox, tombstone horizon, stale cursor → `full_resync_required`, store deleted while offline | Suite runs and **fails**, because sync does not exist |
| 4b.3 | Separate `conformance.yml` workflow | Red in CI, visibly and deliberately |

Writing these first is not test-driven purism. The scenarios *are* the protocol specification, and they are the artifact the iOS and Android clients will later be validated against.

*≈ 3–4 sessions.*

---

## 7. M5 — Sync

**Goal: turn the conformance suite green.** The hardest phase; budget accordingly.

| # | Task | Done when |
|---|---|---|
| 5.1 | Per-list `seq` counter, assigned in the mutation transaction | Monotonic under concurrent writes, race-tested |
| 5.2 | `GET /changes?since=` with tombstones + `full_resync_required` past the horizon | Conformance scenarios for pull pass |
| 5.3 | Idempotency: `mutation_id` table in Postgres, pruned with the tombstone job | Replay scenarios pass |
| 5.4 | Batch mutation endpoint for outbox flush | Ordering scenarios pass |
| 5.5 | Client outbox in Dexie; single transaction for local write + outbox row | Kill-mid-transaction test |
| 5.6 | Flush loop with backoff; pull on reconnect | Offline scenarios pass |
| 5.7 | WebSocket hub behind a `Broadcaster` interface, in-process implementation; **socket as optimisation only**; heartbeats every 30s for the Cloudflare idle timeout | Fan-out test passes; a socket survives 5 minutes idle through the tunnel |
| 5.8 | Tombstone purge job at 90 days | Three-year simulation passes |
| 5.9 | Playwright two-context offline convergence test | Green |

**Checkpoint:** whole conformance suite green, and the two-phone test done for real — one in aeroplane mode in a shop, both reconciling afterwards.

*≈ 9–13 sessions.*

---

## 8. M6 — Combobox, suggestions, PWA

**Goal: R1 complete.**

| # | Task | Done when |
|---|---|---|
| 6.1 | Combobox per §10.4: free text always wins, no pre-highlight, create row, upward opening, `visualViewport` | Component suite passes incl. the "blur never substitutes" test |
| 6.2 | **Recency-ranked suggestions only** — last 10 distinct items | Chips row works, order frozen per session (§7.6) |
| 6.3 | Store combobox (pick-first, confirm-on-create) — still only "Any store" in practice | Ready for R2 |
| 6.4 | PWA: manifest, Workbox service worker, installable, offline cold start | Install on phone, aeroplane mode, hard reload, app opens with data |
| 6.5 | Accessibility pass: axe in Playwright, keyboard traversal, 44px targets | Green |
| 6.6 | Nightly `pg_dump` + offsite copy, **and a tested restore** | You have restored the database from backup at least once |
| 6.7 | Real-shop mis-tap testing (§7.6) | You have actually done a weekly shop with it |
| 6.8 | Tag `v1.0.0`, release notes | Released |

**Then stop. Use it for a month.** R2's shape should come from that month, not from this document.

*≈ 7–9 sessions.*

---

## 9. R2 onwards

Only sketched, deliberately.

- **R2 — sharing:** invite UI, roles UI, store management and filtering, shopping mode, dedupe/merge. First release worth giving to someone else.
- **R3 — the clever bits:** analyse the accumulated `item_events` *first* and decide whether due-ness beats recency. Then pinned staples, usual-shop grid, categories, per-item stats, undo, presence.
- **R4 — native:** iOS and Android against the same API, validated by the same conformance suite. Push notifications.

---

## 10. When things go wrong

**If you have 45 minutes, not 3 hours:** take a test-only task, a migration, or documentation. Do not start sync work in a short window.

**If you stall for a month:** re-read §13 of the spec and cut scope further rather than resuming a stale branch. The plan is designed so that R1 minus M6 is still a usable app.

**If someone refuses to use a Google account:** do not reach for Apple reflexively — it is the most expensive of the three options. Passkeys cost nothing and add no infrastructure, but account recovery is genuinely hard. Email magic links are cheap but reintroduce email sending, the subsystem share-link invites deliberately removed. Decide against a real person's actual objection.

**Kill criteria worth naming honestly.** If at the M4 checkpoint you find you are not actually using it for real shops, the problem is the product, not the remaining phases — and no amount of sync or ranking will fix that. Better to learn it at session 25 than session 50.
