// Smoke tests for Boopmaster 3000 (apps/boopmaster).
//
// The page is served locally and every third-party request is intercepted:
// htmx comes from the pinned devDependency, the metrics API returns canned HTML,
// and the PostHog loader is stubbed so no analytics leave the test. Any request
// to another host is aborted, and any console error or uncaught exception fails
// the test.
import { createRequire } from 'node:module';
import { test as base, expect } from '@playwright/test';

const require = createRequire(import.meta.url);
const HTMX_PATH = require.resolve('htmx.org/dist/htmx.min.js');
const METRICS_API = 'https://metrics-api.jbirdkerr.net';

// Known issue, also present in production: htmx reads its HX-* response headers on
// the cross-origin metrics requests, and the metrics worker does not list them in
// Access-Control-Expose-Headers, so Chrome logs one of these per header per poll.
// Fixing it belongs in apps/metrics-worker; remove this allowance once it ships.
const KNOWN_CONSOLE_ERRORS = [/^Refused to get unsafe header "HX-[A-Za-z-]+"$/];

const test = base.extend({
  page: async ({ page, baseURL }, use) => {
    const problems = [];
    page.on('pageerror', (err) => problems.push(`pageerror: ${err.message}`));
    page.on('console', (msg) => {
      if (msg.type() !== 'error') return;
      if (KNOWN_CONSOLE_ERRORS.some((re) => re.test(msg.text()))) return;
      problems.push(`console.error: ${msg.text()}`);
    });

    const cors = { 'Access-Control-Allow-Origin': baseURL };
    await page.route('**/*', async (route) => {
      const url = new URL(route.request().url());
      if (url.origin === baseURL) return route.continue();
      if (url.hostname === 'unpkg.com' && url.pathname.startsWith('/htmx.org')) {
        return route.fulfill({ path: HTMX_PATH, contentType: 'application/javascript' });
      }
      if (url.origin === METRICS_API) {
        if (url.pathname === '/metrics') {
          return route.fulfill({
            headers: cors,
            contentType: 'text/html',
            body: '<div id="fake-engagement">Total boops: 1234</div>',
          });
        }
        if (url.pathname === '/health-metrics') {
          return route.fulfill({
            headers: cors,
            contentType: 'text/html',
            body: '<div id="fake-health">Requests: 99</div>',
          });
        }
        if (url.pathname.startsWith('/static/')) {
          // PostHog's loader script; an empty script leaves the queueing stub in place.
          return route.fulfill({ contentType: 'application/javascript', body: '/* posthog stub */' });
        }
        return route.fulfill({ status: 204, headers: cors });
      }
      problems.push(`unexpected external request: ${url.href}`);
      return route.abort();
    });

    await use(page);
    expect(problems, 'console errors, page errors, or unexpected requests').toEqual([]);
  },
});

async function openApp(page) {
  await page.goto('/');
  await page.waitForFunction(() => window.HenryBeard && window.htmx && window.posthog);
  // Record analytics events instead of queueing them for PostHog.
  await page.evaluate(() => {
    window.__captured = [];
    window.posthog.capture = (event, properties) => window.__captured.push({ event, properties });
  });
}

const captured = (page) => page.evaluate(() => window.__captured);

test('loads with the photo, controls, and helper API', async ({ page }) => {
  await openApp(page);
  await expect(page).toHaveTitle(/Boopmaster 3000/);
  await expect(page.locator('#photo')).toBeVisible();
  await expect(page.locator('#photo')).toHaveJSProperty('complete', true);
  expect(await page.locator('#photo').evaluate((img) => img.naturalWidth)).toBeGreaterThan(0);
  await expect(page.locator('#metrics-toggle')).toBeVisible();
  await expect(page.locator('#credits-toggle')).toBeVisible();
  expect(await page.evaluate(() => Object.keys(window.HenryBeard).sort())).toEqual(
    ['bark', 'boop', 'party', 'toggleEyes', 'toggleGoogly', 'treat', 'treats'],
  );
});

test('booping the snoot shows a badge and records a boop', async ({ page }) => {
  await openApp(page);
  await page.locator('#snoot').click();
  await expect(page.locator('.boop-badge')).toHaveCount(1);
  await expect(page.locator('.floating-particle').first()).toBeAttached();

  await page.locator('#snoot').click();
  const boops = (await captured(page)).filter((e) => e.event === 'boop');
  expect(boops.map((e) => e.properties)).toEqual([
    { combo_count: 1, via: 'click' },
    { combo_count: 2, via: 'click' },
  ]);
});

test('keyboard shortcuts toggle party mode, googly eyes, and eye tracking', async ({ page }) => {
  await openApp(page);

  await page.keyboard.press('p');
  await expect(page.locator('body')).toHaveClass(/party-mode/);
  await expect(page.locator('#party-banner')).toHaveClass(/active/);
  await page.keyboard.press('p');
  await expect(page.locator('body')).not.toHaveClass(/party-mode/);

  await page.keyboard.press('g');
  await expect(page.locator('#eye-left')).toHaveClass(/active/);
  await expect(page.locator('#eye-right')).toHaveClass(/active/);

  await page.keyboard.press('e');
  await expect(page.locator('#stage')).toHaveClass(/tracking-active/);

  const toggles = (await captured(page)).filter((e) => e.event === 'feature_toggle');
  expect(toggles.map((e) => [e.properties.feature, e.properties.enabled])).toEqual([
    ['party_mode', true],
    ['party_mode', false],
    ['googly_eyes', true],
    ['eye_tracking', true],
  ]);
});

test('metrics modal loads engagement and health tabs, and Escape closes it', async ({ page }) => {
  await openApp(page);
  const modal = page.locator('#metrics-modal');

  await page.locator('#metrics-toggle').click();
  await expect(modal).toHaveClass(/active/);
  await expect(page.locator('#metrics-container #fake-engagement')).toHaveText('Total boops: 1234');

  await page.locator('.tab-btn[data-tab="health"]').click();
  await expect(page.locator('#tab-health')).toHaveClass(/active/);
  await expect(page.locator('#health-container #fake-health')).toHaveText('Requests: 99');

  await page.keyboard.press('Escape');
  await expect(modal).not.toHaveClass(/active/);
});

test('credits modal opens and closes', async ({ page }) => {
  await openApp(page);
  const modal = page.locator('#credits-modal');
  await page.locator('#credits-toggle').click();
  await expect(modal).toHaveClass(/active/);
  await expect(modal.locator('#github-link')).toHaveAttribute(
    'href',
    'https://github.com/jbirdkerr/jbirdkerr-net',
  );
  await modal.locator('.modal-close').click();
  await expect(modal).not.toHaveClass(/active/);
});
