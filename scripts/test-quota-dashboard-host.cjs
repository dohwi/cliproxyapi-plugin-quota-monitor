#!/usr/bin/env node
// Browser regression inside a simulated console: the dashboard is served with the real
// hash-based CSP (zero violations allowed), embedded in a same-origin parent page that
// owns <html lang> and cli-proxy-language. Spec L scenarios: console zh-CN on load, live
// change to ko, a pinned menu choice surviving console changes and reload, "Follow
// CLIProxyAPI", keyboard use, the menu inside the viewport at 412px, the 88px top clearance
// at 412/820/1280 (auth gate and signed-in) and no clipped Chinese text. PLAYWRIGHT_MODULE / BROWSER_EXECUTABLE override defaults; SCREENSHOT_DIR saves PNGs.
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const { SEED_KEY } = require('./lib/seed-key.cjs');
const { LABELS, openMenu, pickLanguage } = require('./lib/lang-menu.cjs');
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
  { account_key: 'weekly-only', provider: 'codex', account: 'weekly-only@example.invalid', status: 'known', endpoint: 'direct', observed_at: now, operational: {}, reset_credits: { available_count: 1 }, api_credits: { balance: 0 }, windows: [{ name: 'primary', window_minutes: 10080, remaining_percent: 100, reset_at: reset(167) }, { name: 'secondary', remaining_percent: 100, reset_at: now }] },
  { account_key: 'weekly-locked', provider: 'codex', account: 'weekly-locked@example.invalid', status: 'known', endpoint: 'direct', observed_at: now, operational: {}, windows: [{ name: 'primary', window_minutes: 0, remaining_percent: 100, reset_at: now }, { name: 'secondary', window_minutes: 10080, remaining_percent: 0, reset_at: reset(20) }] },
  { account_key: 'b', provider: 'claude', account: 'claude@example.invalid', status: 'known', endpoint: 'direct', observed_at: now, operational: {}, windows: [{ name: 'five-hour', remaining_percent: 40, reset_at: reset(2) }, { name: 'weekly', remaining_percent: 70, reset_at: reset(100) }] },
  { account_key: 'c', provider: 'antigravity', account: 'ag@example.invalid', status: 'known', endpoint: 'direct', observed_at: now, operational: {}, windows: ['Gemini five hour', 'Gemini weekly', 'Claude five hour', 'Claude weekly'].map((name, j) => ({ name, remaining_percent: 55, reset_at: reset(j ? 120 : 3) })) },
  { account_key: 'claude-weekly', provider: 'claude', account: 'claude-7d@example.invalid', status: 'known', endpoint: 'direct', observed_at: now, windows: [{ name: 'weekly', window_minutes: 10080, remaining_percent: 75, reset_at: reset(120) }] },
  { account_key: 'ag-weekly', provider: 'antigravity', account: 'ag-7d@example.invalid', status: 'known', endpoint: 'direct', observed_at: now, windows: [{ name: 'Gemini Models / Five Hour Limit Remaining', window_minutes: 10080, remaining_percent: 80, reset_at: reset(120) }, { name: 'Claude and GPT models / Five Hour Limit Remaining', window_minutes: 300, remaining_percent: 60, reset_at: reset(3) }] },
];
const snapshot = { configured: true, snapshot_complete: true, reason: 'auth_files_failed', reason_code: 'auth_files_failed', health: { last_category: 'auth_files_failed', failures: 1 }, accounts, availability: [] };
const ORIGIN = 'http://console.invalid';
const DASH = ORIGIN + '/v0/resource/plugins/quota-monitor/dashboard';
const parentHTML = lang => `<!DOCTYPE html><html lang="${lang}"><head><style>body{margin:0}iframe{position:absolute;top:0;left:0;width:100%;height:100%;border:0}#tools{position:fixed;top:0;right:0;left:0;height:76px;background:#0001;pointer-events:none;z-index:9}@media(max-width:768px){#tools{height:60px}}</style></head><body><div id="tools"></div><iframe id="plugin" src="${DASH}"></iframe></body></html>`;
const shots = process.env.SCREENSHOT_DIR;
if (shots) fs.mkdirSync(shots, { recursive: true });

