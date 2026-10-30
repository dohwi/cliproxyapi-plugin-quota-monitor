var cachedAccounts = [];
var pollTimer = null;
var isFetching = false;
// A GET begun before a manual refresh finishes must never publish afterward.
var snapshotEpoch = 0;
var acceptedSnapshotVersion = null;
var isManualRefreshing = false;
var refreshingAccountKey = null;
var accountRefreshState = Object.create(null);
var manualRefreshCooldownUntil = 0;
var manualRefreshButtonTimer = null;
var manualRefreshSnapshotPending = false;
var MANUAL_REFRESH_TIMEOUT_MS = 60000;
// ── Management authentication ───────────────────────────────────────────────
// Quota data and the refresh action live under /v0/management/..., which the host
// protects with the management key. The page itself is an unauthenticated static
// shell, so the operator types the key once; it is kept only in this tab's session
// storage and sent as a Bearer header (never in a URL). The host counts every
// rejected request toward a 5-strike IP ban, so a key is stored only after the host
// accepted it and, without a key, no data request is sent at all.
var MGMT_BASE = '/v0/management/plugins/quota-monitor';
var KEY_STORAGE = 'cpa_quota_monitor_mgmt_key';
var mgmtKey = '';
var keyVerified = false;
function readStoredKey() { try { return sessionStorage.getItem(KEY_STORAGE) || ''; } catch (e) { return ''; } }
function writeStoredKey(key) { try { if (key) sessionStorage.setItem(KEY_STORAGE, key); else sessionStorage.removeItem(KEY_STORAGE); } catch (e) {} }
function authError() { var err = new Error('auth'); err.authRequired = true; return err; }
function noteKeyAccepted() { if (!keyVerified) { keyVerified = true; writeStoredKey(mgmtKey); } }
function showAuthGate(messageKey) {
  if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; }
  snapshotEpoch++;
  isFetching = false;
  isManualRefreshing = false;
  var gate = document.getElementById('authGate'), content = document.getElementById('dashboardContent');
  var message = document.getElementById('authMessage'), input = document.getElementById('authKeyInput');
  var errorBox = document.getElementById('errorBox'), staleBox = document.getElementById('staleNoticeBox');
  if (errorBox) { errorBox.style.display = 'none'; errorBox.textContent = ''; }
  if (staleBox) staleBox.style.display = 'none';
  var heading = document.getElementById('pageHeading');
  if (content) content.style.display = 'none';
  // The dashboard title and subtitle have nothing to describe until a key is accepted.
  if (heading) heading.style.display = 'none';
  if (gate) gate.style.display = 'block';
  if (message) { message.textContent = messageKey ? t(messageKey) : ''; message.style.display = messageKey ? 'block' : 'none'; }
  if (input) { input.value = ''; try { input.focus(); } catch (e) {} }
}
function hideAuthGate() {
  var gate = document.getElementById('authGate'), heading = document.getElementById('pageHeading');
  if (gate) gate.style.display = 'none';
  if (heading) heading.style.display = '';
}
function dropKeyAndPrompt() { mgmtKey = ''; keyVerified = false; writeStoredKey(''); showAuthGate('auth.invalid'); }
function authGateOpen() { var gate = document.getElementById('authGate'); return !!gate && gate.style.display !== 'none'; }
function initAuthGate() {
  mgmtKey = readStoredKey();
  keyVerified = !!mgmtKey; // only keys the host already accepted are ever stored
  var form = document.getElementById('authForm');
  if (!form) return;
  form.addEventListener('submit', function(e) {
    e.preventDefault();
    var input = document.getElementById('authKeyInput'), value = input ? String(input.value || '').trim() : '';
    if (!value) { showAuthGate('auth.empty'); return; }
    mgmtKey = value; keyVerified = false;
    if (input) input.value = '';
    hideAuthGate();
    cachedAccounts = [];
    loadDashboard(true);
  });
}
var isDisplayingStale = false;
var prevHealthFailures = null;
var resetCountdownTimer = null;
var quotaSnapshot = null;
var pendingQuotaChanges = null;
var selectedProvider = 'all';
var selectedEndpoint = 'all';
var FETCH_TIMEOUT_MS = 16000;
var POLL_IN_PROGRESS_MS = 2000;
// Bounded fallback intervals for adaptive polling (ms)
var POLL_FALLBACK_ACTIVE_MS = 30000;   // active/current data
var POLL_FALLBACK_HEALTHY_MS = 90000;  // healthy accounts
var POLL_FALLBACK_STALE_MS = 300000;   // pending/stale
var POLL_FALLBACK_SLOW_MS = 900000;    // all limited/unavailable/disabled
var POLL_MAX_MS = 900000;              // 15m cap

// Req 4: Memoize static provider SVG
// ── i18n ────────────────────────────────────────────────────────────────────
// The language preference is the only value this page persists (LANG_KEY). The key is
// shared with the other CLIProxyAPI plugin dashboards so one choice applies to all of them.
// Order: saved choice → browser language (ko* → ko, else en) → en.
var LANG_KEY = 'cpa_plugin_lang';
var SUPPORTED_LANGS = ['ko', 'en'];
var I18N = {
  ko: {
    'page.title': '계정별 남은 쿼터',
    'page.heading': '계정 쿼터',
    'page.subheading': '계정별 남은 쿼터와 초기화 시간을 한눈에 확인하세요.',
    'lang.label': '언어',
    'lang.toEn': 'English로 전환',
    'lang.toKo': '한국어로 전환',
    'unconfigured': '관리 키를 확인하는 중입니다. 이 페이지에서 관리 키로 로그인하면 자동으로 설정됩니다',
    'unconfigured.reason': '설정 대기 중: {r}',
    'retry': '다시 시도',
    'retry.aria': '데이터 다시 시도',
    'toolbar.aria': '대시보드 컨트롤',
    'filter.provider': '프로바이더',
    'filter.providerAria': 'Provider 필터',
    'filter.endpoint': '엔드포인트',
    'filter.endpointAria': 'Endpoint 필터',
    'filter.status': '상태',
    'filter.statusAria': '상태 필터',
    'filter.reset': '초기화',
    'filter.resetAria': '쿼터 필터 초기화',
    'all': '전체',
    'all.count': '전체 ({n})',
    'fleet.aria': '전체 가용률',
    'fleet.antigravity_gemini': '안티그래비티 · Gemini',
    'fleet.antigravity_claude': '안티그래비티 · Claude/GPT',
    'fleet.count': '{n}개 계정',
    'fleet.chipCount': '{n}개',
    'accounts.aria': '계정 목록',
    'st.active': '활성',
    'st.pending': '측정 대기',
    'st.limited': '제한됨',
    'st.warning': '주의',
    'st.error': '오류',
    'st.disabled': '비활성',
    'st.stale': 'Stale',
    'st.unknown': '미확인',
    'ep.unknown': 'Endpoint 미확인',
    'av.weekly': '주간 제한',
    'av.limitReached': '한도 도달',
    'av.runningLow': '소진 임박',
    'av.delayed': '정보 지연',
    'av.unavailable': '사용 불가',
    'av.unknown': '상태 미확인',
    'err.noData': '데이터 사용 불가',
    'auth.title': '관리 키가 필요합니다',
    'auth.desc': '쿼터 데이터는 관리 인증이 필요합니다. 관리 키는 이 탭의 세션에만 보관되며 주소나 서버에 기록되지 않습니다.',
    'auth.label': '관리 키',
    'auth.submit': '확인',
    'auth.empty': '관리 키를 입력하세요.',
    'auth.invalid': '관리 키가 올바르지 않거나 접근이 거부되었습니다. 다시 입력하세요.',
    'err.load': '데이터 로드 실패: {e}',
    'err.api': 'API 호출 실패 ({s})',
    'err.empty': '데이터 없음',
    'err.timeout': '요청 시간 초과',
    'stale.failed': '데이터 갱신 실패로 이전 데이터를 표시 중입니다.',
    'stale.failedDetail': '데이터 갱신 실패로 이전 데이터를 표시 중입니다 ({e})',
    'stale.failedReason': '데이터 갱신 실패로 이전 데이터를 표시 중입니다. ({e})',
    'stale.unconf': '환경 미설정으로 이전 데이터를 표시 중입니다',
    'stale.unconfDetail': '환경 미설정으로 이전 데이터를 표시 중입니다 ({e})',
    'stale.progress': '데이터 갱신 중입니다. 이전 데이터를 표시 중입니다.',
    'unit.d': '{n}일',
    'unit.h': '{n}시간',
    'unit.m': '{n}분',
    'unit.s': '{n}초',
    'reset.unknown': '초기화 미확인',
    'reset.reached': '초기화 시각 도달 · 갱신 대기',
    'reset.in': '{t} 후 초기화',
    'rel.now': '방금',
    'rel.s': '{n}초 전',
    'rel.m': '{n}분 전',
    'rel.h': '{n}시간 전',
    'rel.d': '{n}일 전',
    'fresh.noneTitle': '이 계정의 측정 시각이 아직 없습니다',
    'fresh.none': '아직 확인되지 않음',
    'fresh.previous': ' · 이전 데이터',
    'fresh.outdated': ' · 오래된 정보',
    'fresh.title': '계정 상태 확인: {t}',
    'fresh.last': '마지막 확인 {t}',
    'refresh.all': '전체 갱신',
    'refresh.allCooldown': '전체 갱신 ({s}s)',
    'refresh.running': '갱신 중…',
    'refresh.accountRunning': '계정 갱신 중…',
    'refresh.card': '새로고침',
    'refresh.cardCooldown': '새로고침 ({s}s)',
    'refresh.cardAria': '{name} 쿼터 새로고침',
    'refresh.done': '갱신 완료',
    'refresh.noAbort': '이 브라우저에서는 안전하게 갱신을 요청할 수 없습니다. 다른 브라우저에서 다시 시도해 주세요.',
    'refresh.rate': '갱신 요청 간격은 30초입니다. 잠시 후 다시 시도해 주세요.',
    'refresh.busy': '다른 수집이 진행 중입니다. 잠시 후 다시 시도해 주세요.',
    'refresh.removed': '계정이 목록에서 제거되었습니다. 전체 갱신으로 확인해 주세요.',
    'refresh.unsupported': '이 계정은 개별 갱신을 지원하지 않습니다.',
    'refresh.config': '플러그인 설정을 확인해 주세요.',
    'refresh.incomplete': '갱신이 완료되지 않았습니다. 이전 데이터를 유지합니다. 잠시 후 다시 시도해 주세요.',
    'refresh.partial': '수집이 일부만 완료되었습니다(최대 128개 계정). 잠시 후 다시 시도해 주세요.',
    'refresh.staleSuffix': ' 이전 데이터가 포함되어 있습니다. 잠시 후 다시 시도해 주세요.',
    'refresh.timeout': '갱신 요청 시간이 초과되었습니다. 서버에서는 아직 수집 중일 수 있습니다. 잠시 후 확인하고 다시 시도해 주세요.',
    'refresh.failed': '갱신 요청에 실패했습니다. 이전 데이터를 유지합니다. 잠시 후 다시 시도해 주세요.',
    'flash.default': '서버 데이터가 변경됨',
    'flash.aria': ' · 서버 업데이트로 변경됨',
    'flash.lock': '잠금 상태가 서버 업데이트로 변경됨',
    'flash.reset': '재설정 기준이 서버 업데이트로 변경됨',
    'flash.quota': '쿼터 값이 서버 업데이트로 변경됨',
    'flash.credit': '크레딧이 서버 업데이트로 변경됨',
    'grp.antigravity': 'Antigravity 쿼터',
    'grp.claude': 'Claude 쿼터',
    'grp.limits': '요청 한도',
    'grp.unknown': '미확인',
    'grp.default': '쿼터 그룹',
    'q.weekly': '주간 제한',
    'q.fiveHour': '5시간 제한',
    'q.oauthApps': 'OAuth 앱',
    'q.default': '쿼터',
    'q.locked': '잠금',
    'q.lockedAria': '{title} 잠금',
    'q.lockNote': '현재 계정 잠금',
    'q.weeklyLeft': '주간 잔여',
    'q.fiveLeft': '5시간 잔여',
    'q.leftAria': '{label} 잔여 {v}',
    'q.familyAria': '{f} 쿼터',
    'q.none': '쿼터 없음',
    'ag.name': '안티그래비티',
    'credit.reset': '리셋권 {n}장 보유',
    'credit.expiry': '가장 빠른 만료일: {d}',
    'credit.api': 'API 크레딧',
    'credit.unlimited': '무제한',
    'credit.balance': '잔액 {n}',
    'credit.available': '사용 가능',
    'credit.zero': '잔액 0',
    'live.shown': '계정 {n}개 표시 중',
    'empty.accounts': '등록된 계정이 없거나 필터 조건에 일치하는 항목이 없습니다.',
    'card.account': '계정',
    'card.current': ' · 최근 15분 내 토큰이 보고된 완료 요청의 계정 (실시간 진행 중 요청 아님)'
  },
  en: {
    'page.title': 'Remaining quota by account',
    'page.heading': 'Account Quota',
    'page.subheading': 'See each account\u2019s remaining quota and reset time at a glance.',
    'lang.label': 'Language',
    'lang.toEn': 'Switch to English',
    'lang.toKo': 'Switch to Korean',
    'unconfigured': 'Waiting for the management key. Sign in with it on this page and setup completes automatically',
    'unconfigured.reason': 'Waiting for setup: {r}',
    'retry': 'Retry',
    'retry.aria': 'Retry loading data',
    'toolbar.aria': 'Dashboard controls',
    'filter.provider': 'Provider',
    'filter.providerAria': 'Provider filter',
    'filter.endpoint': 'Endpoint',
    'filter.endpointAria': 'Endpoint filter',
    'filter.status': 'Status',
    'filter.statusAria': 'Status filter',
    'filter.reset': 'Reset',
    'filter.resetAria': 'Reset quota filters',
    'all': 'All',
    'all.count': 'All ({n})',
    'fleet.aria': 'Overall availability',
    'fleet.antigravity_gemini': 'Antigravity \u00b7 Gemini',
    'fleet.antigravity_claude': 'Antigravity \u00b7 Claude/GPT',
    'fleet.count': '{n} accounts',
    'fleet.count.one': '{n} account',
    'fleet.chipCount': '{n}',
    'accounts.aria': 'Account list',
    'st.active': 'Active',
    'st.pending': 'Pending',
    'st.limited': 'Limited',
    'st.warning': 'Warning',
    'st.error': 'Error',
    'st.disabled': 'Disabled',
    'st.stale': 'Stale',
    'st.unknown': 'Unknown',
    'ep.unknown': 'Endpoint unknown',
    'av.weekly': 'Weekly limit',
    'av.limitReached': 'Limit reached',
    'av.runningLow': 'Running low',
    'av.delayed': 'Data delayed',
    'av.unavailable': 'Unavailable',
    'av.unknown': 'Status unknown',
    'err.noData': 'Data unavailable',
    'auth.title': 'Management key required',
    'auth.desc': 'Quota data requires management authentication. The key is kept only in this tab\'s session and is never written to the address or the server.',
    'auth.label': 'Management key',
    'auth.submit': 'Continue',
    'auth.empty': 'Enter the management key.',
    'auth.invalid': 'The management key is invalid or access was denied. Enter it again.',
    'err.load': 'Failed to load data: {e}',
    'err.api': 'API request failed ({s})',
    'err.empty': 'No data',
    'err.timeout': 'Request timed out',
    'stale.failed': 'Refresh failed. Showing previous data.',
    'stale.failedDetail': 'Refresh failed. Showing previous data ({e})',
    'stale.failedReason': 'Refresh failed. Showing previous data. ({e})',
    'stale.unconf': 'Environment not configured. Showing previous data',
    'stale.unconfDetail': 'Environment not configured. Showing previous data ({e})',
    'stale.progress': 'Refresh in progress. Showing previous data.',
    'unit.d': '{n}d',
    'unit.h': '{n}h',
    'unit.m': '{n}m',
    'unit.s': '{n}s',
    'reset.unknown': 'Reset unknown',
    'reset.reached': 'Reset time reached \u00b7 awaiting update',
    'reset.in': 'Resets in {t}',
    'rel.now': 'Just now',
    'rel.s': '{n}s ago',
    'rel.m': '{n}m ago',
    'rel.h': '{n}h ago',
    'rel.d': '{n}d ago',
    'fresh.noneTitle': 'No observation time for this account yet',
    'fresh.none': 'Not checked yet',
    'fresh.previous': ' \u00b7 Previous data',
    'fresh.outdated': ' \u00b7 Outdated',
    'fresh.title': 'Account status checked: {t}',
    'fresh.last': 'Last checked {t}',
    'refresh.all': 'Refresh all',
    'refresh.allCooldown': 'Refresh all ({s}s)',
    'refresh.running': 'Refreshing\u2026',
    'refresh.accountRunning': 'Refreshing account\u2026',
    'refresh.card': 'Refresh',
    'refresh.cardCooldown': 'Refresh ({s}s)',
    'refresh.cardAria': 'Refresh quota for {name}',
    'refresh.done': 'Refresh complete',
    'refresh.noAbort': 'This browser cannot request a refresh safely. Please try another browser.',
    'refresh.rate': 'Refresh requests are limited to once every 30 seconds. Please try again shortly.',
    'refresh.busy': 'Another collection is in progress. Please try again shortly.',
    'refresh.removed': 'This account was removed from the list. Use Refresh all to check.',
    'refresh.unsupported': 'This account does not support individual refresh.',
    'refresh.config': 'Please check the plugin configuration.',
    'refresh.incomplete': 'Refresh did not complete. Keeping previous data. Please try again shortly.',
    'refresh.partial': 'Collection completed only partially (up to 128 accounts). Please try again shortly.',
    'refresh.staleSuffix': ' Some data is from a previous collection. Please try again shortly.',
    'refresh.timeout': 'The refresh request timed out. The server may still be collecting. Check again shortly and retry.',
    'refresh.failed': 'The refresh request failed. Keeping previous data. Please try again shortly.',
    'flash.default': 'Server data changed',
    'flash.aria': ' \u00b7 Changed by server update',
    'flash.lock': 'Lock state changed by server update',
    'flash.reset': 'Reset reference changed by server update',
    'flash.quota': 'Quota value changed by server update',
    'flash.credit': 'Credits changed by server update',
    'grp.antigravity': 'Antigravity quota',
    'grp.claude': 'Claude quota',
    'grp.limits': 'Request limits',
    'grp.unknown': 'Unknown',
    'grp.default': 'Quota group',
    'q.weekly': 'Weekly limit',
    'q.fiveHour': '5-hour limit',
    'q.oauthApps': 'OAuth apps',
    'q.default': 'Quota',
    'q.locked': 'Locked',
    'q.lockedAria': '{title} locked',
    'q.lockNote': 'Account currently locked',
    'q.weeklyLeft': 'Weekly left',
    'q.fiveLeft': '5-hour left',
    'q.leftAria': '{label}: {v} remaining',
    'q.familyAria': '{f} quota',
    'q.none': 'No quota',
    'ag.name': 'Antigravity',
    'credit.reset': 'Reset credits: {n}',
    'credit.expiry': 'Earliest expiry: {d}',
    'credit.api': 'API credits',
    'credit.unlimited': 'Unlimited',
    'credit.balance': 'Balance {n}',
    'credit.available': 'Available',
    'credit.zero': 'Balance 0',
    'live.shown': 'Showing {n} accounts',
    'live.shown.one': 'Showing 1 account',
    'empty.accounts': 'No accounts are registered, or none match the current filters.',
    'card.account': 'Account',
    'card.current': ' \u00b7 Account of a completed request with tokens reported in the last 15 minutes (not a live in-flight request)'
  }
};
var currentLang = 'ko';
var lastFleetEntries = null;

