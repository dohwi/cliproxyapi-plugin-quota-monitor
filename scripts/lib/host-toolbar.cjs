// Reproduce the host's fixed top-right tools even when testing standalone HTML.
// No management UI DOM or CSS is changed by the plugin to avoid this overlap.
const assert = require('node:assert/strict');

// The dashboard keeps one header row: brand on the left, language/theme on the
// right. The console draws its own floating buttons over the iframe's top edge,
// so the header must start below --dashboard-top-offset at every width.
module.exports = async function verifyLanguagePlacement(page, panelSelector) {
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const result = await page.evaluate(() => {
    let host = document.getElementById('fixture-host-tools');
    if (!host) {
      host = document.createElement('div');
      host.id = 'fixture-host-tools';
      host.setAttribute('aria-hidden', 'true');
      host.style.cssText = 'position:fixed;top:0;left:0;right:0;height:76px;z-index:2147483647;background:#fff;border-bottom:1px solid #0002';
      document.body.appendChild(host);
    }
    const intersects = (a, b) => a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
    const group = document.getElementById('languageToggle');
    const heading = group.closest('.page-heading');
    const rect = group.getBoundingClientRect();
    const offset = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--dashboard-top-offset'));
    const headingRect = heading.getBoundingClientRect();
    return {
      inHeading: Boolean(heading),
      square: Math.abs(rect.width - rect.height) <= 1,
      hostOverlap: intersects(rect, host.getBoundingClientRect()),
      // The header row itself must clear the console's floating buttons.
      clearsHost: headingRect.top >= offset - 1 && !intersects(headingRect, host.getBoundingClientRect()),
      reachable: group.contains(document.elementFromPoint(rect.x + rect.width / 2, rect.y + rect.height / 2)),
      height: rect.height,
      expectedHeight: parseFloat(getComputedStyle(group).getPropertyValue('--filter-control-height')),
      label: group.getAttribute('aria-label'),
      lang: document.documentElement.lang,
    };
  });
  assert(result.inHeading, 'language switch lives in the page header actions');
  assert(!result.hostOverlap && result.reachable, 'language button must remain unobstructed by the console floating buttons');
  assert(result.clearsHost, 'the header row starts below --dashboard-top-offset');
  assert(result.height >= 30 && result.height <= result.expectedHeight + 1, `language switch shares filter control height: ${result.height} vs ${result.expectedHeight}`);
  assert(result.square, 'language toggle is a single square icon button');
  assert.equal(result.label, result.lang === 'en' ? 'Switch to Korean' : 'English로 전환', 'toggle label names its target language');
};

module.exports.switchTo = async (page, lang) => {
  if ((await page.evaluate(() => document.documentElement.lang)) !== lang) await page.click('#languageToggle');
  await page.waitForFunction(l => document.documentElement.lang === l, lang);
};