const settle = f => f.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
const setConsoleLang = (tab, l) => tab.evaluate(v => document.documentElement.setAttribute('lang', v), l);
const waitLang = (f, l) => f.waitForFunction(v => document.documentElement.lang === v, l);
// Lowest top of visible content (controls, media, own text) in the frame.
const topOf = f => f.evaluate(() => {
  let min = Infinity, who = '';
  const content = el => /^(BUTTON|INPUT|SELECT|IMG|SVG|H1|H2|P|LABEL|LI)$/i.test(el.tagName) || [...el.childNodes].some(n => n.nodeType === 3 && n.textContent.trim());
  for (const el of document.body.querySelectorAll('*')) {
    if (!content(el) || el.classList.contains('sr-only') || el.closest('.lang-menu')) continue;
    const r = el.getBoundingClientRect(), cs = getComputedStyle(el);
    if (!r.width || !r.height || cs.visibility === 'hidden' || cs.display === 'none' || el.closest('[aria-hidden="true"]')) continue;
    if (r.top < min) { min = r.top; who = el.tagName + '#' + el.id + '.' + el.className; }
  }
  return { min, who };
});
// Elements whose text is cut off (horizontal overflow) in the current language.
const clipped = f => f.evaluate(() => {
  const out = [];
  if (document.documentElement.scrollWidth > innerWidth + 1) out.push('page-h-scroll');
  document.querySelectorAll('button, label, h1, h2, .account-card, .fleet-chip, .pct-legend li, .card-freshness, .quota-value-reset, .ag-limit-reset, .quota-value-label, .ag-family-head, .ui-select-trigger, .lang-menu-item, .credit-summary, .status-badge, .badge-endpoint').forEach(e => {
    const cs = getComputedStyle(e);
    if (!e.getClientRects().length || cs.visibility === 'hidden' || e.classList.contains('sr-only') || !e.textContent.trim()) return;
    if (e.scrollWidth > e.clientWidth + 1 && cs.overflowX !== 'visible') out.push((e.id || e.className.toString().slice(0, 30)) + ' ' + e.scrollWidth + '>' + e.clientWidth + ' ' + JSON.stringify(e.textContent.trim().slice(0, 30)));
  });
  return out;
});

