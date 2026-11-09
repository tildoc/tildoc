// Live updates from GET /api/v1/events (Server-Sent Events). Events only name
// what changed (space, documents, transfer); views reload through the API.
// While the stream is down, watchBusy (poll.svelte.js) falls back to polling.

export const live = $state({ connected: false });

const handlers = new Set();
let es = null;
let retry = null;
let everReady = false;

function emit(ev) {
  for (const fn of handlers) {
    try {
      fn(ev);
    } catch (e) {
      console.warn('event handler', e);
    }
  }
}

export function startEvents() {
  if (es || typeof EventSource === 'undefined') return;
  clearTimeout(retry);
  es = new EventSource('/api/v1/events');
  es.addEventListener('ready', () => {
    live.connected = true;
    // anything may have changed while we were not connected
    if (everReady) emit({ type: 'resync' });
    everReady = true;
  });
  es.onmessage = (e) => {
    let evs;
    try {
      evs = JSON.parse(e.data);
    } catch {
      return;
    }
    for (const ev of evs) emit(ev);
  };
  es.onerror = () => {
    live.connected = false;
    // EventSource reconnects by itself unless the server refused (401, 429)
    if (es?.readyState === EventSource.CLOSED) {
      es = null;
      retry = setTimeout(startEvents, 15000);
    }
  };
}

export function stopEvents() {
  clearTimeout(retry);
  es?.close();
  es = null;
  everReady = false;
  live.connected = false;
}

// onEvent registers a handler; returns the unsubscribe function.
export function onEvent(fn) {
  handlers.add(fn);
  return () => handlers.delete(fn);
}

// debounce coalesces bursts (a bulk upload sends many events).
export function debounce(fn, ms = 300) {
  let t;
  const d = (...a) => {
    clearTimeout(t);
    t = setTimeout(() => fn(...a), ms);
  };
  d.cancel = () => clearTimeout(t);
  return d;
}

// watchLive calls reload() (debounced) when match(event) is true or after a
// resync. Call during component initialisation.
export function watchLive(match, reload, ms = 300) {
  $effect(() => {
    const r = debounce(reload, ms);
    const off = onEvent((ev) => {
      if (ev.type === 'resync' || match(ev)) r();
    });
    return () => {
      off();
      r.cancel();
    };
  });
}
