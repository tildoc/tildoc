<script>
  import { tx } from '#lib/i18n/index.js';
  import Icon from './Icon.svelte';
  import { get } from '../api.js';
  import { ui } from '../state.svelte.js';
  import { goto } from '$app/navigation';

  // Tree of one space; current = active folder id. ondropon(keys, folderId).
  let { space, current, ondropon } = $props();
  let open = $state(false);
  let tree = $state([]);
  let expanded = $state({});
  let over = $state(null);

  async function load() {
    tree = await get(`/spaces/${space.id}/tree`);
  }

  $effect(() => {
    ui.treeVersion;
    if (open) load();
  });

  // auto-expand the space containing the current folder; the folder page
  // tells which space that is, so only that one tree is fetched
  $effect(() => {
    if (!current || ui.currentSpace !== space.id) return;
    if (tree.some((f) => f.id === current)) {
      open = true;
      expandTo(current);
    } else if (!open) {
      open = true; // loads the tree; this effect runs again when it arrives
    } else if (tree.length && refetchedFor !== current) {
      refetchedFor = current; // a folder created elsewhere: reload once
      load();
    }
  });
  let refetchedFor = null;

  function expandTo(id) {
    let f = tree.find((x) => x.id === id);
    while (f && f.parent_id) {
      expanded[f.parent_id] = true;
      f = tree.find((x) => x.id === f.parent_id);
    }
  }

  let children = $derived.by(() => {
    const m = {};
    for (const f of tree) if (f.parent_id) (m[f.parent_id] ??= []).push(f);
    return m;
  });
  let root = $derived(tree.find((f) => !f.parent_id));

  function dragover(e, id) {
    if (!e.dataTransfer.types.includes('application/x-tildoc-keys')) return;
    e.preventDefault();
    over = id;
  }
  function drop(e, id) {
    over = null;
    const raw = e.dataTransfer.getData('application/x-tildoc-keys');
    if (!raw) return;
    e.preventDefault();
    ondropon?.(JSON.parse(raw), id);
  }
</script>

{#snippet node(f, depth)}
  <li>
    <div class="row-node" class:active={current === f.id} class:over={over === f.id} style="padding-left:{6 + depth * 14}px"
      role="treeitem" aria-selected={current === f.id} tabindex="-1"
      ondragover={(e) => dragover(e, f.id)} ondragleave={() => (over = null)} ondrop={(e) => drop(e, f.id)}>
      {#if children[f.id]}
        <button class="tw" class:openned={expanded[f.id]} onclick={() => (expanded[f.id] = !expanded[f.id])} aria-label={tx('foldertree_toggle')}><Icon name="chevron" size={14} /></button>
      {:else}<span class="tw"></span>{/if}
      <button class="lbl" onclick={() => goto(`/f/${f.id}`)}><Icon name="folder" size={16} /> <span>{f.name}</span></button>
    </div>
    {#if expanded[f.id] && children[f.id]}
      <ul>{#each children[f.id] as c (c.id)}{@render node(c, depth + 1)}{/each}</ul>
    {/if}
  </li>
{/snippet}

<li class="space">
  <div class="row-node top" class:active={root && current === root.id} class:over={root && over === root.id}
    role="treeitem" aria-selected={root && current === root.id} tabindex="-1"
    ondragover={(e) => root && dragover(e, root.id)} ondragleave={() => (over = null)} ondrop={(e) => root && drop(e, root.id)}>
    <button class="tw" class:openned={open} onclick={() => (open = !open)} aria-label={tx('foldertree_toggle')}><Icon name="chevron" size={14} /></button>
    <button class="lbl" onclick={() => goto(`/f/${space.root_folder_id}`)}>
      <Icon name="space" size={16} /> <span>{space.name}</span>
      {#if space.team_name}<small class="team">{space.team_name}</small>{/if}
    </button>
  </div>
  {#if open && root && children[root.id]}
    <ul>{#each children[root.id] as c (c.id)}{@render node(c, 1)}{/each}</ul>
  {/if}
</li>

<style>
  ul { list-style: none; margin: 0; padding: 0; }
  li { margin: 0; }
  .row-node { display: flex; align-items: center; border-radius: var(--r-control); min-height: 30px; }
  .row-node:hover { background: var(--surface-2); }
  .row-node.active { background: var(--accent-soft); color: var(--accent-ink); }
  .row-node.active .lbl { font-weight: 600; }
  .row-node.over { outline: 2px dashed var(--accent); outline-offset: -2px; }
  .tw { width: 20px; height: 26px; min-height: 0; min-width: 0; display: inline-flex; align-items: center; justify-content: center; border: 0; background: none; padding: 0; color: var(--ink-2); transition: transform 0.15s; flex: none; }
  .tw:hover:not(:disabled) { background: none; color: var(--ink); }
  .tw.openned { transform: rotate(90deg); }
  .lbl { flex: 1; min-width: 0; min-height: 0; justify-content: flex-start; display: flex; align-items: center; gap: 7px; border: 0; background: none; padding: 4px 4px; text-align: left; color: inherit; }
  .lbl:hover:not(:disabled) { background: none; }
  .lbl :global(svg) { flex: none; color: var(--manila); }
  .top .lbl :global(svg) { color: var(--ink-2); }
  .lbl span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .top .lbl { font-weight: 600; }
  .team { margin-left: auto; color: var(--ink-2); font-weight: 400; font-size: var(--t-sm); }
  @media (prefers-reduced-motion: reduce) { .tw { transition: none; } }
</style>
