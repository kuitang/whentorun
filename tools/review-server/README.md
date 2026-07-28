# Whentorun review server

Tailnet-only mockup review server, same pattern as
`~/git/merrit/tools/review-server`. Zero npm dependencies — plain
`node:http` / `node:fs`.

Serves:

- `/` — mobile-first index generated per request (no caching): globs
  `web/mockups/*.html` (title-cased labels, newest-mtime first, clustered by
  filename prefix so e.g. `ledger-v2-*` iterations sit together) and
  `test-artifacts/mockups/*.png` (linked, with inline lazy thumbnails),
  each with a per-file "updated" timestamp. Reload to see the latest.
- `/mockups/<variant>.html`, `/static/...` — `web/` is the document root, so
  mockup pages' absolute `/static/...` references resolve exactly as the Go
  app would serve them.
- `/artifacts/...` — `test-artifacts/` (screenshots land in
  `test-artifacts/mockups/`).

## Port (registered in `~/git/stockski/CONTRACTS.md` §6)

| port | service |
|------|---------|
| 8488 | whentorun review server (this) |

Check (and update) that CONTRACTS.md §6 registry before binding anything new
on this box.

## Start / restart

```sh
nohup setsid ~/.nvm/versions/node/v24.16.0/bin/node \
  ~/git/whentorun/tools/review-server/server.mjs \
  > /tmp/whentorun-review.log 2>&1 &
```

To restart: kill the old one first —

```sh
pkill -f 'whentorun/tools/review-server/server.mjs'
```

`PORT` and `HOST` env vars override the defaults (PORT 8488, HOST = tailnet
IPv4 from `tailscale ip -4`).

## Why tailnet IP only

Binds the machine's Tailscale IPv4 only — never `0.0.0.0` (would expose
unreleased mockups on the LAN) and never `127.0.0.1` (unreachable from the
phone). Reachability is exactly "devices on the tailnet", so no auth layer
is needed.
