#!/usr/bin/env node
// Management-key gate: the unauthenticated page must send no data request without a key,
// must keep a rejected key out of storage, and must send an accepted key only as a Bearer
// header (never in a URL), including for the explicit refresh action.
const fs = require('node:fs'), path = require('node:path'), assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = path.resolve(__dirname, '..');
const html = require('./lib/dashboard-html.cjs').readDashboardHTML(root);
const GOOD = 'good-management-key', STORE = 'cpa_quota_monitor_mgmt_key', PREFIX = '/v0/management/plugins/quota-monitor/data/';
const accounts = [{ account_key: 'k1', provider: 'codex', account: 'a@example.com', status: 'ok', windows: [{ name: 'primary', remaining_percent: 61, reset_after_seconds: 3600 }], endpoint: 'direct', refresh_supported: true }];

(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_EXECUTABLE || undefined, args: ['--no-sandbox'] });
  try {
    const page = await browser.newPage({ locale: 'en-US' });
    const seen = [];
    page.on('pageerror', e => assert.fail('page error: ' + e.message));
    await page.route('**/*', async route => {
      const req = route.request(), url = new URL(req.url());
      if (req.resourceType() === 'document') return route.fulfill({ contentType: 'text/html', body: html });
      if (url.pathname.includes('/assets/')) {
        const asset = path.join(root, 'internal/dashboard/assets', path.basename(url.pathname));
        return fs.existsSync(asset) ? route.fulfill({ contentType: 'font/woff2', body: fs.readFileSync(asset) }) : route.fulfill({ status: 404, body: '' });
      }
      if (!url.pathname.startsWith(PREFIX)) return route.fulfill({ status: 404, body: '' });
      seen.push({ method: req.method(), url: req.url(), auth: req.headers()['authorization'] || '' });
      if ((req.headers()['authorization'] || '') !== 'Bearer ' + GOOD) return route.fulfill({ status: 401, contentType: 'application/json', body: '{"error":"invalid management key"}' });
      if (url.pathname.endsWith('/refresh')) return route.fulfill({ contentType: 'application/json', body: '{"refreshed":true,"count":1}' });
      return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ configured: true, snapshot_complete: true, accounts }) });
    });

    await page.goto('http://fixture.invalid/v0/resource/plugins/quota-monitor/dashboard');
    await page.waitForSelector('#authGate', { state: 'visible' });
    assert.equal(seen.length, 0, 'no data request without a key (would count toward the host IP ban)');
    assert.equal(await page.locator('#dashboardContent').isHidden(), true, 'dashboard hidden behind the gate');
    assert.equal(await page.locator('#authKeyInput').getAttribute('type'), 'password');

    // The gate shows no dashboard title/subtitle, and offers its own language switch.
    assert.equal(await page.locator('#pageHeading').isHidden(), true, 'page title and subtitle hidden behind the gate');
    assert.equal(await page.locator('#authLanguageToggle').isVisible(), true, 'language toggle inside the gate');
    assert.match(await page.locator('#authGateTitle').innerText(), /Management key required/);
    await page.click('#authLanguageToggle');
    assert.match(await page.locator('#authGateTitle').innerText(), /관리 키/, 'gate switches to Korean');
    assert.match(await page.locator('#authForm button[type=submit]').innerText(), /확인/);
    assert.equal(await page.evaluate(() => localStorage.getItem('cpa_plugin_lang')), 'ko', 'choice persisted as the shared language preference');
    await page.click('#authLanguageToggle');
    assert.match(await page.locator('#authGateTitle').innerText(), /Management key required/, 'and back to English');
    assert.equal(await page.locator('#authLanguageToggle').getAttribute('aria-label'), 'Switch to Korean');

    await page.click('#authForm button[type=submit]');
    assert.match(await page.locator('#authMessage').innerText(), /Enter the management key/);
    assert.equal(seen.length, 0);

    await page.fill('#authKeyInput', 'wrong-key');
    await page.click('#authForm button[type=submit]');
    await page.waitForFunction(() => /invalid or access was denied/.test(document.getElementById('authMessage').textContent));
    assert.equal(seen.length, 1, 'a wrong key costs a single failed attempt, got ' + seen.length);
    assert.equal(await page.evaluate(k => sessionStorage.getItem(k), STORE), null, 'rejected key is never stored');
    assert.equal(await page.locator('#authGate').isVisible(), true);
    assert.equal(await page.locator('#dashboardContent').isHidden(), true, 'dashboard stays hidden after a rejection');
    assert.equal(await page.evaluate(() => document.getElementById('authKeyInput').value), '');

    await page.fill('#authKeyInput', GOOD);
    await page.click('#authForm button[type=submit]');
    await page.waitForSelector('#dashboardContent', { state: 'visible' });
    await page.waitForFunction(() => document.body.textContent.includes('a@example.com'));
    assert.equal(await page.locator('#pageHeading').isVisible(), true, 'title returns once the key is accepted');
    assert.equal(await page.locator('#authGate').isHidden(), true);
    assert.equal(await page.evaluate(k => sessionStorage.getItem(k), STORE), GOOD);
    assert.equal(await page.evaluate(k => localStorage.getItem(k), STORE), null, 'key must never reach localStorage');
    assert(seen.every(r => !r.url.includes(GOOD) && !/[?&](key|token|password|management_key)=/i.test(r.url)), 'key must not appear in any URL');

    // Explicit refresh is an authenticated POST.
    await page.click('#fullRefreshBtn');
    await page.waitForFunction(() => /seconds|Refresh|refresh|\d+s/i.test(document.getElementById('fullRefreshBtn').textContent));
    const refresh = seen.find(r => r.url.includes('/data/refresh'));
    assert(refresh, 'refresh request sent');
    assert.equal(refresh.method, 'POST');
    assert.equal(refresh.auth, 'Bearer ' + GOOD);

    // Reload in the same tab: stored key is reused without prompting.
    const before = seen.length;
    await page.reload();
    await page.waitForFunction(() => document.body.textContent.includes('a@example.com'));
    assert.equal(await page.locator('#authGate').isHidden(), true);
    assert(seen.length > before && seen.slice(before).every(r => r.auth === 'Bearer ' + GOOD));

    // Key revoked while the tab is open: it is dropped and polling stops.
    await page.evaluate(() => { mgmtKey = 'revoked'; keyVerified = true; });
    await page.evaluate(() => loadDashboard(false));
    await page.waitForSelector('#authGate', { state: 'visible' });
    assert.equal(await page.evaluate(k => sessionStorage.getItem(k), STORE), null, 'revoked key is dropped from storage');
    assert.equal(await page.locator('#dashboardContent').isHidden(), true);
    const after = seen.length;
    await page.waitForTimeout(400);
    assert.equal(seen.length, after, 'polling stops after a rejection');
    console.log('PASS auth gate: no key => no request; wrong key => 1 attempt, not stored; good key => header only, session only, POST refresh; revoked key => dropped and polling stops');
  } finally {
    await browser.close();
  }
})().catch(err => { console.error(err); process.exit(1); });
