#!/usr/bin/env bash
#
# Install Podman, configure it for rootless use, and bring the einkaufsliste
# stack up locally: postgres, api and caddy. No Redis (spec §5.2).
#
# Rootless on purpose. A shopping list for one household has no business
# running containers as root, and rootless Podman needs no daemon — the
# containers are ordinary child processes of your user.
#
# Idempotent: safe to re-run. Every step checks before it changes anything.
#
#   ./infra/setup-podman.sh              install, build locally, start
#   ./infra/setup-podman.sh --pull       use the published GHCR image instead
#   ./infra/setup-podman.sh --no-install just configure and start
#   ./infra/setup-podman.sh --no-start   set up but do not start the stack
#   ./infra/setup-podman.sh --help

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
readonly REPO_ROOT
readonly COMPOSE_FILE="${REPO_ROOT}/infra/docker-compose.yml"
readonly OVERRIDE_FILE="${REPO_ROOT}/infra/compose.ghcr.yml"
readonly ENV_FILE="${REPO_ROOT}/.env"
readonly ENV_EXAMPLE="${REPO_ROOT}/.env.example"
readonly GHCR_IMAGE="ghcr.io/hprotzek/einkaufsliste-api:latest"

do_install=1
do_start=1
use_ghcr=0

# Filled in as we go.
pkg_manager=""
compose_cmd=()
http_port="8080"

# --- output ------------------------------------------------------------------

log()  { printf '\n\033[1;34m==>\033[0m \033[1m%s\033[0m\n' "$*"; }
info() { printf '    %s\n' "$*"; }
warn() { printf '\033[1;33m    warning:\033[0m %s\n' "$*" >&2; }
die()  { printf '\n\033[1;31merror:\033[0m %s\n\n' "$*" >&2; exit 1; }

usage() {
  # Print the header comment as the help text, stopping at the first line that
  # is not a comment — so this cannot drift as the header grows.
  awk 'NR > 2 { if (!/^#/) exit; sub(/^# ?/, ""); print }' "${BASH_SOURCE[0]}"
  exit 0
}

# --- arguments ---------------------------------------------------------------

parse_args() {
  while [ $# -gt 0 ]; do
    case "$1" in
      --pull)       use_ghcr=1 ;;
      --no-install) do_install=0 ;;
      --no-start)   do_start=0 ;;
      -h|--help)    usage ;;
      *)            die "unknown option: $1 (try --help)" ;;
    esac
    shift
  done
}

# --- preflight ---------------------------------------------------------------

preflight() {
  log "Checking this machine"

  [ "$(uname -s)" = "Linux" ] || die "this script is for Linux; on macOS use 'podman machine init'."

  if [ "$(id -u)" -eq 0 ]; then
    warn "running as root. Podman works, but rootless is the point of using it here."
    warn "re-run as your normal user unless you have a reason not to."
  fi

  # Rootless Podman needs cgroups v2 for resource control and clean teardown.
  # Every distro released in the last several years defaults to it.
  local cgroup_type
  cgroup_type="$(stat -fc %T /sys/fs/cgroup 2>/dev/null || echo unknown)"
  if [ "$cgroup_type" != "cgroup2fs" ]; then
    warn "cgroups v2 not detected (found: ${cgroup_type})."
    warn "rootless Podman may misbehave. On older distros, boot with systemd.unified_cgroup_hierarchy=1."
  else
    info "cgroups v2: yes"
  fi

  if command -v getenforce >/dev/null 2>&1 && [ "$(getenforce)" = "Enforcing" ]; then
    info "SELinux: enforcing — the compose file labels its bind mounts with ,z for this"
  fi

  detect_package_manager
  info "distribution family: ${pkg_manager}"
}

detect_package_manager() {
  if   command -v dnf     >/dev/null 2>&1; then pkg_manager="dnf"
  elif command -v apt-get >/dev/null 2>&1; then pkg_manager="apt"
  elif command -v pacman  >/dev/null 2>&1; then pkg_manager="pacman"
  elif command -v zypper  >/dev/null 2>&1; then pkg_manager="zypper"
  else
    pkg_manager="unknown"
    if [ "$do_install" -eq 1 ]; then
      die "no supported package manager found (dnf, apt, pacman, zypper).
Install podman and podman-compose yourself, then re-run with --no-install."
    fi
  fi
}

# sudo only where it is actually needed, and only if we are not already root.
as_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    die "need root for: $* — install sudo or run this script as root."
  fi
}

# --- install -----------------------------------------------------------------

