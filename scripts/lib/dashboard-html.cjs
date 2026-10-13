'use strict';
// Assembles the dashboard exactly as internal/dashboard/dashboard.go does at start-up.
const fs = require('node:fs');
const path = require('node:path');

function readDashboardHTML(root) {
  const dir = path.join(root, 'internal/dashboard');
  const read = name => fs.readFileSync(path.join(dir, name), 'utf8');
  return read('dashboard.html').replace('@@STYLE@@\n', () => read('style.css')).replace('@@SCRIPT@@\n', () => read('app.js'));
}

module.exports = { readDashboardHTML };
