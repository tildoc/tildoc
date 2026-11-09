// UI text. en-us.json is the source; the files are i18next JSON (v4), which
// Weblate reads: flat keys, {{name}} placeholders, key_one / key_other for
// plurals. tx() needs no framework, so that plain modules and tests can use it.
// The language is settled before the UI renders; changing it reloads the page.
import source from './locales/en-us.json' with { type: 'json' };

export const SOURCE_LOCALE = 'en-us';

let locale = SOURCE_LOCALE;
let messages = source;
let plurals = new Intl.PluralRules(SOURCE_LOCALE);

function fill(text, vars) {
  return text.replace(/\{\{\s*(\w+)\s*\}\}/g, (m, name) => (vars && name in vars ? String(vars[name]) : m));
}

// An empty text counts as not translated yet.
function lookup(key, vars) {
  if (vars && typeof vars.count === 'number') {
    const k = `${key}_${plurals.select(vars.count)}`;
    const text = messages[k] || messages[`${key}_other`] || source[k] || source[`${key}_other`];
    if (text) return text;
  }
  return messages[key] || source[key] || key;
}

// tx returns the text for key in the current language, or the English one.
export function tx(key, vars) {
  return fill(lookup(key, vars), vars);
}

// txParts splits a text with <strong>, <code> or <kbd> for Rich.svelte. Values
// are filled in after parsing, so a file name is never read as markup.
export function txParts(key, vars) {
  const raw = lookup(key, vars);
  const parts = [];
  let last = 0;
  for (const m of raw.matchAll(/<(strong|code|kbd)>(.*?)<\/\1>/g)) {
    if (m.index > last) parts.push({ text: fill(raw.slice(last, m.index), vars) });
    parts.push({ tag: m[1], text: fill(m[2], vars) });
    last = m.index + m[0].length;
  }
  if (last < raw.length) parts.push({ text: fill(raw.slice(last), vars) });
  return parts;
}

// labels makes an object of translated names: each is looked up when read
// (module-level constants exist before the language is chosen).
export function labels(keys) {
  return new Proxy({}, { get: (_, name) => (Object.hasOwn(keys, name) ? tx(keys[name]) : undefined) });
}

export function currentLocale() {
  return locale;
}

// formatLocale is for Intl: an English interface keeps the browser's own date
// and number formats, a translation uses those of its language.
export function formatLocale() {
  return locale === SOURCE_LOCALE ? undefined : locale;
}

function use(id, table) {
  locale = id;
  messages = table;
  plurals = new Intl.PluralRules(id);
  if (typeof document !== 'undefined') document.documentElement.lang = id;
}

// matchLocale picks the best available locale for a list of language tags.
export function matchLocale(wanted, available) {
  const have = [SOURCE_LOCALE, ...available];
  for (const tag of wanted) {
    const w = tag.toLowerCase();
    const hit = have.find((a) => a === w) ?? have.find((a) => a.split('-')[0] === w.split('-')[0]);
    if (hit) return hit;
  }
  return SOURCE_LOCALE;
}

// setLocale switches to a locale; loaders maps ids to functions importing the file.
export async function setLocale(id, loaders = {}) {
  if (id === SOURCE_LOCALE || !loaders[id]) {
    use(SOURCE_LOCALE, source);
    return;
  }
  const mod = await loaders[id]();
  use(id, mod.default ?? mod);
}

// for tests
export function setMessages(id, table) {
  use(id, table);
}
