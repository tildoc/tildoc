// Checks the translation files (npm run i18n:check); --write regenerates template.json.
//  - every key the code asks for exists in en-us.json, and en-us.json has no unused ones
//  - translations have no unknown keys and only placeholders the English text has
//  - template.json is en-us.json with empty values
import { readFileSync, writeFileSync, readdirSync, statSync } from 'node:fs';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const src = join(dirname(fileURLToPath(import.meta.url)), '..', 'src');
const dir = join(src, 'lib', 'i18n', 'locales');
const PLURAL = /_(zero|one|two|few|many|other)$/;
const base = (k) => k.replace(PLURAL, '');
const holes = (s) => [...s.matchAll(/\{\{\s*(\w+)\s*\}\}/g)].map((m) => m[1]);
const tags = (s) => [...s.matchAll(/<\/?([a-zA-Z][\w-]*)[^>]*>/g)].map((m) => m[1]);
const load = (f) => JSON.parse(readFileSync(join(dir, f), 'utf8'));

const files = [];
(function walk(d) {
  for (const n of readdirSync(d)) {
    const p = join(d, n);
    if (statSync(p).isDirectory()) walk(p);
    else if (/\.(svelte|js)$/.test(n) && !n.endsWith('.test.js')) files.push([p, readFileSync(p, 'utf8')]);
  }
})(src);
const text = files.map(([, s]) => s).join('\n');

const en = load('en-us.json');
const errors = [];
for (const [path, source] of files) {
  if (/\btx\(/.test(source) && !/import \{[^}]*\btx\b[^}]*\} from|export function tx\(/.test(source)) errors.push(`${path}: uses tx() without importing it`);
  if (/<Rich /.test(source) && !source.includes("Rich.svelte'")) errors.push(`${path}: uses <Rich> without importing it`);
}
const enBase = new Set(Object.keys(en).map(base));
for (const [, , key] of text.matchAll(/(\btx\('|<Rich key=")([a-z0-9_]+)/g)) if (!enBase.has(key) && !(key in en)) errors.push(`en-us.json has no key ${key}`);
for (const key of enBase) if (!text.includes(`'${key}'`) && !text.includes(`"${key}"`)) errors.push(`en-us.json: ${key} is not used in the code`);

const template = Object.fromEntries(Object.keys(en).map((k) => [k, '']));
const templateText = `${JSON.stringify(template, null, 2)}\n`;
if (process.argv.includes('--write')) writeFileSync(join(dir, 'template.json'), templateText);
else if (readFileSync(join(dir, 'template.json'), 'utf8') !== templateText) errors.push('template.json is out of date: npm run i18n:template');

for (const f of readdirSync(dir).filter((n) => n.endsWith('.json') && n !== 'en-us.json' && n !== 'template.json')) {
  if (!/^[a-z]{2,3}(-[a-z0-9]{2,8})*\.json$/.test(f)) errors.push(`${f}: name it like de-de.json`);
  for (const [k, v] of Object.entries(load(f))) {
    if (!enBase.has(base(k))) {
      errors.push(`${f}: unknown key ${k}`);
      continue;
    }
    const allowed = new Set([...holes(en[k] ?? en[`${base(k)}_other`] ?? ''), 'count']);
    for (const t of tags(v)) if (!['strong', 'code', 'kbd'].includes(t)) errors.push(`${f}: ${k} uses <${t}>; only <strong>, <code> and <kbd> work`);
    for (const h of holes(v)) if (!allowed.has(h)) errors.push(`${f}: ${k} uses {{${h}}}, which the English text doesn't have`);
  }
}
for (const e of errors) console.error(e);
if (errors.length) process.exit(1);
console.log(`translations ok: ${Object.keys(en).length} texts`);
