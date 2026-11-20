// Focused harness for the Icons tab. No dependencies; runs against the inline
// script extracted from index.html.
//
//   node scripts/test-icons-ui.js
//
// The Go side owns the resolution; this harness proves the page builds the same
// URL from the same payload fields the API now sends (bases, aliases, source).

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
for (const id of ['nav-icons', 'page-icons', 'iconResults', 'iconSearch', 'providerFilters']) {
  const count = (html.match(new RegExp('id="' + id + '"', 'g')) || []).length;
  assert.strictEqual(count, 1, 'expected exactly one #' + id + ', found ' + count);
}
assert.ok(
  html.includes("var TAB_IDS = ['dashboard', 'networking', 'traefik', 'integrations', 'issues', 'storage', 'icons', 'help'];"),
  'TAB_IDS is missing icons'
);
assert.ok(/currentTab === 'icons'[^]*?loadIcons\(\)/.test(html), 'refreshActiveTab has no icons branch');
assert.ok(html.includes("apiFetch('/api/icons')"), 'loadIcons does not use /api/icons');

// ---- preview URL builder ----
const match = html.match(/function iconPreviewURL\(set, slug\) \{[\s\S]*?\n\}/);
assert.ok(match, 'iconPreviewURL is missing');
const sandbox = {};
vm.runInNewContext(
  match[0] +
    '\nresult = iconPreviewURL({cdnUrlTemplate: "https://cdn.example/{base}/{slug}.{ext}", bases: {png: "png"}}, "png");' +
    '\nunknown = iconPreviewURL({cdnUrlTemplate: "https://cdn.example/{base}/{slug}.{ext}", bases: {}}, "plain");' +
    '\nlegacy = iconPreviewURL({cdnUrlTemplate: "https://cdn.example/svg/{slug}.svg"}, "alpha");',
  sandbox
);
assert.strictEqual(sandbox.result, 'https://cdn.example/png/png.png', 'png base did not reach the URL');
assert.strictEqual(sandbox.unknown, 'https://cdn.example/svg/plain.svg', 'unknown slug did not fall back to svg');
assert.strictEqual(sandbox.legacy, 'https://cdn.example/svg/alpha.svg', 'a template without placeholders changed');

// ---- table and payload contract ----
assert.ok(
  html.includes('<th>Image Name</th><th>Icon Slug</th><th>Provider</th><th>Format</th><th>Preview</th>'),
  'the icons table header is missing the Format column'
);
assert.ok(html.includes('<td colspan="5"></td>'), 'the lazy-load sentinel colspan does not match five columns');
assert.ok(html.includes('set.bases && set.bases[slug]'), 'the page does not read the per-slug base');
assert.ok(html.includes('set.aliases && set.aliases[slug]'), 'the page does not read the per-slug aliases');
assert.ok(html.includes('set.indexUrl'), 'the page does not read the index source');
assert.ok(html.includes("escapeHtml(row.cdnUrl)"), 'the preview URL is not escaped');
assert.ok(html.includes('Aliases: '), 'the alias tooltip is missing');

const referrerCount = (html.match(/referrerpolicy="no-referrer"/g) || []).length;
assert.ok(referrerCount >= 2, 'card and preview images must both set referrerpolicy, found ' + referrerCount);

console.log('icons ui harness: ok');
