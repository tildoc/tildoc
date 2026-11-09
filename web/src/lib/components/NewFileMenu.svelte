<script>
  // "New file": a text file, or any format the office server can create.
  import Icon from './Icon.svelte';
  import { session } from '../state.svelte.js';
  import { newFileTypes } from '../format.js';

  let { onpick } = $props();
  let open = $state(false);
  let root = $state();
  const types = $derived(newFileTypes(session.office.create));

  function pick(t) {
    open = false;
    onpick(t);
  }
  function outside(e) {
    if (open && !root.contains(e.target)) open = false;
  }
  function key(e) {
    if (open && e.key === 'Escape') {
      e.preventDefault();
      open = false;
    }
  }
</script>

<svelte:window onclick={outside} onkeydown={key} />

<div class="wrap" bind:this={root}>
  <button class="ghost" aria-haspopup="menu" aria-expanded={open} onclick={() => (open = !open)}><Icon name="plus" size={16} /> New file</button>
  {#if open}
    <div class="menu" role="menu">
      <button role="menuitem" onclick={() => pick({ ext: '', label: 'text file' })}>Text file</button>
      {#each types as t (t.ext)}
        <button role="menuitem" onclick={() => pick({ ext: t.ext, label: t.label })}>{t.label} <small>.{t.ext}</small></button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .wrap { position: relative; display: inline-block; }
  .menu { position: absolute; left: 0; top: 110%; z-index: 20; min-width: 240px; max-height: 60vh; overflow-y: auto; background: var(--surface); border: 1px solid var(--line); border-radius: var(--r-panel); padding: 4px; display: flex; flex-direction: column; }
  .menu button { justify-content: space-between; border: 0; background: none; text-align: left; width: 100%; display: flex; gap: 12px; }
  .menu button:hover, .menu button:focus-visible { background: var(--surface-2); }
  small { color: var(--ink-2, inherit); opacity: .7; }
</style>
