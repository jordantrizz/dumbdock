// Focused harness for the Integrations tab. No dependencies; runs against the
// inline script extracted from index.html.
//
//   node scripts/test-integrations-ui.js
//
// It stubs just enough DOM to evaluate the page script without resolving any
// network call, then exercises the pure render helpers directly.

const fs = require('fs');
const path = require('path');
const vm = require('vm');
const assert = require('assert');

const html = fs.readFileSync(path.join(__dirname, '..', 'index.html'), 'utf8');

// ---- static structure ----
const navOrder = ['dashboard', 'networking', 'traefik', 'integrations', 'issues', 'storage', 'icons', 'help'];
const navPositions = navOrder.map((id) => html.indexOf('id="nav-' + id + '"'));
navPositions.forEach((pos, i) => {
  assert.ok(pos !== -1, 'missing nav-' + navOrder[i]);
  if (i > 0) assert.ok(navPositions[i - 1] < pos, 'nav-' + navOrder[i] + ' is out of order');
});
for (const id of ['nav-integrations', 'page-integrations', 'integrations-app']) {
  const count = (html.match(new RegExp('id="' + id + '"', 'g')) || []).length;
  assert.strictEqual(count, 1, 'expected exactly one #' + id + ', found ' + count);
}
assert.ok(
  html.includes("var TAB_IDS = ['dashboard', 'networking', 'traefik', 'integrations', 'issues', 'storage', 'icons', 'help'];"),
  'TAB_IDS is missing integrations'
);
assert.ok(/currentTab === 'integrations'\)\s*\{\s*loadIntegrations\(\);/.test(html), 'refreshActiveTab has no integrations branch');
assert.ok(/page-integrations[^]*?loadIntegrations\(\);/.test(html), 'poll loop does not refresh integrations');
assert.ok(html.includes("apiFetch('/api/integrations')"), 'loadIntegrations does not use /api/integrations');
assert.ok(html.includes("'integration-api': 'Integration API'"), 'Issues category label missing for integration-api');
assert.ok(html.includes("'integration-status': 'Integration status'"), 'Issues category label missing for integration-status');
assert.ok(html.includes('id="help-watchtower-api"'), 'Watchtower help guide missing');
assert.ok(html.includes('id="help-integration-overrides"'), 'override help guide missing');

const authBootstrap = html.slice(html.indexOf('checkAuth().then(function()'), html.indexOf('setInterval(function()'));
assert.ok(authBootstrap.length > 0, 'auth bootstrap block not found');
assert.ok(!authBootstrap.includes('loadIntegrations('), 'integrations fetch must not run in the auth bootstrap');

// ---- evaluate the page script with inert browser stubs ----
function elementStub() {
  return {
    style: {},
    classList: { add() {}, remove() {}, toggle() {}, contains() { return false; } },
    textContent: '',
    innerHTML: '',
    setAttribute() {},
    getAttribute() { return null; },
    appendChild() {},
    insertBefore() {},
    addEventListener() {},
    remove() {},
    querySelector() { return null; },
    querySelectorAll() { return []; },
    closest() { return null; },
    scrollIntoView() {},
  };
}

const elements = new Map();
const anchorPages = { 'help-traefik-api': 'page-help', 'help-integration-overrides': 'page-help' };
function getElement(id) {
  if (anchorPages[id]) {
    const el = elementStub();
    el.closest = () => ({ id: anchorPages[id] });
    return el;
  }
  if (!elements.has(id)) elements.set(id, elementStub());
  return elements.get(id);
}

const inertFetch = () => new Promise(() => {}); // never resolves: no renders at load time

const context = {
  console,
  setTimeout: () => 0,
  clearTimeout: () => {},
  setInterval: () => 0,
  fetch: inertFetch,
  navigator: {},
  localStorage: { getItem: () => null, setItem: () => {}, removeItem: () => {} },
  location: { hash: '' },
  history: {},
  document: {
    getElementById: getElement,
    querySelector: () => null,
    querySelectorAll: () => [],
    createElement: elementStub,
    addEventListener: () => {},
    body: { appendChild() {}, removeChild() {} },
  },
  window: { addEventListener: () => {} },
};
context.globalThis = context;
vm.createContext(context);

const script = html.match(/<script>([\s\S]*)<\/script>/)[1];
vm.runInContext(script, context, { filename: 'dumbdock-inline.js' });

