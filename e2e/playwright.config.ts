import { defineConfig, devices } from '@playwright/test';

// Mockup screenshots are served with web/ as the document root so that the
// /static/... URLs inside tokens.css resolve.
export default defineConfig({
  testDir: './tests',
  outputDir: './test-results',
  timeout: 30_000,
  use: {
    baseURL: 'http://127.0.0.1:8877',
  },
  webServer: {
    command: 'python3 -m http.server 8877 --directory ../web',
    url: 'http://127.0.0.1:8877/static/css/tokens.css',
    reuseExistingServer: true,
  },
  projects: [
    {
      name: 'mobile',
      use: { ...devices['iPhone 15'] },
    },
    {
      name: 'desktop',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 900 } },
    },
  ],
});
