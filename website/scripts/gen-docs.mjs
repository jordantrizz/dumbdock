// Regenerates the two generated blocks in this repository:
//   - website/docs/history/changelog.md between its GENERATED markers
//   - README.md between its docs-index markers
//
// Run from anywhere: `node website/scripts/gen-docs.mjs` (or `npm run gen:docs`
// inside website/). Paths resolve from the repository root, never the cwd.

import {execFileSync} from 'node:child_process';
import {readFileSync, writeFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import path from 'node:path';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..', '..');
const changelogFile = path.join(root, 'website/docs/history/changelog.md');
const readmeFile = path.join(root, 'README.md');

const CHANGELOG_START = '<!-- BEGIN GENERATED: changelog -->';
const CHANGELOG_END = '<!-- END GENERATED: changelog -->';
const README_START = '<!-- BEGIN docs-index -->';
const README_END = '<!-- END docs-index -->';

const SITE = 'https://jordantrizz.github.io/dumbdock';

const changelogSkeleton = `---
title: Changelog
description: Every committed change to dumbdock, newest first, generated from git history.
---

Changes are generated from \`git log\` by \`npm run gen:docs\`; this page is not
edited by hand.

${CHANGELOG_START}
${CHANGELOG_END}
`;

function replaceBlock(file, start, end, body) {
  let text;
  try {
    text = readFileSync(file, 'utf8');
  } catch {
    text = file === changelogFile ? changelogSkeleton : '';
  }
  const from = text.indexOf(start);
  const to = text.indexOf(end);
  if (from === -1 || to === -1 || to < from) {
    throw new Error(
      `${path.relative(root, file)}: expected one ${start} / ${end} pair`,
    );
  }
  const next =
    text.slice(0, from + start.length) +
    '\n' +
    body.trimEnd() +
    '\n' +
    text.slice(to);
  if (next !== text) {
    writeFileSync(file, next);
    return true;
  }
  return false;
}

// -- changelog: git history, excluding the generated file itself (which would
// otherwise make every run differ from the last).
const log = execFileSync(
  'git',
  [
    'log',
    '--no-merges',
    '--pretty=- %s (%h)',
    '--',
    '.',
    ':(exclude)website/docs/history/changelog.md',
  ],
  {cwd: root, encoding: 'utf8'},
).trimEnd();

// -- README: one row per section, absolute site URLs (a README is read on
// GitHub, where a relative docs path does not resolve to the site).
const sectionTable = [
  ['Section', 'Link'],
  ['---', '---'],
  ['Getting started', `${SITE}/getting-started/overview`],
  ['Guides', `${SITE}/guides/traefik`],
  ['Commands', `${SITE}/commands/control-sh`],
  ['Reference', `${SITE}/reference/api`],
  ['Contributing', `${SITE}/contributing/development`],
  ['History', `${SITE}/history/changelog`],
]
  .map(([a, b]) => `| ${a} | ${b} |`)
  .join('\n');

const changedChangelog = replaceBlock(
  changelogFile,
  CHANGELOG_START,
  CHANGELOG_END,
  log,
);
const changedReadme = replaceBlock(
  readmeFile,
  README_START,
  README_END,
  sectionTable,
);

console.log(
  `gen-docs: changelog ${changedChangelog ? 'updated' : 'unchanged'}, ` +
    `README ${changedReadme ? 'updated' : 'unchanged'}`,
);
