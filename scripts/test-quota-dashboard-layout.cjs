#!/usr/bin/env node
// Optional Playwright regression. PLAYWRIGHT_MODULE/BROWSER_EXECUTABLE override defaults.
const fs = require('node:fs');
const path = require('node:path');
const assert = require('node:assert/strict');
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const { seedKey } = require('./lib/seed-key.cjs');
const root = path.resolve(__dirname, '..');
const source = require('./lib/dashboard-html.cjs').readDashboardHTML(root);
const now = new Date().toISOString();
const reset = hours => new Date(Date.now() + hours * 3600000).toISOString();
const accounts = [
  // 0: recent codex card (active)
  {
    account_key: 'fixture-0', provider: 'codex',
    account: 'long-account-abcdefghijklmnopqrstuvwxyz@example.invalid',
    status: 'known', plan_type: 'plus', fetched_at: now,
    endpoint: {kind: 'direct'}, current_account: true, current_kind: 'recent', current_reason: 'recent_usage',
    operational: {},
    windows: [{name: 'primary', remaining_percent: 100, reset_at: reset(5)}, {name: 'secondary', remaining_percent: 100, reset_at: reset(120)}],
  },
  // 1: regular active codex (preserves recent vs regular active comparison)
  {
    account_key: 'fixture-1', provider: 'codex',
    account: 'regular-active@example.invalid',
    status: 'known', plan_type: 'plus', fetched_at: now,
    endpoint: {kind: 'direct'}, current_account: false,
    operational: {},
    windows: [{name: 'primary', remaining_percent: 80, reset_at: reset(5)}, {name: 'secondary', remaining_percent: 100, reset_at: reset(120)}],
  },
  // 2: antigravity card with multi-family windows
  {
    account_key: 'fixture-2', provider: 'antigravity',
    account: 'ag-account@example.invalid',
    status: 'known', fetched_at: now,
    endpoint: {kind: 'direct'}, current_account: false,
    operational: {},
    windows: ['Gemini five hour', 'Gemini weekly', 'Claude five hour', 'Claude weekly'].map((name, j) => ({name, remaining_percent: j ? 9 : 100, reset_at: reset(j ? 120 : 5)})),
  },
  // 3: Team normal account with reset_credits (available_count=1, nearest_expiry) and api_credits.balance=0
  {
    account_key: 'fixture-3', provider: 'codex',
    account: 'team-normal@example.invalid',
    status: 'known', plan_type: 'team', fetched_at: now,
    endpoint: {kind: 'direct'}, current_account: false,
    operational: {},
    reset_credits: { available_count: 1, nearest_expiry: reset(48) },
    api_credits: { balance: 0 },
    windows: [{name: 'primary', remaining_percent: 85, reset_at: reset(5)}, {name: 'secondary', remaining_percent: 100, reset_at: reset(120)}],
  },
  // 4: locked Plus account with credits
  {
    account_key: 'fixture-4', provider: 'codex',
    account: 'plus-locked@example.invalid',
    status: 'known', plan_type: 'plus', fetched_at: now,
    endpoint: {kind: 'direct'}, current_account: false,
    operational: {status: 'active'},
    api_credits: { balance: 15.5 },
    windows: [{name: 'primary', remaining_percent: 0, reset_after_seconds: 3600}, {name: 'secondary', remaining_percent: 60, reset_at: reset(120)}],
  },
  // 5: sparse / no-credits case: unknown status, no windows (quota-pending)
  {
    account_key: 'fixture-5', provider: 'codex',
    account: 'sparse-no-credits@example.invalid',
    status: 'unknown', plan_type: 'plus', fetched_at: now,
    endpoint: {kind: 'direct'}, current_account: false,
    operational: {},
    windows: [],
  },
  // 6: warning status account
  {
    account_key: 'fixture-6', provider: 'codex',
    account: 'warning-account@example.invalid',
    status: 'known', plan_type: 'plus', fetched_at: now,
    endpoint: {kind: 'direct'}, current_account: false,
    operational: {},
    windows: [{name: 'primary', remaining_percent: 15, reset_at: reset(5)}, {name: 'secondary', remaining_percent: 90, reset_at: reset(120)}],
  },
  // 7: disabled account
  {
    account_key: 'fixture-7', provider: 'codex',
    account: 'disabled-account@example.invalid',
    status: 'known', plan_type: 'plus', fetched_at: now,
    endpoint: {kind: 'direct'}, current_account: false,
    operational: {disabled: true},
    windows: [{name: 'primary', remaining_percent: 50, reset_at: reset(5)}, {name: 'secondary', remaining_percent: 100, reset_at: reset(120)}],
  },
];
// Include weekly exhaustion as well as the short-window locked Plus account.
accounts.push({ ...accounts[4], account_key: 'fixture-8', account: 'weekly-locked@example.invalid',
  windows: [{name: 'primary', remaining_percent: 60, reset_at: reset(5)}, {name: 'secondary', remaining_percent: 0, reset_at: reset(120)}] });
