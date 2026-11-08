<script>
  import Icon from './Icon.svelte';
  let { title, onclose, children, footer, wide = false } = $props();
  function key(e) {
    if (e.key === 'Escape') onclose?.();
  }
</script>

<svelte:window onkeydown={key} />
<div class="backdrop" role="presentation" onclick={(e) => e.target === e.currentTarget && onclose?.()}>
  <div class="modal" class:wide role="dialog" aria-modal="true" aria-label={title}>
    <header>
      <h2>{title}</h2>
      <button class="icon" onclick={() => onclose?.()} aria-label="Close"><Icon name="close" size={18} /></button>
    </header>
    <div class="body">{@render children()}</div>
    {#if footer}<footer>{@render footer()}</footer>{/if}
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: var(--scrim); display: grid; place-items: center; z-index: 50; padding: 16px; }
  .modal { background: var(--surface); color: var(--ink); border-radius: var(--r-panel); width: min(460px, 100%); max-height: 88vh;
    display: flex; flex-direction: column; box-shadow: var(--pop-shadow); border: 1px solid var(--line); }
  .modal.wide { width: min(760px, 100%); }
  header { display: flex; align-items: center; justify-content: space-between; padding: 12px 12px 4px 20px; }
  h2 { font-size: var(--t-md); }
  .body { padding: 10px 20px 18px; overflow: auto; }
  footer { display: flex; gap: 8px; justify-content: flex-end; padding: 12px 20px; border-top: 1px solid var(--line); }
</style>
