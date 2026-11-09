// CodeMirror 6 for the text editor, loaded on demand (like pdf.js). Search
// and replace (Ctrl+F), move lines (Alt+↑/↓), duplicate (Shift+Alt+↓), undo
// history, line numbers, syntax highlighting; colours come from the design
// tokens, so light and dark themes follow the UI.
import { EditorState, Compartment } from '@codemirror/state';
import {
  EditorView, keymap, lineNumbers, highlightActiveLine, highlightActiveLineGutter, drawSelection,
  highlightSpecialChars, rectangularSelection
} from '@codemirror/view';
import { defaultKeymap, history, historyKeymap, indentWithTab } from '@codemirror/commands';
import { search, searchKeymap, highlightSelectionMatches } from '@codemirror/search';
import { syntaxHighlighting, HighlightStyle, bracketMatching, indentOnInput, foldGutter, foldKeymap } from '@codemirror/language';
import { tags as t } from '@lezer/highlight';
import { unifiedMergeView } from '@codemirror/merge';

// language support by file extension (or MIME type), each loaded on demand
const LANGS = {
  markdown: () => import('@codemirror/lang-markdown').then((m) => m.markdown()),
  json: () => import('@codemirror/lang-json').then((m) => m.json()),
  xml: () => import('@codemirror/lang-xml').then((m) => m.xml()),
  html: () => import('@codemirror/lang-html').then((m) => m.html()),
  css: () => import('@codemirror/lang-css').then((m) => m.css()),
  javascript: () => import('@codemirror/lang-javascript').then((m) => m.javascript()),
  yaml: () => import('@codemirror/lang-yaml').then((m) => m.yaml())
};
const BY_EXT = {
  md: 'markdown', markdown: 'markdown', json: 'json', xml: 'xml', svg: 'xml', html: 'html', htm: 'html',
  css: 'css', js: 'javascript', mjs: 'javascript', cjs: 'javascript', ts: 'javascript', yaml: 'yaml', yml: 'yaml'
};
const BY_MIME = { 'text/markdown': 'markdown', 'text/html': 'html', 'text/xml': 'xml', 'text/css': 'css', 'text/javascript': 'javascript', 'text/yaml': 'yaml' };

export function languageName(name = '', mime = '') {
  const ext = name.includes('.') ? name.split('.').pop().toLowerCase() : '';
  return BY_EXT[ext] ?? BY_MIME[mime.split(';')[0]] ?? '';
}

async function language(name) {
  try {
    return LANGS[name] ? [await LANGS[name]()] : [];
  } catch {
    return []; // highlighting is optional
  }
}

const highlight = HighlightStyle.define([
  { tag: t.heading, fontWeight: '700', color: 'var(--accent-ink)' },
  { tag: [t.strong], fontWeight: '700' },
  { tag: [t.emphasis], fontStyle: 'italic' },
  { tag: [t.link, t.url], color: 'var(--accent)', textDecoration: 'underline' },
  { tag: [t.keyword, t.operatorKeyword, t.modifier], color: 'var(--accent)' },
  { tag: [t.string, t.special(t.string), t.regexp], color: 'var(--ok)' },
  { tag: [t.number, t.bool, t.null, t.atom], color: 'var(--manila)' },
  { tag: [t.comment, t.meta, t.processingInstruction], color: 'var(--ink-2)', fontStyle: 'italic' },
  { tag: [t.propertyName, t.attributeName, t.labelName], color: 'var(--err)' },
  { tag: [t.tagName, t.typeName, t.className], color: 'var(--accent)' },
  { tag: [t.quote, t.monospace], color: 'var(--ink-2)' },
  { tag: t.invalid, color: 'var(--err)' }
]);

