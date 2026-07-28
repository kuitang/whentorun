// Shared plumbing for the app-* projects: stub control-plane calls and
// health gating against the production server under test.
//
// Every app project runs its own stub+server pair; the stub's control port
// is derived from the app baseURL (stub port = app port + 200, mirroring
// playwright.config.ts / run-app.sh). Control calls use Node's global
// fetch so they work from beforeAll hooks and specs alike.

/** Stub control-plane base URL for the project serving `baseURL`. */
export function stubBase(baseURL: string | undefined): string {
  if (!baseURL) throw new Error('app specs need a project baseURL');
  const port = Number(new URL(baseURL).port);
  return `http://127.0.0.1:${port + 200}`;
}

async function post(url: string): Promise<void> {
  const res = await fetch(url, { method: 'POST' });
  if (!res.ok) throw new Error(`POST ${url} -> ${res.status}: ${await res.text()}`);
}

export type Source =
  | 'nws-grid'
  | 'nws-forecast'
  | 'nws-alerts'
  | 'airnow'
  | 'openmeteo-weather'
  | 'openmeteo-air'
  | 'all';

export const SOURCES = [
  'nws-grid',
  'nws-forecast',
  'nws-alerts',
  'airnow',
  'openmeteo-weather',
  'openmeteo-air',
] as const;

/** Flip one upstream (or 'all') to up | down | stale on the stub. */
export function control(baseURL: string | undefined, source: Source, state: 'up' | 'down' | 'stale') {
  return post(`${stubBase(baseURL)}/control/${source}/${state}`);
}

/** Select the stub scenario: plain recording or injected severe storm. */
export function setScenario(baseURL: string | undefined, name: 'live' | 'storm') {
  return post(`${stubBase(baseURL)}/control/scenario/${name}`);
}

/** Everything up, plain recording. */
export async function resetStub(baseURL: string | undefined): Promise<void> {
  await setScenario(baseURL, 'live');
  await control(baseURL, 'all', 'up');
}

/**
 * Poll /healthz until every source reports available and not stale.
 * run-app.sh compresses cache TTLs (E2E_CACHE_TTL=1s,8s) so recovery after
 * resetStub is a one-to-two-second affair; the deadline is generous.
 * Playwright's webServer poll only checks for HTTP 200, so specs must gate
 * on this before asserting fully-populated pages.
 */
export async function waitAllFresh(baseURL: string | undefined, timeoutMs = 30_000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  let last = '';
  while (Date.now() < deadline) {
    try {
      const res = await fetch(`${baseURL}/healthz`);
      if (res.ok) {
        const body = (await res.json()) as {
          sources: Record<string, { available: boolean; stale: boolean }>;
        };
        const bad = SOURCES.filter((s) => !body.sources[s]?.available || body.sources[s]?.stale);
        if (bad.length === 0) return;
        last = `not fresh: ${bad.join(', ')}`;
      } else {
        last = `healthz ${res.status}`;
      }
    } catch (err) {
      last = String(err);
    }
    await new Promise((r) => setTimeout(r, 250));
  }
  throw new Error(`waitAllFresh(${baseURL}) timed out after ${timeoutMs}ms (${last})`);
}
