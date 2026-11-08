// Fallback for live updates: while something shown is still queued or
// processing and the event stream is down, refresh with backoff
// (2 s → 15 s). Paused while the tab is hidden, refreshed at once when it
// becomes visible again.
import { live } from './events.svelte.js';

const BUSY = new Set(['pending', 'processing']);

export const isBusy = (status) => BUSY.has(status);

// watchBusy must be called during component initialisation. busy() is
// reactive (e.g. "any row is busy"); refresh() reloads the data quietly.
export function watchBusy(busy, refresh) {
  const active = $derived(!!busy() && !live.connected); // re-run only when this flips
  $effect(() => {
    if (!active) return;
    let delay = 2000;
    let timer;
    let stopped = false;
    const tick = async () => {
      if (stopped) return;
      if (document.hidden) return; // resumed by the visibilitychange handler
      try {
        await refresh();
      } catch {}
      if (stopped) return;
      delay = Math.min(Math.round(delay * 1.5), 15000);
      timer = setTimeout(tick, delay);
    };
    const visible = () => {
      if (document.hidden || stopped) return;
      clearTimeout(timer);
      tick();
    };
    document.addEventListener('visibilitychange', visible);
    timer = setTimeout(tick, delay);
    return () => {
      stopped = true;
      clearTimeout(timer);
      document.removeEventListener('visibilitychange', visible);
    };
  });
}
