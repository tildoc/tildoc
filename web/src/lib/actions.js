import { tx } from './i18n/index.js';
// Shared file actions used by several views.
import { post } from './api.js';
import { toast, bumpTree } from './state.svelte.js';

export function splitKeys(keys) {
  return {
    link_ids: keys.filter((k) => k.startsWith('l:')).map((k) => k.slice(2)),
    folder_ids: keys.filter((k) => k.startsWith('f:')).map((k) => k.slice(2))
  };
}

export async function moveKeys(keys, targetFolderId) {
  const ids = splitKeys(keys);
  ids.folder_ids = ids.folder_ids.filter((f) => f !== targetFolderId);
  const res = await post('/bulk/move', { ...ids, target_folder_id: targetFolderId });
  if (res.errors.length) toast(tx('actions_moved_failed', { done: res.done, failed: res.errors.length, error: res.errors[0].error }), 'error', 8000);
  else toast(tx('actions_moved', { count: res.done }), 'success');
  bumpTree();
  window.dispatchEvent(new CustomEvent('tildoc:changed'));
  return res;
}