function normalizeLang(value) {
  var v = String(value || '').toLowerCase();
  if (v.indexOf('ko') === 0) return 'ko';
  if (v.indexOf('en') === 0) return 'en';
  return null;
}
function readStoredLang() {
  try { return normalizeLang(localStorage.getItem(LANG_KEY)); } catch (e) { return null; }
}
function writeStoredLang(lang) {
  try { localStorage.setItem(LANG_KEY, lang); } catch (e) {}
}
function detectLang() {
  var stored = readStoredLang();
  if (stored) return stored;
  try {
    var nav = typeof navigator !== 'undefined' ? navigator : null;
    var list = nav && (nav.languages && nav.languages.length ? nav.languages : [nav.language]);
    if (list && list[0]) return normalizeLang(list[0]) || 'en';
  } catch (e) {}
  return 'en';
}
currentLang = detectLang();

function t(key, params) {
  var table = I18N[currentLang] || I18N.ko;
  var s = table[key];
  if (s == null) s = I18N.ko[key];
  if (s == null) return key;
  if (params) s = s.replace(/\{(\w+)\}/g, function(m, k) { return params[k] != null ? String(params[k]) : m; });
  return s;
}
// Count-aware lookup: English has a '.one' variant where grammar needs it.
function tn(key, n, params) {
  var p = params || {};
  p.n = n;
  if (currentLang === 'en' && Number(n) === 1 && Object.prototype.hasOwnProperty.call(I18N.en, key + '.one')) key += '.one';
  return t(key, p);
}
// Text that must follow later language switches (notices set from async code).
function setI18nText(el, key, params) {
  if (!el) return;
  el.textContent = t(key, params);
  el._i18n = { key: key, params: params };
}
function eachNode(selector, fn) {
  if (typeof document === 'undefined' || !document.querySelectorAll) return;
  Array.prototype.forEach.call(document.querySelectorAll(selector), fn);
}
function applyStaticI18n() {
  if (typeof document === 'undefined') return;
  var root = document.documentElement;
  if (root && root.setAttribute) root.setAttribute('lang', currentLang);
  try { document.title = t('page.title'); } catch (e) {}
  eachNode('[data-i18n]', function(el) { el.textContent = t(el.getAttribute('data-i18n')); });
  eachNode('[data-i18n-aria]', function(el) { el.setAttribute('aria-label', t(el.getAttribute('data-i18n-aria'))); });
  // Enhanced selects copy their aria-label into a separate trigger button.
  eachNode('select[data-ui-select]', function(select) {
    var trigger = document.getElementById(select.id + 'Trigger');
    if (trigger) trigger.setAttribute('aria-label', select.getAttribute('aria-label') || '');
  });
  ['staleNoticeText', 'unconfiguredText'].forEach(function(id) {
    var el = document.getElementById(id);
    if (el && el._i18n) el.textContent = t(el._i18n.key, el._i18n.params);
  });
  // One toggle button: label/tooltip name the language a click switches to.
  eachNode('.lang-toggle', function(btn) {
    var label = t(currentLang === 'ko' ? 'lang.toEn' : 'lang.toKo');
    btn.setAttribute('aria-label', label);
    btn.setAttribute('title', label);
    btn.setAttribute('data-current-lang', currentLang);
  });
}
function setLanguage(lang, fromStorage) {
  var next = normalizeLang(lang);
  if (!next || next === currentLang) return;
  currentLang = next;
  if (!fromStorage) writeStoredLang(next);
  applyStaticI18n();
  if (lastFleetEntries) renderFleetAvailability(lastFleetEntries);
  if (cachedAccounts.length > 0) {
    populateProviderSegmented(cachedAccounts);
    applyFilters();
  }
  updateFilterTitles();
  updateManualRefreshButton();
}
// Translate the static markup as soon as it is parsed to avoid a flash of Korean.
try { applyStaticI18n(); } catch (e) {}
function initLanguageControls() {
  eachNode('.lang-toggle', function(btn) {
    btn.addEventListener('click', function() { setLanguage(currentLang === 'ko' ? 'en' : 'ko'); });
  });
  // Another plugin dashboard (or tab) changed the shared language: follow it.
  if (typeof window !== 'undefined' && window.addEventListener) {
    window.addEventListener('storage', function(e) {
      if (e && e.key === LANG_KEY) setLanguage(e.newValue, true);
    });
  }
  applyStaticI18n();
}

var _iconCache = {};

function esc(str) {
  if (str == null) return '';
  var s = String(str);
  if (typeof document !== 'undefined' && document.createElement) {
    var div = document.createElement('div');
    div.textContent = s;
    // DOM serialization escapes text delimiters, but leaves quotes intact.
    // This helper is also used inside double-quoted HTML attributes.
    if (typeof div.innerHTML === 'string' && div.innerHTML.length > 0) return div.innerHTML.replace(/"/g, '&quot;').replace(/'/g, '&#39;');
  }
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;').replace(/'/g, '&#39;');
}

function formatNum(n) {
  if (n == null || isNaN(n)) return '0';
  return Number(n).toLocaleString(currentLang === 'en' ? 'en-US' : 'ko-KR');
}

function fmtPct(x) {
  if (x == null || isNaN(x)) return '—';
  var s = Number(x).toFixed(1);
  if (s.slice(-2) === '.0') s = s.slice(0, -2);
  return s + '%';
}

function formatProviderLabel(p) {
  if (!p) return 'unknown';
  var lower = String(p).toLowerCase().trim();
  if (lower === 'claude') return 'claude';
  if (lower === 'openai compatible-b.ai' || lower === 'compatible-b.ai' || lower === 'b.ai') return 'b.ai';
  return p;
}

function getProviderKey(p) {
  if (!p) return 'unknown';
  var lower = String(p).toLowerCase().trim();
  if (lower === 'antigravity' || lower.indexOf('antigravity') !== -1) return 'antigravity';
  if (lower === 'codex' || lower.indexOf('codex') !== -1) return 'codex';
  if (lower === 'claude') return 'claude';
  return 'unknown';
}

function providerIcon(provider, size) {
  var s = (size && typeof size === 'number') ? size : 16;
  var cacheKey = getProviderKey(provider) + ':' + s;
  if (_iconCache[cacheKey]) return _iconCache[cacheKey];
  var result;
  var pk = getProviderKey(provider);
  if (pk === 'antigravity') {
    result = '<svg width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="currentColor" fill-rule="evenodd" aria-hidden="true" focusable="false"><path d="M21.751 22.607c1.34 1.005 3.35.335 1.508-1.508C17.73 15.74 18.904 1 12.037 1 5.17 1 6.342 15.74.815 21.1c-2.01 2.009.167 2.511 1.507 1.506 5.192-3.517 4.857-9.714 9.715-9.714 4.857 0 4.522 6.197 9.714 9.715z"></path></svg>';
  } else if (pk === 'codex') {
    result = '<svg width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="currentColor" fill-rule="evenodd" aria-hidden="true" focusable="false"><path d="M9.205 8.658v-2.26c0-.19.072-.333.238-.428l4.543-2.616c.619-.357 1.356-.523 2.117-.523 2.854 0 4.662 2.212 4.662 4.566 0 .167 0 .357-.024.547l-4.71-2.759a.797.797 0 00-.856 0l-5.97 3.473zm10.609 8.8V12.06c0-.333-.143-.57-.429-.737l-5.97-3.473 1.95-1.118a.433.433 0 01.476 0l4.543 2.617c1.309.76 2.189 2.378 2.189 3.948 0 1.808-1.07 3.473-2.76 4.163zM7.802 12.703l-1.95-1.142c-.167-.095-.239-.238-.239-.428V5.899c0-2.545 1.95-4.472 4.591-4.472 1 0 1.927.333 2.712.928L8.23 5.067c-.285.166-.428.404-.428.737v6.898zM12 15.128l-2.795-1.57v-3.33L12 8.658l2.795 1.57v3.33L12 15.128zm1.796 7.23c-1 0-1.927-.332-2.712-.927l4.686-2.712c.285-.166.428-.404.428-.737v-6.898l1.974 1.142c.167.095.238.238.238.428v5.233c0 2.545-1.974 4.472-4.614 4.472zm-5.637-5.303l-4.544-2.617c-1.308-.761-2.188-2.378-2.188-3.948A4.482 4.482 0 014.21 6.327v5.423c0 .333.143.571.428.738l5.947 3.449-1.95 1.118a.432.432 0 01-.476 0zm-.262 3.9c-2.688 0-4.662-2.021-4.662-4.519 0-.19.024-.38.047-.57l4.686 2.71c.286.167.571.167.856 0l5.97-3.448v2.26c0 .19-.07.333-.237.428l-4.543 2.616c-.619.357-1.356.523-2.117.523zm5.899 2.83a5.947 5.947 0 005.827-4.756C22.287 18.339 24 15.84 24 13.296c0-1.665-.713-3.282-1.998-4.448.119-.5.19-.999.19-1.498 0-3.401-2.759-5.947-5.946-5.947-.642 0-1.26.095-1.88.31A5.962 5.962 0 0010.205 0a5.947 5.947 0 00-5.827 4.757C1.713 5.447 0 7.945 0 10.49c0 1.666.713 3.283 1.998 4.448-.119.5-.19 1-.19 1.499 0 3.401 2.759 5.946 5.946 5.946.642 0 1.26-.095 1.88-.309a5.96 5.96 0 004.162 1.713z"></path></svg>';
  } else if (pk === 'claude') {
    result = agFamilyIcon('claude').replace('class="ag-family-icon"', 'width="'+s+'" height="'+s+'"');
  } else {
    result = '<svg width="' + s + '" height="' + s + '" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" focusable="false"><circle cx="12" cy="12" r="8"></circle></svg>';
  }
  _iconCache[cacheKey] = result;
  return result;
}

