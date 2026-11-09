# Translating tildoc

The interface text lives in `web/src/lib/i18n/locales/`. `en-us.json` is the
source. You need no build setup to translate.

## Add a language

1. Copy `template.json` to a file named after the language, in lower case: `de-de.json`, `fr-fr.json`, `pt-br.json`.
2. Fill in the values. Leave a text empty if you don't know it yet: it shows in English.
3. Open a pull request. By contributing you agree to [`CLA.md`](../CLA.md).

The language then appears in Settings, and is chosen automatically for browsers set to it. Partial translations are welcome.

## Format

The files are [i18next JSON v4](https://www.i18next.com/misc/json-format), which translation tools such as Weblate read.

| In the text | Meaning | Rule |
|---|---|---|
| `{{name}}` | a value filled in by the program | keep the name, move it wherever your grammar needs it |
| `<strong>…</strong>`, `<code>…</code>`, `<kbd>…</kbd>` | emphasis | keep the pair around the matching words; no other tags work |
| `key_one`, `key_other` | plural forms | add the forms your language has |

Plural forms follow the [CLDR categories](https://www.unicode.org/cldr/charts/latest/supplemental/language_plural_rules.html): `_zero`, `_one`, `_two`, `_few`, `_many`, `_other`. The template only has the English ones. Polish, for example, adds `search_total_few` and `search_total_many` next to `search_total_one` and `search_total_other`. Every language needs `_other`.

Keep product and file names such as tildoc, WebDAV or OCR unless your language has its own established word.

## Check your file

```
cd web && npm run i18n:check
```

It reports unknown keys, placeholders the English text doesn't have, and tags that don't work. It also runs on every pull request.

## For developers

Never hard-code text. Use `tx('key', { name })`, add the text to `en-us.json`, then:

```
cd web && npm run i18n:template
```

Write whole sentences: don't glue fragments together, and don't pass English words in placeholder values. For inline emphasis use `<Rich key="…" vars={…} />`. For plurals pass a numeric `count`. Constants at module level use `labels()`, because they exist before the language is chosen.

Server messages are not translated yet.
