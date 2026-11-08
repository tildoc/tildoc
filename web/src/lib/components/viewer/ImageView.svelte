<script>
  // Image on the viewer stage: fit to the window or zoomed (Ctrl+wheel, buttons).
  let { url, alt = '', zoom = $bindable('page'), scale = $bindable(1), mode = 'full', transitionName = '' } = $props();
  let box = $state();
  let nat = $state({ w: 0, h: 0 });
  let cw = $state(0), ch = $state(0);
  let failed = $state(false);

  $effect(() => {
    url;
    failed = false;
    nat = { w: 0, h: 0 };
  });

  const clamp = (s) => Math.min(8, Math.max(0.05, s));
  $effect(() => {
    if (!nat.w || !cw) return;
    const pad = mode === 'peek' ? 12 : 28;
    const fit = Math.min((cw - 2 * pad) / nat.w, (ch - 2 * pad) / nat.h, 1);
    const fitW = (cw - 2 * pad) / nat.w;
    scale = zoom === 'page' || zoom === 'auto' ? clamp(fit) : zoom === 'width' ? clamp(fitW) : clamp(+zoom || 1);
  });

  function setup(el) {
    const ro = new ResizeObserver(() => {
      cw = el.clientWidth;
      ch = el.clientHeight;
    });
    ro.observe(el);
    const wheel = (e) => {
      if (!e.ctrlKey && !e.metaKey) return;
      e.preventDefault();
      zoom = Math.round(clamp(scale * Math.exp(-e.deltaY * 0.002)) * 100) / 100;
    };
    el.addEventListener('wheel', wheel, { passive: false });
    return { destroy: () => (ro.disconnect(), el.removeEventListener('wheel', wheel)) };
  }
</script>

<div class="stage {mode}" bind:this={box} use:setup>
  {#if failed}
    <p class="state">This image can't be displayed. Download it to open it in another program.</p>
  {:else}
    <div class="frame" style:min-width="100%" style:min-height="100%">
      <img src={url} {alt} draggable="false"
        style:width={nat.w ? `${nat.w * scale}px` : null} style:view-transition-name={transitionName || null}
        onload={(e) => (nat = { w: e.currentTarget.naturalWidth, h: e.currentTarget.naturalHeight })}
        onerror={() => (failed = true)} />
    </div>
  {/if}
</div>

<style>
  .stage { height: 100%; overflow: auto; background: var(--stage); color: var(--stage-ink-2); overscroll-behavior: contain; }
  .stage.peek { background: var(--surface-2); color: var(--ink-2); }
  .frame { display: grid; place-items: center; padding: 28px; width: max-content; height: max-content; }
  .peek .frame { padding: 12px; }
  img { display: block; max-width: none; box-shadow: var(--paper-shadow); background: repeating-conic-gradient(#e6e6e6 0 25%, #fff 0 50%) 0 0 / 16px 16px; }
  .state { display: grid; place-items: center; height: 100%; margin: 0; max-width: none; text-align: center; padding: 24px; }
</style>
