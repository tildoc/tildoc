<script>
  import { tx } from '#lib/i18n/index.js';
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
      toast(tx('trash_restored_to_x', { path_join: d.links.map((l) => l.path).join(', ') }), 'success');
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
      toast(tx('trash_deleted_for_good', { count: r.purged }), 'success');
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  let spacesWithTrash = $derived([...new Set(items.map((i) => i.space_id))]);
</script>

<div class="page">
  <header>
    <h1>{tx('layout_trash')}</h1>
    <p>{tx('trash_a_document_lands_here_when_its')}</p>
  </header>
  {#if spacesWithTrash.some((s) => session.spaces.find((x) => x.id === s)?.perm >= 3)}
    <div class="row" style="margin-bottom:12px">
      {#each spacesWithTrash as s}
        {#if session.spaces.find((x) => x.id === s)?.perm >= 3}
          <button onclick={() => (confirm = { kind: 'empty', space: s })}><Icon name="trash" size={16} /> {tx('trash_empty_trash_of_x', { space: spaceName(s) })}</button>
        {/if}
      {/each}
    </div>
  {/if}
  {#if items.length}
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>{tx('common_name')}</th><th>{tx('common_space')}</th><th>{tx('trash_deleted_from')}</th><th>{tx('common_size')}</th><th>{tx('trash_deleted')}</th><th>{tx('trash_deleted_for_good_on')}</th><th></th></tr></thead>
        <tbody>
          {#each items as it (it.document_id)}
            <tr>
              <td>{it.name}</td><td>{spaceName(it.space_id)}</td><td class="muted">{it.path}</td><td>{size(it.size)}</td>
              <td>{date(it.deleted_at)}</td><td>{date(it.purge_at)}</td>
              <td class="actions">
                <button class="small" onclick={() => restore(it)}>{tx('historygraph_restore')}</button>
                {#if session.spaces.find((x) => x.id === it.space_id)?.perm >= 3}
                  <button class="small ghost" onclick={() => (confirm = { kind: 'purge', it })}>{tx('trash_delete_for_good')}</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
  {:else}
    <div class="empty-state"><strong>{tx('trash_the_trash_is_empty')}</strong>{tx('trash_deleted_documents_appear_here_until_they')}</div>
  {/if}
</div>

{#if confirm?.kind === 'purge'}
  <Confirm title={tx('trash_delete_for_good')} danger confirm={tx('trash_delete_for_good')} message={tx('trash_delete_x_and_all_its_versions', { name: confirm.it.name })}
    onconfirm={() => purge(confirm.it)} onclose={() => (confirm = null)} />
{:else if confirm?.kind === 'empty'}
  <Confirm title={tx('trash_empty_trash')} danger confirm={tx('trash_empty_trash')} message={tx('trash_delete_every_document_in_the_trash', { space: spaceName(confirm.space) })}
    onconfirm={() => empty(confirm.space)} onclose={() => (confirm = null)} />
{/if}
