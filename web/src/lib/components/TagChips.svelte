<script>
  import { tx } from '#lib/i18n/index.js';
  // Read-only tag chips; onpick makes them clickable (e.g. filter by tag).
  let { tags = [], onpick, small = false } = $props();
</script>

{#if tags?.length}
  <span class="chips" class:small>
    {#each tags as t (t.id)}
      {#if onpick}
        <button class="chip" onclick={(e) => { e.stopPropagation(); onpick(t); }} title={tx('tagchips_show_tagged', { tag: t.name })}>
          <span class="dot" style:background={t.color || null}></span>{t.name}
        </button>
      {:else}
        <span class="chip"><span class="dot" style:background={t.color || null}></span>{t.name}</span>
      {/if}
    {/each}
  </span>
{/if}

<style>
  .chips { display: inline-flex; flex-wrap: wrap; gap: 4px; min-width: 0; }
  .chip { display: inline-flex; align-items: center; gap: 5px; min-height: 0; padding: 0 8px; border: 1px solid var(--line); border-radius: 99px;
    background: var(--surface); color: var(--ink); font-size: var(--t-sm); line-height: 1.55; white-space: nowrap; font-weight: 400; }
  button.chip:hover:not(:disabled) { background: var(--accent-soft); border-color: var(--accent); }
  .small .chip { font-size: 0.75rem; padding: 0 6px; line-height: 1.45; }
  .dot { width: 7px; height: 7px; border-radius: 50%; background: var(--ink-2); flex: none; }
</style>