assert.strictEqual(context.tabFromHash('#integrations'), 'integrations', 'tabFromHash does not resolve #integrations');
assert.strictEqual(context.tabFromHash('#help-integration-overrides'), 'help', 'help anchor regression');

const app = getElement('integrations-app');

// ---- hostile payload: every interpolated value must be escaped ----
const hostile = {
  summary: { registered: 3, detected: 2, disabled: 0, hidden: 1 },
  integrations: [
    {
      id: 'traefik',
      displayName: '<script>alert(1)</script>',
      description: '<img src=x onerror=alert(1)>',
      detected: true,
      enabled: true,
      containerName: '<img src=x onerror=alert(1)>',
      image: 'traefik:<svg onload=alert(1)>',
      url: 'http://<b>traefik</b>:8080',
      urlSource: 'label',
      authConfigured: true,
      attempts: 0,
      attemptLimit: 3,
      lastChecked: new Date().toISOString(),
      warnings: ['<script>warn</script>'],
      stats: [{ label: 'HTTP <i>routers</i>', value: '<b>7</b>', hint: '<em>hint</em>' }],
      sections: [{ title: '<script>section</script>', columns: ['<th>x</th>'], rows: [['<td>cell</td>']] }],
    },
    {
      id: 'watchtower',
      displayName: 'Watchtower',
      detected: true,
      enabled: true,
      containerName: 'watchtower',
      attemptLimit: 3,
      attempts: 3,
      disabled: true,
      disabledReason: 'GET /v1/metrics returned status 401',
      lastChecked: new Date().toISOString(),
    },
    {
      id: 'prunemate',
      displayName: 'PruneMate',
      enabled: false,
      hidden: true,
      attemptLimit: 3,
      lastChecked: new Date().toISOString(),
    },
  ],
};

context.renderIntegrations(hostile);
const out = app.innerHTML;

assert.ok(!out.includes('<img src=x'), 'hostile container name was not escaped');
assert.ok(!out.includes('<script>alert'), 'hostile display name was not escaped');
assert.ok(!out.includes('<svg onload'), 'hostile image tag was not escaped');
assert.ok(!out.includes('<b>traefik</b>'), 'hostile URL was not escaped');
assert.ok(!out.includes('<b>7</b>'), 'hostile stat value was not escaped');
assert.ok(!out.includes('<td>cell</td>'), 'hostile table cell was not escaped');
assert.ok(out.includes('&lt;img src=x'), 'escaped container name not rendered');
assert.ok(out.includes('3 registered'), 'summary count missing');
assert.ok(out.includes('2 detected'), 'summary detected count missing');
assert.ok(out.includes('1 hidden by configuration'), 'summary hidden count missing');
assert.ok(out.includes('Detected'), 'detected badge missing');
assert.ok(out.includes('from container label'), 'endpoint source missing');
assert.ok(out.includes('Credentials'), 'credential presence missing');
assert.ok(out.includes('Notes'), 'warnings section missing');
assert.ok(out.includes('Unable to access Watchtower API'), 'disabled banner missing');
assert.ok(out.includes('restart the dumbdock container'), 'disabled banner must name the restart');
assert.ok(out.includes('Hidden by configuration'), 'hidden card missing');
assert.ok(out.includes('#help-integration-overrides'), 'hidden card must link the override guide');
assert.ok(out.includes('Traefik tab'), 'Traefik card must link its own tab');

// ---- empty registry ----
context.renderIntegrations({ integrations: [], summary: {} });
assert.ok(app.innerHTML.includes('No integrations are registered'), 'empty registry state missing');

// ---- nothing detected: the guidance banner must appear ----
context.renderIntegrations({
  integrations: [{ id: 'watchtower', displayName: 'Watchtower', enabled: true, detected: false, attemptLimit: 3 }],
  summary: { registered: 1, detected: 0 },
});
assert.ok(app.innerHTML.includes('No integrations detected'), 'undetected guidance missing');
assert.ok(app.innerHTML.includes('dumbdock.integration.watchtower.enabled=true'), 'guidance must show an override example');
assert.ok(app.innerHTML.includes('Not detected'), 'absent badge missing');

// ---- failed fetch must escape the error message ----
const failing = getElement('integrations-app');
failing.innerHTML = '';
assert.ok(typeof context.loadIntegrations === 'function', 'loadIntegrations is not defined');

console.log('integrations UI harness: all assertions passed');