install_podman() {
  if [ "$do_install" -eq 0 ]; then
    log "Skipping installation (--no-install)"
    command -v podman >/dev/null 2>&1 || die "podman is not installed and --no-install was given."
    return
  fi

  if command -v podman >/dev/null 2>&1 && have_compose_provider; then
    log "Podman and a compose provider are already installed"
    info "podman $(podman --version | awk '{print $3}')"
    return
  fi

  log "Installing Podman and a compose provider"

  case "$pkg_manager" in
    dnf)
      as_root dnf install -y podman podman-compose
      ;;
    apt)
      as_root apt-get update
      # podman-compose is packaged on Debian 12+ and Ubuntu 22.04+. Where it
      # is missing, podman alone still leaves us the docker-compose path.
      as_root apt-get install -y podman || die "could not install podman"
      as_root apt-get install -y podman-compose \
        || warn "podman-compose not packaged here; will look for another compose provider"
      ;;
    pacman)
      as_root pacman -Sy --needed --noconfirm podman podman-compose
      ;;
    zypper)
      as_root zypper --non-interactive install podman podman-compose
      ;;
    *)
      die "unsupported package manager: ${pkg_manager}"
      ;;
  esac

  command -v podman >/dev/null 2>&1 || die "podman still not on PATH after installing."
  info "podman $(podman --version | awk '{print $3}')"
}

have_compose_provider() {
  podman compose version >/dev/null 2>&1 \
    || command -v podman-compose >/dev/null 2>&1 \
    || command -v docker-compose >/dev/null 2>&1
}

# --- rootless configuration --------------------------------------------------

configure_rootless() {
  log "Configuring rootless Podman"

  if [ "$(id -u)" -eq 0 ]; then
    info "running as root; skipping rootless setup"
    return
  fi

  # Rootless containers map container UIDs onto a range delegated to your
  # user. Without it, only UID 0 maps and the API's USER 65534 cannot start.
  local user
  user="$(id -un)"
  if grep -q "^${user}:" /etc/subuid 2>/dev/null && grep -q "^${user}:" /etc/subgid 2>/dev/null; then
    info "subuid/subgid ranges: present"
  else
    warn "no subuid/subgid range for ${user}; the API container will fail to start as UID 65534."
    info "granting one now"
    as_root usermod --add-subuids 100000-165535 --add-subgids 100000-165535 "$user" \
      || die "could not add subuid/subgid ranges. Add them manually to /etc/subuid and /etc/subgid."
    podman system migrate >/dev/null 2>&1 || true
  fi

  # Without lingering, systemd tears down your user's containers on logout —
  # surprising on a headless box you only ever SSH into.
  if command -v loginctl >/dev/null 2>&1; then
    if [ "$(loginctl show-user "$user" --property=Linger --value 2>/dev/null || echo no)" = "yes" ]; then
      info "lingering: enabled (containers survive logout)"
    else
      info "enabling lingering so containers survive logout"
      as_root loginctl enable-linger "$user" || warn "could not enable lingering; containers will stop when you log out."
    fi
  fi

  # Only the docker-compose provider needs the socket. Harmless otherwise.
  if command -v systemctl >/dev/null 2>&1; then
    if systemctl --user enable --now podman.socket >/dev/null 2>&1; then
      info "user podman.socket: enabled"
    else
      info "user podman.socket: unavailable (fine unless you use docker-compose)"
    fi
  fi
}

detect_compose() {
  log "Selecting a compose provider"

  if podman compose version >/dev/null 2>&1; then
    compose_cmd=(podman compose)
  elif command -v podman-compose >/dev/null 2>&1; then
    compose_cmd=(podman-compose)
  elif command -v docker-compose >/dev/null 2>&1; then
    compose_cmd=(docker-compose)
    info "using docker-compose against the rootless Podman socket"
    export DOCKER_HOST="unix://${XDG_RUNTIME_DIR:-/run/user/$(id -u)}/podman/podman.sock"
  else
    die "no compose provider found. Install podman-compose or docker-compose, then re-run with --no-install."
  fi

  info "using: ${compose_cmd[*]}"
}

# --- configuration -----------------------------------------------------------

generate_password() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 24
  else
    # head reads first, so nothing downstream can SIGPIPE it.
    od -An -N24 -tx1 /dev/urandom | tr -d ' \n'
  fi
}

