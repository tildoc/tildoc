<script>
  import { tx } from '#lib/i18n/index.js';
  // Single files other people shared with you.
  import { get } from '#lib/api.js';
  import { openView } from '#lib/nav.js';
  import { errorToast } from '#lib/state.svelte.js';
  import { size, date, shortType } from '#lib/format.js';

  let files = $state(null);
  $effect(() => { get('/shared').then((f) => (files = f)).catch(errorToast); });
</script>

<svelte:head><title>{tx('shared_shared_with_me_tildoc')}</title></svelte:head>

<div class="page">
  <header><h1>{tx('layout_shared_with_me')}</h1><p>{tx('shared_single_files_other_people_gave_you')}</p></header>
  {#if files}
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>{tx('common_name')}</th><th>{tx('common_type')}</th><th>{tx('common_size')}</th><th>{tx('common_changed')}</th></tr></thead>
        <tbody>
          {#each files as f (f.link_id)}
            <tr>
              <td><button class="link" onclick={() => openView(`/view/${f.link_id}`)}>{f.name}</button></td>
              <td>{shortType(f.mime, f.name)}</td><td>{size(f.size)}</td><td>{date(f.modified_at)}</td>
            </tr>
          {:else}<tr><td colspan="4" class="muted">{tx('shared_nothing_has_been_shared_with_you')}</td></tr>{/each}
        </tbody>
      </table>
    </div>
  {/if}
</div>
