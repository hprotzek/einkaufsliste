# Shared Shopping List — Spec, Requirements & Architecture

**Status:** Draft v0.3 — scope cut into staged releases (§13); all opening decisions resolved (§14).
**Decided constraints:** private family project, not commercial. Self-hosted on a home server behind a **Cloudflare Tunnel**. API-first, web frontend first, native apps unlikely, **sign-in with Google only** (§9).
**Stack:** Go + chi, PostgreSQL, React PWA. Google OIDC, share-link invites, avatar attribution. No Redis, no analytics, no push — see §14 for why these were removed.

---

## 1. Product summary

A shared shopping list that a household can edit at the same time. **Built for one family, self-hosted, not a product** — which is licence to remove things rather than generalise them. Someone adds "milk" at work, the person already in the store sees it appear and ticks it off. Works with a bad signal in the shop basement.

**Core loop:** open list → add/tick items → list stays in sync for everyone.

**Non-goals (for now):** price tracking, store inventory integration, recipe→list generation, budgeting, barcode scanning. Keep these out of v1 but don't design them out.

---

## 2. Personas & key scenarios

| Persona | Need |
|---|---|
| The shopper | Fast tick-off, one-handed, big tap targets, works offline |
| The adder | Add an item in 3 seconds from anywhere, no app-open ceremony |
| The organiser | Create lists, invite people, tidy up duplicates |

**Scenarios that drive the design:**

1. Two people tick the same item within a second of each other → must not break, must not double-toggle.
2. Shopper is offline for 20 minutes in the store, ticks 15 items, comes back online → all changes land, nothing lost.
3. Someone adds an item while the shopper is mid-shop → appears within a couple of seconds without a manual refresh.
4. Someone types "melk" then "Melk" then "melk 2l" → sensible dedupe suggestion, not a hard block.

---

## 3. Functional requirements

### 3.1 Must have (MVP)

**Auth & accounts**
- FR-1: Sign in with Google (OIDC). **The only provider in R1.**
- FR-2: The identity layer is provider-agnostic — adding a second OIDC provider must be additive (a row in `identities`), never a refactor.
- FR-3: Account has a display name and avatar URL, editable.
- FR-4: Sign out; delete account (removes personal data, anonymises past item authorship). Blocked, with a clear explanation, while the user is the sole owner of a shared household (FR-8c).

**Lists**
- FR-5: Every user gets a personal household at signup. Users can create additional households and belong to many.
- FR-6: A household holds many lists; **all members see all of a household's lists**. Create a list with a name and optional icon/colour.
- FR-7: **The household's creator (owner) can invite members** by generating a share link with a token; joining requires being signed in. One join grants access to every list in that household, present and future. Non-owners cannot create invites.
- FR-8: Roles are household-level: `owner`, `member`. Owners can invite, remove members, rename/delete the household, and promote other members to owner. Members can do everything with lists, stores and items. Full matrix in §6.1.
- FR-8b: Leave a household (loses access to all its lists). The personal household cannot be left or deleted.
- FR-8c: The last remaining owner must transfer ownership before leaving or deleting their account.
- FR-9: Archive a list (hidden, data intact). Deleting a list is soft-delete with a 30-day recovery window, and offers an export first.
- FR-9b: Reorder lists on the home screen.

**Items**
- FR-10: Add item with free-text name. **Every entry always has four fields populated: item, count, who added it, and where to buy** (§6.0). Optional: unit, note, category.
- FR-10b: Manage the **household's** stores, shared across all its lists — create, rename, colour, reorder. One undeletable default ("Any store").
- FR-10c: Change an item's store or count inline, without opening a dialog.
- FR-10d: Filter/group the list by store; shopping mode shows one store at a time (§10.3).
- FR-11: Tick/untick an item (checked state), recording who ticked it and when. Both author and ticker are shown as avatars in the UI (§10).
- FR-12: Edit and delete an item.
- FR-13: "Clear checked" — bulk-remove all ticked items; checked items also auto-clear 12 hours after being checked (§6.5).
- FR-13b: Lists are permanent. Buying an item removes the item, never the list; a list persists until explicitly deleted (§6.5).
- FR-14: Items have a stable sort position; manual reorder is optional for MVP, category grouping is not.
- FR-15: Quick-add row of ranked previous items above the input; one tap adds instantly with the last-used quantity (§7.5).
- FR-15b: Item and store fields are editable comboboxes: they list previously used entries, filter as you type, and always allow committing free text that isn't in the list (§10.4).
- FR-15d: Creating a new store from the combobox requires confirmation; creating a new item does not.
- FR-15c: Every add/check/delete writes an `item_events` row from the first release, whether or not stats are surfaced yet (§7.3).

**Sync**
- FR-16: Changes propagate to other online clients within ~2s without manual refresh.
- FR-17: Full offline read and write; queued changes flush automatically on reconnect.
- FR-18: Conflicting concurrent edits resolve deterministically without user-facing errors (see §6).

### 3.2 Should have (v1.1)

- FR-19: Categories / store sections with grouped view and per-list custom order.
- FR-20: Pinned staples — user-curated items always shown first, ahead of algorithmic ranking.
- FR-20b: Empty-list "usual shop" grid: multi-select suggestions, add in one action.
- FR-20c: Item stats sheet (times added, typical interval, typical quantity) and list-level stats view (§7.7).
- FR-20d: Merge two catalog entries when normalisation failed to unify them.
- FR-21: Undo for destructive actions (delete, clear checked) — 10s window, client-side.
- FR-21b: Lingering-item nudge: items unbought for 60+ days are dimmed with a one-tap removal offered, never auto-deleted.
- FR-22: *(Deferred — push notifications now land with the native apps, see §3.3.)*
- FR-23: Presence indicator: "Ada is shopping".
- FR-24: Switch between households when a user belongs to more than one.
- FR-25: A second sign-in method, **only if a real person needs one** — Apple, email magic link, or passkeys. Deliberately unspecified until that happens (§9.1).

### 3.3 Could have (later)

- **Push notifications**, shipping with the native apps via APNs/FCM — opt-in, digest-batched ("Ada added 4 items"), never per-item. Skipping Web Push avoids building a second, weaker notification path that iOS Safari would barely support anyway.
- Assign an item to a person; per-store lists with aisle ordering; barcode scan; recipe import; item photos; shared "pantry" state; web share target ("share to shopping list" from other apps); Siri/Google Assistant intents; widget.

---

## 4. Non-functional requirements

| Area | Requirement |
|---|---|
| Latency | p95 API read < 200 ms, write < 300 ms from Europe. Optimistic UI means perceived latency ≈ 0. |
| Scale target | **Roughly 5–10 users in 1–2 households.** One API process, one small Postgres. Every design choice should be re-read against this number — most "what if it grows" instincts are wrong here, and the cost of the wrong instinct is complexity you maintain alone, forever. |
| Availability | Best-effort, home server, no SLA. Offline-first means a router reboot or a power cut degrades to local-only rather than broken — which for a household is entirely adequate. |
| Security | TLS everywhere; no long-lived secrets in the client; refresh-token rotation; all list access authorised per-request. |
| Privacy | Store only: OIDC subject, email, display name, avatar URL. **No analytics of any kind** (§14, decision 8). Note that Cloudflare terminates TLS at its edge and can in principle see traffic — for a family shopping list that is an acceptable trade for not exposing a home IP, but it is a real trade and worth knowing. |
| Data | Nightly Postgres backup with an **offsite copy** — more important here, not less. This is irreplaceable family data on a home machine with no operations team and no commercial backup behind it. Restore must be tested at least once before anyone relies on it. |
| Retention | Lists are permanent. Tombstones purged at 90 days; `item_events` retained for the life of the list; deleted lists hard-deleted 30 days after soft delete. |
| Accessibility | WCAG 2.1 AA target: 44px tap targets, contrast, screen-reader labels on the tick control. |
| Localisation | Strings externalised from day one, even if only one language ships. **Norwegian is a first-class target**: normalisation must fold æ/ø/å correctly (`melk`, `smør`, `løk`), quantity parsing must accept the decimal comma (`0,5 kg`), and units are metric throughout. Never lowercase or strip diacritics with a naive ASCII fold — it silently merges distinct words. |
| Time | Everything stored and computed in UTC; formatted in the device's zone at the edge. Due-ness (§7.4) and the 12-hour auto-clear (§6.5) are duration maths, so they must not touch local time — but "added yesterday" in the UI must. Cheap now, horrible to retrofit once there is data. |
| Measurement | None. At household scale, asking the four people who use it beats any dashboard (§12.5). |
| Observability | Structured JSON logs, health endpoint, basic request metrics, error tracking. |

