#!/usr/bin/env node
// Browser regression inside a simulated console: the dashboard is served with the real
// hash-based CSP (zero violations allowed), embedded in a same-origin parent page that
// owns <html lang> and cli-proxy-language, and must follow the console language live.
// Also checks the 88px top clearance at 412/820/1280 for the auth gate and the signed-in
// view. PLAYWRIGHT_MODULE / BROWSER_EXECUTABLE override defaults; SCREENSHOT_DIR saves PNGs.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const { SEED_KEY } = require('./lib/seed-key.cjs');
const root = path.resolve(__dirname, '..');
const page = require('./lib/dashboard-html.cjs').readDashboardHTML(root);

// Mirrors internal/dashboard ScriptSources and plugin.dashboardSecurityHeaders.
const hashes = [...page.matchAll(/<script\b[^>]*>([\s\S]*?)<\/script>/g)].map(m => `'sha256-${crypto.createHash('sha256').update(m[1], 'utf8').digest('base64')}'`);
const goSource = fs.readFileSync(path.join(root, 'internal/plugin/plugin.go'), 'utf8');
const cspTemplate = /"Content-Security-Policy": \{"(default-src[^"]*)" \+ dashboard\.ScriptSources \+ "([^"]*)"\}/.exec(goSource);
assert(cspTemplate, 'CSP template found in plugin.go');
const csp = cspTemplate[1] + hashes.join(' ') + cspTemplate[2];
assert(!/script-src[^;]*unsafe-inline/.test(csp), 'no script unsafe-inline');

const now = new Date().toISOString();
const reset = h => new Date(Date.now() + h * 3600000).toISOString();
const accounts = [
  { account_key: 'a', provider: 'codex', account: 'recent@example.invalid', status: 'known', plan_type: 'plus', endpoint: 'direct', observed_at: now, operational: {}, windows: [{ name: 'primary', remaining_percent: 80, reset_at: reset(5) }, { name: 'secondary', remaining_percent: 100, reset_at: reset(120) }] },
  { account_key: 'b', provider: 'claude', account: 'claude@example.invalid', status: 'known', endpoint: 'direct', observed_at: now, operational: {}, windows: [{ name: 'five-hour', remaining_percent: 40, reset_at: reset(2) }, { name: 'weekly', remaining_percent: 70, reset_at: reset(100) }] },
  { account_key: 'c', provider: 'antigravity', account: 'ag@example.invalid', status: 'known', endpoint: 'direct', observed_at: now, operational: {}, windows: ['Gemini five hour', 'Gemini weekly', 'Claude five hour', 'Claude weekly'].map((name, j) => ({ name, remaining_percent: 55, reset_at: reset(j ? 120 : 3) })) },
];
const snapshot = { configured: true, snapshot_complete: true, reason: 'auth_files_failed', reason_code: 'auth_files_failed', health: { last_category: 'auth_files_failed', failures: 1 }, accounts, availability: [] };
const ORIGIN = 'http://console.invalid';
const DASH = ORIGIN + '/v0/resource/plugins/quota-monitor/dashboard';
const parentHTML = lang => `<!DOCTYPE html><html lang="${lang}"><head><style>body{margin:0}iframe{position:absolute;top:0;left:0;width:100%;height:100%;border:0}#tools{position:fixed;top:0;right:0;left:0;height:76px;background:#0001;pointer-events:none;z-index:9}</style></head><body><div id="tools"></div><iframe id="plugin" src="${DASH}"></iframe></body></html>`;
const shots = process.env.SCREENSHOT_DIR;
if (shots) fs.mkdirSync(shots, { recursive: true });

(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_EXECUTABLE || undefined, args: ['--no-sandbox'] });
  try {
    for (const signedIn of [false, true]) {
      const ctx = await browser.newContext({ locale: 'en-US', reducedMotion: 'reduce' });
      if (signedIn) await ctx.addInitScript(key => { try { if (location.pathname.endsWith('/dashboard')) sessionStorage.setItem('cpa_quota_monitor_mgmt_key', key); } catch (e) {} }, SEED_KEY);
      // The console persists its language like Management Center does (zustand JSON).
      await ctx.addInitScript(() => { try { if (location.pathname === '/') localStorage.setItem('cli-proxy-language', JSON.stringify({ state: { language: 'ko' }, version: 0 })); } catch (e) {} });
      const tab = await ctx.newPage();
      const violations = [], errors = [];
      tab.on('console', m => { if (/Content Security Policy|Refused to/.test(m.text())) violations.push(m.text()); });
      tab.on('pageerror', e => errors.push(e.message));
      await ctx.exposeBinding('__cspViolation', (src, v) => violations.push(v));
      await ctx.addInitScript(() => document.addEventListener('securitypolicyviolation', e => window.__cspViolation(e.violatedDirective + ' ' + e.blockedURI + ' ' + (e.sample || ''))));
      await ctx.route('**/*', async route => {
        const url = new URL(route.request().url());
        if (url.pathname === '/') return route.fulfill({ contentType: 'text/html', body: parentHTML('ko') });
        if (url.pathname.endsWith('/dashboard')) return route.fulfill({ contentType: 'text/html; charset=utf-8', headers: { 'Content-Security-Policy': csp }, body: page });
        if (url.pathname.endsWith('/data/accounts')) return route.fulfill({ contentType: 'application/json', body: JSON.stringify(snapshot) });
        const asset = path.join(root, 'internal/dashboard/assets', path.basename(url.pathname));
        if (fs.existsSync(asset) && fs.statSync(asset).isFile()) return route.fulfill({ contentType: 'font/woff2', body: fs.readFileSync(asset) });
        return route.fulfill({ status: 404, body: '' });
      });
      await tab.goto(ORIGIN + '/');
      const frame = await (await tab.waitForSelector('#plugin')).contentFrame();
      const view = signedIn ? '.account-card' : '#authGate';
      await frame.waitForSelector(view, { state: 'visible' });
      // Host ko at load → Korean, title and lang fixed up by the script.
      assert.equal(await frame.evaluate(() => document.documentElement.lang), 'ko', 'follows console ko at load');
      assert.equal(await frame.evaluate(() => document.title), '계정별 남은 쿼터');
      if (signedIn) {
        const notice = await frame.locator('#staleNoticeText').innerText();
        assert.match(notice, /인증 파일 목록을 가져오지 못했습니다/, 'reason code translated: ' + notice);
      }
      // Console switches to English: <html lang> on the parent changes.
      await tab.evaluate(() => document.documentElement.setAttribute('lang', 'en'));
      await frame.waitForFunction(() => document.documentElement.lang === 'en');
      assert.equal(await frame.evaluate(() => localStorage.getItem('cpa_plugin_lang')), 'en', 'shared choice written for other plugin pages');
      if (signedIn) assert.match(await frame.locator('#staleNoticeText').innerText(), /Could not list the auth files/, 'notice re-translated');
      // The plugin's own toggle wins until the console changes again.
      await frame.click(signedIn ? '#languageToggle' : '#authLanguageToggle');
      await frame.waitForFunction(() => document.documentElement.lang === 'ko');
      assert.equal(await frame.evaluate(() => localStorage.getItem('cpa_plugin_lang_host')), 'en', 'toggle records the console language');
      await tab.reload();
      const frame2 = await (await tab.waitForSelector('#plugin')).contentFrame();
      await frame2.waitForSelector(view, { state: 'visible' });
      // Parent page reloads with lang="ko" (static fixture) — the console language differs from the
      // recorded one (en), so it wins again.
      assert.equal(await frame2.evaluate(() => document.documentElement.lang), 'ko', 'console language after reload');
      assert.equal(JSON.parse(await tab.evaluate(() => localStorage.getItem('cli-proxy-language'))).state.language, 'ko', 'console entry untouched');
      for (const lang of ['en', 'ko']) {
        await tab.evaluate(l => document.documentElement.setAttribute('lang', l), lang);
        await frame2.waitForFunction(l => document.documentElement.lang === l, lang);
        for (const width of [412, 820, 1280]) {
          await tab.setViewportSize({ width, height: 900 });
          await frame2.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
          const top = await frame2.evaluate(() => {
            let min = Infinity, who = '';
            // Content, not wrappers: controls, media, and elements with their own text.
            const content = el => /^(BUTTON|INPUT|SELECT|IMG|SVG|H1|H2|P|LABEL|LI)$/i.test(el.tagName) || [...el.childNodes].some(n => n.nodeType === 3 && n.textContent.trim());
            for (const el of document.body.querySelectorAll('*')) {
              if (!content(el) || el.classList.contains('sr-only')) continue;
              const r = el.getBoundingClientRect(), cs = getComputedStyle(el);
              if (!r.width || !r.height || cs.visibility === 'hidden' || cs.display === 'none' || el.closest('[aria-hidden="true"]')) continue;
              if (r.top < min) { min = r.top; who = el.tagName + '#' + el.id + '.' + el.className; }
            }
            return { min, who };
          });
          assert(top.min >= 88 - 0.5, `${signedIn ? 'signed-in' : 'auth gate'} ${lang} @${width}: element above 88px: ${JSON.stringify(top)}`);
          if (shots) await tab.screenshot({ path: path.join(shots, `host-${signedIn ? 'dashboard' : 'auth'}-${lang}-${width}.png`) });
        }
      }
      assert.deepEqual(violations, [], 'CSP violations');
      assert.deepEqual(errors, [], 'page errors');
      await ctx.close();
    }
    console.log('PASS host: follows console language (load, live, toggle, reload), reason codes translated, 0 CSP violations, top >= 88px @412/820/1280');
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