(async () => {
  const browser = await chromium.launch({ headless: true, executablePath: process.env.BROWSER_EXECUTABLE || undefined, args: ['--no-sandbox'] });
  try {
    for (const signedIn of [false, true]) {
      const which = signedIn ? 'dashboard' : 'auth';
      const ctx = await browser.newContext({ locale: 'en-US', reducedMotion: 'reduce' });
      if (signedIn) await ctx.addInitScript(key => { try { if (location.pathname.endsWith('/dashboard')) sessionStorage.setItem('cpa_quota_monitor_mgmt_key', key); } catch (e) {} }, SEED_KEY);
      // The console persists its language like Management Center does (zustand JSON).
      await ctx.addInitScript(() => { try { if (location.pathname === '/') localStorage.setItem('cli-proxy-language', JSON.stringify({ state: { language: 'zh-CN' }, version: 0 })); } catch (e) {} });
      const tab = await ctx.newPage();
      const violations = [], errors = [];
      tab.on('console', m => { if (/Content Security Policy|Refused to/.test(m.text())) violations.push(m.text()); });
      tab.on('pageerror', e => errors.push(e.message));
      await ctx.exposeBinding('__cspViolation', (src, v) => violations.push(v));
      await ctx.addInitScript(() => document.addEventListener('securitypolicyviolation', e => window.__cspViolation(e.violatedDirective + ' ' + e.blockedURI + ' ' + (e.sample || ''))));
      await ctx.route('**/*', async route => {
        const url = new URL(route.request().url());
        if (url.pathname === '/') return route.fulfill({ contentType: 'text/html', body: parentHTML('zh-CN') });
        if (url.pathname.endsWith('/dashboard')) return route.fulfill({ contentType: 'text/html; charset=utf-8', headers: { 'Content-Security-Policy': csp }, body: page });
        if (url.pathname.endsWith('/data/accounts')) return route.fulfill({ contentType: 'application/json', body: JSON.stringify(snapshot) });
        const asset = path.join(root, 'internal/dashboard/assets', path.basename(url.pathname));
        if (fs.existsSync(asset) && fs.statSync(asset).isFile()) return route.fulfill({ contentType: 'font/woff2', body: fs.readFileSync(asset) });
        return route.fulfill({ status: 404, body: '' });
      });
      const view = signedIn ? '.account-card' : '#authGate';
      const button = signedIn ? '#languageToggle' : '#authLanguageToggle';
      const load = async () => { const f = await (await tab.waitForSelector('#plugin')).contentFrame(); await f.waitForSelector(view, { state: 'visible' }); return f; };
      await tab.setViewportSize({ width: 1280, height: 900 });
      await tab.goto(ORIGIN + '/');
      let frame = await load();
      // 1) Console zh-CN at load → Chinese (lang, title, server reason, button label).
      assert.equal(await frame.evaluate(() => document.documentElement.lang), 'zh-CN', 'follows console zh-CN at load');
      assert.equal(await frame.evaluate(() => document.title), '各账号剩余额度');
      assert.equal(await frame.locator(button).getAttribute('aria-label'), LABELS['zh-CN']);
      assert.equal(await frame.locator(button).getAttribute('aria-haspopup'), 'menu');
      if (signedIn) {
        assert.match(await frame.locator('#staleNoticeText').innerText(), /无法获取认证文件列表/, 'reason code translated (zh-CN)');
        assert.match(await frame.locator('.account-card').first().innerText(), /后重置/, 'countdown in Chinese');
      }
      // 2) Console switches to Korean live.
      await setConsoleLang(tab, 'ko');
      await waitLang(frame, 'ko');
      if (signedIn) assert.match(await frame.locator('#staleNoticeText').innerText(), /인증 파일 목록을 가져오지 못했습니다/, 'notice re-translated (ko)');
      assert.equal(await frame.evaluate(() => localStorage.getItem('cpa_plugin_language')), null, 'following the console writes nothing');
      // 5) Keyboard: ArrowDown opens on the checked item (auto), Escape closes and refocuses the button.
      await frame.focus(button);
      await frame.press(button, 'ArrowDown');
      await frame.waitForSelector('.lang-menu', { state: 'visible' });
      assert.equal(await frame.evaluate(() => document.activeElement.getAttribute('data-pref')), 'auto', 'focus on the checked item');
      assert.equal(await frame.locator(button).getAttribute('aria-expanded'), 'true');
      const items = await frame.$$eval('.lang-menu-item', els => els.map(e => [e.getAttribute('data-pref'), e.getAttribute('lang'), e.querySelector('.lang-menu-label').textContent, (e.querySelector('.lang-menu-sub') || {}).textContent || '']));
      assert.deepEqual(items, [['auto', null, 'CLIProxyAPI 설정 따르기', '한국어'], ['en', 'en', 'English', ''], ['ko', 'ko', '한국어', ''], ['zh-CN', 'zh-CN', '简体中文', '']], 'menu items');
      await tab.keyboard.press('ArrowDown');
      assert.equal(await frame.evaluate(() => document.activeElement.getAttribute('data-pref')), 'en', 'ArrowDown moves');
      await tab.keyboard.press('End');
      assert.equal(await frame.evaluate(() => document.activeElement.getAttribute('data-pref')), 'zh-CN', 'End moves to the last item');
      await tab.keyboard.press('Escape');
      await frame.waitForSelector('.lang-menu', { state: 'detached' });
      assert.equal(await frame.evaluate(() => document.activeElement && document.activeElement.id), button.slice(1), 'Escape refocuses the button');
      assert.equal(await frame.locator(button).getAttribute('aria-expanded'), 'false');
      await frame.press(button, 'ArrowUp');
      await frame.waitForSelector('.lang-menu', { state: 'visible' });
      assert.equal(await frame.evaluate(() => document.activeElement.getAttribute('data-pref')), 'zh-CN', 'ArrowUp opens on the last item');
      await tab.mouse.click(5, 880);
      await frame.waitForSelector('.lang-menu', { state: 'detached' });
      // 3) Pick English: stays English after the console changes to zh-CN and after reload.
      await pickLanguage(frame, button, 'en');
      assert.equal(await frame.evaluate(() => localStorage.getItem('cpa_plugin_language')), 'en');
      await setConsoleLang(tab, 'zh-CN');
      await frame.waitForTimeout(200);
      assert.equal(await frame.evaluate(() => document.documentElement.lang), 'en', 'pinned en survives the console change');
      await tab.reload();
      frame = await load();
      assert.equal(await frame.evaluate(() => document.documentElement.lang), 'en', 'pinned en after reload (console zh-CN)');
      // 4) Follow CLIProxyAPI again.
      await pickLanguage(frame, button, 'auto');
      await waitLang(frame, 'zh-CN');
      assert.equal(await frame.evaluate(() => localStorage.getItem('cpa_plugin_language')), 'auto');
      await setConsoleLang(tab, 'ko');
      await waitLang(frame, 'ko');
      await setConsoleLang(tab, 'zh-CN');
      await waitLang(frame, 'zh-CN');
      assert.equal(JSON.parse(await tab.evaluate(() => localStorage.getItem('cli-proxy-language'))).state.language, 'zh-CN', 'console entry untouched');
      for (const lang of ['zh-CN', 'en', 'ko']) {
        await setConsoleLang(tab, lang);
        await waitLang(frame, lang);
        for (const width of [412, 820, 1280]) {
          await tab.setViewportSize({ width, height: 900 });
          await settle(frame);
          const top = await topOf(frame);
          assert(top.min >= 88 - 0.5, `${which} ${lang} @${width}: element above 88px: ${JSON.stringify(top)}`);
          if (signedIn) {
            const weekly = frame.locator('.account-card').filter({ hasText: 'weekly-only@example.invalid' });
            const dual = frame.locator('.account-card').filter({ hasText: 'recent@example.invalid' });
            const locked = frame.locator('.account-card').filter({ hasText: 'weekly-locked@example.invalid' });
            assert.deepEqual(await weekly.locator('.quota-value-row').evaluateAll(rows => rows.map(row => row.dataset.kind)), ['secondary'], `weekly only @${width}`);
            assert.equal(await weekly.locator('.quota-value-reset').getAttribute('data-window-name'), 'primary', 'countdown comes from the measured window');
            assert.equal(await weekly.locator('.credit-summary').count(), 1, 'weekly-only accounts retain credits');
            assert.deepEqual(await dual.locator('.quota-value-row').evaluateAll(rows => rows.map(row => row.dataset.kind)), ['primary', 'secondary'], `dual windows @${width}`);
            assert.equal(await locked.getAttribute('data-availability'), 'unavailable', 'exhausting a weekly-only quota locks the account');
            assert.equal(await locked.locator('.quota-lock-time.quota-reset-weekly').count(), 1, 'weekly lock countdown');
            assert.equal(await locked.locator('.quota-lock-weekly').count(), 0, 'weekly-only lock has no 5h context');
            const claude = frame.locator('[data-account-key="claude-weekly"]');
            assert.deepEqual(await claude.locator('.quota-value-row').evaluateAll(rows => rows.map(row => row.dataset.kind)), ['weekly'], `official Claude weekly only @${width}`);
            const ag = frame.locator('[data-account-key="ag-weekly"]');
            assert.deepEqual(await ag.locator('.ag-limit-value').allTextContents(), ['—', '80%', '60%', '—'], `Antigravity periods and unreported windows @${width}`);
          }
          // 8) No clipped text in Chinese.
          if (lang === 'zh-CN') { const c = await clipped(frame); assert.deepEqual(c, [], `${which} zh-CN @${width}: clipped text`); }
          if (shots && (lang === 'zh-CN' || lang === 'ko')) {
            await tab.screenshot({ path: path.join(shots, `host-${which}-${lang}-${width}.png`) });
            if (signedIn && lang === 'ko') {
              for (const key of ['weekly-only', 'claude-weekly', 'ag-weekly']) {
                await frame.locator(`[data-account-key="${key}"]`).screenshot({ path: path.join(shots, `${key}-${width}.png`) });
              }
              await frame.evaluate(() => window.scrollTo(0, 0));
            }
          }
        }
      }
      // 6) Menu fully inside the viewport at 412 and 1280; the button stays below y=88.
      await setConsoleLang(tab, 'zh-CN');
      await waitLang(frame, 'zh-CN');
      for (const width of [412, 1280]) {
        await tab.setViewportSize({ width, height: 900 });
        await settle(frame);
        await openMenu(frame, button);
        const box = await frame.evaluate(b => { const m = document.querySelector('.lang-menu').getBoundingClientRect(), r = document.querySelector(b).getBoundingClientRect(); return { m: { l: m.left, r: m.right, t: m.top, b: m.bottom }, btnTop: r.top, vw: document.documentElement.clientWidth, vh: document.documentElement.clientHeight }; }, button);
        assert(box.m.l >= 0 && box.m.r <= box.vw && box.m.t >= 0 && box.m.b <= box.vh, `${which} @${width}: menu clipped ${JSON.stringify(box)}`);
        assert(box.btnTop >= 88 - 0.5, `${which} @${width}: language button above 88px`);
        assert.deepEqual(await clipped(frame), [], `${which} @${width}: clipped text with the menu open`);
        if (shots) await tab.screenshot({ path: path.join(shots, `host-${which}-menu-zh-CN-${width}.png`) });
        await tab.keyboard.press('Escape');
        await frame.waitForSelector('.lang-menu', { state: 'detached' });
      }
      // 7) Zero CSP violations and page errors.
      assert.deepEqual(violations, [], 'CSP violations');
      assert.deepEqual(errors, [], 'page errors');
      await ctx.close();
    }
    console.log('PASS host: language sync and menu, provider quota periods and weekly locks, no clipped Chinese, 0 CSP violations, top >= 88px @412/820/1280');
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
