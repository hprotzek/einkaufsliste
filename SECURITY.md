# Security policy

## Reporting a vulnerability

Please report security issues privately through GitHub's
[private vulnerability reporting](https://github.com/hprotzek/einkaufsliste/security/advisories/new)
rather than opening a public issue.

This is a self-hosted family project maintained by one person in their spare
time, so expect a reply in days rather than hours. There is no bounty.

## Scope

The interesting surface is small and worth naming:

- **Authentication and token handling** — ID-token verification, refresh-token
  rotation and family revocation on reuse.
- **Authorisation** — every list access is checked through the two-hop path
  user → household member → list. Cross-household data exposure is the bug
  class that matters most here.
- **Anything that lets one household see another's data.**

Out of scope: denial of service against a self-hosted instance, and the
deployment configuration of anybody else's copy.

## What this project deliberately does not do

Understanding these avoids reports for things that are working as intended:

- **Cloudflare terminates TLS** at its edge, so Cloudflare can in principle see
  traffic. That is a known, accepted trade for not exposing a home IP
  (spec §4).
- **Conflict resolution is silent.** Last write wins, by design; the UI does
  not surface it.
- **No analytics of any kind**, so there is no telemetry endpoint to find.

## Handling of secrets

`.env` is gitignored and only `.env.example` — dummy values — is committed.
Push protection and secret scanning are enabled on this repository. Any secret
that reaches a commit is treated as compromised and rotated, not force-pushed
away: assume anything committed to a public repository is permanently public.