---

## 5. Architecture

### 5.1 Shape

```
┌─────────────┐   ┌──────────────┐   ┌──────────────┐
│  Web (PWA)  │   │ Android (v2) │   │   iOS (v2)   │
└──────┬──────┘   └──────┬───────┘   └──────┬───────┘
       │                 │                  │
       └────── HTTPS REST + WebSocket ───────┘
                         │
                 ┌───────▼────────┐
                 │ Cloudflare edge│  TLS terminates here
                 └───────┬────────┘
                         │  outbound-only tunnel
                 ┌───────▼────────┐
                 │  cloudflared   │  on the home server
                 └───────┬────────┘
                         │
                 ┌───────▼────────┐
                 │     Caddy      │  same-origin routing, plain HTTP
                 └───────┬────────┘
                         │
                 ┌───────▼────────┐
                 │   API server   │  REST /api/v1 + /ws
                 │                │  auth, authz, sync, pub/sub
                 └───┬────────┬───┘
                     │        │
              ┌──────▼──┐
              │ Postgres│   idempotency keys live here too
              └─────────┘
```

**Key principle: the API is the product.** The web app is just the first client. No business logic in the frontend that a mobile app would have to reimplement — the server owns validation, authorisation, dedupe rules and conflict resolution.

### 5.2 Stack recommendation

**Backend: Go + chi.** Compiles to a single static binary, so the production image is a `FROM scratch` container of a few megabytes and deployment is "copy one file". Predictable memory, no runtime to install on the box, and goroutines make the WebSocket hub straightforward.

| Concern | Choice | Why |
|---|---|---|
| Router | `go-chi/chi/v5` | stdlib-compatible handlers, clean middleware |
| Postgres driver | `jackc/pgx/v5` | the standard; use the native interface, not `database/sql` |
| Queries | `sqlc` | write SQL, get type-safe Go. Better fit than an ORM here — the sync queries are hand-tuned anyway |
| Migrations | `pressly/goose` | plain `.sql` files in the repo, forward-only on deploy |
| OIDC | `coreos/go-oidc` | discovery, JWKS caching, ID-token verification for both Google and Apple |
| Our tokens | `golang-jwt/jwt/v5` | signing our own access tokens |
| WebSocket | `coder/websocket` | small, context-aware, no cgo |
| Logging | stdlib `log/slog` | structured JSON, no dependency |
| Validation | `go-playground/validator` | struct-tag validation on request DTOs |
| Tests | `testcontainers-go` | real Postgres in integration tests; don't mock the database |

**Redis is out.** *This reverses decision 3.* It was justified by fan-out between multiple API processes — which will never exist for a household of five on one box. With a single process, in-process pub/sub handles the WebSocket hub and a small Postgres table handles `mutation_id` idempotency perfectly well.

What matters is keeping the seam: define a `Broadcaster` interface with an in-process implementation. Swapping in Redis later is then one file, not a refactor. **A container you don't run is a container you don't back up, patch, monitor or debug at 11pm** — at this scale that is the whole argument.

**Web frontend:** React + Vite + TypeScript, Dexie over IndexedDB as the UI's source of truth, Zustand for ephemeral UI state. Installable PWA with a Workbox service worker. Note that there is deliberately *no* server-state cache library (TanStack Query or similar) in the main path — the sync engine owns server state, and a second cache alongside it would only disagree. TanStack Query is fine for genuine request/response calls like `/me` or accepting an invite.

**The contract between them is OpenAPI.** Go and TypeScript can't share types directly the way two TS packages can, so `api/openapi.yaml` becomes the single source of truth: `oapi-codegen` generates Go server interfaces and request types, `openapi-typescript` generates the web client's types. Change the spec, regenerate both, and the compiler finds every call site. Make regeneration a CI check so the spec can never silently drift from the code.

**Mobile later:** the sync protocol (§6) is deliberately simple enough to reimplement natively in Kotlin and Swift — an HTTP GET with a cursor, an outbox, and a WebSocket. If you'd rather share code, React Native can reuse the generated TS client wholesale.

### 5.3 Repository layout

```
/cmd/api/main.go        wiring only: config, deps, serve
/internal
  /auth                 OIDC verify, token issue + rotation
  /lists                lists, members, invites
  /items                item CRUD, dedupe rules
  /sync                 seq cursor, changes query, WS hub
  /store                sqlc-generated queries + pgx pool
  /transport/http       router, middleware, problem+json errors
/api/openapi.yaml       contract of record — generates both sides
/migrations             goose .sql files
/web                    React PWA (types generated from openapi.yaml)
/infra
  docker-compose.yml
  Caddyfile
  Dockerfile            multi-stage: build, then FROM scratch
```

Keep `internal/` genuinely internal — business rules live there and know nothing about HTTP. Handlers translate, they don't decide.

---

## 6. Data model & sync

### 6.0 Entry invariants

**Every entry carries four mandatory fields, enforced as `NOT NULL` at the database level, not merely validated in the API:**

| Field | Column | Default |
|---|---|---|
| Item | `name` + `catalog_item_id` | — must be supplied |
| Count | `quantity` | `1` |
| Who added it | `created_by` | the authenticated user |
| Where to buy | `store_id` | the list's default store |

Two of these have sensible defaults, and that matters enormously — see §6.4, because "mandatory field" and "the user must make a decision" are not the same thing, and conflating them would wreck the quick-add flow in §7.5.

### 6.1 Ownership hierarchy

```
household ──< lists ──< items
    │                     │
    └──< members          └──> catalog_items ──> stores
         (users)                                    ▲
                                                    └── household-scoped
```

**Membership lives on the household, not the list.** You join a household once and get every list in it — groceries, hardware, the cabin. This is a straight improvement over per-list sharing: nobody has to remember to re-invite their partner each time a new list is created, which is exactly the failure that makes shared apps feel broken.

Consequences:

- **Invites become household invites.** One share link, joined once. The per-list invite flow disappears entirely, which removes a whole surface rather than adding one.
- **Stores are household-scoped.** *Resolves open question 8* — you shop at the same places regardless of which list you're filling, so defining "Rema 1000" once and having it available everywhere is obviously right.
- **Catalogs stay per-list.** *Resolves open question 7* — groceries and hardware share almost no vocabulary, and pooling them would pollute suggestions in both. The catalog is what makes a list feel like *that* list.
- **Every user gets a personal household on signup**, so "a list of my own" needs no special case in the schema. A household of one is just a household.
- **Authorisation becomes a two-hop check:** user → household member → list. Cache the household membership set per request; do not join it on every item query.

**Everyone in a household sees every list in it.** No per-list permissions in v1. Households are small and high-trust by construction, and per-list ACLs would multiply the authorisation surface for a case ("I want a list my flatmate can't see") that a second household already solves.

**Roles and who can invite.** The creator of a household is its `owner`. **Only owners can create or revoke invite links.** Everyone else is a `member`, with full freedom over the actual content.

| Action | Owner | Member |
|---|---|---|
| Create/revoke invite links | ✅ | ❌ |
| Remove a member | ✅ | ❌ |
| Rename / delete the household | ✅ | ❌ |
| Promote a member to owner | ✅ | ❌ |
| Create, rename, archive, delete lists | ✅ | ✅ |
| Manage stores | ✅ | ✅ |
| Add, tick, edit, remove items | ✅ | ✅ |
| Leave the household | ❌ (must transfer first) | ✅ |

Two rules the model needs in order not to strand people:

- **Owners must be promotable.** A single-owner household is a bus-factor problem — if one person is on holiday with a dead phone, nobody can add the new flatmate. An owner can promote any member, and multiple owners are allowed.
- **The last owner cannot simply leave or delete their account.** Both flows must first require transferring ownership (or, for a household of one, deleting the household). Without this rule, a household ends up with no one able to invite, remove, or delete anything — permanently, since §6.5 says lists live forever.

**In the UI, don't show members a button that 403s.** Members simply don't see an invite control; they see who is in the household and, if useful, a "ask an owner to invite someone" hint. A disabled control with a permissions tooltip is worse than no control.

**Naming:** `household` internally. It reads warmly for the primary use case, though it fits flatmates and couples better than it fits a scout troop — see open questions.

### 6.2 Tables