ensure_env() {
  log "Preparing .env"

  if [ ! -f "$ENV_FILE" ]; then
    [ -f "$ENV_EXAMPLE" ] || die "neither .env nor .env.example exists; are you running this inside the repository?"
    cp "$ENV_EXAMPLE" "$ENV_FILE"
    info "created .env from .env.example"
  fi

  # Replace the placeholder rather than any password you have already chosen.
  if grep -q '^POSTGRES_PASSWORD=change-me-before-running$' "$ENV_FILE"; then
    local password
    password="$(generate_password)"
    # A literal replacement, so a generated password containing / or & is safe.
    awk -v pw="$password" \
      '/^POSTGRES_PASSWORD=change-me-before-running$/ { print "POSTGRES_PASSWORD=" pw; next } { print }' \
      "$ENV_FILE" > "${ENV_FILE}.tmp" && mv "${ENV_FILE}.tmp" "$ENV_FILE"
    chmod 600 "$ENV_FILE"
    info "generated a random POSTGRES_PASSWORD"
  else
    info "POSTGRES_PASSWORD already set; leaving it alone"
  fi

  http_port="$(awk -F= '/^HTTP_PORT=/ { print $2 }' "$ENV_FILE" | tail -1)"
  [ -n "$http_port" ] || http_port="8080"
  info "the stack will listen on http://localhost:${http_port}"
}

build_web() {
  log "Building the web app"

  if [ ! -d "${REPO_ROOT}/web" ]; then
    warn "no web/ directory; skipping"
    return
  fi

  if ! command -v npm >/dev/null 2>&1; then
    warn "npm not found, so web/dist will not be built."
    warn "the API still works; Caddy will answer 404 at / until you run 'make web'."
    return
  fi

  ( cd "${REPO_ROOT}/web" && npm ci --no-fund --no-audit && npm run build )
  info "built web/dist"
}

write_ghcr_override() {
  cat > "$OVERRIDE_FILE" <<EOF
# Generated by infra/setup-podman.sh --pull.
# Runs the published image instead of building from source.
services:
  api:
    image: ${GHCR_IMAGE}
EOF
  info "wrote $(basename "$OVERRIDE_FILE")"
}

compose() {
  "${compose_cmd[@]}" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" "$@"
}

compose_with_overrides() {
  if [ "$use_ghcr" -eq 1 ]; then
    "${compose_cmd[@]}" --env-file "$ENV_FILE" -f "$COMPOSE_FILE" -f "$OVERRIDE_FILE" "$@"
  else
    compose "$@"
  fi
}

# --- images and start --------------------------------------------------------

prepare_images() {
  log "Preparing images"

  if [ "$use_ghcr" -eq 1 ]; then
    write_ghcr_override
    info "pulling ${GHCR_IMAGE}"
    podman pull "$GHCR_IMAGE" || die "could not pull ${GHCR_IMAGE}.
If the package is still private, make it public under
Repository -> Packages -> einkaufsliste-api -> Package settings,
or drop --pull and build from source instead."
  else
    info "building the API image from source"
    compose build api || die "image build failed"
  fi

  info "pulling postgres and caddy"
  podman pull docker.io/library/postgres:16-alpine
  podman pull docker.io/library/caddy:2-alpine
}

start_stack() {
  log "Starting the stack"
  compose_with_overrides up -d || die "the stack failed to start; see the output above"
}

# Poll rather than trusting --wait: support for waiting on health varies
# between compose providers, and a real HTTP response is better evidence.
wait_for_health() {
  log "Waiting for the API to answer"

  local url="http://localhost:${http_port}/healthz"
  local attempt
  for attempt in $(seq 1 60); do
    if curl --fail --silent --show-error "$url" >/dev/null 2>&1; then
      info "healthy after ~$((attempt * 2))s"
      return 0
    fi
    sleep 2
  done

  warn "no healthy response from ${url} after two minutes"
  printf '\n--- container status ---\n'
  compose_with_overrides ps || true
  printf '\n--- recent logs ---\n'
  compose_with_overrides logs --tail 50 || true
  die "the stack did not come up. The logs above usually say why."
}

summary() {
  log "Ready"
  cat <<EOF

    App and API:   http://localhost:${http_port}
    Health:        http://localhost:${http_port}/healthz

    Everyday commands, from ${REPO_ROOT}:

      ${compose_cmd[*]} --env-file .env -f infra/docker-compose.yml ps
      ${compose_cmd[*]} --env-file .env -f infra/docker-compose.yml logs -f
      ${compose_cmd[*]} --env-file .env -f infra/docker-compose.yml down

    Postgres is not published to the host. Reach it with:

      podman exec -it einkaufsliste-postgres-1 psql -U einkaufsliste

    Next, when you set up the tunnel: point cloudflared at
    localhost:${http_port}. Nothing else needs to be exposed, and no router
    port should be opened.

EOF
}

main() {
  parse_args "$@"

  preflight
  install_podman
  configure_rootless
  detect_compose
  ensure_env
  build_web

  if [ "$do_start" -eq 0 ]; then
    log "Set up, not started (--no-start)"
    info "start it yourself with: ${compose_cmd[*]} --env-file .env -f infra/docker-compose.yml up -d"
    exit 0
  fi

  prepare_images
  start_stack
  wait_for_health
  summary
}

main "$@"
