#!/usr/bin/env node
// Regenerates the README previews (docs/assets/quota-{desktop,mobile}.{en,ko}.png) from synthetic
// fixtures only. Install Playwright or set PLAYWRIGHT_MODULE; BROWSER_EXECUTABLE selects a browser.
const fs = require('node:fs');
const switchLang = (async (page, lang) => { if ((await page.evaluate(() => document.documentElement.lang)) !== lang) await page.click('.lang-toggle'); await page.waitForFunction(l => document.documentElement.lang === l, lang); });
const path = require('node:path');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const { seedKey } = require('./lib/seed-key.cjs');
const root = path.resolve(__dirname, '..');
const source = require('./lib/dashboard-html.cjs').readDashboardHTML(root);
const out = path.join(root, 'docs/assets');

const minutesAgo = m => new Date(Date.now() - m * 60000).toISOString();
const inHours = h => new Date(Date.now() + h * 3600000).toISOString();
const win = (name, remaining, hours) => ({ name, remaining_percent: remaining, reset_at: inHours(hours) });
const base = { status: 'known', operational: {} };
const accounts = [
  { ...base, account_key: 'ag', provider: 'antigravity', account: 'antigravity-main@example.invalid', endpoint: 'direct', observed_at: minutesAgo(7),
    windows: [win('Gemini five hour', 100, 4), win('Gemini weekly', 9, 120), win('Claude five hour', 9, 4), win('Claude weekly', 9, 120)] },
  { ...base, account_key: 'cx1', provider: 'codex', account: 'codex-primary@example.invalid', plan_type: 'plus', endpoint: 'direct', observed_at: minutesAgo(3),
    current_account: true, current_kind: 'recent', current_reason: 'recent_usage', windows: [win('primary', 100, 5), win('secondary', 100, 120)] },
  { ...base, account_key: 'cx2', provider: 'codex', account: 'codex-sub01@example.invalid', plan_type: 'plus', endpoint: 'warp1', observed_at: minutesAgo(7),
    windows: [win('primary', 80, 5), win('secondary', 100, 120)] },
  { ...base, account_key: 'cl', provider: 'claude', account: 'team-production@example.invalid', plan_type: 'team', endpoint: 'warp2', observed_at: minutesAgo(7),
    windows: [win('five-hour', 85, 2), win('weekly', 100, 110)], reset_credits: { available_count: 1, nearest_expiry: inHours(48) }, api_credits: { balance: 0 } },
  { ...base, account_key: 'cx3', provider: 'codex', account: 'codex-warning@example.invalid', plan_type: 'plus', endpoint: 'direct', observed_at: minutesAgo(7),
    windows: [win('primary', 15, 5), win('secondary', 90, 120)] },
  { ...base, account_key: 'cx4', provider: 'codex', account: 'plus-locked@example.invalid', plan_type: 'plus', endpoint: 'direct', observed_at: minutesAgo(39),
    windows: [{ name: 'primary', remaining_percent: 0, reset_after_seconds: 3600 }, win('secondary', 60, 120)], api_credits: { balance: 15.5 } },
  { ...base, account_key: 'cx5', provider: 'codex', account: 'weekly-locked@example.invalid', plan_type: 'plus', endpoint: 'direct', observed_at: minutesAgo(7),
    windows: [win('primary', 40, 3), { name: 'secondary', remaining_percent: 0, reset_after_seconds: 432000 }], api_credits: { balance: 15.5 } },
  { ...base, account_key: 'cx6', provider: 'codex', account: 'standby-backup@example.invalid', plan_type: 'plus', endpoint: 'direct', observed_at: minutesAgo(7),
    windows: [win('primary', 50, 5), win('secondary', 100, 120)] },
];
const availability = [
  { key: 'claude', label: 'Claude', remaining_percent: 85, accounts: 1 },
  { key: 'codex', label: 'GPT', remaining_percent: 61, accounts: 6 },
  { key: 'antigravity_gemini', label: '안티그래비티 · Gemini', remaining_percent: 100, accounts: 1 },
  { key: 'antigravity_claude', label: '안티그래비티 · Claude/GPT', remaining_percent: 9, accounts: 1 },
];

async function capture(browser, lang, kind) {
  const mobile = kind === 'mobile';
  const ctx = await browser.newContext({ locale: lang === 'en' ? 'en-US' : 'ko-KR', reducedMotion: 'reduce', viewport: mobile ? { width: 390, height: 900 } : { width: 1440, height: 1080 } });
  await seedKey(ctx);
  const page = await ctx.newPage();
  await page.route('**/*', async route => {
    const url = new URL(route.request().url());
    if (route.request().resourceType() === 'document') return route.fulfill({ contentType: 'text/html', body: source });
    if (url.pathname.endsWith('/data/accounts')) return route.fulfill({ contentType: 'application/json', body: JSON.stringify({ configured: true, snapshot_complete: true, accounts, availability }) });
    const asset = path.join(root, 'internal/dashboard/assets', path.basename(url.pathname));
    if (fs.existsSync(asset) && fs.statSync(asset).isFile()) return route.fulfill({ contentType: 'font/woff2', body: fs.readFileSync(asset) });
    return route.fulfill({ status: 404, body: '' });
  });
  await page.goto('http://fixture.invalid/v0/resource/plugins/quota-monitor/dashboard');
  await page.waitForSelector('.account-card');
  await switchLang(page, lang);
  await page.evaluate(() => document.fonts.ready);
  await page.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
  const file = path.join(out, `quota-${kind}.${lang}.png`);
  await page.screenshot({ path: file, fullPage: mobile });
  await ctx.close();
  return file;
}

(async () => {
  fs.mkdirSync(out, { recursive: true });
  const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_EXECUTABLE || undefined, args: ['--no-sandbox'] });
  try {
    for (const lang of ['en', 'ko']) for (const kind of ['desktop', 'mobile']) console.log('wrote', path.relative(root, await capture(browser, lang, kind)));
  } finally { await browser.close(); }
})();