```
users
  id uuid pk
  display_name text
  email citext unique
  avatar_url text
  created_at, updated_at, deleted_at

identities                     -- one row per OIDC provider link
  id uuid pk
  user_id uuid fk -> users
  provider text                -- 'google' | 'apple'
  subject text                 -- provider's stable user id
  unique (provider, subject)

households
  id uuid pk
  name text                    -- "Home", "The Flat"
  is_personal boolean          -- auto-created at signup, cannot be left or deleted
  created_by uuid fk -> users
  created_at, updated_at, deleted_at

household_members
  household_id uuid fk -> households
  user_id uuid fk -> users
  role text                    -- 'owner' | 'member'
  joined_at
  pk (household_id, user_id)

household_invites
  token text pk                -- random 128-bit, url-safe
  household_id uuid fk -> households
  created_by uuid fk -> users
  expires_at
  revoked_at

lists
  id uuid pk
  household_id uuid fk -> households
  name text
  colour text
  position numeric             -- ordering on the home screen
  archived_at timestamptz null
  created_by uuid fk -> users
  created_at, updated_at, deleted_at

stores                         -- "where to buy", scoped to the HOUSEHOLD
  id uuid pk
  household_id uuid fk -> households
  name text                    -- "Rema 1000", "Pharmacy", "Any store"
  normalised_name text
  colour text
  position numeric             -- user's own ordering
  is_default boolean           -- exactly one per list; undeletable
  deleted_at
  unique (household_id, normalised_name)

items
  id uuid pk                   -- CLIENT-generated, enables offline create
  list_id uuid fk -> lists
  name text        NOT NULL
  quantity numeric NOT NULL DEFAULT 1
  store_id uuid    NOT NULL fk -> stores
  unit text null
  note text null
  category_id uuid null
  is_checked boolean
  checked_by uuid null
  checked_at timestamptz null
  position numeric             -- fractional index for reordering
  created_by uuid  NOT NULL fk -> users
  created_at, updated_at
  deleted_at timestamptz null  -- soft delete = tombstone for sync
  version bigint               -- bumped on every write
  seq bigint                   -- per-list monotonic, sync cursor

item_history                   -- powers autocomplete & staples
  list_id, normalised_name, use_count, last_used_at
  pk (list_id, normalised_name)
```

Indexes that matter: `items(list_id, seq)`, `items(list_id, deleted_at)`, `list_members(user_id)`.

### 6.3 Sync protocol

Deliberately simple — a full CRDT is overkill for a shopping list, and you'd regret implementing one three times over for web/iOS/Android.

**Server-authoritative, per-list sequence cursor, last-write-wins per field.**

- Every list has a monotonic `seq` counter. Every item write assigns the next `seq` for that list.
- Client stores `last_seq` per list.
- **Pull:** `GET /lists/{id}/changes?since={last_seq}` returns all items (including tombstones) with `seq > last_seq`, plus the new cursor. Idempotent, cheap, and the recovery path for *everything* — dropped WebSocket, cold start, app reinstall.
- **Push:** client sends mutations with a client-generated `item.id` and a `mutation_id` (UUID). Server stores recent `mutation_id`s and ignores repeats, so retrying a flaky request is safe.
- **Live:** WebSocket pushes `{type: 'items.changed', list_id, items: [...], seq}` to other members. The socket is an optimisation only — if it drops, the client polls the changes endpoint. Never let correctness depend on the socket.
- **Conflicts:** last write wins per field, using server receive order. For the specific case of *two people ticking the same item*, LWW is invisible — both wanted it checked. For *tick vs. delete*, delete wins. For *concurrent name edits*, last writer wins and the other client just sees the value change; acceptable for this domain.
- **Offline queue:** client writes to IndexedDB immediately, appends the mutation to an outbox, applies it optimistically to the UI. On reconnect, flush the outbox in order, then pull changes.

**Clock rule:** never trust client timestamps for ordering. Clients send intent ("check this item"), the server stamps time.

### 6.4 Mandatory ≠ prompted

Making all four fields required is right — it means no half-formed entries, every row is groupable and countable, and the stats in §7.7 have no holes. But if "required" translates into a form with four inputs, adding milk goes from one tap to five and the app dies of friction.

The resolution: **the invariant is enforced on the record, never on the interaction.**

- **Count** defaults to `1`, and to the item's `default_quantity` on quick-add. Adjusted with +/− steppers on the row itself, not in a dialog.
- **Who added it** is never asked — it's the authenticated user.
- **Where to buy** resolves in this order: (1) explicitly chosen by the user; (2) the catalog entry's learned `default_store_id`, i.e. wherever this item usually gets bought; (3) the store currently selected in the list's store filter, so adding ten things for one shop only picks the store once; (4) the list's default store.

Step 2 is what makes this work — after two or three shops the catalog knows that milk is a supermarket item and paracetamol is a pharmacy item, so the correct store is filled in without anyone choosing it. Cold-start lists lean on step 4 and get corrected as people go.

**Server behaviour:** the API fills `quantity` and `store_id` from these defaults when omitted rather than returning a 400. A validation error here would break offline quick-add whenever a client's catalog cache is stale — and rejecting a write for a field the server can correctly infer is hostile, not rigorous.

**The "Any store" row.** Every list is created with one default store that cannot be deleted. It's the honest answer for "milk, wherever" and it keeps `store_id NOT NULL` true without inventing nulls-by-another-name.

### 6.5 List lifecycle: lists are permanent, items are traffic

A list is a **long-lived object representing a shopping context** — "Groceries", "Hardware" — not a single shopping trip. It is created once and persists for years. Items flow through it: added, bought, removed. The list is never archived, reset, or rolled over at the end of a shop. It disappears only when someone explicitly deletes it.

This is the right model — a household has one grocery list, forever, and its accumulated catalog and rhythm data (§7) are exactly what make the app good. But permanence has consequences the design has to absorb:

**Buying is two steps, not one.** Ticking an item marks it as in the trolley; it stays visible, struck through, in a collapsed section at the bottom. Removal happens separately, via "clear checked" at the till or automatically. Collapsing these into one step (tick = vanish) is tempting and wrong: mid-shop you constantly need to see what you've already picked up, and an accidental tap becomes an item you silently go home without.

*Resolves open question 3:* auto-clear checked items 12 hours after checking, so the list is clean by the next morning without anyone tidying, and undo remains possible for the rest of the shopping trip.

**Removal is soft delete, always.** A bought item leaves the active list but persists as a tombstone and as `item_events` history. Nothing that feeds the catalog or the stats is ever destroyed by ordinary use.

**Tombstones cannot accumulate forever.** A weekly shop over three years is ~5,000 dead item rows per list. Two rules:

- Purge tombstones older than **90 days** (`TOMBSTONE_HORIZON`). The `item_events` log is untouched by this — history survives, only the sync scaffolding is reclaimed.
- If a client presents a cursor older than the horizon, the server responds `{ full_resync_required: true }` rather than silently returning an incomplete delta. A client that has been offline for four months must rebuild from a snapshot; anything else risks resurrecting items that were bought months ago.

The snapshot endpoint (`GET /items`) returns live items only. `GET /changes?since=0` is not a supported way to bootstrap — it is a delta path with a horizon.

**Lingering items.** Because nothing resets, an item added once and never bought sits there indefinitely. After 60 days on the list unbought, mark it quietly (dimmed, with an age label) and offer a one-tap removal — a prompt, never an automatic deletion. Silently removing something a person deliberately added destroys trust in the list far faster than a bit of clutter does.

**Deletion destroys history.** Deleting a list ends years of catalog and stats data. Keep the 30-day soft-delete recovery window (FR-9), offer an **archive** option — hidden from the home screen, data intact — so nobody deletes merely to tidy up, and offer a JSON/CSV export from the confirmation dialog before a hard delete proceeds.

### 6.6 Dedupe

On add, normalise the name (lowercase, trim, collapse whitespace, strip trailing quantity) and check for an existing unchecked item in the list. If found, surface a soft suggestion — "milk is already on the list, bump quantity?" — never a hard rejection. Someone may genuinely want two entries.

---

## 7. Item catalog, suggestions & stats

This is the feature that decides whether the app feels fast. A shopping list is one of the most repetitive datasets a person owns — most households cycle through 60–120 distinct items forever. Typing "melk" for the 400th time is the failure state.

### 7.1 Why frequency alone is the wrong ranking

The obvious implementation — count how often each item was added, sort descending — degrades badly:

- Milk, bread and coffee sit permanently at the top and never move, so the row stops carrying information.
- An item bought 40 times last year outranks one bought 5 times last month.
- It suggests milk enthusiastically ten minutes after you bought milk.