const theme = EditorView.theme({
  '&': { height: '100%', backgroundColor: 'var(--editor)', color: 'var(--ink)', fontSize: '0.92rem' },
  '.cm-scroller': { fontFamily: 'var(--font-mono)', lineHeight: '1.55' },
  '.cm-content': { caretColor: 'var(--ink)', padding: '10px 0' },
  '.cm-gutters': { backgroundColor: 'var(--surface)', color: 'var(--ink-2)', borderRight: '1px solid var(--line)' },
  '.cm-activeLine': { backgroundColor: 'color-mix(in srgb, var(--accent-soft) 45%, transparent)' },
  '.cm-activeLineGutter': { backgroundColor: 'var(--accent-soft)', color: 'var(--ink)' },
  '&.cm-focused': { outline: 'none' },
  '&.cm-focused .cm-cursor': { borderLeftColor: 'var(--ink)' },
  '&.cm-focused .cm-selectionBackground, .cm-selectionBackground, ::selection': { backgroundColor: 'color-mix(in srgb, var(--accent) 30%, transparent)' },
  '.cm-searchMatch': { backgroundColor: 'color-mix(in srgb, var(--highlight) 55%, transparent)' },
  '.cm-searchMatch.cm-searchMatch-selected': { backgroundColor: 'var(--highlight)' },
  '.cm-selectionMatch': { backgroundColor: 'color-mix(in srgb, var(--highlight) 30%, transparent)' },
  '.cm-panels': { backgroundColor: 'var(--surface)', color: 'var(--ink)', borderColor: 'var(--line)' },
  '.cm-panel.cm-search': { fontFamily: 'var(--font-ui)', padding: '6px 8px' },
  '.cm-panel.cm-search input, .cm-panel.cm-search button, .cm-panel.cm-search label': { fontSize: 'var(--t-sm)' },
  '.cm-textfield': { backgroundColor: 'var(--editor)', color: 'var(--ink)', border: '1px solid var(--line)', borderRadius: 'var(--r-control)' },
  '.cm-button': { backgroundImage: 'none', backgroundColor: 'var(--surface-2)', color: 'var(--ink)', border: '1px solid var(--line)', borderRadius: 'var(--r-control)' },
  // diff (review before saving)
  '.cm-changedLine': { backgroundColor: 'color-mix(in srgb, var(--ok) 12%, transparent)' },
  '.cm-changedText': { backgroundColor: 'color-mix(in srgb, var(--ok) 30%, transparent)' },
  '.cm-deletedChunk': { backgroundColor: 'color-mix(in srgb, var(--err) 12%, transparent)' },
  '.cm-deletedChunk .cm-deletedText, .cm-deletedChunk del': { backgroundColor: 'color-mix(in srgb, var(--err) 28%, transparent)' },
  '.cm-collapsedLines': { backgroundColor: 'var(--surface)', color: 'var(--ink-2)', fontFamily: 'var(--font-ui)' }
});

const basics = () => [
  lineNumbers(), foldGutter(), highlightSpecialChars(), history(), drawSelection(), rectangularSelection(),
  indentOnInput(), bracketMatching(), highlightActiveLine(), highlightActiveLineGutter(), highlightSelectionMatches(),
  search({ top: true }), syntaxHighlighting(highlight), theme, EditorView.lineWrapping
];

// createEditor mounts an editor. crlf keeps Windows line endings on save.
export async function createEditor({ parent, text, lang = '', crlf = false, readOnly = false, onChange, onSave }) {
  const ro = new Compartment();
  const roExt = (v) => [EditorState.readOnly.of(v), EditorView.editable.of(!v)];
  const view = new EditorView({
    parent,
    state: EditorState.create({
      doc: text,
      extensions: [
        basics(),
        ...(crlf ? [EditorState.lineSeparator.of('\r\n')] : []),
        await language(lang),
        ro.of(roExt(readOnly)),
        keymap.of([
          { key: 'Mod-s', preventDefault: true, run: () => (onSave?.(), true) },
          ...defaultKeymap, ...historyKeymap, ...searchKeymap, ...foldKeymap, indentWithTab
        ]),
        EditorView.updateListener.of((u) => u.docChanged && onChange?.())
      ]
    })
  });
  return {
    view,
    getText: () => view.state.doc.toString(),
    setText: (s) => view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: s } }),
    setReadOnly: (v) => view.dispatch({ effects: ro.reconfigure(roExt(v)) }),
    focus: () => view.focus(),
    destroy: () => view.destroy()
  };
}

// createDiff shows modified with the changes against original inline
// (deleted lines red, new ones green), unchanged stretches collapsed.
export async function createDiff({ parent, original, modified, lang = '' }) {
  return new EditorView({
    parent,
    state: EditorState.create({
      doc: modified,
      extensions: [
        lineNumbers(), syntaxHighlighting(highlight), theme, EditorView.lineWrapping, await language(lang),
        EditorState.readOnly.of(true), EditorView.editable.of(false),
        unifiedMergeView({ original, mergeControls: false, highlightChanges: true, gutter: true, collapseUnchanged: { margin: 3, minSize: 6 } })
      ]
    })
  });
}
