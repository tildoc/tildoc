// Global client state (Svelte 5 runes).
import { get, patch, setUnauthorizedHandler, clearCache } from './api.js';
import { startEvents, stopEvents, onEvent } from './events.svelte.js';

export const session = $state({ user: null, ready: false, spaces: [], info: null, teams: [], collections: [], tags: [], office: [] });
export const ui = $state({ toasts: [], uploads: [], sidebarOpen: false, treeVersion: 0, palette: false, returnTo: null, currentSpace: null });

setUnauthorizedHandler(() => {
  session.user = null;
  stopEvents();
  clearCache();
});

let toastId = 0;
export function toast(message, kind = 'info', ms = 4500) {
  const id = ++toastId;
  ui.toasts.push({ id, message, kind });
  setTimeout(() => {
    const i = ui.toasts.findIndex((t) => t.id === id);
    if (i >= 0) ui.toasts.splice(i, 1);
  }, ms);
}

export function errorToast(e) {
  toast(e?.message || String(e), 'error', 7000);
}

// loadSession fetches everything the shell needs in parallel (one round
// trip instead of four) and starts the live event stream.
export async function loadSession() {
  const nr = { noRedirect: true };
  const [me, spaces, colls, info, office] = await Promise.allSettled([
    get('/me', nr),
    get('/spaces', nr),
    get('/collections', nr),
    session.info ? Promise.resolve(session.info) : get('/info', nr),
    get('/office', nr)
  ]);
  if (me.status === 'fulfilled') {
    session.user = me.value;
    session.spaces = spaces.status === 'fulfilled' ? spaces.value : [];
    session.collections = colls.status === 'fulfilled' ? colls.value : [];
    session.office = office.status === 'fulfilled' ? office.value.extensions : [];
    startEvents();
  } else {
    session.user = null;
  }
  if (info.status === 'fulfilled') session.info = info.value;
  session.ready = true;
}

// endSession forgets everything of the signed-out user.
export function endSession() {
  session.user = null;
  stopEvents();
  clearCache();
}

// folder trees reload when folders change anywhere (other tabs, WebDAV, ...)
onEvent((ev) => {
  if ((ev.type === 'space' && ev.tree) || ev.type === 'resync') bumpTree();
});

export async function refreshSpaces() {
  session.spaces = await get('/spaces');
}

export async function refreshCollections() {
  try {
    session.collections = await get('/collections');
  } catch {
    session.collections = [];
  }
}

export async function refreshTags() {
  session.tags = await get('/tags');
  return session.tags;
}

export function spaceById(id) {
  return session.spaces.find((s) => s.id === id);
}

// bumpTree tells folder trees to reload (after structural changes).
export function bumpTree() {
  ui.treeVersion++;
}

// prefs returns the user's preferences with defaults.
export function prefs() {
  return { pdf_viewer: 'pdfjs', ...(session.user?.prefs ?? {}) };
}

export async function setPref(key, value) {
  const u = await patch('/me', { prefs: { [key]: value } });
  session.user = u;
  return u.prefs;
}

// Theme is a per-browser choice: system | light | dark.
export function getTheme() {
  try {
    return localStorage.getItem('tildoc.theme') || 'system';
  } catch {
    return 'system';
  }
}

export function applyTheme(t = getTheme()) {
  const el = document.documentElement;
  if (t === 'light' || t === 'dark') el.dataset.theme = t;
  else delete el.dataset.theme;
}

export function setTheme(t) {
  try {
    localStorage.setItem('tildoc.theme', t);
  } catch {}
  applyTheme(t);
}

// stored reads/writes small per-browser UI conveniences.
export function stored(key, def) {
  try {
    const v = localStorage.getItem('tildoc.' + key);
    return v === null ? def : JSON.parse(v);
  } catch {
    return def;
  }
}
export function store(key, v) {
  try {
    localStorage.setItem('tildoc.' + key, JSON.stringify(v));
  } catch {}
}
