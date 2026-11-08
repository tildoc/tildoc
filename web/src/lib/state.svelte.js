// Global client state (Svelte 5 runes).
import { get, patch, setUnauthorizedHandler } from './api.js';

export const session = $state({ user: null, ready: false, spaces: [], info: null, teams: [], collections: [], tags: [] });
export const ui = $state({ toasts: [], uploads: [], sidebarOpen: false, treeVersion: 0, palette: false, returnTo: null });

setUnauthorizedHandler(() => {
  session.user = null;
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

export async function loadSession() {
  try {
    session.user = await get('/me', { noRedirect: true });
    await refreshSpaces();
    await refreshCollections();
    session.info ??= await get('/info');
  } catch {
    session.user = null;
  }
  session.ready = true;
}

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
