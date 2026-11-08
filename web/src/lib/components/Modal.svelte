<script>
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
      <button class="icon" onclick={() => onclose?.()} aria-label="Close">×</button>
    </header>
    <div class="body">{@render children()}</div>
    {#if footer}<footer>{@render footer()}</footer>{/if}
  </div>
</div>

<style>
  .backdrop { position: fixed; inset: 0; background: rgb(0 0 0 / 0.45); display: grid; place-items: center; z-index: 50; padding: 16px; }
  .modal { background: var(--surface); color: var(--text); border-radius: 12px; width: min(460px, 100%); max-height: 88vh;
    display: flex; flex-direction: column; box-shadow: 0 20px 60px rgb(0 0 0 / 0.35); border: 1px solid var(--border); }
  .modal.wide { width: min(760px, 100%); }
  header { display: flex; align-items: center; justify-content: space-between; padding: 14px 18px; border-bottom: 1px solid var(--border); }
  h2 { font-size: 1.05rem; margin: 0; }
  .body { padding: 16px 18px; overflow: auto; }
  footer { display: flex; gap: 8px; justify-content: flex-end; padding: 12px 18px; border-top: 1px solid var(--border); }
  .icon { font-size: 1.4rem; line-height: 1; padding: 2px 8px; }
</style>
