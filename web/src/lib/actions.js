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
  if (res.errors.length) toast(`Moved ${res.done}, ${res.errors.length} failed: ${res.errors[0].error}`, 'error', 8000);
  else toast(`Moved ${res.done} item${res.done === 1 ? '' : 's'}`, 'success');
  bumpTree();
  window.dispatchEvent(new CustomEvent('tildoc:changed'));
  return res;
}
