'use strict';
// The dashboard reads its management key from sessionStorage. Fixture pages are not
// authenticated, so seed a dummy key before any page script runs.
const SEED_KEY = 'fixture-management-key';
async function seedKey(target) {
  await target.addInitScript(key => { try { sessionStorage.setItem('cpa_quota_monitor_mgmt_key', key); } catch (e) {} }, SEED_KEY);
}
module.exports = { seedKey, SEED_KEY };
