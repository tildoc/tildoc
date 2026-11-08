// Global client state (Svelte 5 runes).
import { get, setUnauthorizedHandler } from './api.js';

export const session = $state({ user: null, ready: false, spaces: [], info: null, teams: [] });
export const ui = $state({ toasts: [], uploads: [], sidebarOpen: false, treeVersion: 0 });

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
    session.info ??= await get('/info');
  } catch {
    session.user = null;
  }
  session.ready = true;
}

export async function refreshSpaces() {
  session.spaces = await get('/spaces');
}

export function spaceById(id) {
  return session.spaces.find((s) => s.id === id);
}

// bumpTree tells folder trees to reload (after structural changes).
export function bumpTree() {
  ui.treeVersion++;
}
