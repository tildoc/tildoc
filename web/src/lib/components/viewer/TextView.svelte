<script>
  // Plain text in a reading column; code-like types use a monospace face.
  import { tick } from 'svelte';
  import { terms as parseTerms, findAll, segments } from '../../viewer/find.js';

  const LIMIT = 2 << 20;
  let { url, mime = '', name = '', size = 0, query = '', mode = 'full', matchCount = $bindable(0), current = $bindable(-1) } = $props();
  let text = $state(null);
  let error = $state('');
  let body = $state();

  const codeLike = $derived(/json|xml|csv|yaml|toml|x-|javascript|css|html/.test(mime) || /\.(go|js|ts|py|rs|c|h|sh|json|ya?ml|toml|ini|conf|log|csv|sql|xml|html?)$/i.test(name));

  $effect(() => {
    const u = url;
    text = null;
    error = '';
    fetch(u, { headers: { Range: `bytes=0-${LIMIT - 1}` }, credentials: 'same-origin' })
      .then((r) => (r.ok ? r.text() : Promise.reject(new Error(r.statusText))))
      .then((t) => (text = t))
      .catch((e) => (error = e.message));
  });

  let ranges = $derived(text ? findAll(text, parseTerms(query)) : []);
  let segs = $derived(text ? segments(text, ranges) : []);

  $effect(() => {
    matchCount = ranges.length;
    current = ranges.length ? 0 : -1;
  });

  $effect(() => {
    const c = current;
    if (c < 0) return;
    tick().then(() => body?.querySelector(`[data-hit="${c}"]`)?.scrollIntoView({ block: 'center' }));
  });

  export function findNext() {
    if (ranges.length) current = (current + 1) % ranges.length;
  }
  export function findPrev() {
    if (ranges.length) current = (current - 1 + ranges.length) % ranges.length;
  }
</script>

<div class="stage {mode}">
  {#if error}
    <p class="state">This file can't be displayed ({error}).</p>
  {:else if text === null}
    <p class="state">Opening document…</p>
  {:else}
    <article class="sheet-text" class:code={codeLike} bind:this={body}>
      {#each segs as s}{#if s.hit}<mark data-hit={s.index} class:current={s.index === current}>{s.text}</mark>{:else}{s.text}{/if}{/each}
      {#if size > LIMIT}<p class="cut">Showing the first 2 MB of {Math.round(size / 1048576)} MB. Download the file to read all of it.</p>{/if}
    </article>
  {/if}
</div>

<style>
  .stage { height: 100%; overflow: auto; background: var(--stage); padding: 28px; overscroll-behavior: contain; }
  .stage.peek { background: var(--surface-2); padding: 12px; }
  .sheet-text {
    background: var(--paper); color: #1d2a35; box-shadow: var(--paper-shadow);
    max-width: 75ch; margin: 0 auto; padding: 48px 56px; min-height: 100%;
    font-family: var(--font-read); font-size: 1.0667rem; line-height: 1.65;
    white-space: pre-wrap; overflow-wrap: anywhere;
  }
  .peek .sheet-text { padding: 20px 22px; font-size: var(--t-sm); }
  .sheet-text.code { font-family: var(--font-mono); font-size: 0.8667rem; line-height: 1.55; max-width: 110ch; white-space: pre; overflow-wrap: normal; overflow-x: auto; }
  mark { background: #f2e35b; color: inherit; }
  mark.current { background: #e8c71a; box-shadow: 0 0 0 2px #9a7b00; }
  .cut { font-family: var(--font-ui); color: #56636b; border-top: 1px solid #dde2da; padding-top: 12px; margin-top: 24px; white-space: normal; }
  .state { color: var(--stage-ink-2); text-align: center; margin: 40px auto; }
  .peek .state { color: var(--ink-2); }
  @media (max-width: 760px) { .stage { padding: 12px; } .sheet-text { padding: 24px 20px; } }
</style>