// Match the real endpoint and observation contract, including the user's two card shapes.
accounts.forEach((account, i) => {
  account.endpoint = i === 1 ? 'warp1' : 'direct';
  account.observed_at = new Date(Date.now() - (i === 4 ? 39 : 7) * 60000).toISOString();
});
(async () => {
  const browser = await chromium.launch({headless: true, executablePath: process.env.BROWSER_EXECUTABLE || undefined, args: ['--no-sandbox']});
  try {
    const page = await browser.newPage({reducedMotion: 'reduce', locale: 'ko-KR'});
    await seedKey(page);
    const errors = [];
    page.on('pageerror', error => errors.push(error.message));
    await page.route('**/*', async route => {
      const url = new URL(route.request().url());
      if (route.request().resourceType() === 'document') return route.fulfill({contentType: 'text/html', body: source});
      if (url.pathname.endsWith('/data/accounts')) return route.fulfill({contentType: 'application/json', body: JSON.stringify({configured: true, snapshot_complete: true, accounts})});
      const asset = path.join(root, 'internal/dashboard/assets', path.basename(url.pathname));
      if (fs.existsSync(asset) && fs.statSync(asset).isFile()) return route.fulfill({contentType: 'font/woff2', body: fs.readFileSync(asset)});
      return route.fulfill({status: 404, body: ''});
    });
    await page.goto('http://fixture.invalid/v0/resource/plugins/quota-monitor/dashboard');
    await page.waitForSelector('.account-card');
    await page.evaluate(() => document.fonts.ready);
    assert.equal(await page.locator('.availability-state').count(), 0, 'no redundant status chips');
    assert.equal(await page.locator('.badge-current').count(), 0, 'no redundant recent token-use badge');
    assert((await page.locator('.account-card[data-current="true"]').getAttribute('aria-label')).includes('최근 15분 내 토큰이 보고된 완료 요청의 계정'), 'recent token use remains accessible without a badge');
    const semantics = await page.locator('.account-card').evaluateAll(cards => cards.map(card => ({role: card.getAttribute('role'), label: card.getAttribute('aria-label'), state: card.getAttribute('data-availability'), locked: card.dataset.locked === 'true'})));
    semantics.forEach(card => {
      assert.equal(card.role, 'group');
      assert(card.label && card.label.includes(' · ') && card.state, 'state remains accessible beyond color');
    });
    assert.equal(semantics.filter(card => card.locked).length, 2, 'short and weekly exhaustion must mark the card locked');
    assert(semantics.filter(card => card.locked).every(card => ['limited', 'unavailable'].includes(card.state)), 'gray presentation must preserve availability semantics');
    const lockedChecks = await page.locator('.account-card[data-locked="true"]').evaluateAll(cards => cards.map(card => {
      const style = getComputedStyle(card);
      const regions = [...card.children].map(child => getComputedStyle(child));
      const time = card.querySelector('.quota-lock-time');
      return { background: style.backgroundColor, filters: regions.map(region => region.filter), countdown: getComputedStyle(time).color, opacity: style.opacity, pointerEvents: style.pointerEvents };
    }));
    for (const locked of lockedChecks) {
      assert.equal(locked.background, 'rgb(236, 239, 243)', 'locked card must use disabled gray surface');
      assert(locked.filters.every(filter => filter === 'grayscale(1)'), 'locked card regions must desaturate provider, quota, freshness and credits');
      assert.equal(locked.countdown, 'rgb(71, 85, 105)', 'locked reset countdown must use readable gray');
      assert.equal(locked.opacity, '1', 'locked content must not lose contrast through opacity');
      assert.notEqual(locked.pointerEvents, 'none', 'locked card must remain selectable and tooltips accessible');
    }
    assert(await page.locator('.badge-provider').count() > 0 && await page.locator('.badge-endpoint').count() > 0, 'provider and endpoint context remains');
    const surfaceChecks = await page.evaluate(() => {
      const states = {
        active: ['#e7f3eb', '#f5faf7'], pending: ['#eaf2f8', '#f7fafc'],
        warning: ['#f8f0d9', '#fcfaf3'], limited: ['#faeadc', '#fcf8f4'],
        unavailable: ['#f8e5e5', '#fcf6f6'], stale: ['#e9eff7', '#f6f8fb'],
        disabled: ['#eceff3', '#f8f9fb'], unknown: ['#eff3f7', '#f8fafc'],
      };
      const rgb = hex => hex.slice(1).match(/../g).map(value => parseInt(value, 16));
      const luminance = channels => channels.map(value => {
        const s = value / 255;
        return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
      }).reduce((sum, value, i) => sum + value * [0.2126, 0.7152, 0.0722][i], 0);
      return Object.entries(states).map(([state, [expected, previous]]) => {
        const card = document.createElement('div');
        card.className = 'account-card';
        card.setAttribute('data-availability', state);
        document.body.appendChild(card);
        const style = getComputedStyle(card);
        const actual = style.backgroundColor;
        const actualBorder = style.borderColor;
        const foreground = style.color.match(/\d+/g).slice(0, 3).map(Number);
        const contrast = (luminance(rgb(expected)) + 0.05) / (luminance(foreground) + 0.05);
        card.remove();
        return {state, actual, actualBorder, expected: 'rgb(' + rgb(expected).join(', ') + ')', contrast,
          darker: luminance(rgb(expected)) < luminance(rgb(previous))};
      });
    });
    for (const surface of surfaceChecks) {
      assert.equal(surface.actual, surface.expected, `${surface.state}: stronger status surface must render`);
      assert.equal(surface.actualBorder, 'rgb(228, 231, 236)', `${surface.state}: border must use the subtle default #e4e7ec`);
      assert(surface.darker, `${surface.state}: card background must be darker than before`);
      assert(surface.contrast >= 4.5, `${surface.state}: body text contrast must meet WCAG AA`);
    }
    for (const width of [1440, 1280, 1024, 900, 820, 768, 600, 560, 390, 360, 320]) {
      await page.setViewportSize({width, height: 1000});
      await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
      const result = await page.evaluate(() => {
        const rect = el => el.getBoundingClientRect();
        const alignments = [];
        for (const group of document.querySelectorAll('.quota-values')) {
          const rows = [...group.querySelectorAll('.quota-value-row')];
          if (rows.length < 2) continue;
          const a = rect(rows[0]), b = rect(rows[1]);
          if (Math.abs(a.top - b.top) > 1) {
            for (const selector of ['.quota-value-label', '.quota-value-number', '.quota-value-reset']) {
              const x = rect(rows[0].querySelector(selector)), y = rect(rows[1].querySelector(selector));
              alignments.push({selector, left: Math.abs(x.left - y.left), right: Math.abs(x.right - y.right)});
            }
          }
        }
        for (const group of document.querySelectorAll('.ag-family-limits')) {
          const values = [...group.querySelectorAll('.ag-limit-value')];
          const resets = [...group.querySelectorAll('.ag-limit-reset')];
          if (values.length > 1) {
            alignments.push({selector: '.ag-limit-value', right: Math.abs(rect(values[0]).right - rect(values[1]).right), left: 0});
            alignments.push({selector: '.ag-limit-reset', right: Math.abs(rect(resets[0]).right - rect(resets[1]).right), left: 0});
          }
        }
        const current = document.querySelector('.account-card[data-current="true"]');
        const regular = document.querySelector('.account-card:not([data-current])[data-availability="' + current.getAttribute('data-availability') + '"]');
        const currentStyle = getComputedStyle(current), regularStyle = getComputedStyle(regular);
        const recentBorder = {
          width: currentStyle.borderTopWidth,
          color: currentStyle.borderTopColor,
          preservesSurface: currentStyle.backgroundColor === regularStyle.backgroundColor,
          horizontalInset: parseFloat(currentStyle.borderLeftWidth) + parseFloat(currentStyle.paddingLeft),
          regularHorizontalInset: parseFloat(regularStyle.borderLeftWidth) + parseFloat(regularStyle.paddingLeft),
          verticalInset: parseFloat(currentStyle.borderTopWidth) + parseFloat(currentStyle.paddingTop),
          regularVerticalInset: parseFloat(regularStyle.borderTopWidth) + parseFloat(regularStyle.paddingTop),
        };
        const nonCurrentCards = [...document.querySelectorAll('.account-card:not([data-current="true"])')];
        const nonCurrentBorders = nonCurrentCards.map(c => {
          const s = getComputedStyle(c);
          return { color: s.borderTopColor, width: s.borderTopWidth };
        });

        // Structure validations: .card-heading grouping header and freshness, .card-quota wrapping quota block
        const headingChecks = [...document.querySelectorAll('.account-card')].map(c => {
          const heading = c.querySelector(':scope > .card-heading');
          const header = heading ? heading.querySelector(':scope > .card-header') : null;
          const freshness = heading ? heading.querySelector(':scope > .card-freshness') : null;
          return { hasHeading: !!heading, hasHeader: !!header, hasFreshness: !!freshness };
        });

        const quotaWrappers = [...document.querySelectorAll('.account-card')].map(c => {
          const quota = c.querySelector(':scope > .card-quota');
          const style = quota ? getComputedStyle(quota) : null;
          const block = quota && quota.firstElementChild;
          const topSpace = block ? rect(block).top - rect(quota).top : 0;
          const bottomSpace = block ? rect(quota).bottom - rect(block).bottom : 0;
          return {
            balancedSpace: !!block && Math.abs(topSpace - bottomSpace) <= 1.5,
            hasQuotaWrapper: !!quota,
            isCentered: style ? style.justifyContent === 'center' : false,
            isFlexColumn: style ? (style.display === 'flex' && style.flexDirection === 'column') : false,
          };
        });

        // Credit summary checks: direct final card child, margin-top: 0, bottom within padding, no dead space, no overlap
        const creditCards = [...document.querySelectorAll('.account-card')].filter(c => c.querySelector('.credit-summary'));
        const creditSummaryChecks = creditCards.map(c => {
          const lastChild = c.lastElementChild;
          const isCredit = lastChild && lastChild.classList.contains('credit-summary');
          const creditRect = lastChild ? rect(lastChild) : null;
          const cardRect = rect(c);
          const cardStyle = getComputedStyle(c);
          const paddingBottom = parseFloat(cardStyle.paddingBottom);
          const borderBottom = parseFloat(cardStyle.borderBottomWidth);
          const creditStyle = getComputedStyle(lastChild);
          const bottomGap = cardRect.bottom - borderBottom - paddingBottom - creditRect.bottom;
          const prevSibling = lastChild.previousElementSibling;
          const prevRect = prevSibling ? rect(prevSibling) : null;
          const noOverlap = prevRect ? (creditRect.top >= prevRect.bottom - 1) : true;
          return {
            isDirectFinalChild: isCredit,
            marginTopZero: creditStyle.marginTop === '0px',
            bottomWithinPadding: Math.abs(bottomGap) <= 1.5,
            noOverlap,
          };
        });

        const heights = [...document.querySelectorAll('.account-card')].map(el => rect(el).height);
        const clips = [...document.querySelectorAll('.quota-value-label,.quota-value-number,.quota-value-reset,.ag-limit-label,.ag-limit-value,.ag-limit-reset,.credit-reset-expiry-text,.credit-api-summary')].filter(el => el.clientWidth && (el.scrollWidth > el.clientWidth + 1 || el.scrollHeight > el.clientHeight + 1)).map(el => el.className);
        return {alignments, heights, clips, recentBorder, nonCurrentBorders, headingChecks, quotaWrappers, creditSummaryChecks, overflow: document.documentElement.scrollWidth > innerWidth + 1};
      });
      assert(!result.overflow && !result.clips.length, `overflow/clipping at ${width}: ${JSON.stringify(result.clips)}`);
      assert.equal(result.recentBorder.width, '2px', `${width}px: recent account border must be 2px`);
      assert.equal(result.recentBorder.color, 'rgb(37, 99, 235)', `${width}px: recent account border must be distinct blue`);
      assert(result.recentBorder.preservesSurface, `${width}px: recent usage must preserve the status background`);
      assert.equal(result.recentBorder.horizontalInset, result.recentBorder.regularHorizontalInset, `${width}px: horizontal content alignment must stay unchanged`);
      assert.equal(result.recentBorder.verticalInset, result.recentBorder.regularVerticalInset, `${width}px: vertical content alignment must stay unchanged`);

      // Only recent usage gets a strong outline; all other borders stay subtle.
      for (const b of result.nonCurrentBorders) {
        assert.equal(b.color, 'rgb(228, 231, 236)', `${width}px: non-current border must be subtle #e4e7ec`);
        assert.equal(b.width, '1px', `${width}px: non-current border must be thin`);
      }
      const lockTitles = await page.locator('.quota-lock-title').evaluateAll(titles => titles.map(title => {
        const icon = title.querySelector('.quota-lock-icon'), label = title.querySelector('span');
        if (!icon || !label) return null;
        const i = icon.getBoundingClientRect(), l = label.getBoundingClientRect();
        return { sameLine: Math.abs((i.top + i.height / 2) - (l.top + l.height / 2)) <= 1,
          gap: l.left - i.right, size: i.width, standalone: !!title.parentElement.querySelector(':scope > .quota-lock-icon') };
      }));
      assert.equal(lockTitles.length, 2, `${width}px: both locked fixtures must have a title`);
      for (const title of lockTitles) {
        assert(title && title.sameLine && title.gap >= 4 && title.gap <= 6 && title.size === 14 && !title.standalone,
          `${width}px: small lock icon must sit immediately beside the restriction title`);
      }

      // Structure assertions: card-heading & card-quota
      for (const h of result.headingChecks) {
        assert(h.hasHeading && h.hasHeader && h.hasFreshness, `${width}px: card must group header and freshness in .card-heading`);
      }
      for (const q of result.quotaWrappers) {
        assert(q.hasQuotaWrapper, `${width}px: card must have .card-quota wrapper`);
        assert(q.isCentered && q.isFlexColumn, `${width}px: .card-quota must be centered flex column`);
        assert(q.balancedSpace, `${width}px: spare quota height must be balanced above and below its content`);
      }

      // Credit summary assertions: direct final child, margin-top:0, bottom anchored within padding, no overlaps
      assert(result.creditSummaryChecks.length > 0, `${width}px: must have cards with credit summary`);
      for (const cs of result.creditSummaryChecks) {
        assert(cs.isDirectFinalChild, `${width}px: credit-summary must be direct final card child`);
        assert(cs.marginTopZero, `${width}px: credit-summary margin-top must be 0`);
        assert(cs.bottomWithinPadding, `${width}px: credit-summary must anchor to bottom within padding`);
        assert(cs.noOverlap, `${width}px: credit-summary must not overlap preceding siblings`);
      }
      assert(Math.max(...result.heights) - Math.min(...result.heights) <= 1, `unequal cards at ${width}`);
      result.alignments.forEach(row => assert(row.left <= 1 && row.right <= 1, `${width}px misaligned ${JSON.stringify(row)}`));
      assert(result.alignments.length > 0, 'alignment assertions must inspect actual quota rows');
      if (process.env.SCREENSHOT_DIR) {
        fs.mkdirSync(process.env.SCREENSHOT_DIR, {recursive: true});
        await page.screenshot({path: path.join(process.env.SCREENSHOT_DIR, 'quota-' + width + '.png'), fullPage: true});
      }
      console.log(`PASS ${width}px: stacked quota labels/percentages/resets and family limits aligned, uniform cards, no clipping`);
    }
    // Exercise border semantics and replacement in the real DOM for every
    // official provider, not only the initial recent Codex fixture.
    const borderTransitions = await page.evaluate(() => {
      const failures = [];
      const check = (condition, message) => { if (!condition) failures.push(message); };
      for (const provider of ['codex', 'antigravity', 'claude']) {
        const fixture = {
          account_key: 'border-' + provider, provider, account: 'border@example.invalid',
          status: 'known', observed_at: new Date().toISOString(), endpoint: 'direct',
          current_account: true, current_kind: 'recent', current_reason: 'recent_usage',
          windows: [{name: provider === 'antigravity' ? 'Gemini five hour' : 'primary', remaining_percent: 80}],
        };
        if (provider === 'claude') fixture.windows.push({name: 'overage', remaining_percent: 0});
        cachedAccounts = [fixture];
        renderAccounts(cachedAccounts);
        let card = document.querySelector('.account-card');
        check(card.dataset.current === 'true', provider + ': audited use must receive border');
        check(getComputedStyle(card).borderTopColor === 'rgb(37, 99, 235)', provider + ': recent border must be blue');
        check(getComputedStyle(card).borderTopWidth === '2px', provider + ': recent border must be 2px');
        for (const metadata of [
          {current_account: false}, {current_kind: 'candidate'}, {current_reason: 'priority_candidate'},
          {current_account: undefined, current_kind: undefined, current_reason: undefined},
        ]) {
          cachedAccounts = [{...fixture, ...metadata}];
          renderAccounts(cachedAccounts);
          card = document.querySelector('.account-card');
          check(!card.hasAttribute('data-current'), provider + ': removed or non-audited metadata must clear old border');
          check(getComputedStyle(card).borderTopWidth === '1px', provider + ': cleared border must return to 1px');
          check(getComputedStyle(card).borderTopColor === 'rgb(228, 231, 236)', provider + ': cleared border must return to subtle gray');
        }
        cachedAccounts = [fixture, {...fixture, account_key: 'next-' + provider, current_account: false}];
        renderAccounts(cachedAccounts);
        cachedAccounts = cachedAccounts.map((account, i) => ({...account, current_account: i === 1}));
        renderAccounts(cachedAccounts);
        const selected = [...document.querySelectorAll('.account-card[data-current="true"]')];
        check(selected.length === 1 && selected[0].dataset.accountKey === 'next-' + provider, provider + ': selection change must move, not duplicate, border');
      }
      return failures;
    });
    assert.deepEqual(borderTransitions, [], 'all providers must apply, clear, and move recent-use borders consistently');
    console.log('PASS all providers: recent borders apply, clear, and move without stale or duplicate outlines');
    assert.deepEqual(errors, []);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
