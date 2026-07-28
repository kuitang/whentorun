import { defineConfig, devices } from '@playwright/test';

// Two server groups share this config:
//
//  - Mockup gallery (projects: mobile, desktop): python http.server with
//    web/ as the document root so the /static/... URLs inside tokens.css
//    resolve. Runs mockups.spec.ts + ui-check.spec.ts.
//
//  - Production app (projects: app-mobile, app-desktop, app-evidence):
//    e2e/run-app.sh boots the fixture stub (e2e/stub) plus the real server
//    with every upstream base URL pointed at the stub and compressed cache
//    TTLs (E2E_CACHE_TTL). Each app project gets its OWN stub+server pair
//    (stub port = app port + 200, see app-helpers.ts) so degraded-mode
//    control flips in one project never bleed into another running
//    concurrently.
export const APP_PORTS: Record<string, number> = {
  'app-mobile': 8891,
  'app-desktop': 8892,
  'app-evidence': 8893,
};

const appBase = (name: string) => `http://127.0.0.1:${APP_PORTS[name]}`;

export default defineConfig({
  testDir: './tests',
  outputDir: './test-results',
  timeout: 30_000,
  use: {
    baseURL: 'http://127.0.0.1:8877',
  },
  webServer: [
    {
      command: 'python3 -m http.server 8877 --directory ../web',
      url: 'http://127.0.0.1:8877/static/css/tokens.css',
      reuseExistingServer: true,
    },
    ...Object.values(APP_PORTS).map((port) => ({
      command: `APP_PORT=${port} ./run-app.sh`,
      url: `http://127.0.0.1:${port}/healthz`,
      reuseExistingServer: true,
      timeout: 120_000,
    })),
  ],
  projects: [
    // Mockup gallery audits (unchanged behavior).
    {
      name: 'mobile',
      testIgnore: /app.*\.spec\.ts/,
      use: { ...devices['iPhone 15'] },
    },
    {
      name: 'desktop',
      testIgnore: /app.*\.spec\.ts/,
      use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 900 } },
    },
    // Production app over the fixture stub.
    {
      name: 'app-mobile',
      testMatch: /app\.spec\.ts/,
      timeout: 60_000,
      use: {
        ...devices['iPhone 15'],
        baseURL: appBase('app-mobile'),
        video: 'retain-on-failure',
      },
    },
    {
      name: 'app-desktop',
      testMatch: /app\.spec\.ts/,
      timeout: 60_000,
      use: {
        ...devices['Desktop Chrome'],
        viewport: { width: 1280, height: 900 },
        baseURL: appBase('app-desktop'),
        video: 'retain-on-failure',
      },
    },
    // Evidence walkthrough: always records; the spec copies the video to
    // test-artifacts/e2e/.
    {
      name: 'app-evidence',
      testMatch: /app-video\.spec\.ts/,
      timeout: 180_000,
      use: {
        ...devices['iPhone 15'],
        baseURL: appBase('app-evidence'),
        video: 'on',
      },
    },
  ],
});
