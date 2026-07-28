# E2E evidence — Playwright vs. stub upstreams

Committed evidence for the E2E node of PLAN.md: the production server
(`cmd/server` + `internal/web`) exercised end-to-end over the fixture stub
(`e2e/stub`), plus the mockup-gallery audit suite.

## Suite scope

187 tests in 4 spec files across 5 projects, one Playwright run
(`npx playwright test` from `e2e/`):

| Project      | Viewport                      | Specs                            | Tests |
|--------------|-------------------------------|----------------------------------|-------|
| mobile       | iPhone 15 emulation (touch)   | mockups.spec.ts + ui-check.spec.ts | 72  |
| desktop      | Desktop Chrome 1280×900       | mockups.spec.ts + ui-check.spec.ts | 72  |
| app-mobile   | iPhone 15 emulation (touch)   | app.spec.ts                      | 21    |
| app-desktop  | Desktop Chrome 1280×900       | app.spec.ts                      | 21    |
| app-evidence | iPhone 15, video always on    | app-video.spec.ts                | 1     |

Coverage of the production app (app.spec.ts):

- **SSR completeness** — no horizontal page scroll; full `data-metric` set
  with real values; WBGT hero hierarchy; chart y-axis pinned / single pill /
  date lock / precip marks; scrub arrows adapt to scroll position; hourly
  table folds on mobile; banned copy absent; 16px font + 44px tap floors;
  default route chip is My Location; WBGT expander above the fold.
- **Preferences** — theme: three POST states × both OS color-schemes with
  server-stamped `data-theme` and background luminance checks; units F→C
  converts hero and chart axis; path switch cookie + 303/302/HX-Redirect
  flows, unknown path 400.
- **Fragments** — `/fragment/current|alerts` HTMX-compatible HTML with
  correct hx-get/hx-trigger/hx-swap wiring; page polling attributes; 404 on
  unknown fragments.
- **Nearest API** — `/api/nearest` closest-path JSON, 400 on bad/missing
  coords.
- **Degraded modes** (stub control plane, compressed TTLs) — AirNow down →
  modeled CAMS AQI tag, never blank; NWS grid quiet → "(stale)" label, page
  stays 200; alerts down → "Alert feed unavailable / do not read this as
  all-clear" banner; everything down → honest em dashes + footer
  "unavailable", then full recovery.
- **Storm scenario** — injected Severe Thunderstorm Warning: banner, vetoed
  hours naming the warning, struck windows, storm narrative.

## Results (two back-to-back full runs)

Both runs identical: **180 passed, 7 skipped, 0 failed** (~54 s each).

The 7 skips are all intentional viewport/variant guards:

- 4× ui-check "tables fold to viewport width" — mobile-only requirement,
  skipped in the desktop project (one per ledger mockup variant).
- 2× ui-check "compass rose shows wind direction" — v3 dropped the static
  rose per Kui's feedback (per-hour vanes only); skipped for
  ledger-v3-synthesis in both projects.
- 1× app.spec "hourly table folds to viewport width on mobile" — skipped in
  app-desktop.

`go test ./...`, `go vet ./...` and `gofmt -l .` are clean.

## Determinism / flake-proofing

- Each app project owns an isolated stub+server pair (stub port = app port
  + 200) so degraded-mode control flips never bleed across projects.
- `run-app.sh` compresses cache TTLs via `E2E_CACHE_TTL=1s,8s` so entries
  age fresh → stale → unavailable in seconds.
- Specs gate on deterministic signals only: `/healthz` polling
  (`waitAllFresh`), stub `/control/status`, and `expect(...).toPass()`
  polling of page content — no bare sleeps in app.spec.ts.
- The stub time-shifts a recorded muggy-July NYC dataset (storms included)
  so the baseline page already exercises vetoes; the storm scenario layers a
  Severe Thunderstorm Warning on top. It accepts any AirNow key — the real
  key is never used in E2E.

## Files

- `evidence-walkthrough.webm` — always-recorded mobile walkthrough: load,
  WBGT expander, 48 h chart scrub, theme system→light→dark, units F→C,
  path switch to Brooklyn (from the app-evidence project).
- `app-mobile-fold.png` — iPhone 15 first viewport: masthead, flood-watch
  alert, compressed current panel, next-window line (the fold test).
- `app-mobile-full.png` — full mobile page.
- `app-dark-fold.png` — same fold with the dark theme cookie set.
- `app-desktop-full.png` — full desktop (1280 px) page.

## Rerun

```sh
cd e2e
npx playwright test              # full suite: mockups + app, mobile + desktop
npx playwright test --project=app-mobile   # one project
APP_PORT=8899 ./run-app.sh       # stub+server pair by hand (stub on :9099)
```

Playwright boots everything itself (python http.server for mockups, one
`run-app.sh` stub+server pair per app project) and reuses already-running
servers. Screenshots here were captured against an `APP_PORT=8899` pair.
