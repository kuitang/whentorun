import { test, expect, Page } from '@playwright/test';
import { control, setScenario, resetStub, waitAllFresh } from './app-helpers';

// End-to-end coverage of the production server (cmd/server + internal/web)
// rendered over the fixture stub (e2e/stub). The stub time-shifts a
// recorded muggy-July NYC dataset — storms included — so the baseline page
// already exercises vetoes; the storm scenario layers a Severe
// Thunderstorm Warning on top.
//
// Tests in this file run sequentially in one worker (fullyParallel is
// off), and each app project owns an isolated stub+server pair, so the
// degraded-mode control flips below cannot race other tests.

test.beforeAll(async ({ baseURL }) => {
  await resetStub(baseURL);
  await waitAllFresh(baseURL);
});

// ---------- 1. SSR completeness (ported ui-check assertions) ----------

test.describe('ssr completeness', () => {
  test.beforeEach(async ({ page }) => {
    await page.goto('/');
  });

  test('page never scrolls horizontally', async ({ page }) => {
    const overflow = await page.evaluate(
      () => document.documentElement.scrollWidth - document.documentElement.clientWidth,
    );
    expect(overflow).toBeLessThanOrEqual(0);
  });

  test('full data-metric set present with real values', async ({ page }) => {
    for (const m of ['wbgt', 'temp', 'dew', 'uv', 'aqi', 'rh', 'wind', 'rain']) {
      await expect(page.locator(`[data-metric="${m}"]`).first(), `data-metric=${m}`).toBeVisible();
    }
    await expect(page.locator('.wbgt-num')).toHaveText(/\d+/);
    await expect(page.locator('[data-metric="rh"]')).toContainText(/\d+\s?%/);
    await expect(page.locator('[data-metric="aqi"]')).toContainText(/AQI \d+/);
  });

  test('hero hierarchy: WBGT biggest, air temp and dew side by side below', async ({ page }) => {
    const r = await page.evaluate(() => {
      const wbgt = document.querySelector<HTMLElement>('.wbgt-num')!;
      const temp = document.querySelector<HTMLElement>('[data-metric="temp"]')!.closest<HTMLElement>('.pair-cell')!;
      const dew = document.querySelector<HTMLElement>('[data-metric="dew"]')!.closest<HTMLElement>('.pair-cell')!;
      const pairNum = temp.querySelector<HTMLElement>('.pair-num')!;
      const w = wbgt.getBoundingClientRect();
      const t = temp.getBoundingClientRect();
      const d = dew.getBoundingClientRect();
      return {
        wbgtFs: parseFloat(getComputedStyle(wbgt).fontSize),
        pairFs: parseFloat(getComputedStyle(pairNum).fontSize),
        below: t.top > w.top,
        sameRow: Math.abs(t.top - d.top) < 12,
      };
    });
    expect(r.wbgtFs, 'WBGT numeral larger than the temp numeral').toBeGreaterThan(r.pairFs);
    expect(r.below, 'temp/dew pair sits below the WBGT hero').toBe(true);
    expect(r.sameRow, 'temp and dew share a row').toBe(true);
  });

  test('chart y-axis pinned, single pill, date lock, precip marks', async ({ page }) => {
    const r = await page.evaluate(() => {
      const axis = document.querySelector<HTMLElement>('[data-y-axis]');
      const lock = document.querySelector<HTMLElement>('[data-date-lock]');
      const pinned = (el: HTMLElement | null) =>
        !!el && ['sticky', 'absolute', 'fixed'].includes(getComputedStyle(el).position);
      const pills = Array.from(document.querySelectorAll<HTMLElement>('[data-y-axis]')).filter(
        (el) => el.getBoundingClientRect().width > 0,
      ).length;
      const precip = document.querySelector('[data-precip-axis]');
      return {
        axisPinned: pinned(axis),
        lockPinned: pinned(lock),
        lockText: lock?.textContent?.trim() || '',
        pills,
        precipMarks: !!precip && /50/.test(precip.textContent || '') && /100/.test(precip.textContent || ''),
      };
    });
    expect(r.axisPinned, 'y-axis (data-y-axis) pinned via sticky/absolute').toBe(true);
    expect(r.lockPinned, 'date chip (data-date-lock) pinned inside the chart scroller').toBe(true);
    expect(r.lockText, 'date chip carries a date').toMatch(/[A-Z]{3}/);
    expect(r.pills, 'exactly one visible y-axis pill').toBe(1);
    expect(r.precipMarks, 'precip axis (data-precip-axis) marks 50 and 100').toBe(true);
  });

  test('chart scrub arrows adapt to scroll position', async ({ page }) => {
    const r = await page.evaluate(async () => {
      const scroller = document.querySelector<HTMLElement>('.fig-scrub');
      if (!scroller || scroller.scrollWidth <= scroller.clientWidth + 40) {
        return { ok: false, why: 'chart scroller missing or not scrollable' };
      }
      const wrap = scroller.parentElement!;
      const vis = (sel: string) => {
        const el = wrap.querySelector<HTMLElement>(sel);
        if (!el) return null;
        if (el.hidden) return false;
        const cs = getComputedStyle(el);
        return cs.display !== 'none' && cs.visibility !== 'hidden' && parseFloat(cs.opacity || '1') > 0.05;
      };
      scroller.scrollLeft = 0;
      await new Promise((r2) => setTimeout(r2, 150));
      const atStart = { left: vis('[data-scrub-left]'), right: vis('[data-scrub-right]') };
      scroller.scrollLeft = scroller.scrollWidth;
      await new Promise((r2) => setTimeout(r2, 150));
      const atEnd = { left: vis('[data-scrub-left]'), right: vis('[data-scrub-right]') };
      scroller.scrollLeft = 0;
      return { ok: true, atStart, atEnd };
    });
    expect(r.ok, r.why || '').toBe(true);
    expect(r.atStart?.right, 'right arrow visible at start').toBe(true);
    expect(r.atStart?.left, 'left arrow hidden at start').toBe(false);
    expect(r.atEnd?.left, 'left arrow visible at right extreme').toBe(true);
    expect(r.atEnd?.right, 'right arrow hidden at right extreme').toBe(false);
  });

  test('hourly table folds to viewport width on mobile', async ({ page }, testInfo) => {
    test.skip(testInfo.project.name !== 'app-mobile', 'mobile-only requirement');
    const offenders = await page.evaluate(() => {
      const bad: string[] = [];
      for (const t of document.querySelectorAll<HTMLElement>('table')) {
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

  test('banned copy absent', async ({ page }) => {
    const banned = await page.evaluate(() => {
      const text = document.body.innerText.toLowerCase();
      return [
        'no composite score',
        'no single number',
        'not a stop order',
        'not a veto',
        "band's ink",
        'one clock',
        'fig. 1',
        'fig 1',
        'nyc running conditions',
        'new york running conditions',
        'vetoed',
        'changes ink',
        'scrub',
      ].filter((s) => text.includes(s));
    });
    expect(banned, `banned copy present: ${banned.join(' | ')}`).toEqual([]);
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
        // Route slider is exempt from the 16px floor (Kui: smaller there) —
        // links aren't iOS zoom targets; form fields keep 16px. 13px floor
        // still applies everywhere.
        const inRouteSlider = !!el.closest('[data-route-slider], .route-nav, .path-nav');
        const isFormField = /^(INPUT|SELECT|TEXTAREA)$/.test(el.tagName);
        const floor = inRouteSlider && !isFormField ? 13 : 16;
        if (fs < floor) bad.push(`${el.tagName}.${el.className} font-size ${fs}px < ${floor}px ("${(el.textContent || '').trim().slice(0, 30)}")`);
        if (r.height < 44 && r.width < 44) {
          const inProse = el.tagName === 'A' && el.closest('p, li, figcaption, aside');
          if (!inProse) bad.push(`${el.tagName}.${el.className} tap target ${Math.round(r.width)}x${Math.round(r.height)} < 44px ("${(el.textContent || '').trim().slice(0, 30)}")`);
        }
      }
      return bad;
    });
    expect(failures, failures.join('\n')).toEqual([]);
  });

  test('default selected route chip is My Location', async ({ page }) => {
    const active = page.locator('nav.paths [aria-current]');
    await expect(active).toHaveCount(1);
    await expect(active).toContainText(/my location/i);
    await expect(active).toHaveAttribute('data-geo', '');
  });

  test('WBGT expander above the fold with jump link to explainers', async ({ page }) => {
    const r = await page.evaluate(() => {
      const d = document.querySelector<HTMLElement>('.wbgt-what');
      if (!d) return { found: false, jump: false, aboveFold: false };
      return {
        found: true,
        aboveFold: d.getBoundingClientRect().top < 700,
        jump: !!d.querySelector('a[href="#explainers"]'),
      };
    });
    expect(r.found, 'WBGT expander (.wbgt-what)').toBe(true);
    expect(r.aboveFold, 'expander above the fold').toBe(true);
    expect(r.jump, 'jump link to #explainers').toBe(true);
    const summary = page.locator('.wbgt-what summary');
    await summary.click();
    await expect(page.locator('.wbgt-what')).toHaveAttribute('open', '');
    await summary.click();
  });
});

// ---------- 2. Preferences ----------

test.describe('preferences', () => {
  test('theme: three POST states x both OS color-schemes', async ({ page }) => {
    for (const theme of ['system', 'light', 'dark'] as const) {
      for (const scheme of ['light', 'dark'] as const) {
        const res = await page.request.post('/prefs/theme', { form: { theme } });
        expect(res.ok(), `POST /prefs/theme theme=${theme}`).toBe(true);
        await page.emulateMedia({ colorScheme: scheme });
        await page.goto('/');

        const attr = await page.locator('html').getAttribute('data-theme');
        expect(attr, `data-theme for theme=${theme}`).toBe(theme === 'system' ? null : theme);

        const bg = await page.evaluate(() => getComputedStyle(document.body).backgroundColor);
        const m = bg.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
        expect(m, `parseable background (${bg})`).toBeTruthy();
        const lum = (Number(m![1]) + Number(m![2]) + Number(m![3])) / 3;
        const wantDark = theme === 'dark' || (theme === 'system' && scheme === 'dark');
        expect(
          lum < 128,
          `theme=${theme} scheme=${scheme}: background ${bg} should be ${wantDark ? 'dark' : 'light'}`,
        ).toBe(wantDark);
      }
    }
  });

  test('units: F to C changes the hero and the chart axis', async ({ page }) => {
    await page.goto('/');
    const heroF = (await page.locator('.wbgt-num').innerText()).trim();
    await expect(page.locator('.wbgt-line')).toContainText('°F');
    await expect(page.locator('.fig-axis .ax-unit')).toHaveText('°F');
    const axisF = await page.locator('.fig-axis').innerText();

    const res = await page.request.post('/prefs/units', { form: { units: 'C' } });
    expect(res.ok()).toBe(true);
    await page.reload();

    await expect(page.locator('.wbgt-line')).toContainText('°C');
    await expect(page.locator('.fig-axis .ax-unit')).toHaveText('°C');
    const heroC = (await page.locator('.wbgt-num').innerText()).trim();
    expect(heroC, 'hero numeral converts with the unit').not.toBe(heroF);
    const axisC = await page.locator('.fig-axis').innerText();
    expect(axisC, 'y-axis labels convert with the unit').not.toBe(axisF);
  });

  test('path switch sets the cookie and redirects', async ({ page }) => {
    // Plain form post: 303 back to the chosen path.
    const res = await page.request.post('/prefs/path', {
      form: { path: 'brooklyn' },
      maxRedirects: 0,
    });
    expect(res.status()).toBe(303);
    expect(res.headers()['location']).toBe('/p/brooklyn');

    // The cookie now steers the index.
    const idx = await page.request.get('/', { maxRedirects: 0 });
    expect(idx.status()).toBe(302);
    expect(idx.headers()['location']).toBe('/p/brooklyn');

    await page.goto('/');
    await expect(page).toHaveURL(/\/p\/brooklyn$/);
    const active = page.locator('nav.paths [aria-current]');
    await expect(active).toHaveCount(1);
    await expect(active).toContainText('Brooklyn');

    // HTMX post answers 204 + HX-Redirect instead.
    const hx = await page.request.post('/prefs/path', {
      form: { path: 'central-park' },
      headers: { 'HX-Request': 'true' },
    });
    expect(hx.status()).toBe(204);
    expect(hx.headers()['hx-redirect']).toBe('/p/central-park');

    // Unknown path rejected.
    const bad = await page.request.post('/prefs/path', { form: { path: 'nowhere' } });
    expect(bad.status()).toBe(400);
  });
});

// ---------- 3. Fragments (HTMX polling) ----------

test.describe('fragments', () => {
  test('/fragment/current and /fragment/alerts return htmx-compatible HTML', async ({ page }) => {
    const cur = await page.request.get('/fragment/current?path=central-park');
    expect(cur.status()).toBe(200);
    expect(cur.headers()['content-type']).toContain('text/html');
    const curBody = await cur.text();
    expect(curBody).toContain('hx-get="/fragment/current?path=central-park"');
    expect(curBody).toContain('hx-trigger="every 120s"');
    expect(curBody).toContain('hx-swap="outerHTML"');
    expect(curBody).toContain('data-metric="wbgt"');

    const al = await page.request.get('/fragment/alerts?path=central-park');
    expect(al.status()).toBe(200);
    expect(al.headers()['content-type']).toContain('text/html');
    const alBody = await al.text();
    expect(alBody).toContain('id="alert-live"');
    expect(alBody).toContain('hx-get="/fragment/alerts?path=central-park"');
    expect(alBody).toContain('hx-trigger="every 120s"');

    const bogus = await page.request.get('/fragment/nope');
    expect(bogus.status()).toBe(404);
  });

  test('the page wires polling via hx-get/hx-trigger', async ({ page }) => {
    await page.goto('/');
    await expect(page.locator('#current')).toHaveAttribute('hx-get', /\/fragment\/current\?path=/);
    await expect(page.locator('#current')).toHaveAttribute('hx-trigger', 'every 120s');
    await expect(page.locator('#alert-live')).toHaveAttribute('hx-get', /\/fragment\/alerts\?path=/);
    await expect(page.locator('#alert-live')).toHaveAttribute('hx-trigger', 'every 120s');
    await expect(page.locator('#windows')).toHaveAttribute('hx-get', /\/fragment\/windows\?path=/);
    await expect(page.locator('#windows')).toHaveAttribute('hx-trigger', 'every 600s');
  });
});

// ---------- 4. Nearest-path API ----------

test.describe('nearest api', () => {
  test('returns the closest path as JSON', async ({ page }) => {
    const cp = await page.request.get('/api/nearest?lat=40.7829&lon=-73.9654');
    expect(cp.status()).toBe(200);
    expect(cp.headers()['content-type']).toContain('application/json');
    const cpBody = await cp.json();
    expect(cpBody.slug).toBe('central-park');
    expect(cpBody.name).toBeTruthy();
    expect(cpBody.distance_km).toBeLessThan(1);

    const bk = await page.request.get('/api/nearest?lat=40.6605&lon=-73.9690');
    expect((await bk.json()).slug).toBe('brooklyn');

    const bad = await page.request.get('/api/nearest?lat=abc');
    expect(bad.status()).toBe(400);
    const missing = await page.request.get('/api/nearest');
    expect(missing.status()).toBe(400);
  });
});

// ---------- 5. Degraded modes (stub control plane) ----------

// run-app.sh compresses cache TTLs to 1s fresh / 8s serve-stale, so a
// flipped source ages through stale (label) into unavailable (fallback or
// honest dash) within ~10s. Each test restores a fully-fresh baseline
// first and the describe block leaves the stub clean.

async function pollPage(page: Page, timeoutMs: number, check: (body: string, status: number) => void) {
  await expect(async () => {
    const res = await page.request.get('/');
    check(await res.text(), res.status());
  }).toPass({ timeout: timeoutMs, intervals: [400] });
}

test.describe('degraded modes', () => {
  test.beforeEach(async ({ baseURL }) => {
    await resetStub(baseURL);
    await waitAllFresh(baseURL);
  });

  test.afterAll(async ({ baseURL }) => {
    await resetStub(baseURL);
  });

  test('airnow down: AQI falls back to the modeled CAMS tag, never blank', async ({ page, baseURL }) => {
    await control(baseURL, 'airnow', 'down');
    await pollPage(page, 25_000, (body, status) => {
      expect(status).toBe(200);
      expect(body).toContain('data-aqi-modeled');
    });
    await page.goto('/');
    const aqi = page.locator('[data-metric="aqi"]');
    await expect(aqi).toContainText(/AQI \d+/); // a real number, not an em dash
    await expect(aqi.locator('[data-aqi-modeled]')).toBeVisible();
    await expect(aqi.locator('[data-aqi-modeled]')).toContainText(/modeled, CAMS/);
  });

  test('nws grid gone quiet: stale label appears, page still 200', async ({ page, baseURL }) => {
    await control(baseURL, 'nws-grid', 'down');
    // The last good fetch ages past the 1s fresh TTL; the footer and
    // explainers label it "(stale)" until the 8s serve-stale cutoff.
    await pollPage(page, 15_000, (body, status) => {
      expect(status).toBe(200);
      expect(body).toMatch(/\(stale\)/);
    });
  });

  test('alerts down: unavailable banner, never an implicit all-clear', async ({ page, baseURL }) => {
    await control(baseURL, 'nws-alerts', 'down');
    await pollPage(page, 25_000, (body, status) => {
      expect(status).toBe(200);
      expect(body).toContain('Alert feed unavailable');
      expect(body).toContain('do not read this as all-clear');
    });
    await page.goto('/');
    await expect(page.locator('#alert-live .alert').first()).toContainText(/alert feed unavailable/i);
    // The fragment endpoint carries the same banner for the poller.
    const frag = await page.request.get('/fragment/alerts?path=central-park');
    expect(await frag.text()).toContain('Alert feed unavailable');
  });

  test('every upstream down: honest em dashes and freshness, then recovery', async ({ page, baseURL }) => {
    await control(baseURL, 'all', 'down');
    await pollPage(page, 25_000, (body, status) => {
      expect(status).toBe(200);
      expect(body).toContain('Alert feed unavailable');
      expect(body).toMatch(/wbgt-num[^>]*>—/);
    });
    await page.goto('/');
    await expect(page.locator('.wbgt-num')).toContainText('—');
    await expect(page.locator('footer.foot')).toContainText('unavailable');

    // Restore: the 1s refresh cadence recovers the page within seconds.
    await control(baseURL, 'all', 'up');
    await waitAllFresh(baseURL);
    await page.goto('/');
    await expect(page.locator('.wbgt-num')).toHaveText(/\d+/);
    await expect(page.locator('footer.foot')).not.toContainText('unavailable');
    await expect(page.locator('#alert-live')).not.toContainText('Alert feed unavailable');
  });
});

// ---------- 6. Storm scenario ----------

test.describe('storm scenario', () => {
  test.afterAll(async ({ baseURL }) => {
    await resetStub(baseURL);
  });

  test('severe thunderstorm: warning banner, vetoed hours and windows, storm narrative', async ({ page, baseURL }) => {
    await resetStub(baseURL);
    await waitAllFresh(baseURL);
    await setScenario(baseURL, 'storm');
    await pollPage(page, 15_000, (body, status) => {
      expect(status).toBe(200);
      expect(body).toContain('Severe Thunderstorm Warning');
    });
    await page.goto('/');

    // Warning banner in the alert strip.
    await expect(page.locator('#alert-live .alert').filter({ hasText: 'Severe Thunderstorm Warning' })).toBeVisible();

    // The injected 6h thunder block vetoes upcoming hours with the warning
    // named in the row.
    const vetoRows = page.locator('.hours tr.veto');
    expect(await vetoRows.count()).toBeGreaterThanOrEqual(1);
    await expect(page.locator('.hours .w-word.vt').filter({ hasText: 'Severe Thunderstorm Warning' }).first()).toBeAttached();

    // Vetoed windows strike their ranges (the muggy-July recording plus
    // the injected block keeps the evening window struck).
    expect(await page.locator('#windows s').count()).toBeGreaterThanOrEqual(1);
    await expect(page.locator('#windows .sub')).toContainText(/is out/);

    // Narrative names the storms.
    await expect(page.locator('#windows [data-narrative]')).toContainText(/storm|thunder|lightning/i);
  });
});
