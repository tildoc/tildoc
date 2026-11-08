// openView navigates to the viewer. A navigation that is superseded (the
// user goes back or opens something else before it finished) rejects with
// "navigation aborted"; that is expected and must not surface as an error.
import { goto } from '$app/navigation';

export function openView(url, opts) {
  return goto(url, opts).catch((e) => {
    if (!/navigation aborted/i.test(e?.message ?? '')) throw e;
  });
}