function effectiveUsed(w) {
  if (!w) return null;
  if (w.limit_reached === true || w.allowed === false) return 100;
  if (w.used_percent !== null && w.used_percent !== undefined && !isNaN(Number(w.used_percent))) {
    return Math.min(100, Math.max(0, Number(w.used_percent)));
  }
  if (w.remaining_percent !== null && w.remaining_percent !== undefined && !isNaN(Number(w.remaining_percent))) {
    return 100 - Math.min(100, Math.max(0, Number(w.remaining_percent)));
  }
  return null;
}

// Req 2: Canonical status — precedence: disabled>error>limited>warning>stale>active>pending>unknown
function getCanonicalStatus(acc) {
  if (!acc) return { key: 'unknown', label: t('st.unknown'), tone: 'chip-unknown' };
  var op = acc.operational || {};
  var windows = Array.isArray(acc.windows) ? acc.windows : [];

  if (op.disabled === true) return { key: 'disabled', label: t('st.disabled'), tone: 'chip-disabled' };

  var hasKnownWindows = acc.status === 'known' && windows.length > 0;
  var hasError = (acc.status === 'error' || (!hasKnownWindows && op.status === 'error'));
  var isStale  = (acc.stale === true || acc.status === 'stale');
  if (hasError) {
    if (isStale) return { key: 'stale', label: t('st.stale'), tone: 'chip-stale' };
    return { key: 'error', label: t('st.error'), tone: 'chip-error' };
  }

  var isLimited = false;
  var pKey = getProviderKey(acc.provider);
  for (var i = 0; i < windows.length; i++) {
    var w = windows[i] || {};
    if (pKey === 'antigravity' && !/gemini/i.test(String(w.name || ''))) continue;
    if (w.limit_reached === true || w.allowed === false) isLimited = true;
    var weu = effectiveUsed(w);
    if (weu !== null && weu >= 100) isLimited = true;
  }
  if (!hasKnownWindows) {
    if (op.unavailable === true) isLimited = true;
    if (acc.retry_after != null && Number(acc.retry_after) > 0) isLimited = true;
    if (op.next_retry_after != null && op.next_retry_after !== '') {
      var retrySec = Number(op.retry_after_seconds || op.retry_after || 0);
      if (retrySec > 0) isLimited = true;
      else { var rtDate = new Date(op.next_retry_after).getTime(); if (!isNaN(rtDate) && rtDate > Date.now()) isLimited = true; }
    }
    if (Number(op.retry_after_seconds || op.retry_after || 0) > 0) isLimited = true;
  }
  if (isLimited) return { key: 'limited', label: t('st.limited'), tone: 'chip-limited' };

  var isWarning = false;
  for (var j = 0; j < windows.length; j++) {
    var w2 = windows[j] || {};
    if (pKey === 'antigravity' && !/gemini/i.test(String(w2.name || ''))) continue;
    var weu2 = effectiveUsed(w2);
    if (weu2 !== null && weu2 >= 80) { isWarning = true; break; }
  }
  if (isWarning) return { key: 'warning', label: t('st.warning'), tone: 'chip-warning' };

  if (isStale) return { key: 'stale', label: t('st.stale'), tone: 'chip-stale' };

  if (acc.status === 'known' && windows.length > 0) {
    var hasValue = false;
    for (var k = 0; k < windows.length; k++) { if (effectiveUsed(windows[k]) !== null) { hasValue = true; break; } }
    if (hasValue) return { key: 'active', label: t('st.active'), tone: 'chip-active' };
    return { key: 'pending', label: t('st.pending'), tone: 'chip-pending' };
  }
  if (acc.status === 'known' && windows.length === 0) return { key: 'pending', label: t('st.pending'), tone: 'chip-pending' };

  return { key: 'unknown', label: t('st.unknown'), tone: 'chip-unknown' };
}

// Status rank is the secondary sort key within each endpoint group.
// active=0, pending=1, warning=2, stale=3, limited=4, error=5, disabled=6, unknown=7
function statusRank(acc) {
  var key = getCanonicalStatus(acc).key;
  var order = { active:0, pending:1, warning:2, stale:3, limited:4, error:5, disabled:6, unknown:7 };
  return (key in order) ? order[key] : 7;
}

// Endpoints are discovered from the data: "direct", "unknown", or any
// sanitized single-label proxy name the server reports (e.g. "warp1",
// "my-proxy"). Group order: Direct → named proxies (natural order) → Unknown.
var EP_BASE = { direct:0, unknown:1000000 };
function normalizeEndpoint(ep) {
  var k = String(ep || 'unknown').toLowerCase();
  return /^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$/.test(k) ? k : 'unknown';
}
function endpointCompare(a, b) {
  if (a === b) return 0;
  var ra = (a in EP_BASE) ? EP_BASE[a] : 1, rb = (b in EP_BASE) ? EP_BASE[b] : 1;
  if (ra !== rb) return ra - rb;
  var an = a.match(/^(.*?)(\d+)$/), bn = b.match(/^(.*?)(\d+)$/);
  if (an && bn && an[1] === bn[1]) return Number(an[2]) - Number(bn[2]);
  return a < b ? -1 : 1;
}
function endpointRank(ep) {
  return (ep in EP_BASE) ? EP_BASE[ep] : 1;
}

function filterAccountsByEndpoint(accounts, selectedEp) {
  if (!selectedEp || selectedEp === 'all') return accounts.slice();
  return accounts.filter(function(acc) {
    return normalizeEndpoint(acc && acc.endpoint) === selectedEp;
  });
}

// Names are shown exactly as reported ("direct", "warp1", "my-proxy"); only
// the "unknown" placeholder is localized.
function endpointLabel(ep) {
  return ep === 'unknown' ? t('ep.unknown') : ep;
}

function displayedQuotaWindows(acc) {
  var windows = acc && Array.isArray(acc.windows) ? acc.windows : [];
  // Disabled pay-as-you-go overage is not an exhausted subscription window.
  if (getProviderKey(acc && acc.provider) === 'claude') {
    return windows.filter(function(w) { return !/^overage(?:[-_ ]|$)/i.test(String(w && w.name || '')); });
  }
  return windows;
}

function quotaWindowRole(w, provider) {
  var name = String(w && w.name || '').toLowerCase();
  var minutes = Number(w && w.window_minutes);
  if (getProviderKey(provider) === 'codex') {
    if (/secondary|weekly/.test(name) || minutes === 10080) return 'long';
    if (/primary|five[- ]hour|5h/.test(name) || minutes === 300) return 'short';
  } else {
    if (/weekly/.test(name) || minutes === 10080) return 'long';
    if (/five[- ]hour|5h/.test(name) || minutes === 300) return 'short';
  }
  return 'other';
}

function displayedQuotaState(acc) {
  var wins = displayedQuotaWindows(acc);
  // Antigravity card-level background/status follows Gemini only. Claude keeps
  // its independent section lock without dimming or marking the whole card.
  // When Gemini windows are absent, do NOT fall back to Claude windows; return pending.
  if (getProviderKey(acc && acc.provider) === 'antigravity') {
    wins = wins.filter(function(w) { return /gemini/i.test(String(w && w.name || '')); });
  }
  var hasValue = false, minRemaining = null, shortRemaining = null, longRemaining = null;
  for (var i = 0; i < wins.length; i++) {
    var rem = quotaNumber(wins[i]);
    if (rem === null || !isFinite(rem)) continue;
    hasValue = true;
    if (minRemaining === null || rem < minRemaining) minRemaining = rem;
    var role = quotaWindowRole(wins[i], acc && acc.provider);
    if (role === 'short' && (shortRemaining === null || rem < shortRemaining)) shortRemaining = rem;
    if (role === 'long' && (longRemaining === null || rem < longRemaining)) longRemaining = rem;
  }
  if (!hasValue) return { key:'pending', label:t('st.pending'), remaining:null };
  // Stable state contract: weekly/long-term exhaustion blocks the account,
  // while 5-hour/short-term exhaustion is a temporary quota-limit state.
  if (longRemaining !== null && longRemaining <= 0) return { key:'unavailable', label:t('av.weekly'), remaining:longRemaining, reason:'weekly' };
  if (shortRemaining !== null && shortRemaining <= 0) return { key:'limited', label:t('av.limitReached'), remaining:shortRemaining, reason:'5h' };
  if (minRemaining < 20) return { key:'warning', label:t('av.runningLow'), remaining:minRemaining };
  return { key:'active', label:t('st.active'), remaining:minRemaining };
}

function getAvailabilityState(acc) {
  var op = acc && acc.operational || {};
  var canonical = getCanonicalStatus(acc);
  if (op.disabled === true || canonical.key === 'disabled') return { key:'disabled', label:t('st.disabled') };
  var pKey = getProviderKey(acc && acc.provider);
  var quotaState = displayedQuotaState(acc);
  if (pKey === 'antigravity') {
    // Antigravity whole-card state & background derives exclusively from Gemini windows.
    // Claude exhaustion/failure must not induce unavailable/error on the whole card.
    // If Gemini is exhausted (weekly or 5h), return that state.
    if (quotaState.key === 'unavailable' || quotaState.key === 'limited') return quotaState;
    // Missing Gemini windows => pending (never fall back to Claude).
    if (quotaState.key === 'pending') return quotaState;
    if (quotaState.key === 'warning') return quotaState;
    if (canonical.key === 'stale') return { key:'stale', label:t('av.delayed') };
    return quotaState; // active
  }
  if (quotaState.key === 'unavailable' || quotaState.key === 'limited') return quotaState;
  if (quotaState.key === 'active' || quotaState.key === 'warning') {
    if (canonical.key === 'stale') return { key:'stale', label:t('av.delayed') };
    return quotaState;
  }
  if (op.unavailable === true || op.status === 'error' || (acc && acc.status === 'error')) return { key:'unavailable', label:t('av.unavailable') };
  if (quotaState.key === 'pending') return quotaState;
  if (canonical.key === 'stale') return { key:'stale', label:t('av.delayed') };
  if (op.status === 'active') return quotaState;
  return { key:'unknown', label:t('av.unknown') };
}

// Sort accounts — provider group primary, endpoint secondary, status tertiary.
function providerRank(provider) {
  var key = getProviderKey(provider);
  return key === 'antigravity' ? 0 : key === 'codex' ? 1 : key === 'claude' ? 2 : 3;
}
function sortAccountsByEndpoint(accounts) {
  var arr = accounts.slice();
  arr.sort(function(a, b) {
    var pr = providerRank(a.provider) - providerRank(b.provider);
    if (pr !== 0) return pr;
    var er = endpointCompare(normalizeEndpoint(a.endpoint), normalizeEndpoint(b.endpoint));
    if (er !== 0) return er;
    var sr = statusRank(a) - statusRank(b);
    if (sr !== 0) return sr;
    var ak = String(a.account_key || '');
    var bk = String(b.account_key || '');
    return ak < bk ? -1 : ak > bk ? 1 : 0;
  });
  return arr;
}

function showError(msg) {
  var el = document.getElementById('errorBox');
  if (!el) return;
  el.innerHTML = '';
  var span = document.createElement('span');
  span.textContent = msg || t('err.noData');
  el.appendChild(span);
  var btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'btn btn-secondary';
  btn.style.marginLeft = '10px';
  btn.textContent = t('retry');
  btn.setAttribute('aria-label', t('retry.aria'));
  btn.onclick = function() { loadDashboard(false); };
  el.appendChild(btn);
  el.style.display = 'block';
}

function clearError() {
  var el = document.getElementById('errorBox');
  if (!el) return;
  el.style.display = 'none';
  el.innerHTML = '';
}

function humanizeSeconds(sec) {
  if (sec == null || isNaN(sec)) return '—';
  var s = Math.round(sec);
  if (s <= 0) return t('unit.s', { n: 0 });
  if (s >= 86400) { var d = Math.floor(s/86400); var rh = Math.floor((s%86400)/3600); return t('unit.d', { n: d })+(rh>0?' '+t('unit.h', { n: rh }):''); }
  if (s >= 3600)  { var h = Math.floor(s/3600);  var m = Math.floor((s%3600)/60);   return t('unit.h', { n: h })+(m>0?' '+t('unit.m', { n: m }):''); }
  if (s >= 60)    { var mn = Math.floor(s/60); var rs = s%60; return t('unit.m', { n: mn })+(rs>0?' '+t('unit.s', { n: rs }):''); }
  return t('unit.s', { n: s });
}

function resetSeconds(w) {
  if (!w) return null;
  if (w.reset_at) {
    var t = new Date(w.reset_at).getTime();
    if (!isNaN(t)) return Math.max(0, Math.round((t - Date.now()) / 1000));
  }
  if (w.reset_after_seconds !== null && w.reset_after_seconds !== undefined && !isNaN(Number(w.reset_after_seconds))) {
    return Math.max(0, Number(w.reset_after_seconds));
  }
  return null;
}

function formatResetCountdownSeconds(sec) {
  if (sec === null || sec === undefined || isNaN(sec)) return t('reset.unknown');
  if (Number(sec) <= 0) return t('reset.reached');
  return t('reset.in', { t: humanizeSeconds(sec) });
}

function formatRelativeReset(w) {
  if (!w) return t('reset.unknown');
  return formatResetCountdownSeconds(resetSeconds(w));
}

function formatResetExpiryKo(ts) {
  if (!ts) return '—';
  try {
    var d = new Date(ts);
    if (!isNaN(d.getTime())) {
      var hour24 = d.getHours();
      var hour12 = hour24 % 12 || 12;
      var period = hour24 < 12 ? '오전' : '오후';
      var minute = String(d.getMinutes()).padStart(2, '0');
      var second = String(d.getSeconds()).padStart(2, '0');
      return d.getFullYear()+'. '+(d.getMonth()+1)+'. '+d.getDate()+'. '+period+' '+hour12+':'+minute+':'+second;
    }
  } catch(e) {}
  return '—';
}

