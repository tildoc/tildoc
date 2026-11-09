// openView navigates to the viewer. A navigation that is superseded (the
// user goes back or opens something else before it finished) rejects with
// "navigation aborted"; that is expected and must not surface as an error.
import { goto, replaceState } from '$app/navigation';
import { page } from '$app/state';

export function openView(url, opts) {
  return goto(url, opts).catch((e) => {
    if (!/navigation aborted/i.test(e?.message ?? '')) throw e;
  });
}

// syncSelection keeps ?sel=<file> in the address bar in step with the
// selected file, without navigating (shallow routing): the URL can be copied
// and sent, and opens the same folder with that file selected and previewed
// for anyone who can read the space. replaceState: selecting does not add
// browser history entries.
export function syncSelection(linkId) {
  const u = new URL(location.href);
  if ((u.searchParams.get('sel') ?? null) === (linkId ?? null)) return;
  if (linkId) u.searchParams.set('sel', linkId);
  else u.searchParams.delete('sel');
  try {
    replaceState(u, page.state);
  } catch {} // router not ready yet (first render): the next change syncs
}
