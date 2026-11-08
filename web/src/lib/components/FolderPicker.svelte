<script>
  import { untrack } from 'svelte';
  import Modal from './Modal.svelte';
  import Icon from './Icon.svelte';
  import { get } from '../api.js';
  import { session } from '../state.svelte.js';

  // mode: move | copy | link. sameSpace restricts the choice to spaceId.
  let { title, confirm = 'Select', spaceId, sameSpace = false, exclude = [], onpick, onclose } = $props();
  let space = $state(untrack(() => spaceId));
  let tree = $state([]);
  let selected = $state(null);
  let spaces = $derived(session.spaces.filter((s) => s.perm >= 2 && (!sameSpace || s.id === spaceId)));

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
  <label>Space
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
          {f.path === '/' ? '(root)' : f.name}
        </button>
      </li>
    {/each}
  </ul>
  <div class="row end">
    <button onclick={onclose}>Cancel</button>
    <button class="primary" disabled={!selected} onclick={() => onpick(selected, space)}>{confirm}</button>
  </div>
</Modal>

<style>
  .picker { list-style: none; margin: 10px 0; padding: 4px; max-height: 50vh; overflow: auto; border: 1px solid var(--border); border-radius: 8px; }
  .picker button { width: 100%; text-align: left; border: 0; background: none; display: flex; gap: 6px; align-items: center; border-radius: 6px; padding: 5px 8px; }
  .picker button.sel { background: var(--accent-soft); color: var(--accent-text); }
  .picker button:disabled { opacity: 0.4; }
</style>
