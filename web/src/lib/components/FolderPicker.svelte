<script>
  import { tx } from '#lib/i18n/index.js';
  import { untrack } from 'svelte';
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';
  import { get } from '../api.js';
  import { session } from '../state.svelte.js';

  // Picks a target folder in a writable space (move, copy).
  let { title, confirm = tx('filetable_select'), spaceId, exclude = [], onpick, onclose } = $props();
  let space = $state(untrack(() => spaceId));
  let tree = $state([]);
  let selected = $state(null);
  let spaces = $derived(session.spaces.filter((s) => s.perm >= 2));

  $effect(() => {
    const id = space;
    get(`/spaces/${id}/tree`).then((t) => {
      tree = t;
      selected = t[0]?.id ?? null;
    });
  });
  const depth = (p) => (p === '/' ? 0 : p.split('/').length - 1);
  const excluded = (f) => exclude.some((ex) => tree.find((t) => t.id === ex) && (f.id === ex || f.path.startsWith(tree.find((t) => t.id === ex).path + '/')));
</script>

<Modal {title} {onclose}>
  <label>{tx('common_space')}
    <select bind:value={space}>
      {#each spaces as s}<option value={s.id}>{s.name} ({s.slug})</option>{/each}
    </select>
  </label>
  <ul class="picker">
    {#each tree as f (f.id)}
      <li>
        <button class:sel={selected === f.id} disabled={excluded(f)} style="padding-left:{8 + depth(f.path) * 16}px"
          onclick={() => (selected = f.id)} ondblclick={() => onpick(selected, space)}>
          <Icon name={f.path === '/' ? 'home' : 'folder'} size={16} />
          {f.path === '/' ? tx('folderpicker_top_level') : f.name}
        </button>
      </li>
    {/each}
  </ul>
  <div class="row end">
    <button onclick={onclose}>{tx('common_cancel')}</button>
    <button class="primary" disabled={!selected} onclick={() => onpick(selected, space)}>{confirm}</button>
  </div>
</Modal>

<style>
  .picker { list-style: none; margin: 4px 0 14px; padding: 4px; max-height: 50vh; overflow: auto; border: 1px solid var(--line); border-radius: var(--r-control); background: var(--paper); }
  :global(:root[data-theme='dark']) .picker { background: var(--board); }
  @media (prefers-color-scheme: dark) { :global(:root:not([data-theme='light'])) .picker { background: var(--board); } }
  .picker button { width: 100%; justify-content: flex-start; text-align: left; border: 0; background: none; display: flex; gap: 7px; align-items: center; border-radius: var(--r-control); padding: 4px 8px; min-height: 30px; }
  .picker button :global(svg) { color: var(--manila); }
  .picker button.sel { background: var(--accent-soft); color: var(--accent-ink); font-weight: 600; }
  .picker button:disabled { opacity: 0.4; }
</style>
