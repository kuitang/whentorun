# whentorun.com — NYC Running Conditions MVP Plan

## Context

Kui wants a public, unauthenticated web app answering "when should I run?" for NYC runners by showing the real meteorological metrics (WBGT, dew point, UV, AQI, wind, precipitation, alerts) with official categorical interpretations — explicitly **no composite run score**. Best windows are chosen by transparent lexicographic ranking with safety vetoes. Full product rationale: `PRODUCT_BRIEF.md`.

Decisions confirmed in interview:
- **Backend: Go** (stdlib-first: `net/http`, `html/template`, `embed`; zero non-stdlib deps, except possibly `golang.org/x/sync/singleflight`), **HTMX** (vendored) for interactivity, no React.
- **Deploy: Fly.io**, region `ewr`.
- **Data sources: all three** — NWS `api.weather.gov`, EPA **AirNow** (key from Kui's account, stored only as Fly secret / env var; credentials never committed), **Open-Meteo** (keyless) as supplement/fallback.
- **Units: °F primary with °C toggle** (cookie).
- **Scope: 5 fixed NYC running paths** (per Kui: merge the two Brooklyn entries): Central Park, Hudson River Greenway, East River, Brooklyn (Prospect Park/waterfront), Van Cortlandt Park. Browser geolocation picks the nearest; static route-context notes (shade/wind exposure/flood-prone segments) per path. *Correction to surface: live NWS lookups show all candidate points fall in **distinct** grid cells (the "shared cell" premise was wrong); re-adding Prospect Park as its own path later is trivial.*
- **AQI: worst nearby AirNow monitor** (max across pollutants/sites — protective framing); **48-hour horizon** (no extension).
- **Domain: whentorun.com** — confirmed available via Verisign RDAP (double-checked); Kui registers it; nothing depends on it until the last phase. (25 alternate available names recorded in scratchpad.)
- **Design system: Merritt** — extracted from Kui's Claude Design artifact (decoded to scratchpad `merritt_artifact/`, incl. the actual woff2 fonts). Editorial memo aesthetic: Spectral serif + Archivo tracked sans labels; ink `#16191B`, steel `#5C8FA6` (large/accent only per AA note; `#426F86` for steel text), ice tint `#D6E3E9`, bg `#F6F7F7`, dark `#0B0D0F/#14181B/#1A2025`; 2px masthead rule, hairline dividers, 2px radii, breathing-hairline loading (no spinners). Merritt principle "Weather is a phrase, never a score" matches the brief.
- **Repo: create `kuitang/whentorun`** on GitHub at implementation start; push this workspace repo (brief already committed); develop on a feature branch, commit+push at each phase boundary.
- **Process: 4 design mockups for Kui's feedback BEFORE building**; Playwright autonomous testing with screenshot + video evidence; interview Kui throughout.

Deferred: **merritt repo clone** (tailnet review-server pattern) — add_repo succeeded but the git proxy denies credentials (401/403); retry during implementation, non-blocking (Fly URLs + Playwright artifacts cover review).

## Verified API facts (live-checked 2026-07-28)

- **NWS gridpoints** (hardcode; no runtime `/points`): central-park OKX/34,45 · hudson-greenway OKX/33,43 · east-river OKX/34,43 · brooklyn OKX/34,41 (or 35,40) · van-cortlandt OKX/36,51. Alert zones dedupe to 3 (NYZ072/NYZ075/NYZ073). `GET /gridpoints/OKX/{x},{y}` includes **`wetBulbGlobeTemperature` (°C) out to ~7.5 days** — covers the whole 48 h window (computed WBGT is fallback-only). Values are run-length encoded (`validTime: "…/PT3H"`) and need interval expansion. Also used: temperature, dewpoint, windSpeed/windGust, probabilityOfPrecipitation, skyCover, windChill, apparentTemperature, weather (freezing rain/thunder detection), probabilityOfThunder, iceAccumulation, hazards. **No UV in NWS grid** → UV from Open-Meteo. Alerts: `GET /alerts/active?zone={zone}`; NWS requires an identifying `User-Agent`.
- **AirNow**: legacy `/aq/observation/latLong/*` endpoints **retire 2026-09-30** — build against the new services: `/aq/observation/current/ziplatlong/` and `/aq/forecast/current/` (`latitude/longitude/distance=25&API_KEY=`). Per-pollutant records; display AQI = max, keep dominant pollutant ("AQI 54 — Moderate (PM2.5)"). One NYC fetch serves all paths. 500 req/hr limit (we use ~12). Exact new-endpoint params need one 15-min live check once the key exists (docs behind login) — keep URL paths in config.
- **Open-Meteo** (keyless): weather API hourly `uv_index`, `shortwave_radiation`, direct/diffuse, temp/RH/wind — sole UV source + WBGT-fallback inputs; air-quality API hourly `us_aqi` (CAMS) 5 days — hourly-AQI forecast source (AirNow forecast is daily-granularity) and no-key fallback, labeled "modeled (CAMS)" vs AirNow "monitored".
- **WBGT fallback formula**: Kong & Huber 2024 (*GeoHealth* 8(10)) zero-iteration analytic Liljegren — within 1 °C of full model in 99% of cases, ~150 lines of Go, inputs all from Open-Meteo. Cite in `internal/wbgt/doc.go`; computed values always labeled "estimated".

## Architecture

```
cmd/server/main.go       wiring, graceful shutdown; -dev flag reads templates from disk
internal/domain/         core types: Hour, Metric, SourceTag{Origin, FetchedAt, Stale}, Alert; paths.go (5 paths:
                         slug, name, lat/lon, gridpoint, alert zone, route notes, clothing table)
internal/nws/            client.go (gridpoint+alerts, User-Agent, retry-once), grid.go (run-length→hourly expansion)
internal/airnow/         new-style ziplatlong + forecast clients, URL paths in config
internal/openmeteo/      weather + air-quality clients
internal/wbgt/           Kong–Huber analytic Liljegren + doc.go citation; tests vs published reference values
internal/categories/     tables-as-data + boundary tests: WBGT flags <80/80–85/85–88/88–90/≥90 °F (wording verified
                         against NWS SGF during impl); dew point <55/55–60/60–65/65–70/70–75/≥75; EPA UV; EPA AQI;
                         NWS wind-chill formula + runner bands
internal/rank/           veto.go (named-reason vetoes), lex.go (warm/cold comparators, coarse bucketing),
                         windows.go (morning 5–9 / midday 11–14 / evening 16–20 ET, today+tomorrow; span extension;
                         explanation names the first differing key), season.go (apparent-temp branch)
internal/merge/          field→source fallback (table below) + staleness tagging — single source of truth
internal/cache/          TTL cache + singleflight; background refresher goroutines per source×key (~17 keys);
                         handlers only read cache — never per-visitor upstream calls
internal/web/            server, routes, render funcs, haversine geo, golden/*.html
web/templates/           base + fragments/{current,hourly,windows,alerts,explain,pathnav}.html (embed.FS)
web/static/              css/tokens.css, js/{htmx.min.js,geo.js,units.js}, fonts/*.woff2 (from decoded artifact)
web/mockups/             Phase-1 static variants
e2e/                     Playwright + stub/stubserver.go (fixture upstreams); test-artifacts/ committed evidence
Makefile, Dockerfile (distroless), fly.toml
```

**Source priority** — WBGT: NWS grid → Kong–Huber computed ("estimated") → "—". Temp/dewpoint/wind/gust/PoP/sky/ice: NWS → Open-Meteo. UV: Open-Meteo only. AQI current: AirNow (max pollutant) → Open-Meteo ("modeled"). AQI hourly: Open-Meteo anchored to AirNow daily category. Alerts: NWS only; if alert feed is down show "alert feed unavailable" banner — never a false all-clear.

**TTLs** (serve-stale-with-label, then explicit "unavailable"; page never 500s for one source): NWS grid 10 min/3 h · alerts 2 min/15 min · AirNow 30 min/3 h · Open-Meteo 30–60 min/6 h.

**Vetoes** (each yields a named reason in UI): warning-class alerts (Tornado/Severe T-storm/Flash Flood/Excessive Heat/Ice Storm/Winter Storm/Blizzard); probabilityOfThunder ≥ 30% or thunder in weather layer; AQI ≥ 201; WBGT ≥ 90 °F; freezing rain/sleet/ice accumulation; wind chill < −10 °F. *(30%/201/90 are proposed defaults — flag to Kui at review.)*

## HTTP/HTMX

`GET /` (path-cookie redirect, else Central Park + "Use my location" affordance — no auto-prompt) · `GET /p/{slug}` full SSR page (works without JS) · fragments `/fragment/{current,hourly,windows,alerts}` (HTMX polling 120 s for current/alerts; day tabs hx-push-url) · `POST /prefs/units`, `POST /prefs/path` (cookies; HX-Refresh/HX-Redirect) · `GET /api/nearest?lat&lon` (haversine over 5 points) · `GET /healthz` (per-source freshness JSON, Fly checks) · static hash-named, `immutable`.

**Mobile-first (per Kui: almost everyone on phones).** Design at 390 px first; desktop is the adaptation. `<meta name="viewport" content="width=device-width, initial-scale=1">` (no `maximum-scale` — don't disable pinch-zoom for accessibility). iOS no-zoom minimums: every `input`/`select`/`button` ≥ **16px font-size** (iOS Safari auto-zooms focused controls below 16px); tap targets ≥ 44×44 pt (path nav, unit toggle, day tabs, `<details>` summaries); body text ≥ 16px, data-table mono may go to 13–14px but never inside focusable controls. Hourly table on narrow screens: sticky first (time) column with horizontal scroll inside its own container — the page itself never scrolls horizontally. `100dvh` not `100vh`; safe-area-inset padding for the masthead; hover states always paired with active/touch states. Mockups are built and screenshotted 390 px-first; desktop 1280 is the secondary rendering. Playwright E2E runs the full suite in a mobile viewport (iPhone 15 emulation incl. touch) plus one desktop pass.

**Theming (per Kui).** Default follows `prefers-color-scheme`; idiomatic three-state override toggle (System / Light / Dark) persisted in a `theme` cookie via `POST /prefs/theme`. Server-rendered so no flash: handler reads the cookie and stamps `<html data-theme="light|dark">` (absent = system). CSS: tokens defined on `:root`, dark values under both `@media (prefers-color-scheme: dark)` (when no `data-theme`) and `[data-theme="dark"]`; `color-scheme: light dark` declared so form controls/scrollbars follow. The d-nocturne mockup doubles as the dark-token proof; whichever direction wins ships with both themes. Playwright asserts all three toggle states in both OS-scheme emulations.

**Above the fold (per Kui): current conditions AND forecast both visible without scrolling on a phone.** The first 390×~700 px viewport must contain: compact masthead, alert strip (when active), the current panel *compressed* (WBGT phrase + a one-line mono strip of dew point/UV/AQI/wind — not a tall card), the best-window line for the next relevant window, and the top of the hourly forecast (at least the next 6–8 hours visible or an hourly mini-ribbon). Density is achieved Merritt-style — tight leading, hairline dividers, tracked overlines, words not gauges — never by shrinking below the 16px control minimum. Every mockup variant is judged against this fold test in the 390 px screenshot; explainers, route context, clothing, attribution live below the fold.

Page structure (Merritt): masthead (tracked label + Spectral-light wordmark, 2px ink rule, path nav + unit toggle) → alert banner → compressed current panel (ice-tint, big Spectral-light WBGT + phrase, one-line mono metric strip) → next-best-window line → 48 h hourly table (words for categories; color only as subtle left-border tier ticks; remaining windows summarized above it) → `<details>` explainers per metric (measures/omits/why/authority/measured-vs-forecast-vs-estimated, driven by live SourceTags) → route-context italic aside → one clothing line → attribution footer with fetched-at times.

## Schedule — DAG, maximum parallelism (commit+push at every node completion)

The mockup review gate blocks ONLY the production UI. Backend proceeds immediately and in parallel.

```
S  Scaffold (repo kuitang/whentorun, go.mod, Makefile, tokens.css, fonts)
   ├──────────────┬──────────────────────────┐
M  Mockups        B1 Data clients            D1 Domain logic
   (4 variants,      (nws / airnow* /           (categories, wbgt,
   390px fold        openmeteo, fixtures,       veto, lex, windows
   test, shots,      recorder)                  — pure, no I/O;
   artifact                                     table-driven tests)
   gallery)       B2 Cache + merge ← B1      (D1 ∥ B1 — disjoint pkgs)
   │              B3 debug dump + live       DEP  Dockerfile, fly.toml,
KUI REVIEW GATE      verification ← B2            fly app + AIRNOW secret,
   │                                              /healthz stub deploy ← S
UI Production templates (chosen direction), HTMX, geo, themes,
   units, golden tests            ← gate + B2 + D1
E2E Playwright vs stub upstreams, mobile-first + desktop,
   3 theme states, degraded modes, video   ← UI
GO-LIVE full app on Fly + live smoke spec  ← E2E + DEP
DOM whentorun.com cert + DNS               ← GO-LIVE + Kui registering domain
```

Concurrency plan: after S, run M, B1, D1 (and DEP once S lands) as parallel workstreams — B/D/DEP are pure backend and don't wait for mockup feedback; if the review gate is slow, backend finishes to a deployed `/healthz`-only app with verified live data underneath. Independent workstreams may run as parallel subagents in separate worktrees (merge at phase boundaries) when that genuinely speeds things up.

Node details:
- **M**: **a-broadsheet** (editorial prose-led) / **b-almanac** (dense table-first) / **c-ledger** (timeline ribbon + hairline sparklines) / **d-nocturne** (dark athletic) in `web/mockups/`, shared tokens, realistic muggy-July data incl. an active-alert veto state; each judged 390 px-first against the above-the-fold test (current + forecast visible), then 1280 px; Playwright screenshots to `test-artifacts/mockups/` + private Artifact gallery. **Gate: Kui picks a direction (or hybrid).**
- **B1–B3**: clients + cache + merge + `internal/tools/record`; AirNow new-endpoint 15-min live verification (needs key from Kui's account). *Verify: unit tests green; debug dump shows merged live 48 h for Central Park.*
- **D1**: pure functions, no I/O — fully parallel with B1. *Verify: full table suite incl. brief's canary (18 °C WBGT + AQI 160 must flag, never average) and a recorded Flash Flood fixture producing a veto.*
- **DEP**: Fly app exists early (distroless Dockerfile, `min_machines_running=1`, `AIRNOW_API_KEY` secret, `/healthz`) so later nodes deploy into a proven pipeline.
- **UI/E2E/GO-LIVE/DOM**: as in the sections above; E2E evidence committed to `test-artifacts/e2e/` and shared with Kui before cutover.

## Verification summary

`go test ./...` (categories boundaries, comparator, vetoes, window edge cases incl. DST, WBGT vs published values ±0.5 °C, grid expansion, clients vs fixtures, golden HTML) · `make e2e` (stubbed, with screenshots+video) · one `@live` smoke spec · deployed `/healthz` all-fresh · mockup and E2E evidence delivered to Kui at each gate.

## Flagged for Kui (at mockup review, not blocking start)

1. Veto thresholds: thunder ≥ 30%, AQI ≥ 201, WBGT ≥ 90 °F top band.
2. Grid-cell correction above — keep 5 paths or restore Prospect Park as #6.
3. East River (Stuyvesant Cove) & Brooklyn (Brooklyn Bridge Park) representative coordinates.
