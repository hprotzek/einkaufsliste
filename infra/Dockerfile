# syntax=docker/dockerfile:1

# --- Build -------------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies first: a code-only change reuses this layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO off produces a static binary, which is what allows the runtime stage to
# be scratch at all. -trimpath drops build paths, -s -w drops the symbol table
# and DWARF.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath \
      -ldflags="-s -w" \
      -o /out/api ./cmd/api

# --- Runtime -----------------------------------------------------------------
# Nothing but the binary and CA certs (spec §12.6). No shell, no package
# manager, nothing to exploit and nothing to patch.
FROM scratch

# Needed for outbound HTTPS — Google's OIDC discovery and JWKS at M1.
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt

COPY --from=build /out/api /api

# scratch has no /etc/passwd, so the user must be numeric. 65534 is nobody.
USER 65534:65534

EXPOSE 8080

# The binary probes itself: there is no curl, wget or shell in here for
# Docker's HEALTHCHECK to call. See cmd/api/healthcheck.go.
HEALTHCHECK --interval=10s --timeout=3s --start-period=15s --retries=5 \
  CMD ["/api", "-healthcheck"]

ENTRYPOINT ["/api"]
