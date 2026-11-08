<script>
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

  // auto-expand the space containing the current folder
  $effect(() => {
    if (current && tree.length === 0 && !open) {
      get(`/spaces/${space.id}/tree`).then((t) => {
        if (t.some((f) => f.id === current)) {
          tree = t;
          open = true;
          expandTo(current);
        }
      });
    } else if (current && tree.some((f) => f.id === current)) {
      expandTo(current);
    }
  });

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
        <button class="tw" class:openned={expanded[f.id]} onclick={() => (expanded[f.id] = !expanded[f.id])} aria-label="Toggle"><Icon name="chevron" size={14} /></button>
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
    <button class="tw" class:openned={open} onclick={() => (open = !open)} aria-label="Toggle"><Icon name="chevron" size={14} /></button>
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
  .row-node { display: flex; align-items: center; border-radius: 7px; }
  .row-node:hover { background: var(--hover); }
  .row-node.active { background: var(--accent-soft); color: var(--accent-text); }
  .row-node.over { outline: 2px dashed var(--accent); }
  .tw { width: 20px; height: 24px; display: inline-flex; align-items: center; justify-content: center; border: 0; background: none; padding: 0; color: var(--muted); transition: transform 0.15s; flex: none; }
  .tw.openned { transform: rotate(90deg); }
  .lbl { flex: 1; min-width: 0; display: flex; align-items: center; gap: 6px; border: 0; background: none; padding: 4px 4px; text-align: left; color: inherit; font-size: 0.9rem; }
  .lbl span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .top .lbl { font-weight: 600; }
  .team { margin-left: auto; color: var(--muted); font-weight: normal; font-size: 0.72rem; }
</style>
