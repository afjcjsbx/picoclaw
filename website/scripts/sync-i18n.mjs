// Builds the zh-Hans locale tree from docs/: every doc is copied (so relative
// markdown links resolve inside one tree), then X.zh.md overlays X.md (when X.md exists). The repo
// keeps translations beside the English source; the output is git-ignored.
import {existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync} from 'node:fs';
import {dirname, join, relative} from 'node:path';
import {fileURLToPath} from 'node:url';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const src = join(root, '..', 'docs');
const dst = join(root, 'i18n', 'zh-Hans', 'docusaurus-plugin-content-docs', 'current');

rmSync(dst, {recursive: true, force: true});

const walk = (dir) =>
  readdirSync(dir, {withFileTypes: true}).flatMap((e) => {
    const p = join(dir, e.name);
    if (e.isDirectory()) return e.name.startsWith('_') ? [] : walk(p);
    return e.name.endsWith('.md') ? [p] : [];
  });

const put = (file, name, text) => {
  const out = join(dst, name);
  mkdirSync(dirname(out), {recursive: true});
  writeFileSync(out, text);
};

const files = walk(src).map((f) => [f, relative(src, f)]);
// English first, translations second so they win.
for (const [f, rel] of files.filter(([, r]) => !r.endsWith('.zh.md'))) put(f, rel, readFileSync(f, 'utf8'));
let n = 0;
for (const [f, rel] of files.filter(([, r]) => r.endsWith('.zh.md'))) {
  // Docs are only discovered from docs/, so a translation with no English
  // counterpart cannot be a page; src/remark/links.js links it on GitHub.
  if (!existsSync(f.replace(/\.zh\.md$/, '.md'))) continue;
  put(f, rel.replace(/\.zh\.md$/, '.md'), readFileSync(f, 'utf8'));
  n++;
}
console.log(`sync-i18n: ${n} translated docs`);
