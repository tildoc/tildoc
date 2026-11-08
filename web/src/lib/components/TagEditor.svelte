<script>
  // Tags of one document: remove with ×, add by typing (new names create a tag
  // in the server-wide list).
  import Icon from './Icon.svelte';
  import { post, del } from '../api.js';
  import { session, errorToast, refreshTags } from '../state.svelte.js';

  let { doc, canWrite = false, onchange } = $props();
  let value = $state('');
  let busy = $state(false);
  const listId = `tags-${Math.random().toString(36).slice(2)}`;

  $effect(() => {
    if (canWrite && !session.tags.length) refreshTags().catch(() => {});
  });

  let suggestions = $derived(session.tags.filter((t) => !doc.tags.some((x) => x.id === t.id)));

  async function add(e) {
    e.preventDefault();
    const name = value.trim();
    if (!name || busy) return;
    busy = true;
    try {
      const d = await post(`/documents/${doc.id}/tags`, { name });
      value = '';
      onchange?.(d);
      refreshTags().catch(() => {});
    } catch (err) {
      errorToast(err);
    }
    busy = false;
  }

  async function remove(t) {
    try {
      onchange?.(await del(`/documents/${doc.id}/tags/${t.id}`));
      refreshTags().catch(() => {});
    } catch (err) {
      errorToast(err);
    }
  }
</script>

<div class="tageditor">
  {#each doc.tags as t (t.id)}
    <span class="chip">
      <span class="dot" style:background={t.color || null}></span>{t.name}
      {#if canWrite}<button class="x" onclick={() => remove(t)} aria-label="Remove tag {t.name}"><Icon name="close" size={12} /></button>{/if}
    </span>
  {/each}
  {#if canWrite}
    <form onsubmit={add}>
      <input bind:value list={listId} placeholder={doc.tags.length ? 'Add tag' : 'Add a tag, e.g. Steuer 2025'} aria-label="Add tag" disabled={busy} />
      <datalist id={listId}>{#each suggestions as t (t.id)}<option value={t.name}></option>{/each}</datalist>
    </form>
  {:else if !doc.tags.length}
    <span class="muted small">No tags</span>
  {/if}
</div>

<style>
  .tageditor { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; }
  .chip { display: inline-flex; align-items: center; gap: 5px; padding: 0 4px 0 9px; border: 1px solid var(--line); border-radius: 99px;
    background: var(--surface); font-size: var(--t-sm); line-height: 1.8; }
  .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--ink-2); }
  .x { border: 0; background: none; min-height: 20px; min-width: 20px; padding: 0; border-radius: 50%; color: var(--ink-2); }
  .x:hover:not(:disabled) { background: var(--surface-2); color: var(--ink); }
  form { flex: 1; min-width: 140px; }
  input { min-height: 28px; padding: 2px 8px; font-size: var(--t-sm); }
</style>