The insight worth building around: **a shopping list isn't about frequency, it's about rhythm.** You buy milk every 5 days. If it's been 6 days, milk is *due* — and that's a far stronger signal than "milk is common". Ranking on due-ness makes the top suggestions change usefully day to day, which is what makes people look at them.

### 7.2 The catalog

Promote `item_history` into a proper per-list catalog. Items become instances of catalog entries.

```
catalog_items                  -- per LIST: groceries and hardware share no vocabulary
  id uuid pk
  list_id uuid fk -> lists
  canonical_name text            -- "Milk"
  normalised_name text           -- "milk", the match key
  default_quantity numeric null  -- last-used, prefilled on quick-add
  default_store_id uuid null     -- learned: where this item usually gets bought
  default_unit text null
  default_category_id uuid null
  add_count int                  -- raw lifetime count, for stats
  score numeric                  -- time-decayed, see 7.4
  score_updated_at timestamptz
  median_interval_days numeric null
  last_added_at timestamptz
  is_pinned boolean              -- user-curated staples beat any algorithm
  unique (list_id, normalised_name)

catalog_aliases
  catalog_item_id uuid fk
  normalised_alias text          -- "melk", "milk 2l", "semi skimmed"
  unique (list_id, normalised_alias)

items
  + catalog_item_id uuid null fk -> catalog_items
```

Normalisation for the match key: lowercase, trim, collapse whitespace, strip leading quantities and trailing units ("2l milk" and "milk 2l" both → `milk`, with the quantity captured separately). Keep the user's literal typed text in `items.name` — never rewrite what someone typed, only *link* it.

### 7.3 Log events from day one

```
item_events                      -- append-only
  id bigserial
  list_id, catalog_item_id, item_id
  user_id
  event_type                     -- 'added' | 'checked' | 'deleted_unchecked' | 'unchecked'
  quantity, unit, store_id
  occurred_at
```

**Build this in phase 3, before any stats feature exists.** Every aggregate in §7.7 is trivially derivable from an event log and completely unrecoverable without one. Retrofitting stats onto a year-old app means a year of blank charts. Writing one extra row per action costs nothing now and is impossible to buy later.

`deleted_unchecked` matters as a negative signal — an item repeatedly added and then removed without being bought is one the suggester should learn to stop offering.

### 7.4 Ranking

**Ship the dumb version first.** Everything below is a hypothesis: that due-ness beats recency for real households. Nobody has validated it, and it may turn out people lean on pinned staples and barely look at the row. So release 1 ranks suggestions by **recency — the last 10 distinct items added, newest first**, which is perhaps thirty lines of code.

Then use the accumulated `item_events` to check the model offline against real data before building it: does due-ness actually predict what a household adds next, better than recency does? The event log is exactly what makes deferring this safe rather than lazy. Build the machinery below only once the data says it earns its place.

Two factors, multiplied.

**Decayed frequency.** Maintain incrementally rather than scanning history. On each add:

```
score = score * 2^(-Δdays / HALF_LIFE) + 1        HALF_LIFE = 30 days
```

At read time, decay again to *now* before sorting. This is a running exponential average — recent behaviour dominates, old behaviour fades without ever being deleted.

