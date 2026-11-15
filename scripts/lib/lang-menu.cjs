'use strict';
// Browser helpers for the shared language menu (initLangMenu in app.js).
// target: a Playwright Page or Frame; button: selector of a language icon button.
const LABELS = { en: 'Language: English', ko: '언어: 한국어', 'zh-CN': '语言：简体中文' };

async function openMenu(target, button) {
  await target.click(button);
  await target.waitForSelector('.lang-menu', { state: 'visible' });
}

// pref: 'auto' | 'en' | 'ko' | 'zh-CN'. Resolves once the menu closed and, for a pinned
// language, <html lang> shows it.
async function pickLanguage(target, button, pref) {
  await openMenu(target, button);
  await target.click(`.lang-menu-item[data-pref="${pref}"]`);
  await target.waitForSelector('.lang-menu', { state: 'detached' });
  if (pref !== 'auto') await target.waitForFunction(l => document.documentElement.lang === l, pref);
}

module.exports = { LABELS, openMenu, pickLanguage };