var EN_MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];
function formatResetExpiryEn(ts) {
  if (!ts) return '—';
  try {
    var d = new Date(ts);
    if (!isNaN(d.getTime())) {
      var hour24 = d.getHours();
      var hour12 = hour24 % 12 || 12;
      var minute = String(d.getMinutes()).padStart(2, '0');
      var second = String(d.getSeconds()).padStart(2, '0');
      return EN_MONTHS[d.getMonth()]+' '+d.getDate()+', '+d.getFullYear()+', '+hour12+':'+minute+':'+second+' '+(hour24 < 12 ? 'AM' : 'PM');
    }
  } catch(e) {}
  return '—';
}
function formatResetExpiry(ts) {
  return currentLang === 'en' ? formatResetExpiryEn(ts) : formatResetExpiryKo(ts);
}
function formatTimestamp(ts) {
  return formatResetExpiry(ts);
}

function relativeTime(date) {
  if (!date) return '';
  var diff = Math.round((Date.now() - date.getTime()) / 1000);
  if (diff < 5) return t('rel.now');
  if (diff < 60) return t('rel.s', { n: diff });
  if (diff < 3600) return t('rel.m', { n: Math.floor(diff/60) });
  if (diff < 86400) return t('rel.h', { n: Math.floor(diff/3600) });
  return t('rel.d', { n: Math.floor(diff/86400) });
}

function getAccountObservedTime(account) {
  if (!account) return null;
  var latest = 0;
  var candidates = [account.last_success, account.observed_at];
  if (account.windows && Array.isArray(account.windows)) {
    for (var w = 0; w < account.windows.length; w++) {
      if (account.windows[w] && account.windows[w].observed_at) candidates.push(account.windows[w].observed_at);
    }
  }
  for (var c = 0; c < candidates.length; c++) {
    if (!candidates[c]) continue;
    var t = new Date(candidates[c]).getTime();
    if (!isNaN(t) && t > latest) latest = t;
  }
  return latest > 0 ? new Date(latest) : null;
}

function renderAccountFreshness(acc) {
  var observed = getAccountObservedTime(acc);
  if (!observed) {
    return '<div class="card-freshness is-unknown" title="'+esc(t('fresh.noneTitle'))+'">'+esc(t('fresh.none'))+'</div>';
  }
  var ageSeconds = Math.max(0, Math.floor((Date.now() - observed.getTime()) / 1000));
  var freshnessClass = '';
  var suffix = '';
  var availability = getAvailabilityState(acc || {});
  if (acc && (acc.error || acc.stale === true || availability.key === 'stale' || availability.key === 'error')) {
    freshnessClass = ' is-failed';
    suffix = t('fresh.previous');
  } else if (ageSeconds >= 2700) {
    freshnessClass = ' is-old';
    suffix = t('fresh.outdated');
  } else if (ageSeconds >= 900) {
    freshnessClass = ' is-aging';
  }
  var exact = formatTimestamp(observed.toISOString());
  return '<div class="card-freshness'+freshnessClass+'" title="'+esc(t('fresh.title', { t: exact }))+'">'+esc(t('fresh.last', { t: relativeTime(observed) || t('rel.now') }))+esc(suffix)+'</div>';
}

// ── Adaptive one-shot setTimeout polling ──────────────────────────────────────
function computeAdaptivePollMs(nextPollHintMs, accounts) {
  // Server hints and fallback intervals remain the normal polling cadence.
  var pollMs = nextPollHintMs && nextPollHintMs > 0 ? Math.min(nextPollHintMs, POLL_MAX_MS) : null;
  // Fallback: classify accounts client-side when no server hint is available.
  var hasCurrent = false, hasActive = false, hasLimited = false, hasStale = false;
  var accs = accounts || [];
  for (var i = 0; i < accs.length; i++) {
    var acc = accs[i];
    if (acc && acc.current_account === true && acc.current_kind === 'recent' && acc.current_reason === 'recent_usage') hasCurrent = true;
    var av = getAvailabilityState(acc);
    if (av.key === 'active' || av.key === 'warning') hasActive = true;
    if (av.key === 'limited' || av.key === 'unavailable' || av.key === 'disabled') hasLimited = true;
    if (av.key === 'unknown' || av.key === 'pending') hasStale = true;
  }
  if (pollMs === null) {
    pollMs = hasCurrent ? POLL_FALLBACK_ACTIVE_MS :
             hasActive ? POLL_FALLBACK_HEALTHY_MS :
             hasStale ? POLL_FALLBACK_STALE_MS : POLL_FALLBACK_SLOW_MS;
  }
  // Only snapshot GETs are accelerated: quota reset deadlines never trigger probes.
  for (var j = 0; j < accs.length; j++) {
    var resetAcc = accs[j];
    if (!resetAcc || (resetAcc.operational || {}).disabled === true) continue;
    var wins = displayedQuotaWindows(resetAcc);
    for (var k = 0; k < wins.length; k++) {
      var w = wins[k];
      if (effectiveUsed(w) !== 100) continue;
      var sec;
      if (w.reset_at) {
        var resetAt = new Date(w.reset_at).getTime();
        if (!isFinite(resetAt)) continue;
        sec = (resetAt - Date.now()) / 1000;
      } else {
        if (w.reset_after_seconds == null || w.reset_after_seconds === '' || !isFinite(Number(w.reset_after_seconds))) continue;
        sec = resetSeconds(w);
      }
      if (sec === null || !isFinite(sec)) continue;
      var resetPollMs = sec > 0 ? Math.max(5000, (sec + 5) * 1000) : 30000;
      pollMs = Math.min(pollMs, resetPollMs);
    }
  }
  return pollMs;
}

function scheduleNextPoll(delayMs) {
  if (isManualRefreshing || manualRefreshSnapshotPending) delayMs = POLL_IN_PROGRESS_MS;
  if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; }
  if (delayMs <= 0) return;
  pollTimer = setTimeout(function() {
    pollTimer = null;
    if (document.hidden) return; // paused; will reload on visibilitychange
    if (!isFetching) loadDashboard(false);
  }, delayMs);
}

var manualRefreshStatusTimer = null;
function setManualRefreshStatus(message, dismissAfterMs) {
  if (manualRefreshStatusTimer !== null) {
    clearTimeout(manualRefreshStatusTimer);
    manualRefreshStatusTimer = null;
  }
  var status = document.getElementById('manualRefreshStatus');
  if (status) status.textContent = message;
  if (message && dismissAfterMs > 0) {
    manualRefreshStatusTimer = setTimeout(function() {
      manualRefreshStatusTimer = null;
      if (status) status.textContent = '';
    }, dismissAfterMs);
  }
}

function updateManualRefreshButton() {
  updateAccountRefreshButtons();
  if (manualRefreshButtonTimer) {
    clearTimeout(manualRefreshButtonTimer);
    manualRefreshButtonTimer = null;
  }
  var button = document.getElementById('fullRefreshBtn');
  if (!button) return;
  var seconds = Math.max(0, Math.ceil((manualRefreshCooldownUntil - Date.now()) / 1000));
  button.disabled = isManualRefreshing || seconds > 0;
  button.textContent = isManualRefreshing ? (refreshingAccountKey ? t('refresh.accountRunning') : t('refresh.running')) : seconds > 0 ? t('refresh.allCooldown', { s: seconds }) : t('refresh.all');
  button.setAttribute('aria-busy', isManualRefreshing ? 'true' : 'false');
  if (!isManualRefreshing && seconds > 0) {
    manualRefreshButtonTimer = setTimeout(updateManualRefreshButton, 1000);
  }
}

function refreshAllAccounts() { return refreshAccounts(null); }
function refreshAccount(accountKey) { return refreshAccounts(accountKey); }
async function refreshAccounts(accountKey) {
  updateManualRefreshButton();
  var cardState = accountKey ? (accountRefreshState[accountKey] || (accountRefreshState[accountKey] = { until: 0, message: '' })) : null;
  if (isManualRefreshing || Date.now() < (cardState ? cardState.until : manualRefreshCooldownUntil)) return;
  var report = function(message, dismissAfterMs) {
    if (cardState) { cardState.message = message; updateAccountRefreshButtons(); }
    else setManualRefreshStatus(message, dismissAfterMs);
  };
  var cooldown = function() {
    if (cardState) cardState.until = Date.now() + 30000;
    else manualRefreshCooldownUntil = Date.now() + 30000;
  };
  if (typeof AbortController === 'undefined') {
    report(t('refresh.noAbort'));
    return;
  }

  var authRejected = false;
  isManualRefreshing = true;
  refreshingAccountKey = accountKey;
  updateManualRefreshButton();
  report('');
  scheduleNextPoll(POLL_IN_PROGRESS_MS);
  var controller = new AbortController();
  var timedOut = false;
  // Keep the timeout active through response parsing, beyond the 51s management deadline.
  var timer = setTimeout(function() {
    timedOut = true;
    controller.abort();
  }, MANUAL_REFRESH_TIMEOUT_MS);
  try {
    // Authenticated management POST; this confirmed action is never polled.
    if (!mgmtKey) throw authError();
    var response = await fetch(MGMT_BASE + '/data/refresh?confirm=1' + (accountKey ? '&account_key=' + encodeURIComponent(accountKey) : ''), {
      method: 'POST',
      redirect: 'error',
      cache: 'no-store',
      credentials: 'omit',
      headers: { 'Authorization': 'Bearer ' + mgmtKey },
      signal: controller.signal
    });
    if (response.status === 401 || response.status === 403) throw authError();
    noteKeyAccepted();
    if (response.status === 429) {
      cooldown();
      report(t('refresh.rate'));
      return;
    }
    if (accountKey && [409, 404, 422, 503].indexOf(response.status) >= 0) {
      report(response.status === 409 ? t('refresh.busy') : response.status === 404 ? t('refresh.removed') : response.status === 422 ? t('refresh.unsupported') : t('refresh.config'));
      return;
    }
    if (!response.ok) throw new Error('refresh failed');
    var data = await response.json();
    if (!data || data.refreshed !== true || data.error) {
      if (cardState) cooldown();
      report(t('refresh.incomplete'));
      return;
    }
    cooldown();
    if (cardState && data.truncated !== true && data.stale !== true) {
      // Plain success is signalled by a brief check icon, not persistent card text.
      cardState.message = '';
      cardState.doneUntil = Date.now() + 2500;
      updateAccountRefreshButtons();
      return;
    }
    var message = data.truncated === true
      ? t('refresh.partial')
      : t('refresh.done');
    if (data.stale === true) message += t('refresh.staleSuffix');
    report(message, data.truncated === true || data.stale === true ? 0 : 3000);
  } catch (err) {
    if (err && err.authRequired) { authRejected = true; }
    else report(timedOut
      ? t('refresh.timeout')
      : t('refresh.failed'));
  } finally {
    clearTimeout(timer);
    isManualRefreshing = false;
    refreshingAccountKey = null;
    if (authRejected) { dropKeyAndPrompt(); return; }
    updateManualRefreshButton();
    snapshotEpoch++;
    manualRefreshSnapshotPending = true;
    if (isFetching) scheduleNextPoll(POLL_IN_PROGRESS_MS);
    else loadDashboard(false);
  }
}

var accountRefreshButtonTimer = null;
function updateAccountRefreshButtons() {
  if (accountRefreshButtonTimer) { clearTimeout(accountRefreshButtonTimer); accountRefreshButtonTimer = null; }
  if (!document.querySelectorAll) return;
  var ticking = false;
  document.querySelectorAll('[data-refresh-account]').forEach(function(button) {
    var key = button.getAttribute('data-refresh-account');
    var state = accountRefreshState[key] || { until: 0, message: '' };
    var seconds = Math.max(0, Math.ceil((state.until - Date.now()) / 1000));
    var busy = isManualRefreshing && refreshingAccountKey === key;
    button.disabled = isManualRefreshing || seconds > 0;
    var refreshLabel = busy ? t('refresh.running') : seconds > 0 ? t('refresh.cardCooldown', { s: seconds }) : t('refresh.card');
    button.title = refreshLabel;
    button.setAttribute('aria-busy', busy ? 'true' : 'false');
    var done = !busy && Date.now() < (state.doneUntil || 0);
    button.classList.toggle('is-done', done);
    if (done) button.title = t('refresh.done');
    ticking = ticking || seconds > 0 || done;
  });
  document.querySelectorAll('[data-refresh-status]').forEach(function(el) {
    var state = accountRefreshState[el.getAttribute('data-refresh-status')];
    el.textContent = state ? state.message : '';
  });
  if (ticking) accountRefreshButtonTimer = setTimeout(updateAccountRefreshButtons, 1000);
}
function initManualRefreshControls() {
  var button = document.getElementById('fullRefreshBtn');
  if (button) button.onclick = refreshAllAccounts;
  updateManualRefreshButton();
}

function startResetCountdownTick() {
  if (resetCountdownTimer) clearTimeout(resetCountdownTimer);
  resetCountdownTimer = setTimeout(function tickCountdown() {
    if (!document.hidden && cachedAccounts.length > 0) {
      applyFilters();
    }
    resetCountdownTimer = setTimeout(tickCountdown, 30000);
  }, 30000);
}

// ─────────────────────────────────────────────────────────────────────────────

function captureRenderState() {
  var active = document.activeElement;
  var path = null;
  if (active) {
    if (active.id) path = { id: active.id };
    else {
      var cursor = active;
      while (cursor && cursor.getAttribute) {
        var key = cursor.getAttribute('data-account-key');
        if (key) {
          path = { accountKey: key };
          var child = active, indices = [];
          while (child && child !== cursor && child.parentNode) {
            var siblings = child.parentNode.children || [];
            var idx = Array.prototype.indexOf.call(siblings, child);
            indices.unshift(idx);
            child = child.parentNode;
          }
          path.indices = indices;
          break;
        }
        cursor = cursor.parentNode;
      }
    }
  }
  return {
    scrollTop: (typeof window !== 'undefined' && typeof window.pageYOffset === 'number') ? window.pageYOffset : ((document.documentElement && document.documentElement.scrollTop) || (document.body && document.body.scrollTop) || 0),
    active: path,
    selectionStart: active && typeof active.selectionStart === 'number' ? active.selectionStart : null,
    selectionEnd: active && typeof active.selectionEnd === 'number' ? active.selectionEnd : null
  };
}

