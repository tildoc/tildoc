<script>
  import { get, post, del, api } from '#lib/api.js';
  import { session, toast, errorToast, bumpTree } from '#lib/state.svelte.js';
  import { size, date } from '#lib/format.js';
  import Confirm from '#lib/components/Confirm.svelte';
  import Icon from '#lib/components/Icon.svelte';

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
      toast(`Deleted ${r.purged} document${r.purged === 1 ? '' : 's'} for good`, 'success');
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  let spacesWithTrash = $derived([...new Set(items.map((i) => i.space_id))]);
</script>

<div class="page">
  <header>
    <h1>Trash</h1>
    <p>A document lands here when its last place is deleted. It stays until the date shown, then it is deleted for good. Restoring recreates missing folders.</p>
  </header>
  {#if spacesWithTrash.some((s) => session.spaces.find((x) => x.id === s)?.perm >= 3)}
    <div class="row" style="margin-bottom:12px">
      {#each spacesWithTrash as s}
        {#if session.spaces.find((x) => x.id === s)?.perm >= 3}
          <button onclick={() => (confirm = { kind: 'empty', space: s })}><Icon name="trash" size={16} /> Empty trash of {spaceName(s)}</button>
        {/if}
      {/each}
    </div>
  {/if}
  {#if items.length}
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>Name</th><th>Space</th><th>Deleted from</th><th>Size</th><th>Deleted</th><th>Deleted for good on</th><th></th></tr></thead>
        <tbody>
          {#each items as it (it.document_id)}
            <tr>
              <td>{it.name}</td><td>{spaceName(it.space_id)}</td><td class="muted">{it.path}</td><td>{size(it.size)}</td>
              <td>{date(it.deleted_at)}</td><td>{date(it.purge_at)}</td>
              <td class="actions">
                <button class="small" onclick={() => restore(it)}>Restore</button>
                {#if session.spaces.find((x) => x.id === it.space_id)?.perm >= 3}
                  <button class="small ghost" onclick={() => (confirm = { kind: 'purge', it })}>Delete for good</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div class="empty-state"><strong>The trash is empty</strong>Deleted documents appear here until they are removed for good.</div>
  {/if}
</div>

{#if confirm?.kind === 'purge'}
  <Confirm title="Delete for good" danger confirm="Delete for good" message={`Delete “${confirm.it.name}” and all its versions for good? This can't be undone.`}
    onconfirm={() => purge(confirm.it)} onclose={() => (confirm = null)} />
{:else if confirm?.kind === 'empty'}
  <Confirm title="Empty trash" danger confirm="Empty trash" message={`Delete every document in the trash of ${spaceName(confirm.space)} for good? This can't be undone.`}
    onconfirm={() => empty(confirm.space)} onclose={() => (confirm = null)} />
{/if}
