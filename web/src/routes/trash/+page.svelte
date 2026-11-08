<script>
  import { get, post, del, api } from '#lib/api.js';
  import { session, toast, errorToast, bumpTree } from '#lib/state.svelte.js';
  import { size, date } from '#lib/format.js';
  import Confirm from '#lib/components/Confirm.svelte';

  let items = $state([]);
  let confirm = $state(null);
  async function load() {
    try {
      items = await get('/trash');
    } catch (e) {
      errorToast(e);
    }
  }
  $effect(() => { load(); });
  const spaceName = (id) => session.spaces.find((s) => s.id === id)?.name ?? '';

  async function restore(it) {
    try {
      const d = await post(`/trash/${it.document_id}/restore`);
      toast(`Restored to ${d.links.map((l) => l.path).join(', ')}`, 'success');
      bumpTree();
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  async function purge(it) {
    confirm = null;
    try {
      await del(`/trash/${it.document_id}`);
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  async function empty(spaceId) {
    confirm = null;
    try {
      const r = await api('DELETE', `/trash?space=${spaceId}`);
      toast(`Permanently deleted ${r.purged} document(s)`, 'success');
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  let spacesWithTrash = $derived([...new Set(items.map((i) => i.space_id))]);
</script>

<div class="page">
  <h1>Trash</h1>
  <p class="muted">Deleted documents are kept until their purge date. Restoring recreates the original folders if needed.</p>
  <div class="row">
    {#each spacesWithTrash as s}
      {#if session.spaces.find((x) => x.id === s)?.perm >= 3}
        <button onclick={() => (confirm = { kind: 'empty', space: s })}>Empty trash of {spaceName(s)}</button>
      {/if}
    {/each}
  </div>
  <div class="card" style="margin-top:12px;padding:0;overflow:auto">
    <table class="plain">
      <thead><tr><th>Name</th><th>Space</th><th>Deleted from</th><th>Size</th><th>Deleted</th><th>Purged after</th><th></th></tr></thead>
      <tbody>
        {#each items as it (it.document_id)}
          <tr>
            <td>{it.name}</td><td>{spaceName(it.space_id)}</td><td class="muted">{it.path}</td><td>{size(it.size)}</td>
            <td>{date(it.deleted_at)}</td><td>{date(it.purge_at)}</td>
            <td class="row end">
              <button class="small" onclick={() => restore(it)}>Restore</button>
              {#if session.spaces.find((x) => x.id === it.space_id)?.perm >= 3}
                <button class="small" onclick={() => (confirm = { kind: 'purge', it })}>Delete forever</button>
              {/if}
            </td>
          </tr>
        {:else}
          <tr><td colspan="7" class="muted" style="text-align:center;padding:30px">Trash is empty.</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

{#if confirm?.kind === 'purge'}
  <Confirm title="Delete forever" danger confirm="Delete forever" message={`Permanently delete “${confirm.it.name}” including all versions?`}
    onconfirm={() => purge(confirm.it)} onclose={() => (confirm = null)} />
{:else if confirm?.kind === 'empty'}
  <Confirm title="Empty trash" danger confirm="Empty trash" message={`Permanently delete all trashed documents of ${spaceName(confirm.space)}?`}
    onconfirm={() => empty(confirm.space)} onclose={() => (confirm = null)} />
{/if}