function restoreRenderState(saved) {
  if (!saved) return;
  var target = null;
  if (saved.active && saved.active.id) target = document.getElementById(saved.active.id);
  if (!target && saved.active && saved.active.accountKey && document.querySelector) {
    target = document.querySelector('[data-account-key="' + String(saved.active.accountKey).replace(/"/g, '\\"') + '"]');
    var indices = saved.active.indices || [];
    for (var i = 0; target && i < indices.length; i++) {
      target = target.children && target.children[indices[i]];
    }
  }
  if (target && target.focus && document.activeElement !== target) {
    try { target.focus(); } catch(e) {}
    if (saved.selectionStart !== null && target.setSelectionRange) {
      try { target.setSelectionRange(saved.selectionStart, saved.selectionEnd); } catch(e) {}
    }
  }
  if (typeof window !== 'undefined' && window.scrollTo) {
    try { window.scrollTo(0, saved.scrollTop); } catch(e) {}
  } else if (document.documentElement) {
    document.documentElement.scrollTop = saved.scrollTop;
  }
}

function renderSkeletons() {
  var content = document.getElementById('dashboardContent');
  var container = document.getElementById('accountsList');
  if (content) { content.style.display = 'block'; content.setAttribute('aria-busy','true'); }
  if (!container) return;
  var html = '';
  for (var i = 0; i < 8; i++) {
    html += '<div class="skeleton-card" aria-hidden="true">'+
      '<div class="skeleton-meta"><div class="skeleton-line" style="width:42%;height:16px;"></div><div class="skeleton-line" style="width:20%;height:13px;"></div></div>'+
      '<div class="skeleton-line" style="width:58%;height:9px;"></div>'+
      '<div class="skeleton-quota-stack">'+
        '<div class="skeleton-family"><div class="skeleton-line" style="width:14px;height:14px;border-radius:3px;"></div><div class="skeleton-family-values"><div class="skeleton-line" style="height:14px;"></div><div class="skeleton-line" style="height:14px;"></div></div></div>'+
        '<div class="skeleton-family"><div class="skeleton-line" style="width:14px;height:14px;border-radius:3px;"></div><div class="skeleton-family-values"><div class="skeleton-line" style="height:14px;"></div><div class="skeleton-line" style="height:14px;"></div></div></div>'+
      '</div></div>';
  }
  container.innerHTML = html;
}

function acceptSnapshotVersion(data) {
  var generation = data && typeof data.generation === 'number' && isFinite(data.generation) ? data.generation : null;
  var collections = data && data.health && typeof data.health.collections === 'number' && isFinite(data.health.collections) ? data.health.collections : null;
  var observed = Date.parse(data && (data.collected_at || data.cached_at || data.cachedAt) || '');
  var version = { generation: isFinite(generation) ? generation : null, collections: isFinite(collections) ? collections : null, observed: isFinite(observed) ? observed : null };
  var previous = acceptedSnapshotVersion;
  if (previous) {
    if (version.observed !== null && previous.observed !== null && version.observed < previous.observed) return false;
    // Collection counters/generations restart with the process. A newer server
    // timestamp is authoritative even when those counters reset on deployment.
    var newerObservation = version.observed !== null && previous.observed !== null && version.observed > previous.observed;
    if (!newerObservation && version.generation !== null && previous.generation !== null && version.generation < previous.generation) return false;
    var sameGeneration = version.generation === previous.generation || version.generation === null || previous.generation === null;
    if (sameGeneration) {
      if (!newerObservation && version.collections !== null && previous.collections !== null && version.collections < previous.collections) return false;
    }
  }
  // Responses omitting optional ordering metadata must not erase our watermark.
  acceptedSnapshotVersion = {
    generation: version.generation !== null ? version.generation : previous && previous.generation,
    collections: version.collections !== null ? version.collections : previous && previous.collections,
    observed: version.observed !== null ? version.observed : previous && previous.observed
  };
  return true;
}

function loadDashboard(isInitial) {
  if (isFetching) return;
  if (!mgmtKey) {
    // Keep what the operator is typing if the gate is already open (e.g. tab refocus).
    if (!authGateOpen()) showAuthGate('');
    return;
  }
  var requestEpoch = snapshotEpoch;
  manualRefreshSnapshotPending = false;
  isFetching = true;
  clearError();
  var content = document.getElementById('dashboardContent');
  if (cachedAccounts.length === 0) {
    renderSkeletons();
  } else if (content) {
    content.setAttribute('aria-busy', 'true');
  }

  var controller = typeof AbortController !== 'undefined' ? new AbortController() : null;
  var fetchTimer = controller ? setTimeout(function() {
    try { controller.abort(); } catch(e) {}
  }, FETCH_TIMEOUT_MS) : null;

  function clearFetchTimer() {
    if (fetchTimer) { clearTimeout(fetchTimer); fetchTimer = null; }
  }

  function finishFetch() {
    clearFetchTimer();
    isFetching = false;
    var c = document.getElementById('dashboardContent');
    // A rejected key opened the auth gate; the dashboard must stay hidden behind it.
    if (c && !authGateOpen()) {
      c.style.display = 'block';
      c.setAttribute('aria-busy', 'false');
    }
  }

  function handleFailure(errText) {
    if (cachedAccounts.length > 0) {
      isDisplayingStale = true;
      var staleBox = document.getElementById('staleNoticeBox');
      var staleText = document.getElementById('staleNoticeText');
      if (staleBox) {
        setI18nText(staleText, errText ? 'stale.failedDetail' : 'stale.failed', { e: errText });
        staleBox.style.display = 'flex';
      }
      applyFilters();
    } else {
      showError(t('err.load', { e: errText || 'unknown' }));
      renderAccounts([]);
    }
    scheduleNextPoll(POLL_FALLBACK_STALE_MS);
  }

  fetch(MGMT_BASE + '/data/accounts', { cache: 'no-store', credentials: 'omit', redirect: 'error', headers: { 'Authorization': 'Bearer ' + mgmtKey }, signal: controller ? controller.signal : undefined })
    .then(function(res) {
      clearFetchTimer();
      if (res.status === 401 || res.status === 403) throw authError();
      if (!res.ok) throw new Error(t('err.api', { s: res.status }));
      return res.json().then(function(data) { noteKeyAccepted(); return data; });
    })
    .then(function(data) {
      if (!data) {
        handleFailure(t('err.empty'));
        return null;
      }
      // Resource data is deliberately snapshot-only. The first privileged
      // collection is owned by authenticated management /status, /accounts,
      // or POST /refresh; a browser resource must never become an implicit
      // credential/probe trigger.
      return data;
    })
    .then(function(data) {
      if (!data) return;
      if (requestEpoch !== snapshotEpoch || !acceptSnapshotVersion(data)) {
        scheduleNextPoll(POLL_IN_PROGRESS_MS);
        return;
      }

      var unconfBox = document.getElementById('unconfiguredBox');
      var unconfText = document.getElementById('unconfiguredText');
      if (data.configured === false) {
        if (unconfText) setI18nText(unconfText, data.reason ? 'unconfigured.reason' : 'unconfigured', { r: data.reason });
        if (unconfBox) unconfBox.style.display = 'block';

        if (cachedAccounts.length > 0) {
          isDisplayingStale = true;
          var staleBox = document.getElementById('staleNoticeBox');
          var staleText = document.getElementById('staleNoticeText');
          if (staleBox) {
            if (staleText) setI18nText(staleText, data.reason ? 'stale.unconfDetail' : 'stale.unconf', { e: data.reason });
            staleBox.style.display = 'flex';
          }
          applyFilters();
        } else {
          renderAccounts([]);
        }
        scheduleNextPoll(POLL_FALLBACK_STALE_MS);
        return;
      } else if (unconfBox) {
        unconfBox.style.display = 'none';
      }

      if (data.error) {
        handleFailure(data.error);
        return;
      }

      var incomingAccounts = Array.isArray(data.accounts) ? data.accounts : [];
      if (Array.isArray(data.availability)) { lastFleetEntries = data.availability; renderFleetAvailability(data.availability); }

      // Empty startup remains atomic. After the first complete snapshot, an
      // in-progress refresh may expose account-local progressive updates while
      // the backend keeps committed order/current selection fixed until finalize.
      if (data.snapshot_complete !== true) {
        if (cachedAccounts.length > 0) {
          isDisplayingStale = true;
          var staleBox = document.getElementById('staleNoticeBox');
          var staleText = document.getElementById('staleNoticeText');
          if (staleBox) {
            if (staleText) setI18nText(staleText, 'stale.progress');
            staleBox.style.display = 'flex';
          }
          applyFilters();
        } else {
          renderSkeletons();
        }
        scheduleNextPoll(POLL_IN_PROGRESS_MS);
        return;
      }

      if (data.collection_in_progress === true) {
        var savedRenderState = captureRenderState();
        cachedAccounts = incomingAccounts;
        populateProviderSegmented(cachedAccounts);
        applyFilters();
        restoreRenderState(savedRenderState);
        scheduleNextPoll(POLL_IN_PROGRESS_MS);
        return;
      }

      var health = data.health || {};
      var lastCat = (typeof health.last_category === 'string') ? health.last_category : '';
      var curFailures = (typeof health.failures === 'number') ? health.failures : 0;
      var failuresIncreased = (prevHealthFailures !== null && curFailures > prevHealthFailures);
      prevHealthFailures = curFailures;

      var isFailureCategory = (lastCat !== '' && lastCat !== 'success');
      var isExplicitFailureReason = (data.reason && data.reason !== 'cache_expired' && data.reason !== 'not_collected' && data.reason !== '');
      var isRealFailure = failuresIncreased || isFailureCategory || isExplicitFailureReason;

      isDisplayingStale = isRealFailure;

      var sBox = document.getElementById('staleNoticeBox');
      if (sBox) {
        if (isRealFailure) {
          var sText = document.getElementById('staleNoticeText');
          var showReason = data.reason && data.reason !== 'cache_expired';
          if (sText) setI18nText(sText, showReason ? 'stale.failedReason' : 'stale.failed', { e: data.reason });
          sBox.style.display = 'flex';
        } else {
          sBox.style.display = 'none';
        }
      }

      pendingQuotaChanges = diffQuotaSnapshots(quotaSnapshot, incomingAccounts);
      quotaSnapshot = buildQuotaSnapshot(incomingAccounts);
      cachedAccounts = incomingAccounts;
      populateProviderSegmented(cachedAccounts);
      applyFilters();
      pendingQuotaChanges = null;

      var hint = (typeof data.next_poll_hint_ms === 'number') ? data.next_poll_hint_ms : 0;
      scheduleNextPoll(computeAdaptivePollMs(hint, cachedAccounts));
    })
    .catch(function(err) {
      clearFetchTimer();
      if (err && err.authRequired) { dropKeyAndPrompt(); return; }
      if (requestEpoch !== snapshotEpoch) { scheduleNextPoll(POLL_IN_PROGRESS_MS); return; }
      var msg = err && err.message ? err.message : (err && err.name === 'AbortError' ? t('err.timeout') : 'unknown');
      handleFailure(msg);
    })
    .finally(function() {
      finishFetch();
    });
}

// Stable fallback identity prevents a one-account array from resetting duplicate keys to index 0.
function quotaAccountKeys(accounts) { var seen={}, keys=[]; (accounts||[]).forEach(function(acc,index){acc=acc||{}; var base=String(acc.account_key || acc.endpoint || acc.name || 'account'); var n=seen[base]||0;seen[base]=n+1;keys[index]=base+'#'+n;}); return keys; }
function quotaWindowMap(acc) { var seen={}, out={}; (acc.windows||[]).forEach(function(w){w=w||{};var base=String(w.name||w.kind||'window'),n=seen[base]||0;seen[base]=n+1;out[base+'#'+n]=[quotaNumber(w),String(w.reset_at||w.reset_time||w.resets_at||'')];});return out; }
// Only server snapshots enter this comparison; countdown/filter renders never do.
function buildQuotaSnapshot(accounts) { var out={}, keys=quotaAccountKeys(accounts); (accounts||[]).forEach(function(acc,index) { acc=acc||{}; var key=keys[index], credits=acc.api_credits||{}, reset=acc.reset_credits||{}, observed=getAccountObservedTime(acc); out[key]={observed_at:observed?observed.toISOString():'',availability:getAvailabilityState(acc).key,locked:!!lockedWindowInfo(acc,getAvailabilityState(acc)),windows:quotaWindowMap(acc),credits:[credits.balance,credits.has_credits,credits.unlimited,reset.available_count,reset.applicable_available_count,reset.nearest_expiry]}; }); return out; }
function quotaTone(before, after, inverse) { var a=Number(before),b=Number(after);if(isFinite(a)&&isFinite(b)&&a!==b)return (b>a)!==!!inverse?'flash-good':'flash-bad';return 'flash-neutral'; }
function diffQuotaSnapshots(previous, accounts) { if(!previous)return {};var changed={},next=buildQuotaSnapshot(accounts),keys=quotaAccountKeys(accounts);keys.forEach(function(key){var old=previous[key],now=next[key];if(!old||!now)return;var fields={};if(old.observed_at!==now.observed_at&&now.observed_at)fields.sync='flash-sync';if(old.availability!==now.availability)fields.availability='flash-neutral';if(old.locked!==now.locked)fields.lock=now.locked?'flash-danger':'flash-good';Object.keys(old.windows).forEach(function(windowKey){var before=old.windows[windowKey],after=now.windows[windowKey];if(!after||before[0]!==after[0])fields['window:'+windowKey]=quotaTone(before[0],after&&after[0],false);if(after&&before[1]!==after[1])fields['reset:'+windowKey]='flash-neutral';});old.credits.forEach(function(v,i){if(v!==now.credits[i])fields['credit:'+i]=quotaTone(v,now.credits[i],false);});if(Object.keys(fields).length)changed[key]=fields;});return changed; }
function applyValueFlash(el, tone, label) { if(!el||!tone||!el.classList)return;var tones=['flash-good','flash-bad','flash-danger','flash-neutral'];if(el._flashTimer)clearTimeout(el._flashTimer);tones.forEach(function(c){el.classList.remove(c);});el.classList.remove('value-flash');var aria=el.getAttribute('aria-label'),title=el.getAttribute('title');el.classList.add('value-flash',tone);el.setAttribute('title',label||t('flash.default'));el.setAttribute('aria-label',(aria||el.textContent||'')+t('flash.aria'));el._flashTimer=setTimeout(function(){el.classList.remove('value-flash');tones.forEach(function(c){el.classList.remove(c);});if(title===null)el.removeAttribute('title');else el.setAttribute('title',title);if(aria===null)el.removeAttribute('aria-label');else el.setAttribute('aria-label',aria);el._flashTimer=null;},1000); }
function strongestQuotaTone(changes) { var rank={'flash-sync':0,'flash-neutral':1,'flash-good':2,'flash-bad':3,'flash-danger':4},best='flash-sync';Object.keys(changes||{}).forEach(function(key){var tone=changes[key];if((rank[tone]||0)>(rank[best]||0))best=tone;});return best; }
function applyCardPulse(card, tone) { if(!card||!tone||!card.classList)return;var tones=['flash-sync','flash-good','flash-bad','flash-danger','flash-neutral'];if(card._cardPulseTimer)clearTimeout(card._cardPulseTimer);tones.forEach(function(c){card.classList.remove(c);});card.classList.remove('card-pulse');void card.offsetWidth;card.classList.add('card-pulse',tone);card._cardPulseTimer=setTimeout(function(){card.classList.remove('card-pulse');tones.forEach(function(c){card.classList.remove(c);});card._cardPulseTimer=null;},1400); }
function decorateQuotaChanges(card, accountKey) { var changes=pendingQuotaChanges&&pendingQuotaChanges[String(accountKey)];if(!changes)return;applyCardPulse(card,strongestQuotaTone(changes));applyValueFlash(card.querySelector('.quota-lock-state'),changes.lock,t('flash.lock'));card.querySelectorAll('.ag-family-lock').forEach(function(el){applyValueFlash(el,changes.lock,t('flash.lock'));});var seen={};card.querySelectorAll('[data-window-name]').forEach(function(el){var base=el.getAttribute('data-window-name'),n=seen[base]||0;if(el.getAttribute('data-window-role')==='value'){seen[base]=n+1;}var key=base+'#'+n;applyValueFlash(el,changes[el.getAttribute('data-window-role')+':'+key],el.getAttribute('data-window-role')==='reset'?t('flash.reset'):t('flash.quota'));});if(Object.keys(changes).some(function(k){return k.indexOf('credit:')===0;}))applyValueFlash(card.querySelector('.credit-summary'),'flash-neutral',t('flash.credit')); }

function populateProviderSegmented(accounts) {
  var select = document.getElementById('providerFilter');
  if (!select) return;
  var accs = accounts || [];
  var provMap = {};
  var provCounts = {};
  for (var i = 0; i < accs.length; i++) {
    var pKey = formatProviderLabel(accs[i].provider || 'unknown');
    provMap[pKey] = true;
    provCounts[pKey] = (provCounts[pKey] || 0) + 1;
  }
  if (selectedProvider !== 'all' && !provMap[selectedProvider]) selectedProvider = 'all';
  while (select.firstChild) select.removeChild(select.firstChild);
  function appendOption(value, text) {
    var option = document.createElement('option');
    option.value = value;
    option.textContent = text;
    select.appendChild(option);
  }
  appendOption('all', t('all.count', { n: accs.length }));
  Object.keys(provMap).sort().forEach(function(provider) {
    appendOption(provider, provider + ' (' + (provCounts[provider] || 0) + ')');
  });
  select.value = selectedProvider;
  updateFilterTitles();
}

function updateFilterTitles() {
  ['providerFilter', 'endpointFilter', 'statusFilter'].forEach(function(id) {
    var select = document.getElementById(id);
    var option = select && select.options && select.options[select.selectedIndex];
    if (select) select.title = option ? option.textContent : '';
  });
}

function initLoadingState() {
  if (cachedAccounts.length === 0) renderSkeletons();
}

function resetFilters() {
  selectedProvider = 'all';
  selectedEndpoint = 'all';
  var provider = document.getElementById('providerFilter');
  var endpoint = document.getElementById('endpointFilter');
  var status = document.getElementById('statusFilter');
  if (provider) provider.value = 'all';
  if (endpoint) endpoint.value = 'all';
  if (status) status.value = 'all';
  applyFilters();
}

function initFilterControls() {
  var provider = document.getElementById('providerFilter');
  var endpoint = document.getElementById('endpointFilter');
  var status = document.getElementById('statusFilter');
  var reset = document.getElementById('resetFiltersBtn');
  if (provider) provider.addEventListener('change', function() { selectedProvider = provider.value || 'all'; applyFilters(); });
  if (endpoint) endpoint.addEventListener('change', function() { selectedEndpoint = endpoint.value || 'all'; applyFilters(); });
  if (status) status.addEventListener('change', applyFilters);
  if (reset) reset.addEventListener('click', resetFilters);
}

// Fleet availability is computed server-side (availability.go) so this header
// and the Herdr tab bar can never disagree. Always reflects every account,
// independent of the provider/status filters below.
// Server labels are shared with the host tab bar and stay untouched; the
// dashboard localizes known families by key and falls back to the server text.
function fleetLabel(e) {
  var key = 'fleet.' + (e && e.key);
  var text = t(key);
  return text !== key ? text : ((e && (e.label || e.key)) || '');
}
function renderFleetAvailability(entries) {
  var el = document.getElementById('fleetAvailability');
  if (!el) return;
  var list = Array.isArray(entries) ? entries : [];
  // Antigravity's Gemini and Claude/GPT families collapse into one "AGY" chip.
  var chips = [];
  var agy = null;
  for (var i = 0; i < list.length; i++) {
    var e = list[i] || {};
    var rem = Number(e.remaining_percent);
    if (!isFinite(rem)) continue;
    var count = Number(e.accounts) || 0;
    if (e.key === 'antigravity_gemini' || e.key === 'antigravity_claude') {
      if (!agy) {
        agy = { label: 'AGY', provider: 'antigravity', title: '', values: [], accounts: 0 };
        chips.push(agy);
      }
      agy.values.push(rem);
      agy.accounts = Math.max(agy.accounts, count);
      agy.title += (agy.title ? ' / ' : '') + fleetLabel(e) + ' ' + (Math.round(rem * 10) / 10) + '%';
      continue;
    }
    chips.push({ label: fleetLabel(e), provider: String(e.key || ''), title: fleetLabel(e) + ' · ' + tn('fleet.count', count), values: [rem], accounts: count });
  }
  var html = '';
  for (var k = 0; k < chips.length; k++) {
    var c = chips[k];
    var vals = '';
    for (var v = 0; v < c.values.length; v++) {
      if (v > 0) vals += '<span class="fleet-chip-sep" aria-hidden="true">·</span>';
      vals += '<span style="color:' + remainingTone(c.values[v]) + '">' + esc(String(Math.round(c.values[v] * 10) / 10)) + '%</span>';
    }
    html += '<li class="fleet-chip" title="' + esc(c.title + (c.values.length > 1 ? ' · ' + tn('fleet.count', c.accounts) : '')) + '"><span class="fleet-chip-label"><span class="fleet-chip-icon" aria-hidden="true">' + providerIcon(c.provider, 16) + '</span><span class="fleet-chip-name">' + esc(c.label) + '</span></span>' +
      '<span class="fleet-chip-value">' + vals + '</span>' +
      '<span class="fleet-chip-count">' + esc(t('fleet.chipCount', { n: c.accounts })) + '</span></li>';
  }
  el.innerHTML = html;
}

// Endpoint UI is only useful when accounts actually span more than one
// network path. With a single distinct endpoint (e.g. all Direct) the filter
// and per-card labels are hidden as noise.
function presentEndpoints() {
  var seen = {};
  for (var i = 0; i < cachedAccounts.length; i++) {
    seen[normalizeEndpoint(cachedAccounts[i] && cachedAccounts[i].endpoint)] = true;
  }
  return seen;
}

// Rebuild the filter options from the endpoints actually present so any
// proxy name configured by the user shows up without code changes.
function rebuildEndpointOptions(sel, present) {
  if (!sel || !sel.appendChild) return;
  var keys = Object.keys(present).sort(endpointCompare);
  var sig = keys.join('|');
  if (sel.getAttribute && sel.getAttribute('data-ep-sig') === sig) return;
  while (sel.firstChild) sel.removeChild(sel.firstChild);
  var all = document.createElement('option');
  all.value = 'all';
  all.textContent = t('all');
  all.setAttribute('data-i18n', 'all');
  sel.appendChild(all);
  keys.forEach(function(k) {
    var o = document.createElement('option');
    o.value = k;
    o.textContent = endpointLabel(k);
    sel.appendChild(o);
  });
  if (sel.setAttribute) sel.setAttribute('data-ep-sig', sig);
  sel.value = present[selectedEndpoint] ? selectedEndpoint : 'all';
}

function hasMultipleEndpoints() {
  return Object.keys(presentEndpoints()).length > 1;
}

function syncEndpointFilterVisibility() {
  var present = presentEndpoints();
  var show = Object.keys(present).length > 1;
  var sel = document.getElementById('endpointFilter');
  var field = sel && sel.closest ? sel.closest('.filter-endpoint-field') : null;
  if (field) field.hidden = !show;
  rebuildEndpointOptions(sel, present);
  if (!show || (selectedEndpoint !== 'all' && !present[selectedEndpoint])) {
    selectedEndpoint = 'all';
    if (sel) sel.value = 'all';
  }
  return show;
}

function applyFilters() {
  syncEndpointFilterVisibility();
  updateFilterTitles();
  var stEl = document.getElementById('statusFilter');
  var st = (stEl && stEl.value) ? stEl.value : 'all';
  var filtered = [];
  for (var i = 0; i < cachedAccounts.length; i++) {
    var acc = cachedAccounts[i];
    if (selectedProvider !== 'all') {
      var normP = formatProviderLabel(acc.provider);
      if (normP !== selectedProvider && acc.provider !== selectedProvider) continue;
    }
    if (st !== 'all') {
      var canonKey = getCanonicalStatus(acc).key;
      var availKey = getAvailabilityState(acc).key;
      if (canonKey !== st && availKey !== st) continue;
    }
    filtered.push(acc);
  }

  filtered = filterAccountsByEndpoint(filtered, selectedEndpoint);
  filtered = sortAccountsByEndpoint(filtered);
  renderAccounts(filtered);
  if (typeof window !== 'undefined' && window.motionConsumeEnter) window.motionConsumeEnter();
}

function quotaNumber(w) {
  if (!w) return null;
  if (w.remaining_percent != null && !isNaN(Number(w.remaining_percent))) return Math.max(0, Math.min(100, Number(w.remaining_percent)));
  var used = effectiveUsed(w);
  return used === null ? null : 100 - used;
}

// Numeric percentage tone by remaining quota — finite/clamped, no gradients.
function remainingTone(rem) {
  if (rem === null || rem === undefined || !isFinite(rem)) return 'var(--pct-unknown)';
  var r = Math.max(0, Math.min(100, rem));
  if (r >= 60) return 'var(--pct-high)';
  if (r >= 30) return 'var(--pct-med)';
  if (r >= 10) return 'var(--pct-warn)';
  return 'var(--pct-low)';
}

function groupQuotaWindows(windows, provider) {
  var list = Array.isArray(windows) ? windows : [];
  var key = getProviderKey(provider);
  var groups = [], byKey = Object.create(null), seen = Object.create(null);
  function add(k, label, kind, win) {
    var g = byKey[k];
    if (!g) { g = { key:k, label:label||t('grp.default'), windows:[] }; byKey[k]=g; groups.push(g); }
    g.windows.push({ kind:kind, window:win });
  }
  list.forEach(function(w, i) {
    w = w || {};
    var name = String(w.name||'').trim(), low = name.toLowerCase(), kind = null, group = name;
    if (key === 'antigravity') {
      var family = /gemini/i.test(low) ? 'gemini' : (/claude|gpt/i.test(low) ? 'claude' : 'other');
      var duration = /weekly/i.test(low)||Number(w.window_minutes)===10080 ? 'weekly' :
                     (/five hour|5h/i.test(low)||Number(w.window_minutes)===300 ? 'five-hour' : null);
      kind = duration ? family+'-'+duration : family;
      group = 'antigravity';
    } else if (key === 'claude') {
      low = low.replace(/_/g, '-').replace(/^seven-day(?=-|$)/, 'weekly');
      if (/^(5h|5-hour|5-hours|five-hours)$/.test(low)) low = 'five-hour';
      if (low === '7d') low = 'weekly';
      kind = /^(five-hour|weekly(?:-(?:opus|sonnet|oauth-apps|cowork|sessions))?)$/.test(low) ? low :
             (Number(w.window_minutes)===300 ? 'five-hour' : (Number(w.window_minutes)===10080 ? low || 'weekly' : null));
      group = 'claude';
      if (kind) { add('claude', t('grp.claude'), kind, w); return; }
    } else if (key === 'codex') {
      kind = /secondary/i.test(low)?'secondary':(/primary/i.test(low)?'primary':null);
      group = 'limits';
    } else {
      kind = /secondary|weekly/i.test(low)||Number(w.window_minutes)===10080 ? 'secondary' :
             (/primary|five hour|5h/i.test(low)||Number(w.window_minutes)===300 ? 'primary' : null);
      group = kind ? 'limits' : 'unknown';
    }
    if (kind && !seen[group.toLowerCase()+'|'+kind]) {
      seen[group.toLowerCase()+'|'+kind] = true;
      add(group.toLowerCase(), t('grp.'+group), kind, w);
    } else {
      add('unknown-'+i, t('grp.unknown'), 'unknown', w);
    }
  });
  return groups;
}

function quotaKindLabel(kind) {
  var weekly = t('q.weekly'), five = t('q.fiveHour');
  var labels = {
    'gemini-weekly':weekly, 'gemini-five-hour':five,
    'claude-weekly':weekly, 'claude-five-hour':five,
    secondary:weekly, primary:five,
    weekly:weekly, 'five-hour':five,
    'weekly-opus':'Opus · '+weekly, 'weekly-sonnet':'Sonnet · '+weekly,
    'weekly-oauth-apps':t('q.oauthApps')+' · '+weekly, 'weekly-cowork':'Cowork · '+weekly,
    'weekly-sessions':'Sessions · '+weekly
  };
  return labels[kind] || t('q.default');
}

function lockedWindowInfo(acc, availability) {
  if (!availability || (availability.key !== 'limited' && availability.key !== 'unavailable')) return null;
  var role = availability.key === 'unavailable' ? 'long' : 'short';
  var wins = displayedQuotaWindows(acc);
  var chosen = null, bestSeconds = null;
  for (var i = 0; i < wins.length; i++) {
    if (quotaWindowRole(wins[i], acc.provider) !== role) continue;
    var rem = quotaNumber(wins[i]);
    if (rem === null || rem > 0) continue;
    var sec = resetSeconds(wins[i]);
    if (chosen === null || (sec !== null && (bestSeconds === null || sec < bestSeconds))) { chosen = wins[i]; bestSeconds = sec; }
  }
  return chosen ? { window:chosen, seconds:bestSeconds, role:role } : null;
}

function renderQuotaLock(acc, availability) {
  var info = lockedWindowInfo(acc, availability);
  if (!info) return '';
  var isWeekly = info.role === 'long';
  var roleClass = isWeekly ? 'quota-reset-weekly' : 'quota-reset-primary';
  var title = isWeekly ? t('q.weekly') : t('q.fiveHour');
  if (getProviderKey(acc.provider) === 'claude') {
    var lockGroups = groupQuotaWindows([info.window], acc.provider);
    if (lockGroups.length && lockGroups[0].windows.length) title = quotaKindLabel(lockGroups[0].windows[0].kind);
  }
  var countdown = formatResetCountdownSeconds(info.seconds);
  var ariaParts = [t('q.lockedAria', { title: title }), countdown];
  var weeklyExtra = '';
  {
    // The other window's remaining quota is shown under either lock type so both lock cards carry the same information.
    var otherRole = info.role === 'short' ? 'long' : 'short';
    var otherLabel = info.role === 'short' ? t('q.weeklyLeft') : t('q.fiveLeft');
    var otherClass = info.role === 'short' ? 'quota-reset-weekly' : 'quota-reset-primary';
    var wins = displayedQuotaWindows(acc);
    for (var i = 0; i < wins.length; i++) {
      if (quotaWindowRole(wins[i], acc.provider) !== otherRole) continue;
      var weeklyRemaining = quotaNumber(wins[i]);
      var weeklyValue = weeklyRemaining===null?'—':fmtPct(weeklyRemaining);
      var weeklyCountdown = formatResetCountdownSeconds(resetSeconds(wins[i]));
      var weeklyAria = otherLabel+' '+weeklyValue+', '+weeklyCountdown;
      ariaParts.push(weeklyAria);
      weeklyExtra = '<div class="quota-lock-weekly" role="group" aria-label="'+esc(weeklyAria)+'"><span class="quota-lock-weekly-summary">'+esc(otherLabel)+' <strong>'+esc(weeklyValue)+'</strong><span aria-hidden="true"> · </span>'+(info.role === 'short' ? '<small class="quota-reset-weekly">' : '<small class="quota-reset-primary">')+''+esc(weeklyCountdown)+'</small></span></div>';
      break;
    }
  }
  return '<div class="quota-lock-state" role="status" aria-label="'+esc(ariaParts.join(', '))+'">'+
    '<div class="quota-lock-title"><svg class="quota-lock-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="5" y="10" width="14" height="10" rx="2"></rect><path d="M8 10V7a4 4 0 0 1 8 0v3"></path></svg><span>'+esc(title)+'</span></div><div class="quota-lock-time '+roleClass+'">'+esc(countdown)+'</div><div class="quota-lock-note">'+esc(t('q.lockNote'))+'</div>'+weeklyExtra+'</div>';
}

function percentageState(window) {
  var rem = quotaNumber(window);
  var value = rem === null || !isFinite(rem) ? null : Math.max(0, Math.min(100, rem));
  return { remaining: value, color: remainingTone(value) };
}

function agFamilyIcon(family) {
  if (family === 'gemini') {
    return '<svg class="ag-family-icon" viewBox="0 0 24 24" fill="currentColor" aria-hidden="true" focusable="false" role="img">'
      +'<title>'+esc(t('ag.name'))+'</title>'
      +'<path d="M11.04 19.32Q12 21.51 12 24q0-2.49.93-4.68.96-2.19 2.58-3.81t3.81-2.55Q21.51 12 24 12q-2.49 0-4.68-.93a12.3 12.3 0 0 1-3.81-2.58 12.3 12.3 0 0 1-2.58-3.81Q12 2.49 12 0q0 2.49-.96 4.68-.93 2.19-2.55 3.81a12.3 12.3 0 0 1-3.81 2.58Q2.49 12 0 12q2.49 0 4.68.96 2.19.93 3.81 2.55t2.55 3.81"/>'
      +'</svg>';
  }
  return '<svg class="ag-family-icon" viewBox="0 0 94 94" fill="currentColor" aria-hidden="true" focusable="false" role="img">'
    +'<title>Claude</title>'
    +'<path d="M18.7657 62.4437L37.1822 52.1167L37.4857 51.2122L37.1822 50.7085H36.2715L33.1852 50.5208L22.6615 50.2391L13.5545 49.8636L4.70044 49.3942L2.47428 48.9248L0.399902 46.1553L0.602281 44.794L2.47428 43.5266L5.15579 43.7613L11.0754 44.1837L19.98 44.794L26.4055 45.1695L35.9679 46.1553H37.4857L37.6881 45.545L37.1822 45.1695L36.7774 44.794L27.5692 38.5508L17.6021 31.9791L12.3908 28.1769L9.60812 26.2524L8.19147 24.4686L7.58433 20.5256L10.1141 17.7091L13.5545 17.9438L14.4146 18.1785L17.9056 20.8542L25.343 26.6279L35.0572 33.7629L36.4739 34.9364L37.0443 34.5514L37.1316 34.2792L36.4739 33.1996L31.212 23.6706L25.596 13.9539L23.0663 9.91695L22.4086 7.52296C22.1538 6.51831 22.0038 5.68714 22.0038 4.65957L24.8877 0.716544L26.5067 0.200195L30.4025 0.716544L32.0215 2.12477L34.4501 7.66379L38.3458 16.3478L44.4172 28.1769L46.188 31.6975L47.1493 34.9364L47.5035 35.9222H48.1106V35.3589L48.6166 28.6933L49.5273 20.5256L50.438 10.0108L50.7415 7.05356L52.2088 3.48605L55.1433 1.56148L57.42 2.64112L59.292 5.31674L59.039 7.05356L57.926 14.2824L55.7504 25.5952L54.3337 33.1996H55.1433L56.1046 32.2138L59.9497 27.1442L66.3752 19.0704L69.2085 15.8784L72.5478 12.3579L74.6728 10.668H78.7203L81.6548 15.0804L80.3394 19.6337L76.1906 24.8911L72.7502 29.3504L67.8172 35.9595L64.7562 41.2734L65.0307 41.7118L65.7681 41.6489L76.8989 39.255L82.9197 38.1753L90.1041 36.9549L93.3422 38.457L93.6963 40.006L92.4315 43.151L84.7411 45.0287L75.7353 46.8594L62.3244 50.0164L62.1759 50.1358L62.3512 50.3958L68.399 50.9432L70.9794 51.084H77.3037L89.0922 51.9759L92.1785 53.9944L93.9999 56.4822L93.6963 58.4068L88.9404 60.8008L82.5655 59.2987L67.6401 55.7312L62.5301 54.4638H61.8217V54.8862L66.0717 59.064L73.9139 66.1051L83.6786 75.2116L84.1845 77.4648L82.9197 79.2485L81.6042 79.0608L73.0032 72.5829L69.6639 69.6726L62.1759 63.3356H61.67V63.9928L63.3902 66.5276L72.5478 80.2812L73.0032 84.5059L72.3454 85.8672L69.9675 86.7121L67.3871 86.2427L61.9735 78.6852L56.4587 70.2359L52.0064 62.6315L51.4687 62.971L48.8189 91.2654L47.6047 92.7206L44.7714 93.8002L42.3934 92.0164L41.1286 89.1061L42.3934 83.3324L43.9113 75.8219L45.1255 69.8604L46.2386 62.4437L46.9184 59.9661L46.8583 59.8003L46.3153 59.8916L40.7238 67.5603L32.2239 79.0608L25.4948 86.2427L23.8758 86.8999L21.0931 85.4447L21.3461 82.863L22.9145 80.5629L32.2239 68.7338L37.8399 61.3641L41.4594 57.1337L41.4242 56.5218L41.2244 56.5048L16.489 72.6299L12.0873 73.1932L10.1647 71.4094L10.4176 68.4991L11.3283 67.5603L18.7657 62.4437Z"/>'
    +'</svg>';
}

function findAGWindow(windows, family, role) {
  var familyRE = family === 'claude' ? /claude|gpt/i : /gemini/i;
  for (var i = 0; i < windows.length; i++) {
    if (familyRE.test(String(windows[i].name || '')) && quotaWindowRole(windows[i], 'antigravity') === role) return windows[i];
  }
  return null;
}

function renderAGLimitItem(label, win) {
  win = win || {}; var st = percentageState(win);
  return '<div class="ag-limit-item"><span class="ag-limit-label">'+esc(label)+'</span><strong class="ag-limit-value" data-window-name="'+esc(String(win.name||win.kind||''))+'" data-window-role="value" style="color:'+st.color+'">'+esc(st.remaining===null?'—':fmtPct(st.remaining))+'</strong><span class="ag-limit-reset" data-window-name="'+esc(String(win.name||win.kind||''))+'" data-window-role="reset">'+esc(formatRelativeReset(win))+'</span></div>';
}

function renderAGFamilySection(family, windows) {
  var shortWin = findAGWindow(windows, family, 'short');
  var longWin = findAGWindow(windows, family, 'long');
  var shortRem = quotaNumber(shortWin), longRem = quotaNumber(longWin);
  var familyLabel = family === 'claude' ? 'Claude' : t('ag.name');
  var sectionAria = t('q.familyAria', { f: familyLabel });
  var icon = agFamilyIcon(family);
  var lockRole = longRem !== null && longRem <= 0 ? 'long' : (shortRem !== null && shortRem <= 0 ? 'short' : '');
  if (lockRole) {
    var isWeekly = lockRole === 'long';
    var roleClass = isWeekly ? 'quota-reset-weekly' : 'quota-reset-primary';
    var locked = isWeekly ? longWin : shortWin;
    var timeText = formatResetCountdownSeconds(resetSeconds(locked));
    var context = '';
    var limitName = isWeekly ? t('q.weekly') : t('q.fiveHour');
    var lockAria = familyLabel+' '+limitName+', '+timeText;
    if (lockRole === 'short') {
      var weeklyValue = longRem===null?'—':fmtPct(longRem);
      var weeklyCountdown = formatResetCountdownSeconds(resetSeconds(longWin));
      var weeklyAria = t('q.weeklyLeft')+' '+weeklyValue+', '+weeklyCountdown;
      lockAria += ', '+weeklyAria;
      context = '<div class="ag-family-lock-context" role="group" aria-label="'+esc(weeklyAria)+'">'+esc(t('q.weeklyLeft'))+' <strong>'+esc(weeklyValue)+'</strong><span aria-hidden="true"> · </span><span class="quota-reset-weekly">'+esc(weeklyCountdown)+'</span></div>';
    }
    var padlockSvg = '<svg class="ag-lock-padlock" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" aria-hidden="true"><rect x="5" y="10" width="14" height="10" rx="2"></rect><path d="M8 10V7a4 4 0 0 1 8 0v3"></path></svg>';
    var lockTitle = padlockSvg+icon+'<span>'+esc(familyLabel)+' · '+limitName+'</span>';
    return '<section class="ag-family-section is-locked" aria-label="'+esc(sectionAria)+'"><div class="ag-family-lock" role="status" aria-label="'+esc(lockAria)+'">'
      +'<div class="ag-family-lock-title">'+lockTitle+'</div><div class="ag-family-lock-time '+roleClass+'">'+esc(timeText)+'</div>'+context+'</div></section>';
  }
  return '<section class="ag-family-section" aria-label="'+esc(sectionAria)+'">'
    +'<div class="ag-family-head" title="'+esc(familyLabel)+'">'+icon+'<span class="ag-family-name">'+esc(family === 'claude' ? 'Claude / GPT' : 'Gemini')+'</span></div>'
    +'<div class="ag-family-limits">'+renderAGLimitItem(t('q.fiveHour'),shortWin)+renderAGLimitItem(t('q.weekly'),longWin)+'</div>'
    +'</section>';
}

function renderAntigravityQuota(windows) {
  return '<div class="quota-group"><div class="ag-family-stack">'+renderAGFamilySection('gemini',windows)+renderAGFamilySection('claude',windows)+'</div></div>';
}

function renderQuotaPercentages(group, provider, accountStatus) {
  var items = (group && Array.isArray(group.windows)) ? group.windows.slice() : [];
  var key = getProviderKey(provider);
  var order = key === 'codex'
    ? { primary:0, secondary:1, unknown:9 }
    : key === 'claude' ? { 'five-hour':0, weekly:1, 'weekly-opus':2, 'weekly-sonnet':3, 'weekly-oauth-apps':4, 'weekly-cowork':5, 'weekly-sessions':6, unknown:9 }
    : { 'gemini-five-hour':0, 'gemini-weekly':1, 'claude-five-hour':2, 'claude-weekly':3, 'five-hour':4, weekly:5, unknown:9 };
  items.sort(function(a, b) {
    return (order[a.kind] == null ? 2 : order[a.kind]) - (order[b.kind] == null ? 2 : order[b.kind]);
  });

  var aria = (group.label || t('grp.default')) + ': ' + items.map(function(item) {
    var st = percentageState(item.window);
    return t('q.leftAria', { label: quotaKindLabel(item.kind), v: st.remaining===null?t('st.unknown'):fmtPct(st.remaining) });
  }).join(', ');

  var html = '<section class="quota-group" data-group-status="'+esc(accountStatus && accountStatus.key || 'unknown')+'" aria-label="'+esc(aria)+'"><div class="quota-values">';

  items.forEach(function(item) {
    var st = percentageState(item.window);
    var unknown = st.remaining === null;
    var locked = st.remaining !== null && st.remaining <= 0;
    var family = String(item.kind || '').indexOf('gemini-') === 0 ? t('ag.name')+' · ' : (String(item.kind || '').indexOf('claude-') === 0 ? 'Claude · ' : '');
    var label = family + quotaKindLabel(item.kind);
    html += '<div class="quota-value-row'+(locked?' is-locked':'')+'" data-kind="'+esc(item.kind)+'">'+
      '<span class="quota-value-label">'+esc(label)+'</span>'+
      '<span class="quota-value-number" data-window-name="'+esc(String(item.window.name||item.window.kind||''))+'" data-window-role="value" style="color:'+st.color+';" aria-label="'+esc(t('q.leftAria', { label: label, v: unknown?t('st.unknown'):fmtPct(st.remaining) }))+'">'+esc(unknown?'—':(locked?t('q.locked'):fmtPct(st.remaining)))+'</span>'+
      '<span class="quota-value-reset" data-window-name="'+esc(String(item.window.name||item.window.kind||''))+'" data-window-role="reset">'+esc(formatRelativeReset(item.window))+'</span></div>';
  });
  return html+'</div></section>';
}

function renderQuotaGroups(windows, provider, status) {
  var providerKey = getProviderKey(provider);
  if (providerKey === 'antigravity') return '<div class="quota-groups provider-antigravity">'+renderAntigravityQuota(windows)+'</div>';
  var groups = groupQuotaWindows(windows, provider);
  var html = '<div class="quota-groups provider-'+esc(providerKey)+'">';
  groups.forEach(function(g) { html += renderQuotaPercentages(g, provider, status); });
  return html+'</div>';
}

function renderCreditSummary(acc) {
  var reset = acc && acc.reset_credits;
  var api = acc && acc.api_credits;
  var out = [];
  var accessibleRows = [];
  var availableCount = reset && reset.available_count != null ? Number(reset.available_count) : null;
  if (availableCount !== null && isFinite(availableCount) && availableCount > 0) {
    var resetPrimaryLabel = t('credit.reset', { n: formatNum(availableCount) });
    var resetLabel = resetPrimaryLabel;
    var resetPrimaryText = '<strong>'+esc(resetPrimaryLabel)+'</strong>';
    var expiryRow = '';
    var expiryText = reset.nearest_expiry ? formatResetExpiry(reset.nearest_expiry) : '—';
    if (expiryText !== '—') {
      var expiryLabel = t('credit.expiry', { d: expiryText });
      resetLabel += '. '+expiryLabel;
      expiryRow = '<div class="credit-reset-expiry"><span class="credit-reset-expiry-tree" aria-hidden="true">└ </span><span class="credit-reset-expiry-text">'+esc(expiryLabel)+'</span></div>';
    }
    out.push('<div class="credit-reset-summary reset-credit-summary" role="group" aria-label="'+esc(resetLabel)+'"><div class="credit-reset-primary">'+resetPrimaryText+'</div>'+expiryRow+'</div>');
    accessibleRows.push(resetLabel);
  }
  if (api) {
    var parts = [];
    if (api.unlimited === true) {
      parts.push(t('credit.unlimited'));
    } else if (api.balance != null && isFinite(Number(api.balance))) {
      parts.push(t('credit.balance', { n: formatNum(api.balance) }));
    } else if (api.has_credits === true) {
      parts.push(t('credit.available'));
    } else if (api.has_credits === false) {
      parts.push(t('credit.zero'));
    }
    if (parts.length) {
      var apiLabel = t('credit.api')+' · '+parts.join(' · ');
      out.push('<div class="credit-api-summary" role="group" aria-label="'+esc(apiLabel)+'"><strong>'+esc(t('credit.api'))+'</strong> · '+esc(parts.join(' · '))+'</div>');
      accessibleRows.push(apiLabel);
    }
  }
  return out.length ? '<div class="credit-summary" role="group" aria-label="'+esc(accessibleRows.join('. '))+'">'+out.join('')+'</div>' : '';
}

function alignAccountGrid() {
  var grid = document.getElementById('accountsList');
  if (!grid || !grid.style || !grid.getBoundingClientRect) return;
  // Preserve card width when filtering down to a few accounts; leave empty columns.
  var columns = Math.min(4, Math.max(1, Math.floor((grid.getBoundingClientRect().width + 10) / 290)));
  grid.style.setProperty('grid-template-columns', 'repeat(' + columns + ', minmax(0, 1fr))');
}

function renderAccounts(accounts) {
  var container = document.getElementById('accountsList');
  if (!container) return;

  container.setAttribute('data-count', String(accounts ? accounts.length : 0));
  alignAccountGrid();
  var savedScrollTop = (typeof window !== 'undefined' && typeof window.pageYOffset === 'number') ? window.pageYOffset : ((document.documentElement && document.documentElement.scrollTop) || 0);
  var activeEl = document.activeElement;
  var activeId = activeEl ? activeEl.id : null;
  var selStart = (activeEl && typeof activeEl.selectionStart === 'number') ? activeEl.selectionStart : null;
  var selEnd = (activeEl && typeof activeEl.selectionEnd === 'number') ? activeEl.selectionEnd : null;

  var liveEl = document.getElementById('accountStatusLive');
  if (liveEl) {
    liveEl.textContent = tn('live.shown', accounts ? accounts.length : 0);
  }

  if (!accounts || accounts.length === 0) {
    var emptyCard = document.createElement('div');
    emptyCard.className = 'card text-empty';
    emptyCard.style.textAlign = 'center';
    emptyCard.textContent = t('empty.accounts');
    if (container.replaceChildren) { container.replaceChildren(emptyCard); } else { container.innerHTML = ''; container.appendChild(emptyCard); }
    if (activeId) {
      var targetEmpty = document.getElementById(activeId);
      if (targetEmpty && targetEmpty.focus && document.activeElement !== targetEmpty) {
        targetEmpty.focus();
        if (selStart !== null && targetEmpty.setSelectionRange) { try { targetEmpty.setSelectionRange(selStart, selEnd); } catch(e) {} }
      }
    }
    return;
  }

  var frag = document.createDocumentFragment ? document.createDocumentFragment() : null;
  var nodes = [];

  for (var i = 0; i < accounts.length; i++) {
    var acc = accounts[i] || {};
    var canon = getCanonicalStatus(acc);
    var windows = displayedQuotaWindows(acc);
    var pKey = getProviderKey(acc.provider);
    var isCurrent = acc.current_account === true && acc.current_kind === 'recent' && acc.current_reason === 'recent_usage';

    var card = document.createElement('div');
    card.className = 'account-card';
    card.setAttribute('data-status', canon.key);
    var availability = getAvailabilityState(acc);
    card.setAttribute('data-availability', availability.key);
    if (lockedWindowInfo(acc, availability)) card.setAttribute('data-locked', 'true');
    // Color supplies the visual state; keep its meaning available without a redundant chip.
    card.setAttribute('role', 'group');
    card.setAttribute('aria-label', String(acc.account || acc.alias || t('card.account')) + ' · ' + availability.label + (isCurrent ? t('card.current') : ''));
    card.setAttribute('data-provider', pKey);
    var cardEndpoint = normalizeEndpoint(acc.endpoint);
    card.setAttribute('data-endpoint', cardEndpoint);
    if (isCurrent) card.setAttribute('data-current', 'true');
    if (acc.account_key) card.setAttribute('data-account-key', acc.account_key);

    var header = document.createElement('div');
    header.className = 'card-header';

    var iden = document.createElement('div');
    iden.className = 'card-identity';

    var metaLeft = document.createElement('span');
    metaLeft.className = 'card-meta-left';
    var provEl = document.createElement('span');
    var provBadgeClass = pKey === 'antigravity' ? 'badge-provider-antigravity' : (pKey === 'codex' ? 'badge-provider-codex' : (pKey === 'claude' ? 'badge-provider-claude' : 'badge-provider-default'));
    provEl.className = 'badge-provider ' + provBadgeClass;
    var provBaseLabel = formatProviderLabel(acc.provider);
    var provHTML = providerIcon(acc.provider, 14);
    if (pKey === 'codex' && acc.plan_type) {
      var planKey = String(acc.plan_type).toLowerCase();
      var planLabels = { k12: 'K12', plus: 'Plus', pro: 'Pro', team: 'Team', free: 'Free', enterprise: 'Enterprise' };
      var planDisplay = planLabels[planKey] || String(acc.plan_type);
      provHTML += '<span class="prov-name"><span>' + esc(provBaseLabel) + '</span><span class="prov-sep" aria-hidden="true">·</span><span class="prov-plan">' + esc(planDisplay) + '</span></span>';
      provEl.setAttribute('aria-label', provBaseLabel + ' ' + planDisplay);
    } else {
      provHTML += '<span>' + esc(provBaseLabel) + '</span>';
    }
    provEl.innerHTML = provHTML;
    metaLeft.appendChild(provEl);

    iden.appendChild(metaLeft);

    var aliasEl = document.createElement('span');
    aliasEl.className = 'account-alias';
    var labelText = acc.account || acc.alias || '—';
    aliasEl.textContent = labelText;
    aliasEl.title = labelText;
    iden.appendChild(aliasEl);

    if (hasMultipleEndpoints()) {
      var endpointKey = normalizeEndpoint(acc.endpoint);
      var endpointEl = document.createElement('span');
      endpointEl.className = 'badge-endpoint badge-endpoint-' + endpointKey;
      endpointEl.textContent = endpointLabel(endpointKey);
      iden.appendChild(endpointEl);
    }

    header.appendChild(iden);
    if (acc.account_key && acc.refresh_supported !== false && ['codex', 'claude', 'antigravity'].indexOf(pKey) >= 0 && availability.key !== 'disabled') {
      var refreshBtn = document.createElement('button');
      refreshBtn.type = 'button';
      refreshBtn.className = 'btn btn-secondary card-refresh';
      refreshBtn.id = 'refresh-' + encodeURIComponent(acc.account_key);
      refreshBtn.setAttribute('data-refresh-account', acc.account_key);
      refreshBtn.setAttribute('aria-label', t('refresh.cardAria', { name: labelText }));
      refreshBtn.title = t('refresh.card');
      refreshBtn.innerHTML = '<svg class="ico-refresh" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M21 12a9 9 0 1 1-2.64-6.36"/><path d="M21 3v6h-6"/></svg>' +
        '<svg class="ico-check" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';
      refreshBtn.onclick = (function(key) { return function() { refreshAccount(key); }; })(acc.account_key);
      header.appendChild(refreshBtn);
    }
    var heading = document.createElement('div');
    heading.className = 'card-heading';
    heading.appendChild(header);
    card.appendChild(heading);
    var refreshStatus = document.createElement('div');
    refreshStatus.className = 'card-refresh-status';
    refreshStatus.setAttribute('role', 'status');
    refreshStatus.setAttribute('aria-live', 'polite');
    refreshStatus.setAttribute('data-refresh-status', String(acc.account_key || ''));
    heading.appendChild(refreshStatus);

    // Keep the provider observation time near the account identity so it is
    // not mistaken for the dashboard synchronization time in the toolbar.
    var freshnessEl = document.createElement('div');
    freshnessEl.innerHTML = renderAccountFreshness(acc);
    if (freshnessEl.firstChild) heading.appendChild(freshnessEl.firstChild);

    var quotaBody = document.createElement('div');
    quotaBody.className = 'card-quota';
    card.appendChild(quotaBody);
    var lockHTML = (pKey === 'codex' || pKey === 'claude') ? renderQuotaLock(acc, availability) : '';
    if (lockHTML) {
      var lockEl = document.createElement('div');
      lockEl.className = 'quota-group';
      lockEl.innerHTML = lockHTML;
      quotaBody.appendChild(lockEl);
      // Keep model-specific Claude weekly quotas visible even while the account is locked.
      if (pKey === 'claude') {
        var modelWindows = windows.filter(function(w) { return /^weekly-/.test(String(w.name || '').toLowerCase()); });
        if (modelWindows.length) {
          var modelQuotaEl = document.createElement('div');
          modelQuotaEl.innerHTML = renderQuotaGroups(modelWindows, acc.provider, canon);
          quotaBody.appendChild(modelQuotaEl);
        }
      }
    } else if (windows.length > 0) {
      var quotaEl = document.createElement('div');
      quotaEl.innerHTML = renderQuotaGroups(windows, acc.provider, canon);
      if (quotaEl.firstChild) {
        while (quotaEl.firstChild) quotaBody.appendChild(quotaEl.firstChild);
      } else {
        quotaBody.appendChild(quotaEl);
      }
    } else {
      var noWin = document.createElement('div');
      noWin.className = 'quota-pending';
      noWin.textContent = availability.key === 'unavailable' || availability.key === 'disabled' ? t('q.none') : t('st.pending');
      quotaBody.appendChild(noWin);
    }

    var creditsHTML = renderCreditSummary(acc);
    if (creditsHTML) {
      var creditsEl = document.createElement('div');
      creditsEl.innerHTML = creditsHTML;
      if (creditsEl.firstChild) {
        while (creditsEl.firstChild) card.appendChild(creditsEl.firstChild);
      } else {
        card.appendChild(creditsEl);
      }
    }

    decorateQuotaChanges(card, quotaAccountKeys(cachedAccounts)[i]);
    nodes.push(card);
    if (frag) frag.appendChild(card);
  }

  if (frag && container.replaceChildren) {
    container.replaceChildren(frag);
  } else if (frag) {
    container.innerHTML = '';
    container.appendChild(frag);
  } else {
    container.innerHTML = '';
    for (var n = 0; n < nodes.length; n++) container.appendChild(nodes[n]);
  }

  updateAccountRefreshButtons();
  if (activeId) {
    var target = document.getElementById(activeId);
    if (target && target.focus && document.activeElement !== target) {
      target.focus();
      if (selStart !== null && target.setSelectionRange) {
        try { target.setSelectionRange(selStart, selEnd); } catch(e) {}
      }
    }
  }
  if (typeof window !== 'undefined' && window.scrollTo) {
    try { window.scrollTo(0, savedScrollTop); } catch(e) {}
  } else if (document.documentElement) {
    document.documentElement.scrollTop = savedScrollTop;
  }
}

document.addEventListener('DOMContentLoaded', function() {
  if (typeof window !== 'undefined' && window.addEventListener) window.addEventListener('resize', alignAccountGrid);
  initLanguageControls();
  initLoadingState();
  initFilterControls();
  initManualRefreshControls();
  startResetCountdownTick();

  document.addEventListener('visibilitychange', function() {
    if (!document.hidden) {
      updateManualRefreshButton();
      if (pollTimer) { clearTimeout(pollTimer); pollTimer = null; }
      if (!isFetching) loadDashboard(false);
      startResetCountdownTick();
    }
  });

  initAuthGate();
  loadDashboard(true);
});
