// Reproduce the host's fixed top-right tools even when testing standalone HTML.
// No management UI DOM or CSS is changed by the plugin to avoid this overlap.
const assert = require('node:assert/strict');
module.exports = async function verifyLanguagePlacement(page, panelSelector) {
  await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
  const result = await page.evaluate(panelSelector => {
    let host = document.getElementById('fixture-host-tools');
    if (!host) {
      host = document.createElement('div');
      host.id = 'fixture-host-tools';
      host.setAttribute('aria-hidden', 'true');
      host.style.cssText = 'position:fixed;top:16px;right:12px;width:176px;height:52px;z-index:2147483647;background:#fff;border-radius:16px;box-shadow:0 8px 24px #0002';
      document.body.appendChild(host);
    }
    const group = document.getElementById('languageToggle');
    const panel = document.querySelector(panelSelector);
    const rect = group.getBoundingClientRect(), surface = panel.getBoundingClientRect();
    const intersects = (a, b) => a.left < b.right && a.right > b.left && a.top < b.bottom && a.bottom > b.top;
    const buttons = [group];
    return {
      insidePanel: panel.contains(group) && rect.left >= surface.left && rect.right <= surface.right + 1 && rect.top >= surface.top && rect.bottom <= surface.bottom + 1,
      outsideHeading: !group.closest('.page-heading'),
      square: Math.abs(rect.width - rect.height) <= 1,
      hostOverlap: intersects(rect, host.getBoundingClientRect()),
      reachable: buttons.every(button => {
        const r = button.getBoundingClientRect();
        return button.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2));
      }),
      controlOverlap: [...panel.querySelectorAll('.ui-select-trigger, #resetFiltersBtn, #fullRefreshBtn')].some(control => intersects(rect, control.getBoundingClientRect())),
      height: rect.height,
      expectedHeight: parseFloat(getComputedStyle(group).getPropertyValue('--filter-control-height')),
      label: group.getAttribute('aria-label'),
      lang: document.documentElement.lang,
    };
  }, panelSelector);
  assert(result.insidePanel && result.outsideHeading, 'language switch belongs inside the filter surface, never the header');
  assert(!result.hostOverlap && !result.controlOverlap && result.reachable, 'language buttons must remain unobstructed by host tools and filter controls');
  assert(result.height >= 30 && result.height <= result.expectedHeight + 1, `language switch shares filter control height: ${result.height} vs ${result.expectedHeight}`);
  assert(result.square, 'language toggle is a single square icon button');
  assert.equal(result.label, result.lang === 'en' ? 'Switch to Korean' : 'English로 전환', 'toggle label names its target language');
};

module.exports.switchTo = async (page, lang) => {
  if ((await page.evaluate(() => document.documentElement.lang)) !== lang) await page.click('#languageToggle');
  await page.waitForFunction(l => document.documentElement.lang === l, lang);
};