**Due-ness.** Once an item has 4+ observations, compute the median gap between adds (median, not mean — one holiday shouldn't skew it). Then with `r = days_since_last / median_interval`:

| r | Meaning | Multiplier |
|---|---|---|
| < 0.5 | Just bought it | 0.3 — actively suppress |
| 0.5–0.8 | Soon | 0.8 |
| 0.8–1.5 | **Due now** | 1.5 |
| > 1.5 | Lapsed — habit may have changed | 1.0 |

Fewer than 4 observations → multiplier 1.0, rank on decayed frequency alone.

**Hard filters, applied after scoring:** never suggest an item already on the list unchecked; never suggest an item removed from the list in the last hour; never suggest one whose `deleted_unchecked` count exceeds its `checked` count.

The whole catalog for a list is a few hundred rows, so this is a single indexed query — no background jobs, no ML, no recommendation service.

### 7.5 Where suggestions surface

1. **Quick-add row** — horizontally scrollable chips directly above the input, above the keyboard. 8–10 chips. **One tap adds instantly**, using `default_quantity`, no confirmation dialog. Long-press to adjust quantity first.
2. **Empty-list "usual shop"** — when a list is empty, the quick-add row expands to a grid with bigger targets. Multi-select, then one "Add 7 items" button. This turns the weekly shop from 7 interactions into 8 taps.
3. **Inline autocomplete** — as you type, prefix and alias matches ranked by the same score. Enter accepts the top match.
4. **Pinned staples** — always first, in user-chosen order, before anything algorithmic. Human curation beats the ranker for the top five and gives people a way to fix it when the algorithm is wrong.

**Store-aware ranking.** When the list is filtered to one store, rank suggestions whose `default_store_id` matches that store first. Planning a pharmacy trip should not surface bread.

**Hide quick-add in shopping mode.** When someone is in the store ticking items off, suggestions are pure noise competing for screen space. Planning and shopping are different modes; the same screen shouldn't serve both identically.

### 7.6 Order stability — the usability trap

If the row re-ranks after each tap, the chip you're reaching for moves under your finger and you add the wrong thing. Rule: **compute the ranking once when the list screen opens and freeze it for that session.** A tapped chip fades out in place and leaves its gap for a beat before the row closes up. Never reorder a visible row in response to a user's own action.

This sounds minor. It's the difference between the feature being loved and being switched off.

### 7.7 Stats worth showing

Per item (long-press → detail sheet): times added, last added, typical interval ("about every 5 days"), typical quantity, who usually adds it, who usually buys it.

Per list: **deliberately not in the plan.** A charts screen showing items-per-week and top-10 rankings is a lot of work for something people open twice. Stats in this app earn their place by feeding the suggester, which happens invisibly. Keep the event log, keep the per-item sheet, and build the dashboard only if someone asks for it twice.

**A caution about framing.** With avatars showing who added and ticked things (§3.1), it would be easy to slide into per-person leaderboards — "Ada bought 68%, you bought 32%". Don't. In a household that reads as scorekeeping, and it turns a shared utility into an argument. Frame stats around the *household's* patterns, and keep per-person data descriptive and only visible on the individual item.

### 7.8 Cold start

A new list has no history and the feature is invisible for a week. Resist seeding it with a generic "common groceries" list — a Norwegian household and a Spanish one share almost nothing, and wrong suggestions are worse than none. Instead, show "recently added" for the first ~15 items, which fills up within one shop, then transition to ranked suggestions once there are enough observations to rank.

### 7.9 Later

Co-occurrence ("you usually add tortillas with mince") is a genuinely good v2 feature and falls straight out of `item_events` grouped by session — another reason to log from day one. Seasonal patterns and per-store variants likewise.

---

## 8. API surface (v1)

All under `/api/v1`, JSON, bearer access token. Authorisation is the two-hop check from §6.1: user → household member → list.

```
POST   /auth/oidc/{provider}/callback   exchange code -> our tokens
POST   /auth/refresh                    rotate refresh token
POST   /auth/logout

GET    /me
PATCH  /me
DELETE /me

GET    /households
POST   /households
PATCH  /households/{hid}                       owner only
DELETE /households/{hid}                       owner only
GET    /households/{hid}/members
DELETE /households/{hid}/members/{userId}      owner only, or self (leave)
POST   /households/{hid}/invites               owner only -> { token, url, expires_at }
DELETE /households/{hid}/invites/{token}       owner only
GET    /households/{hid}/invites               owner only — active links
PATCH  /households/{hid}/members/{userId}      owner only — promote to owner
POST   /invites/{token}/accept                 -- joins the household

GET    /households/{hid}/stores
POST   /households/{hid}/stores
PATCH  /households/{hid}/stores/{storeId}
DELETE /households/{hid}/stores/{storeId}      -- reassigns its items to the default store

GET    /households/{hid}/lists
POST   /households/{hid}/lists
GET    /lists/{id}
PATCH  /lists/{id}                             -- incl. archive, reorder
DELETE /lists/{id}

GET    /lists/{id}/items              full snapshot
GET    /lists/{id}/changes?since=     delta sync
POST   /lists/{id}/items              create (client-supplied id)
PATCH  /lists/{id}/items/{itemId}
DELETE /lists/{id}/items/{itemId}
POST   /lists/{id}/items:batch        offline outbox flush
POST   /lists/{id}/items:clearChecked

GET    /lists/{id}/catalog           full catalog w/ scores, cached client-side
GET    /lists/{id}/suggestions       ranked quick-add row (server-ranked)
PATCH  /lists/{id}/catalog/{itemId}  pin/unpin, rename, set default quantity
POST   /lists/{id}/catalog/merge     merge two catalog entries (dedupe fix)
GET    /lists/{id}/stats             household aggregates (§7.7)
GET    /lists/{id}/catalog/{itemId}/stats

WS     /ws?token=                     subscribe to all lists in the user's households
GET    /healthz  /readyz
```

**Conventions:** cursor pagination, `Idempotency-Key` honoured on all POSTs, RFC 9457 problem+json errors, `409` never used for sync conflicts (the server resolves them silently).

---

## 9. Auth design

**Google is the only provider in R1**, and **your server issues its own tokens** rather than passing provider tokens around. That second point is what makes the first one safe: the session model is yours, so adding providers later is additive and the mobile apps eventually use the identical mechanism.

### 9.1 Why Google only, and what it costs

Google Sign-In is free and requires no verification for the non-sensitive scopes this app uses (`openid`, `email`, `profile`). Apple's OIDC requires the $99/year Apple Developer Program, which buys nothing until there is a native iOS app to ship.

**Apple's requirement to offer Sign in with Apple applies to App Store apps, not to websites.** A PWA has no such obligation, so a Google-only web app is fully compliant on iPhones. The $99 becomes necessary only at R4, and only if native iOS actually happens — which it may not, given iOS PWAs are installable, work offline, and support web push once installed.

**What this defers, and what stays live:**

- `identities` keyed on `(provider, subject)` ships in R1 with exactly one provider in it. The table does not change when a second arrives.
- The account-linking rules below are written and **tested against the fake issuer**, but dormant in production until there are two providers. Building them later, against live user data, is far riskier than building them now against fakes.
- The `provider` column is never assumed to be `'google'` anywhere in the code.

**If someone genuinely won't use a Google account**, the options in rough order of cost: passkeys (free, no new infrastructure, but account recovery is a real problem — losing a phone loses the list); email magic links (free tier available, but reintroduces email sending, the exact subsystem share-link invites removed); Apple ($99/yr, only worth it alongside native iOS). Decide when a real person asks, not now.

**Flow (Authorization Code + PKCE):**
1. Client opens the provider's authorize URL with PKCE challenge and a state nonce.
2. Provider redirects back with a code.
3. Client posts the code to `/auth/oidc/{provider}/callback`.
4. Server exchanges the code with the provider, verifies the ID token signature against the provider's JWKS, checks `iss`, `aud`, `exp`, `nonce`.
5. Server upserts `identities` + `users`, issues:
   - **Access token:** JWT, 15 min, signed with a server key.
   - **Refresh token:** opaque random string, hashed in DB, 60 days, **rotated on every use**, with reuse detection (a replayed refresh token revokes the whole family).
6. Web stores the refresh token in an `HttpOnly; Secure; SameSite=Lax` cookie. Mobile stores it in Keychain/Keystore.

**Invite acceptance** is a household join, so it is the one flow where an unauthenticated user hits a link and must sign in mid-flow. Preserve the invite token across the OIDC round-trip (in the `state` parameter or a short-lived cookie) so the user lands inside the household rather than on an empty home screen wondering what happened.

**Gotchas for when Apple is eventually added** — recorded now so the decision is informed, not so it blocks R1:
- Apple requires a paid Apple Developer account, and the "client secret" is a JWT you sign with a `.p8` key that must be regenerated at most every 6 months — automate this or it *will* expire on a Sunday.
- Apple gives you the user's name **only on first authorisation**. Capture it then or you never get it.
- Apple offers private relay emails (`@privaterelay.appleid.com`). Never treat email as the identity key — `(provider, subject)` is the key.
- Same person signing in with Google and Apple on the same email: **auto-link.** Rules, which need enforcing carefully because email-based linking is an account-takeover vector if done loosely:
  - Link only when the *incoming* provider asserts `email_verified: true` **and** the existing account's email was itself verified at signup. If either is unverified, create a separate account rather than guessing.
  - Never link on an Apple private-relay address — it is per-app and per-user, so it can never legitimately match an existing Google email.
  - Linking adds a row to `identities`; it never merges two existing user rows. If both accounts already exist with lists of their own, leave them alone and offer a manual merge later — silently merging two people's data is unrecoverable.
  - Log every link event with both provider subjects.
- Apple's App Store rules require offering Sign in with Apple if you offer other third-party sign-in — which is why native iOS and the $99 arrive together, and why neither is needed before then.

---

## 10. Frontend notes (web first)

### 10.1 Layering

Four layers, only the last of which imports React. This split is what makes the frontend testable — three of the four layers can be tested with no DOM at all.

1. **Store** — Dexie tables mirroring the server (`lists`, `items`), plus `outbox` (pending mutations) and `sync_state` (per-list `seq` cursor).
2. **Sync engine** — plain TypeScript, zero React imports. Owns the pull loop, the WebSocket subscription, and the outbox flush. Framework-free so React Native imports it unchanged and a native Swift/Kotlin port has an obvious thing to copy.
3. **Mutations** — one function per user action. Each performs a *single* Dexie transaction that applies the local change **and** appends to the outbox atomically. If the tab dies mid-write you get both or neither.
4. **Components** — read via Dexie's `useLiveQuery`, re-rendering on local DB changes. A WebSocket message and a local tap flow through the identical path: something wrote to IndexedDB, the query re-fired.

### 10.2 Auth in the browser

Access token in memory only — never `localStorage`, which XSS can read. Refresh token in the `HttpOnly` cookie. A `fetch` wrapper catches 401, refreshes once, retries, and **de-duplicates concurrent refreshes** so ten parallel requests don't fire ten rotations and trip the server's reuse detection (§9), logging the user out for no reason.

### 10.3 Interaction notes

- **PWA:** manifest, service worker (Workbox), installable, offline shell. Add a Web Share Target so "share to shopping list" works from other Android apps.
- **Rendering:** the list view is the whole app. Optimise it — virtualise only if lists get long, otherwise keep it simple.
- **Interaction:** whole row is the tick target, not a small checkbox. Checked items animate to a collapsed section at the bottom rather than vanishing. Add-item field is pinned above the keyboard and stays focused after submit for rapid multi-add.
- **Hide the household concept until it earns its place.** A user with one household should never see the word. The home screen shows their lists; "household" surfaces only when they belong to more than one, or when they open sharing settings. The model needs the hierarchy; the interface mostly should not show it.
- **Store as the primary grouping.** The list groups by store, collapsed to a single section when everything is "Any store" so a simple household never sees the machinery. A store filter chip row sits at the top; picking one enters shopping mode — that store's items only, quick-add hidden, big tap targets.
- **Count on the row**, not in a dialog: the quantity sits at the left of each row with a tap-to-increment and a long-press stepper. Showing `1` on every row is visual noise, so render the count only when it is greater than 1.
- **Changing store** is a swipe action or a long-press menu on the row, listing the list's stores with their colours. Never a modal with a dropdown.
- **Attribution:** a small avatar on the right of each row shows who added the item; once ticked, it shows who ticked it. Keep it quiet — 20px, no names inline, full detail on long-press. The point is ambient awareness ("Ada's already got the milk"), not an audit trail.
- **State:** IndexedDB is the source of truth for the UI; the network layer syncs it. The UI never waits on the network.
- **Design a mobile-native mental model now** — the web app should feel like the app it'll become.

---

### 10.4 The add-entry combobox

Both the item field and the store field use the same control: **an editable combobox that offers what you've used before and always accepts something new.** This is not a `<select>`, and it is not a plain text input with a suggestion list bolted underneath — it is the ARIA combobox pattern, and getting the details right is most of the work.

**Free text always wins.** The rule that keeps this usable: when what's typed doesn't exactly match an existing entry, the *first* row in the dropdown is `Add "sourdough starter"`. The control never forces a choice from the list, never silently substitutes the closest match, and never auto-completes on blur — that is how "bananas" becomes "bandages" and people stop trusting the field.

**Keyboard and selection rules:**

| Input | Result |
|---|---|
| Type, then Enter with nothing highlighted | Creates exactly what was typed |
| Arrow down / up | Moves through the list; nothing is highlighted by default |
| Enter on a highlighted row | Selects that catalog entry |
| Blur / dismiss | Keeps the typed text, selects nothing |

Never pre-highlight the first row. Aggressive autocomplete costs more in wrong entries than it saves in keystrokes.

**Matching**, all client-side against the cached catalog: exact → prefix → alias (§7.2) → substring → fuzzy, and within each tier ordered by the §7.4 score. Highlight the matched span in each row so the reason for a suggestion is visible. With a few hundred catalog rows this runs in under a millisecond, so **no debounce and no network call per keystroke** — which also means the dropdown works fully offline, exactly like the rest of the app.

**On focus with an empty field**, show the top-ranked suggestions rather than an empty dropdown — the same ranking that feeds the quick-add row, so the two surfaces never disagree.

**Item field vs store field are deliberately asymmetric:**

- **Items:** type-first. Hundreds of entries, so the dropdown filters as you type. Creating a new one is instant and unconfirmed — items are cheap, per-list, and easy to remove.
- **Stores:** pick-first. A household has maybe 5–15, so tapping the field shows all of them immediately with no typing. Creating a new store **asks for confirmation** ("Create store 'Rema1000'?"), because stores are household-scoped, few, long-lived, and shared — a typo pollutes the store list for everyone, permanently. The asymmetry is justified precisely because the blast radius differs.

**Quantity parsing:** "2 milk", "milk x2" and "milk 2l" resolve to a count (and unit) plus a clean item name, with the parse shown in the row so it can be corrected before committing.

**Mobile specifics, which are where this control usually breaks:**

- The virtual keyboard covers the bottom half of the screen, so the dropdown opens **upward** from the input, never downward. Track the keyboard with the `visualViewport` API rather than guessing at heights.
- Input `font-size` must be at least 16px or iOS Safari zooms the page on focus and the layout jumps.
- Rows are full-width with 44px minimum height.
- Cap the visible list at ~6 rows and scroll — a taller list covers the shopping list behind it and disorients.
- After committing an entry, clear the input, keep focus and keep the keyboard open, so adding ten things is ten cycles of type-Enter.
- The dropdown and the quick-add chip row (§7.5) must never be visible at once; opening the dropdown hides the chips.

**Near-miss handling:** typing "melk" where "Milk" exists surfaces it via alias matching, but if someone creates the duplicate anyway, don't block them. Instead offer a quiet "Merge with Milk?" affordance afterwards, which is the same operation as `POST /catalog/merge` (FR-20d). Blocking a create to prevent a duplicate is a bad trade — it stops a correct entry to prevent a fixable one.

**Accessibility:** `role="combobox"` with `aria-expanded`, `aria-controls` and `aria-activedescendant`; the listbox announces its option count; the create row is announced as such rather than as an ordinary option. This is on the §4 WCAG AA commitment, and comboboxes are the single most commonly broken widget for screen-reader users.

### 10.5 Failure states, empty states and silence

The spec has so far described the app working. Most of the felt quality of an offline-first app is in what it does when things go wrong, so these are requirements, not polish.

**Connection state — three states, not two.** *Synced* (say nothing at all — a green tick is visual noise for the normal case), *offline with pending changes* (a quiet persistent bar: "Offline · 4 changes waiting"), and *sync failing while online* (distinct from offline, and the only one that warrants an explicit retry affordance, since it usually means a bug or an expired session rather than a tunnel).

**Losing an edit to last-write-wins: say nothing.** §6.3 resolves concurrent edits silently by design. The alternative — "Ada changed this while you were editing" — turns a rare, low-stakes event into an interruption during shopping, for a domain where the cost of losing an edit is retyping one word. The exception is *your own pending change being rejected by the server*, which is a bug and must surface. **This is a deliberate decision, not an oversight**, and it belongs in the doc precisely because it looks like one.

**Empty states carry the onboarding.** A brand-new list is the only teaching moment the app gets: it should show what to do, not an illustration. First list empty → the add field focused and the keyboard up. No suggestions yet → say so plainly rather than showing a blank row. Everything bought → "All done" rather than a void, since an empty list mid-shop and a completed list look identical otherwise.

**The failures worth designing explicitly:** expired or revoked invite link; joining a household you are already in; a list deleted by someone else while you have it open; session expiry mid-shop (must never lose queued offline changes — refresh silently, and if that fails, keep the outbox and prompt at the *end* of the trip, not during it); an item whose store was deleted; and quota or rate-limit responses.

**Never block the list on a failed network call.** Every error path degrades to local-only operation. The app working offline is not a feature that sits alongside error handling — it *is* the error handling.

---

## 11. Testing strategy

Testing is a first-class requirement here, not a phase at the end. The guiding rule: **test at the layer where the logic actually lives, and don't mock anything you can cheaply run for real.** Containers make a real Postgres available in milliseconds; a mocked database only ever tests your mock.

### 11.1 Backend layers

| Layer | What it covers | How |
|---|---|---|
| **Domain** (`internal/*`, pure funcs) | Name normalisation and alias matching, the four-field default resolution chain (§6.4), decay and due-ness scoring (§7.4), fractional index calculation, conflict resolution, token rotation state machine | Table-driven Go tests, no test doubles, microseconds to run |
| **Store** (`internal/store`) | Every sqlc query, migration correctness, index behaviour, constraint violations | `testcontainers-go` with real Postgres 16; goose migrations run at suite start; each test in a transaction that rolls back |
| **Service** | Authorisation across the two-hop hierarchy (household → list → item), cross-household isolation, business flows, idempotency | Real store, fake clock, fake OIDC issuer |
| **Transport** (HTTP) | Routing, status codes, error shapes, auth middleware, rate limits | `httptest` against the real chi router and real DB |
| **Contract** | Responses actually match `openapi.yaml` | `kin-openapi` validating middleware enabled in the test suite; CI fails if generated code drifts from the spec |
| **Realtime** | WS fan-out, multi-subscriber delivery, disconnect mid-broadcast, fan-out through the `Broadcaster` interface | In-process `Broadcaster` with multiple subscribers in one process; no external broker to stand up (§5.2) |

Run everything with `-race`, always, not just in CI. Concurrency bugs in the sync path are exactly the kind that pass a hundred serial tests.

### 11.2 The sync conformance suite

**The single highest-value thing in this document.** Write the sync scenarios once as a language-agnostic suite that drives the *HTTP API* — a list of scenarios in YAML or plain Go, each a sequence of client actions and expected server state.

Why it matters: you're going to build three clients (web, iOS, Android). Without this, you verify sync three times, differently, and the Android one has a subtle cursor bug nobody finds for six months. With it, every client implementation runs the same suite and either passes or doesn't.

Scenarios it must contain from day one:

- A store is deleted on one client while another client is offline adding items to it → offline items reassign to the default store, nothing is orphaned, `store_id` is never null.
- Two clients tick the same item within milliseconds → both converge to checked, no error surfaced.
- Client A ticks, client B deletes, concurrently → delete wins, A's local copy disappears on next pull.
- Client goes offline, performs 15 mutations including create-then-edit-then-delete of the same item, reconnects → server state correct, no orphans.
- The same `mutation_id` submitted three times → applied once.
- Outbox flushed out of order (edit arrives before create) → server handles or client guarantees ordering; assert which.
- Pull from `seq = 0` on a list with tombstones → client correctly ends up *not* showing deleted items.
- Client with a cursor older than `TOMBSTONE_HORIZON` → server returns `full_resync_required`, client rebuilds from snapshot, and items bought months ago do **not** reappear. This is the single most likely real-world corruption path in a permanent list.
- A list running for a simulated three years with weekly shops → snapshot response size stays flat, tombstone purge does not break an actively-syncing client.
- WebSocket drops mid-session, three mutations happen elsewhere, socket reconnects → the pull-on-reconnect catches all three.

### 11.3 Frontend layers

| Layer | How |
|---|---|
| **Sync engine** | The frontend's most valuable tests by a wide margin. Pure TS, so test it with `fake-indexeddb` + MSW mocking the API — no browser, no React. Outbox ordering, retry with backoff, cursor advance, tombstone application, reconnect behaviour |
| **Mutations** | `fake-indexeddb`; assert transactional atomicity — kill the transaction mid-flight and assert neither the item nor the outbox row exists |
| **Components** | Vitest + Testing Library against a seeded fake IndexedDB. Test what the user sees, never internal state. The combobox (§10.4) needs its own suite: free text commits verbatim, blur never substitutes a match, Enter with nothing highlighted creates rather than selects, and the create row appears whenever there is no exact match |
| **E2E** | Playwright, **two browser contexts**, `context.setOffline(true)`. Tick in both while one is offline, reconnect, assert convergence. Also: install the PWA, go offline, hard-reload, assert the app still opens with data |
| **Accessibility** | Keyboard-only traversal of the combobox and correct `aria-activedescendant` announcements, plus `axe-core` assertions in the Playwright suite — catches the contrast and label regressions that manual testing misses |

### 11.4 Auth testing

Auth is hard to test because it depends on Google and Apple. Solve it by running your own OIDC issuer in tests: generate a keypair, serve a JWKS endpoint, sign your own ID tokens. Then you can actually test the cases that matter:

- `email_verified: false` on the incoming provider → **no auto-link**, separate account created.
- Apple private-relay address matching an existing Google email → **no link**.
- Two existing users, one per provider, same email → **no merge**, manual path offered.
- A member (not owner) attempting to create an invite, remove a member, or promote themselves → 403 on every path, including direct API calls that bypass the UI.
- The sole owner attempting to leave or delete their account → blocked until ownership transfers.
- Refresh token replayed after rotation → whole token family revoked.
- Expired, wrong-`aud`, wrong-`iss`, and unsigned ID tokens → all rejected.

Each of these is a security control. If it isn't tested, it isn't a control.

### 11.5 CI gates

Fail the build on: `go vet`, `golangci-lint`, race-enabled tests, OpenAPI drift (regenerate and diff), frontend typecheck, unit suites, the conformance suite, and Playwright on pull requests.

On coverage: set a floor on `internal/` (70% is honest) but don't chase a number. A green 95% with no concurrency test is worse than 70% with the conformance suite passing.

---

## 12. Repository, CI/CD & operations

The project lives in a **public GitHub repository**, which on the free plan gives away almost everything this project needs. <cite index="9-1">Standard GitHub-hosted runners are free in public repositories, as are GitHub Pages and Dependabot</cite> — <cite index="3-1">and GitHub has confirmed Actions remains free for public repositories under the 2026 pricing changes</cite>. The 2,000-minute cap people worry about applies only to private repos.

### 12.1 What to take from the free tier

| Need | Use | Notes |
|---|---|---|
| CI | **GitHub Actions**, standard runners | Unlimited minutes. Run the full §11 suite on every PR — no reason to economise |
| Container hosting | **GHCR** (`ghcr.io`) | Public packages are free to store and pull; your server pulls without credentials |
| Static analysis | **CodeQL code scanning** | <cite index="19-1">Free for public repositories on GitHub.com</cite>; paid elsewhere. Add the Go and JavaScript analyses |
| Dependency updates | **Dependabot** | Version updates for Go modules, npm, Docker base images and Actions themselves. Group patch updates weekly or it becomes noise |
| Secret leak protection | **Push protection / secret scanning** | Free on public repos. Turn it on before the first commit, not after |
| Planning | **Issues + Projects** | The §13 phases become milestones; the §11.2 conformance scenarios become checklist issues |
| Docs / demo | **GitHub Pages** | Good home for the OpenAPI spec rendered as docs |
| Releases | **Releases + tags** | Tag `vX.Y.Z`, attach the built binary, let CI cut it |
| Dev environment | **Codespaces** | Free monthly core-hours on personal accounts — useful precisely because you're often on a phone. Check your current allowance before relying on it |

### 12.2 Workflows

- `ci.yml` — on PR and push: `golangci-lint`, `go vet`, `go test -race ./...` with a Postgres **service container**, OpenAPI drift check, web typecheck and unit tests, Playwright E2E. Matrix across Go versions if you like; it costs nothing.
- `conformance.yml` — the §11.2 sync suite, split out so a failure is unmistakable.
- `codeql.yml` — weekly plus on PR.
- `release.yml` — on tag: build multi-arch images (`linux/amd64`, `linux/arm64`), push to GHCR, create a release.

Cache aggressively (`actions/cache` for the Go module and build cache, npm, and Playwright browsers) — not to save money, but because a 90-second PR loop is the difference between running tests and skipping them.

### 12.3 Deploying to your own server

**Cloudflare Tunnel replaces the reverse-proxy-with-public-IP model.** `cloudflared` runs as a container alongside the others and makes an *outbound* connection to Cloudflare, so:

- No ports forwarded on your home router, and no public IP exposed. This is the main reason to do it.
- No Let's Encrypt, no cert renewal — TLS terminates at Cloudflare's edge.
- A dynamic home IP stops mattering entirely; no DDNS.
- It is free.

Keep Caddy *behind* the tunnel anyway, serving the built web app and proxying `/api` on plain HTTP. That preserves the same-origin setup (so no CORS, per §5.1) and means the whole stack still runs unchanged if you ever leave Cloudflare.

**Two Cloudflare gotchas worth knowing before M5:**

- **WebSockets idle out.** Cloudflare closes idle WebSocket connections after roughly 100 seconds. Send an application-level ping every ~30s from both ends. The spec already treats the socket as an optimisation over the pull path (§6.3), so a dropped connection degrades gracefully — but without heartbeats it will drop constantly and you will chase a phantom bug.
- **Tunnel restarts are invisible to clients** but do drop sockets. The reconnect-and-pull path must be solid, which the §11.2 conformance scenarios already cover.

**Use pull-based deployment, not SSH-from-Actions.** The temptation is a workflow that SSHes into your box, but that means an SSH private key in the secrets of a public repository and an inbound port open to GitHub's IP ranges. Instead:

1. CI pushes `ghcr.io/<you>/shoppinglist-api:<sha>` and `:latest`, multi-arch (`amd64` + `arm64`, in case the home server is a Pi or similar).
2. The server runs a systemd timer (or Watchtower) every few minutes: `docker compose pull && docker compose up -d && docker image prune -f`.
3. Migrations run on container start, forward-only.

No inbound access, no deploy keys, no secrets in a public repo at all — and with a Cloudflare Tunnel there is no inbound path to the home network for anything to reach even if it wanted to. Roll back by pinning the previous SHA in the compose file.

### 12.4 Public-repo hygiene

- **Every secret is a production incident.** `.env` in `.gitignore` from commit one, `.env.example` documenting each variable with dummy values, push protection enabled. Assume anything committed is permanently public even after a force-push.
- **Never use `pull_request_target`** in a public repo — it runs with write permissions and repo secrets in the context of untrusted fork code. Plain `pull_request` doesn't expose secrets to forks, which is exactly what you want.
- Pin third-party Actions to a commit SHA, not a tag.
- Set `permissions: contents: read` at workflow level and widen only where needed.
- Add `LICENSE`, `SECURITY.md`, and a `CONTRIBUTING.md` even if nobody contributes — a public repo without them reads as abandoned.
- Branch protection on `main`: require the CI and conformance checks, no direct pushes. Worth doing solo, because it stops the 11pm "just this once" commit.

### 12.5 Measurement — deliberately none

*This reverses decision 8.* Self-hosted analytics was the right answer for a product with unknown users. For a household of five it is not: **you can simply ask them.** Direct feedback from four people who use the app daily beats any dashboard, and standing up Umami or Plausible would mean another container, another database and another backup for data you could get over dinner.

What replaces it: after a week of use, ask each person what annoyed them. That is a better signal than taps-to-add, and it costs nothing to maintain.

If this ever goes beyond the family, revisit — the metrics that would matter are listed in the git history of this document.

### 12.6 Runtime operations

- Docker Compose on the home server: `api`, `postgres`, `caddy`, `cloudflared`. Four containers, no Redis.
- Multi-stage Dockerfile: build with `CGO_ENABLED=0`, then `FROM scratch` with the binary and CA certs. Expect an image under 20 MB.
- Caddy serves the built web app as static files and proxies `/api` to the Go service, all on the same origin (avoids CORS entirely). No TLS config — the tunnel handles it.
- Backups: nightly `pg_dump` to object storage, 30-day retention, **plus a documented restore procedure you've actually run**.
- Monitoring: an uptime ping on `/healthz` and log rotation. Skip error-tracking infrastructure — with five users you will hear about problems directly, and `docker logs` plus structured `slog` output is enough.

---

## 13. Delivery plan

**The scope has grown, and that is the main risk in this document.** Households, stores, catalogs, rhythm ranking, an event log, stats and a combobox have accumulated into something that is months of solo evenings. The failure mode for a project like this is not technical — it is losing momentum before anything is usable. So R1 is cut hard, and the schema carries things the UI does not yet expose.

### R1 — the minimum lovable list

| Phase | Scope | Notes |
|---|---|---|
| 0 | Public repo, LICENSE, push protection, branch protection, Compose, Postgres, goose, OpenAPI + codegen, CI green on an empty service | get the pipeline working before there is anything to break |
| 1 | Auth: **Google only**, own tokens, `/me`, linking logic tested against a fake issuer | riskiest piece — do it early; §9.1 |
| 2 | Households (one per user, no invites yet), lists, **full schema including `stores` and `catalog_items`** | the hierarchy and the tables are cheap now, brutal to retrofit |
| 3 | Items CRUD, snapshot endpoint, **`item_events` log writing from the first commit** | cannot be backfilled — this is the one thing that must not slip |
| 4 | Web UI: list view, add, tick, clear checked. Store fixed to "Any store", no store UI | first usable version |
| 4b | Sync conformance suite, **written before the sync code** | §11.2 — these tests *are* the protocol spec |
| 5 | Sync: changes endpoint, outbox, IndexedDB, WebSocket, failure states (§10.5) | the piece that makes it good |
| 6 | Combobox (§10.4), **recency-ranked suggestions only**, PWA polish | R1 complete — use it for a month before building more |

Deliberately *not* in R1: household invites, store management UI, decay-and-due-ness ranking, pinned staples, categories, stats, undo, presence. The tables exist; the screens do not.

### R2 — sharing

Household invites and roles, store management and store filtering, shopping mode, dedupe and merge. This is the first release worth showing anyone else, and it should be shaped by a month of single-user experience rather than guessed at now.

### R3 — the clever bits, if the data supports them

Decay-and-due-ness ranking (§7.4) validated against real `item_events` first, pinned staples, usual-shop grid, categories, per-item stats sheet, undo, presence.

### R4 — native

iOS and Android against the same API and the same conformance suite, plus push notifications — and this is the point at which Apple Developer membership and Sign in with Apple become necessary, not before.

**Sequencing rules worth holding to:** the event log ships in R1 no matter what; sync is not started before its conformance suite exists; and phase 6 ends with real mis-tap testing on a real phone in a real shop (§7.6), which is where this category of app is actually won or lost.

---

## 14. Decision log

| # | Decision | Consequence |
|---|---|---|
| 1 | **Go + chi** for the backend | Tiny static-binary deploys; OpenAPI replaces shared types (§5.2) |
| 2 | **Auto-link Google/Apple on verified email** | Needs the strict guard rails in §9 — this is the one decision with a security edge |
| 3 | ~~Redis from the start~~ → **no Redis** | Reversed once scale became "one household". In-process pub/sub behind a `Broadcaster` interface; idempotency keys in Postgres (§5.2) |
| 4 | **Share-link invites only** | No email infrastructure needed at all in v1 — worth protecting, it removes a whole subsystem |
| 5 | **No push until native apps** | v1.1 stays small; PWA relies on the WebSocket while open |
| 6 | **Show "added by" avatars** | Items carry `created_by` / `checked_by`; account deletion must anonymise rather than cascade |

| 7 | **Recency ranking in R1, rhythm ranking only if the data supports it** | §7.4 becomes a hypothesis to test, not a commitment |
| 8 | ~~Self-hosted analytics~~ → **no measurement at all** | Reversed at family scale: ask the four users directly (§12.5) |
| 12 | **Home server behind a Cloudflare Tunnel** | No open ports, no public IP, no TLS management, no DDNS, free. Costs: Cloudflare sees plaintext, and WebSockets need heartbeats (§12.3) |
| 13 | **Family project, not commercial** | Scale target ~10 users; prefer removing infrastructure over adding it |
| 9 | **Silent conflict resolution** | No "someone else changed this" interruptions; only rejected local writes surface (§10.5) |
| 11 | **Google-only sign-in in R1; Apple deferred to native iOS, possibly forever** | Zero auth cost; provider-agnostic `identities` table and linking tests still ship (§9.1) |
| 10 | **UTC internally, Norwegian-aware normalisation** | æ/ø/å folding and decimal-comma parsing are correctness requirements, not i18n polish |

## 14b. Next open questions

Smaller, and none of them block starting work:

1. **Invite link lifetime** — 7 days and single-use, or long-lived and reusable for a household? Reusable is friendlier; single-use is safer if the link ends up in a group chat that outlives the household.
2. **Does the link auto-join, or request approval?** Auto-join is the whole point of a share link, but it means anyone holding the URL is in.
3. ~~Auto-clear checked items~~ — resolved in §6.5: auto-clear at 12 hours, plus manual "clear checked".
4. **List size ceiling** — a soft cap (500 items?) protects the sync payload and catches runaway clients.
5. **Do avatars need image hosting**, or is the provider's `avatar_url` enough? Hotlinking Google/Apple avatars is simplest but the URLs can expire.
6. **Half-life of 30 days** — right for a weekly shop, but worth making a tunable constant and revisiting with real data.
7. ~~Catalog per-list or per-user~~ — resolved in §6.1: per-list.
8. ~~Stores per-list or per-household~~ — resolved in §6.1: per-household.
8a. **Should members be able to request an invite**, or generate a link that an owner approves? Owner-only invites are clean but put one person on the critical path for something as ordinary as adding a flatmate.
8b. **Is "household" the right word?** It fits couples and flatmates; it fits a scout troop or a shared office kitchen badly. "Space" or "group" is blander and more general. Cheap to change now, painful once it is in the API and the UI copy.
8c. **Guest access to a single list** — sharing one party-planning list with a friend currently means adding them to the whole household. A list-level guest role would solve it and would also be the first crack in the "no per-list permissions" simplification. Defer, but expect to be asked for it.
9. **Do stores need locations?** Attaching a real place (and therefore a maps dependency) would enable "you're near Rema, 4 items waiting" — genuinely useful, but it adds a paid API and location permissions. Deliberately out of v1.
10. **Should stats be visible to all members or only oneself?** §7.7 argues against leaderboards; the safe default is household-level aggregates for everyone, per-person detail for nobody but on the item sheet.

---

## 15. Risks

| Risk | Mitigation |
|---|---|
| Sync bugs are the hardest part and surface late | Build the changes-endpoint pull path *first*; treat WebSocket as an optimisation. Write tests for concurrent tick, offline-then-delete, and reinstall-from-cursor. |
| Apple client secret expiry | Automate regeneration; alert 30 days out. |
| Google-only excludes someone who refuses a Google account | Provider-agnostic identity layer means a second method is additive; decide which one when a real person needs it, not speculatively (§9.1). |
| Auto-linking accounts by email is a takeover vector if done loosely | Enforce every rule in §9: both sides verified, never link relay addresses, never merge two existing users. Write tests for the unverified and relay cases specifically. |
| Home server dies, taking irreplaceable family data with it | Offline-first means everyone keeps a full local copy on their phone; nightly `pg_dump` with an **offsite** copy is the real protection. Test the restore before the family depends on it. |
| Cloudflare WebSocket idle timeout looks like a sync bug | Heartbeat both directions every ~30s; the pull path must work regardless (§12.3). |
| Public repo leaks a secret | Push protection on from commit zero, `.env` gitignored, pull-based deploy so no SSH key or server credential ever needs to exist in repo secrets. |
| A household ends up with no owner (sole owner leaves, deletes their account, or is lost) and can never be invited to or managed again | Block the last owner from leaving or self-deleting until transfer; allow multiple owners; make promotion prominent in the members screen rather than buried. |
| Household membership makes every list query a two-hop authorisation check | Resolve and cache the user's household set once per request; add an explicit test that a member of household A cannot read any list, store or catalog entry in household B. |
| Permanent lists grow unbounded tombstones; a long-offline client resurrects bought items | 90-day tombstone horizon plus an explicit `full_resync_required` response (§6.5), with both paths in the conformance suite. |
| Combobox silently substitutes a near match, so people add the wrong thing and lose trust in the field | Free text always wins: no pre-highlighted row, no autocomplete on blur, explicit create row (§10.4), each asserted in the component suite. |
| Four required fields turn adding an item into a form | Enforce on the record, never in the interaction (§6.4). Measure it: adding a known item must stay one tap, and that should be an explicit E2E assertion. |
| Suggestion row re-ranks under the user's finger, causing mis-taps | Freeze ranking per session (§7.6); test explicitly with rapid successive taps. |
| Stats become a household scoreboard | Aggregate at household level by default; no per-person comparisons anywhere in the UI. |
| Scope outgrows a solo evenings-and-weekends budget and the project stalls before it is usable | R1 cut hard (§13); schema carries stores, catalog and events while the UI does not. Ship something usable, then use it for a month before extending. |
| The rhythm-ranking hypothesis turns out to be wrong after it is built | Ship recency ranking first; validate due-ness against real `item_events` before implementing it (§7.4). |
| Scope creep into recipes/prices | The §3.3 list is a parking lot, not a backlog. |
