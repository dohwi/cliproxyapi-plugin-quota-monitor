#!/usr/bin/env node
// Browser regression for the English/Korean switch: detection, toggle, no stray Korean in English,
// persistence and overflow at 1440/768/390/320px. Install Playwright or set PLAYWRIGHT_MODULE;
// BROWSER_EXECUTABLE selects a system browser; SCREENSHOT_DIR captures pages.
const fs = require('fs'), path = require('path');
const switchLang = (async (page, lang) => { if ((await page.evaluate(() => document.documentElement.lang)) !== lang) await page.click('.lang-toggle'); await page.waitForFunction(l => document.documentElement.lang === l, lang); });
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = path.resolve(__dirname, '..');
const source = require('./lib/dashboard-html.cjs').readDashboardHTML(root);
const verifyLanguagePlacement = require('./lib/host-toolbar.cjs');
const now = new Date().toISOString();
const reset = h => new Date(Date.now() + h * 3600000).toISOString();
const accounts = [
  { account_key: 'a', provider: 'codex', account: 'recent@example.invalid', status: 'known', plan_type: 'plus', endpoint: 'direct', observed_at: now, current_account: true, current_kind: 'recent', current_reason: 'recent_usage', operational: {}, windows: [{ name: 'primary', remaining_percent: 80, reset_at: reset(5) }, { name: 'secondary', remaining_percent: 100, reset_at: reset(120) }], reset_credits: { available_count: 2, nearest_expiry: reset(48) }, api_credits: { balance: 0 } },
  { account_key: 'b', provider: 'codex', account: 'locked@example.invalid', status: 'known', plan_type: 'plus', endpoint: 'warp1', observed_at: now, operational: {}, windows: [{ name: 'primary', remaining_percent: 0, reset_after_seconds: 3600 }, { name: 'secondary', remaining_percent: 60, reset_at: reset(120) }] },
  { account_key: 'c', provider: 'antigravity', account: 'ag@example.invalid', status: 'known', endpoint: 'direct', observed_at: now, operational: {}, windows: ['Gemini five hour', 'Gemini weekly', 'Claude five hour', 'Claude weekly'].map((name, j) => ({ name, remaining_percent: j === 2 ? 0 : 55, reset_at: reset(j ? 120 : 3) })) },
  { account_key: 'd', provider: 'claude', account: 'claude@example.invalid', status: 'known', endpoint: 'warp2', observed_at: now, operational: {}, windows: [{ name: 'five-hour', remaining_percent: 40, reset_at: reset(2) }, { name: 'weekly', remaining_percent: 70, reset_at: reset(100) }, { name: 'weekly-oauth-apps', remaining_percent: 90, reset_at: reset(100) }] },
];
const avail = [{ key: 'claude', label: 'Claude', remaining_percent: 40, accounts: 1 }, { key: 'codex', label: 'GPT', remaining_percent: 50, accounts: 2 }, { key: 'antigravity_gemini', label: '안티그래비티 · Gemini', remaining_percent: 55, accounts: 1 }, { key: 'antigravity_claude', label: '안티그래비티 · Claude/GPT', remaining_percent: 0, accounts: 1 }];
const hangul = s => /[가-힣]/.test(s);
(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_EXECUTABLE || undefined, args: ['--no-sandbox'] });
  const fail = [];
  try {
    for (const locale of ['en-US', 'ko-KR']) {
      const ctx = await browser.newContext({ locale, reducedMotion: 'reduce' });
      const page = await ctx.newPage();
      const errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.route('**/*', async route => {
        const url = new URL(route.request().url());
        if (route.request().resourceType() === 'document') return route.fulfill({ contentType: 'text/html', body: source });
        if (url.pathname.endsWith('/data/accounts')) return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ configured: true, snapshot_complete: true, accounts, availability: avail }) });
        const asset = path.join(root, 'internal/dashboard/assets', path.basename(url.pathname));
        if (fs.existsSync(asset) && fs.statSync(asset).isFile()) return route.fulfill({ contentType: 'font/woff2', body: fs.readFileSync(asset) });
        return route.fulfill({ status: 404, body: '' });
      });
      const url = 'http://fixture.invalid/v0/resource/plugins/quota-monitor/dashboard';
      await page.goto(url);
      await page.waitForSelector('.account-card');
      const initial = await page.evaluate(() => document.documentElement.lang);
      const wantInitial = locale === 'en-US' ? 'en' : 'ko';
      if (initial !== wantInitial) fail.push(`${locale}: initial lang ${initial} != ${wantInitial}`);

      // switch to the opposite language
      const target = wantInitial === 'en' ? 'ko' : 'en';
      await switchLang(page, target);
      const pressed = await page.locator('.lang-toggle').getAttribute('data-current-lang');
      if (pressed !== target) fail.push(`${locale}: aria-pressed ${pressed} != ${target}`);

      for (const lang of [target, wantInitial]) {
        if (lang !== target) await switchLang(page, lang);
        const text = await page.evaluate(() => document.body.innerText + '\n' + [...document.querySelectorAll('[aria-label],[title]')].map(e => (e.getAttribute('aria-label') || '') + ' ' + (e.title || '')).join('\n') + '\n' + document.title);
        const stray = text.split('\n').filter(l => (lang === 'en' ? hangul(l) : false) && !/한국어|^\s*가(\s+A)?\s*$/.test(l));
        if (lang === 'en' && stray.length) fail.push(`${locale}: Korean left in EN UI: ${JSON.stringify(stray.slice(0, 6))}`);
        if (lang === 'ko' && /Retry|Refresh|Weekly limit|Resets in/.test(text)) fail.push(`${locale}: English left in KO UI`);
        for (const width of [1440, 768, 390, 320]) {
          await page.setViewportSize({ width, height: 900 });
          await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
          const over = await page.evaluate(() => {
            const out = [];
            if (document.documentElement.scrollWidth > innerWidth + 1) out.push('page-h-scroll');
            document.querySelectorAll('.account-card, .toolbar, .page-heading, .lang-toggle, .fleet-chip, .quota-lock-state, .ag-family-section, .quota-value-row').forEach(e => {
              if (e.scrollWidth > e.clientWidth + 1) out.push(e.className.toString().slice(0, 40) + ' ' + e.scrollWidth + '>' + e.clientWidth + ' text=' + JSON.stringify(e.textContent.trim().replace(/\s+/g,' ').slice(0,40)) + ' fonts=' + document.fonts.status + ' suit=' + document.fonts.check('600 12px SUIT') + ' lang=' + document.documentElement.lang);
            });
            return out;
          });
          await verifyLanguagePlacement(page, '.dashboard-control-panel');
          if (over.length) fail.push(`${locale}/${lang}/${width}px overflow: ${over.slice(0, 5).join(' | ')}`);
        }
        await page.setViewportSize({ width: 1280, height: 900 });
        if (process.env.SCREENSHOT_DIR) await page.screenshot({ path: path.join(process.env.SCREENSHOT_DIR, `lang-${locale}-${lang}.png`), fullPage: true });
      }
      // reload keeps the explicit choice
      await switchLang(page, 'en');
      await page.reload();
      await page.waitForSelector('.account-card');
      const after = await page.evaluate(() => [document.documentElement.lang, localStorage.getItem('cpa_plugin_lang')]);
      if (after[0] !== 'en' || after[1] !== 'en') fail.push(`${locale}: not persisted ${after}`);
      if (errors.length) fail.push(`${locale}: page errors ${errors}`);
      await ctx.close();
    }
  } finally { await browser.close(); }
  if (fail.length) { console.log('FAIL\n' + fail.join('\n')); process.exit(1); }
  console.log('PASS: lang detect, toggle, no stray Korean in EN, persistence, no overflow @1440/768/390/320');
})();
