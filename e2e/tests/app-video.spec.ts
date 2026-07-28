import { test, expect } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';
import { resetStub, waitAllFresh } from './app-helpers';

// Evidence walkthrough: one always-recorded pass over the production page
// (app-evidence project runs with video: 'on'). The recording is copied to
// test-artifacts/e2e/ for review before cutover.

const artifactsDir = path.resolve(__dirname, '../../test-artifacts/e2e');

test('evidence walkthrough: load, expander, chart scrub, theme, units, path', async ({ page, baseURL }) => {
  await resetStub(baseURL);
  await waitAllFresh(baseURL);

  // Load the page and let the fold settle on camera.
  await page.goto('/');
  await expect(page.locator('.wbgt-num')).toHaveText(/\d+/);
  await page.waitForTimeout(900);

  // Open the WBGT expander.
  await page.locator('.wbgt-what summary').click();
  await expect(page.locator('.wbgt-what')).toHaveAttribute('open', '');
  await page.waitForTimeout(900);

  // Scrub the 48h chart: sweep right, then back to now.
  const fig = page.locator('.fig-scrub');
  await fig.scrollIntoViewIfNeeded();
  const max = await fig.evaluate((el) => el.scrollWidth - el.clientWidth);
  for (let i = 1; i <= 6; i++) {
    await fig.evaluate((el, x) => el.scrollTo({ left: x }), Math.round((max * i) / 6));
    await page.waitForTimeout(250);
  }
  await page.waitForTimeout(500);
  await fig.evaluate((el) => el.scrollTo({ left: 0, behavior: 'smooth' }));
  await page.waitForTimeout(900);

  // Theme: system -> light -> dark (server round-trip reloads the page).
  const themeBtn = page.locator('[data-theme-btn]');
  await themeBtn.click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'light', { timeout: 10_000 });
  await page.waitForTimeout(700);
  await page.locator('[data-theme-btn]').click();
  await expect(page.locator('html')).toHaveAttribute('data-theme', 'dark', { timeout: 10_000 });
  await page.waitForTimeout(900);

  // Units: F -> C, hero and chart axis convert.
  await page.locator('[data-unit-toggle]').click();
  await expect(page.locator('.wbgt-line')).toContainText('°C', { timeout: 10_000 });
  await expect(page.locator('.fig-axis .ax-unit')).toHaveText('°C');
  await page.waitForTimeout(900);

  // Switch path via the route slider.
  await page.locator('nav.paths a[href="/p/brooklyn"]').click();
  await expect(page).toHaveURL(/\/p\/brooklyn/);
  await expect(page.locator('.wbgt-num')).toHaveText(/\d+|—/);
  await page.waitForTimeout(900);

  // Close the page so the recording finalizes, then keep a copy under
  // test-artifacts/e2e/.
  const video = page.video();
  await page.close();
  if (video) {
    fs.mkdirSync(artifactsDir, { recursive: true });
    await video.saveAs(path.join(artifactsDir, 'evidence-walkthrough.webm'));
  }
});
