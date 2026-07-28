import { test, expect } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

const mockupDir = path.resolve(__dirname, '../../web/mockups');
const VARIANTS = fs.existsSync(mockupDir)
  ? fs.readdirSync(mockupDir).filter((f) => f.endsWith('.html')).map((f) => f.replace(/\.html$/, ''))
  : [];

for (const variant of VARIANTS) {
  test(`mockup ${variant} renders and screenshots`, async ({ page }, testInfo) => {
    await page.goto(`/mockups/${variant}.html`);
    await page.waitForLoadState('networkidle');
    await expect(page.locator('body')).toBeVisible();

    const dir = `../test-artifacts/mockups`;
    // Fold view: exactly what fits in the first viewport.
    await page.screenshot({
      path: `${dir}/${variant}-${testInfo.project.name}-fold.png`,
    });
    // Full page.
    await page.screenshot({
      path: `${dir}/${variant}-${testInfo.project.name}-full.png`,
      fullPage: true,
    });

    // The page itself must never scroll horizontally.
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow, 'no horizontal page scroll').toBeLessThanOrEqual(0);
  });
}
