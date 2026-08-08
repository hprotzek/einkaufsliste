# einkaufsliste

A shared shopping list for one household. Self-hosted, not commercial.

Go + chi API, Postgres, React PWA, behind a Cloudflare Tunnel. Offline-first:
the phone in your hand is the source of truth for the UI, and the server
reconciles.

## Status

Early. M0 (foundation) is in progress — the service builds, tests, migrates and
serves `/healthz`. There is no UI and no auth yet. See
[`docs/implementation-plan.md`](docs/implementation-plan.md) for the milestone
order and [`docs/shared-shopping-list-spec.md`](docs/shared-shopping-list-spec.md)
for the requirements, data model and sync protocol.

## Running it

Requires Go (version per `go.mod`) and a Postgres 16 you can reach.

```bash
cp .env.example .env          # then edit DATABASE_URL
make run                      # applies migrations, then serves on :8080
curl localhost:8080/healthz   # {"status":"ok"}
```

`DATABASE_URL` is required and has no fallback: a service that cannot reach its
database should fail at start-up, not on the first request. Migrations are
applied automatically before the listener opens, so there is no separate deploy
step.

## Commands

| Command | Does |
|---|---|
| `make run` | Run the API from source |
| `make build` | Compile to `bin/api` |
| `make test` | `go test -race ./...` |
| `make lint` | golangci-lint at the pinned version |
| `make vet` | `go vet ./...` |
| `make migrate` | Apply pending migrations (goose up) |
| `make migrate-status` | Show which migrations are applied |
| `make migrate-create NAME=x` | Scaffold a new migration |
| `make generate` | Regenerate sqlc queries and both sides of `api/openapi.yaml` |
| `make web` | Build the web app into `web/dist` |
| `make web-dev` | Vite dev server, proxying the API so dev is same-origin too |
| `make web-typecheck` | Typecheck the web app |
| `make up` / `make down` | Start / stop the compose stack |

`make help` lists them all.

## The whole stack

```bash
cp .env.example .env          # then set POSTGRES_PASSWORD
make web                      # build the app Caddy serves
make up                       # postgres + api + caddy, waits for healthy
open http://localhost:8080    # the app, with the API on the same origin
make down
```

Skipping `make web` is not fatal — the stack still starts and the API answers,
but Caddy has nothing to serve and `/` returns 404.

### Podman

`infra/setup-podman.sh` installs Podman, configures it for rootless use and
brings the stack up in one go. It is idempotent, so re-running it is safe.

```bash
./infra/setup-podman.sh              # install, build from source, start
./infra/setup-podman.sh --pull       # run the published GHCR image instead
./infra/setup-podman.sh --no-install # already have podman; just configure and start
./infra/setup-podman.sh --help
```

It handles the things that otherwise cost an evening: rootless `subuid`/`subgid`
ranges, without which the API's `USER 65534` cannot start; lingering, so
containers survive logging out of a headless box; and picking whichever compose
provider you have. On SELinux hosts the bind mounts are already labelled `,z` in
the compose file.

Rootless is the default because a household shopping list has no business
running containers as root, and rootless Podman needs no daemon at all —
the containers are ordinary child processes of your user.

Postgres and the API publish no host ports; only Caddy does. In production even
that is reached over the container network by `cloudflared`, so nothing is
exposed on the host and no router port is opened.

## Container images

Every merge to `main` publishes a multi-architecture image to GHCR:

```
ghcr.io/hprotzek/einkaufsliste-api:latest
ghcr.io/hprotzek/einkaufsliste-api:sha-<commit>
```

`linux/amd64` and `linux/arm64`, so it runs on a normal server or a Pi. The
runtime image is `FROM scratch` — the binary and CA certificates, nothing else,
about 11 MiB, with CI failing the build if it ever exceeds 20 MiB.

Deployment is pull-based: the server runs a timer that pulls `:latest` and
restarts. Nothing pushes into the home network, so there is no SSH key in this
public repository and no inbound path to open.

## How this repository is put together

- **`api/openapi.yaml` is the contract of record.** The chi router is built
  from it, so adding a path without writing its handler is a compile error.
  Change the spec first, run `make generate`, then write code — CI fails if the
  generated Go or TypeScript drifts.
- **Migrations are embedded in the binary** and applied at start-up. The
  production image is `FROM scratch`, so there is no goose binary and no
  filesystem to read `.sql` files from.
- **No ORM.** sqlc generates Go from hand-written SQL.
- **Tests use a real Postgres**, never a mock — `DATABASE_URL` when set,
  otherwise a testcontainer.
- **Business logic lives in `internal/`** and knows nothing about HTTP.
  Handlers translate; they do not decide.

## Contributing

This is a family project rather than one seeking contributors, but see
[CONTRIBUTING.md](CONTRIBUTING.md) if you want to run it yourself or send a
patch. Security reports: [SECURITY.md](SECURITY.md).

## Licence

[MIT](LICENSE).
