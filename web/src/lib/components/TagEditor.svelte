<script>
  import { tx } from '#lib/i18n/index.js';
  // Tags of one document as a token field: the tags are chips inside the
  // text box, typing adds (Enter or comma; new names create a tag in the
  // server-wide list), × or Backspace in the empty box removes.
  import { tick } from 'svelte';
  import Icon from './Icon.svelte';
  import { post, del } from '../api.js';
  import { session, errorToast, refreshTags } from '../state.svelte.js';

  let { doc, canWrite = false, onchange } = $props();
  let value = $state('');
  let busy = $state(false);
  let input = $state();
  const listId = `tags-${Math.random().toString(36).slice(2)}`;

  $effect(() => {
    if (canWrite && !session.tags.length) refreshTags().catch(() => {});
  });

  let suggestions = $derived(session.tags.filter((t) => !doc.tags.some((x) => x.id === t.id)));

  async function add(e) {
    e?.preventDefault();
    const name = value.trim().replace(/,$/, '').trim();
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
    await tick(); // the input is enabled again only after the update
    input?.focus();
  }

  function keydown(e) {
    if (e.key === ',') {
      e.preventDefault();
      add();
    } else if (e.key === 'Backspace' && value === '' && doc.tags.length) {
      e.preventDefault();
      remove(doc.tags[doc.tags.length - 1]);
    }
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

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="tageditor" class:field={canWrite} onclick={(e) => e.target === e.currentTarget && input?.focus()}>
  {#each doc.tags as t (t.id)}
    <span class="chip">
      <span class="dot" style:background={t.color || null}></span>{t.name}
      {#if canWrite}<button type="button" class="x" onclick={() => remove(t)} aria-label={tx('tageditor_remove_tag', { tag: t.name })}><Icon name="close" size={12} /></button>{/if}
    </span>
  {/each}
  {#if canWrite}
    <form onsubmit={add}>
      <input bind:this={input} bind:value list={listId} onkeydown={keydown} placeholder={doc.tags.length ? '' : tx('tageditor_add_a_tag_e_g_steuer')}
        aria-label={tx('tageditor_add_tag')} disabled={busy} />
      <datalist id={listId}>{#each suggestions as t (t.id)}<option value={t.name}></option>{/each}</datalist>
    </form>
  {:else if !doc.tags.length}
    <span class="muted small">{tx('tageditor_no_tags')}</span>
  {/if}
</div>

<style>
  .tageditor { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; }
  /* looks and behaves like one text input; the chips sit inside it */
  .tageditor.field { min-height: 34px; padding: 3px 6px; border: 1px solid var(--line); border-radius: var(--r-control); background: var(--paper); cursor: text; }
  :global(:root[data-theme='dark']) .tageditor.field { background: var(--board); }
  @media (prefers-color-scheme: dark) { :global(:root:not([data-theme='light'])) .tageditor.field { background: var(--board); } }
  .tageditor.field:focus-within { outline: 2px solid color-mix(in srgb, var(--accent) 35%, transparent); border-color: var(--accent); }
  .chip { display: inline-flex; align-items: center; gap: 5px; padding: 0 4px 0 9px; border: 1px solid var(--line); border-radius: 99px;
    background: var(--surface); font-size: var(--t-sm); line-height: 1.8; }
  .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--ink-2); }
  .x { border: 0; background: none; min-height: 20px; min-width: 20px; padding: 0; border-radius: 50%; color: var(--ink-2); }
  .x:hover:not(:disabled) { background: var(--surface-2); color: var(--ink); }
  form { flex: 1; min-width: 90px; }
  input { min-height: 24px; padding: 0 2px; font-size: var(--t-sm); border: 0; background: transparent; width: 100%; }
  input:focus { outline: none; border: 0; }
</style>
