import { test, expect } from '@playwright/test';
import * as fs from 'fs';
import * as path from 'path';

// Mechanical readability/interactability audit over every mockup page.
// The graphical UI-check agent drives this suite, then reviews the
// screenshots it produces for the qualitative half (overlap, hierarchy,
// whether scrub affordances actually read as scrollable).

const mockupDir = path.resolve(__dirname, '../../web/mockups');
// Audit only the go-forward ledger line; the four v1 exploration mockups
// predate the all-serif / My-Location / WBGT-expander requirements.
const variants = fs.existsSync(mockupDir)
  ? fs
      .readdirSync(mockupDir)
      .filter((f) => f.startsWith('ledger-') && f.endsWith('.html'))
      .map((f) => f.replace(/\.html$/, ''))
  : [];

for (const variant of variants) {
  test.describe(`ui-check ${variant}`, () => {
    test.beforeEach(async ({ page }) => {
      await page.goto(`/mockups/${variant}.html`);
      await page.waitForLoadState('networkidle');
    });

    test('page never scrolls horizontally', async ({ page }) => {
      const overflow = await page.evaluate(
        () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
      );
      expect(overflow).toBeLessThanOrEqual(0);
    });

    test('interactive controls meet 16px font and 44px tap floors', async ({ page }) => {
      const failures = await page.evaluate(() => {
        const bad: string[] = [];
        const controls = document.querySelectorAll<HTMLElement>(
          'a, button, summary, input, select, label[for], [role="button"], [role="tab"]',
        );
        for (const el of controls) {
          const r = el.getBoundingClientRect();
          if (r.width === 0 || r.height === 0) continue; // hidden
          const fs = parseFloat(getComputedStyle(el).fontSize);
          // Route slider is exempt from the 16px floor (Kui: smaller/narrower
          // there because the strip is long) — links aren't iOS zoom targets;
          // form fields always keep 16px. Floor of 13px still applies.
          const inRouteSlider = !!el.closest('[data-route-slider], .route-nav, .path-nav');
          const isFormField = /^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName);
          const floor = inRouteSlider && !isFormField ? 13 : 16;
          if (fs < floor) bad.push(`${el.tagName}.${el.className} font-size ${fs}px < ${floor}px ("${(el.textContent || '').trim().slice(0, 30)}")`);
          // Effective tap target may be padded by the parent; measure the element box.
          if (r.height < 44 && r.width < 44) {
            // Allow inline text links inside prose paragraphs (WCAG inline exception).
            const inProse = el.tagName === 'A' && el.closest('p, li, figcaption, aside');
            if (!inProse) bad.push(`${el.tagName}.${el.className} tap target ${Math.round(r.width)}x${Math.round(r.height)} < 44px ("${(el.textContent || '').trim().slice(0, 30)}")`);
          }
        }
        return bad;
      });
      expect(failures, failures.join('\n')).toEqual([]);
    });

    test('body text stays readable (>=13px everywhere, >=16px for prose)', async ({ page }) => {
      const failures = await page.evaluate(() => {
        const bad: string[] = [];
        for (const el of document.querySelectorAll<HTMLElement>('p, li, td, th, dd, dt, figcaption, span, div')) {
          if (!el.childNodes.length) continue;
          const hasText = Array.from(el.childNodes).some(
            (n) => n.nodeType === Node.TEXT_NODE && (n.textContent || '').trim().length > 0,
          );
          if (!hasText) continue;
          const r = el.getBoundingClientRect();
          if (r.width === 0 || r.height === 0) continue;
          const fs = parseFloat(getComputedStyle(el).fontSize);
          if (fs < 13) bad.push(`${el.tagName}.${el.className} ${fs}px ("${(el.textContent || '').trim().slice(0, 30)}")`);
          if (el.tagName === 'P' && fs < 16) bad.push(`prose P ${fs}px < 16px ("${(el.textContent || '').trim().slice(0, 30)}")`);
        }
        return bad;
      });
      expect(failures, failures.join('\n')).toEqual([]);
    });

    test('no sans-serif or monospace computed families', async ({ page }) => {
      const failures = await page.evaluate(() => {
        const bad = new Set<string>();
        for (const el of document.querySelectorAll<HTMLElement>('body, body *')) {
          const fam = getComputedStyle(el).fontFamily.toLowerCase();
          if (/archivo|system-ui|sans-serif|monospace|menlo|consolas/.test(fam)) {
            bad.add(`${el.tagName}.${el.className}: ${fam}`);
          }
        }
        return [...bad].slice(0, 20);
      });
      expect(failures, failures.join('\n')).toEqual([]);
    });

    test('details expanders open and close', async ({ page }) => {
      const summaries = page.locator('details > summary');
      const n = await summaries.count();
      expect(n, 'page should have explainer expanders').toBeGreaterThan(0);
      const first = summaries.first();
      await first.click();
      await expect(page.locator('details[open]').first()).toBeVisible();
      await first.click();
    });

    test('hero WBGT expander exists above the fold with jump link', async ({ page }) => {
      const result = await page.evaluate(() => {
        const details = Array.from(document.querySelectorAll('details'));
        const hero = details.find(
          (d) => /wbgt|wet.?bulb/i.test(d.textContent || '') && d.getBoundingClientRect().top < 700,
        );
        if (!hero) return { found: false, jump: false };
        const jump = !!hero.querySelector('a[href^="#"]');
        return { found: true, jump };
      });
      expect(result.found, 'WBGT expander above the fold').toBe(true);
      expect(result.jump, 'jump link to full explainers inside the expander').toBe(true);
    });

    test('tables fold to viewport width — no sideways scroll on mobile', async ({ page }, testInfo) => {
      test.skip(testInfo.project.name !== 'mobile', 'mobile-only requirement');
      const offenders = await page.evaluate(() => {
        const bad: string[] = [];
        for (const t of document.querySelectorAll<HTMLElement>('table')) {
          const scroller = t.closest<HTMLElement>('[class]') || t;
          // The table itself must fit; a wrapping scroll container counts as failure.
          let el: HTMLElement | null = t;
          while (el && el !== document.body) {
            const cs = getComputedStyle(el);
            if (/(auto|scroll)/.test(cs.overflowX) && el.scrollWidth > el.clientWidth + 4) {
              bad.push(`table inside scrolling .${el.className.toString().slice(0, 40)}`);
              break;
            }
            el = el.parentElement;
          }
          if (t.scrollWidth > document.documentElement.clientWidth + 4) {
            bad.push(`table wider than viewport (${t.scrollWidth}px)`);
          }
        }
        return bad;
      });
      expect(offenders, offenders.join('\n')).toEqual([]);
    });

    test('horizontal scrollers are scrollable and carry a scrub affordance', async ({ page }) => {
      const report = await page.evaluate(() => {
        const out: { cls: string; scrollable: boolean; hasAffordance: boolean }[] = [];
        for (const el of document.querySelectorAll<HTMLElement>('body *')) {
          const cs = getComputedStyle(el);
          if (!/(auto|scroll)/.test(cs.overflowX)) continue;
          if (el.scrollWidth <= el.clientWidth + 4) continue;
          // Affordance heuristics: a mask/gradient edge fade, a visible
          // scrollbar style, an aria hint, or a dedicated hint element.
          const hasAffordance =
            cs.maskImage !== 'none' ||
            (cs as any).webkitMaskImage !== 'none' ||
            !!el.closest('[data-scroll-hint]') ||
            !!el.parentElement?.querySelector('.scroll-hint, .scrub-hint, [class*="fade"]') ||
            !!el.getAttribute('aria-label')?.match(/scroll|scrub/i);
          out.push({ cls: el.className.toString().slice(0, 60), scrollable: true, hasAffordance });
        }
        return out;
      });
      for (const r of report) {
        expect(r.hasAffordance, `scroller .${r.cls} needs a visible scrub affordance`).toBe(true);
      }
      // scrollers must respond to programmatic scroll (interactability)
      const moved = await page.evaluate(() => {
        const el = Array.from(document.querySelectorAll<HTMLElement>('body *')).find((e) => {
          const cs = getComputedStyle(e);
          return /(auto|scroll)/.test(cs.overflowX) && e.scrollWidth > e.clientWidth + 4;
        });
        if (!el) return true;
        el.scrollLeft = 40;
        return el.scrollLeft > 0;
      });
      expect(moved).toBe(true);
    });

    test('WBGT hero and air temperature are distinct, temp second-biggest below', async ({ page }) => {
      const r = await page.evaluate(() => {
        const cand = Array.from(document.querySelectorAll<HTMLElement>('[data-metric], .hero, .current, header ~ * [class*="current"], main *'))
          .filter((el) => el.getBoundingClientRect().top < 700);
        const sized = cand
          .filter((el) => /^\d{1,3}°?/.test((el.textContent || '').trim()) && el.children.length <= 2)
          .map((el) => ({
            fs: parseFloat(getComputedStyle(el).fontSize),
            top: el.getBoundingClientRect().top,
            ctx: (el.closest('[data-metric]')?.getAttribute('data-metric') || el.parentElement?.textContent || '').toLowerCase().slice(0, 120),
          }))
          .sort((a, b) => b.fs - a.fs);
        if (sized.length < 2) return { ok: false, why: 'fewer than two sized figures above fold' };
        const [first, second] = sized;
        const firstIsWbgt = /wbgt|heat stress/.test(first.ctx);
        const secondIsTemp = /air temp|temperature/.test(second.ctx) && !/wbgt/.test(second.ctx.slice(0, 40));
        const below = second.top > first.top;
        const distinctSizes = first.fs > second.fs;
        return { ok: firstIsWbgt && secondIsTemp && below && distinctSizes, why: JSON.stringify({ first, second }) };
      });
      expect(r.ok, `WBGT biggest + labeled, air temp second-biggest directly below: ${r.why}`).toBe(true);
    });

    test('relative humidity appears alongside dew point', async ({ page }) => {
      const ok = await page.evaluate(() => {
        const rh = document.querySelector('[data-metric="rh"]');
        const dew = document.querySelector('[data-metric="dew"], [data-metric="dewpoint"]');
        if (!rh || !dew) return false;
        return /\d{1,3}\s?%/.test(rh.textContent || '');
      });
      expect(ok, 'data-metric="rh" with a % value, next to dew point').toBe(true);
    });

    test('sunrise and sunset marked in ledger graphic and table', async ({ page }) => {
      const r = await page.evaluate(() => {
        const sunGlyphs = document.querySelectorAll('[data-glyph="sunrise"], [data-glyph="sunset"]');
        // At least one pair in the graphic; table rows/dividers marked too.
        const tableMarks = document.querySelectorAll(
          'table [data-sun], table .sunrise, table .sunset, [data-sun-row]',
        );
        return { glyphs: sunGlyphs.length, tableMarks: tableMarks.length };
      });
      expect(r.glyphs, 'sunrise/sunset glyphs in the ledger graphic').toBeGreaterThanOrEqual(2);
      expect(r.tableMarks, 'sunrise/sunset markers in the table view').toBeGreaterThanOrEqual(2);
    });

    test('compass rose shows wind direction in the current panel', async ({ page }) => {
      const ok = await page.evaluate(() => {
        const rose = document.querySelector('svg[data-glyph="compass"], [data-glyph="compass"] svg');
        if (!rose) return false;
        const r = rose.getBoundingClientRect();
        return r.width > 0 && r.top < 900; // present and in/near the current panel
      });
      expect(ok, 'engraved compass rose (data-glyph="compass") near current wind').toBe(true);
    });

    test('default selected route is My Location', async ({ page }) => {
      const ok = await page.evaluate(() => {
        const nav = document.querySelector('nav') || document.body;
        const active = nav.querySelector(
          '[aria-current], [data-active], .active, .selected, [aria-selected="true"]',
        );
        return /my location/i.test(active?.textContent || '');
      });
      expect(ok, 'path nav should default-select "My Location"').toBe(true);
    });
  });
}
