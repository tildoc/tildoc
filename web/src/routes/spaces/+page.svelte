<script>
  import { tx } from '#lib/i18n/index.js';
  import { get, post, patch, del } from '#lib/api.js';
  import { session, refreshSpaces, toast, errorToast } from '#lib/state.svelte.js';
  import { date } from '#lib/format.js';
  import { goto } from '$app/navigation';
  import Confirm from '#lib/components/Confirm.svelte';
  import ShareDialog from '#lib/components/ShareDialog.svelte';
  import TransferDialog from '#lib/components/TransferDialog.svelte';
  import SpaceSettings from '#lib/components/SpaceSettings.svelte';
  import { canShareAny } from '#lib/perms.js';
  import ImportDialog from '#lib/components/ImportDialog.svelte';
  import Icon from '#lib/components/Icon.svelte';

  let teams = $state([]);
  let name = $state(''), slug = $state(''), team = $state('');
  let confirm = $state(null);
  let sharing = $state(null);
  let transferring = $state(null);
  let settingsFor = $state(null);
  let importing = $state(false);
  $effect(() => { get('/teams').then((t) => (teams = t)).catch(() => {}); });
  let manageable = $derived(teams.filter((t) => t.role === 'manager'));

  async function create(e) {
    e.preventDefault();
    try {
      const s = await post('/spaces', { name, slug, team_id: team });
      await refreshSpaces();
      name = slug = '';
      toast(tx('spaces_created_space_x', { slug: s.slug }), 'success');
    } catch (err) {
      errorToast(err);
    }
  }
  async function rename(s) {
    const n = prompt(tx('spaces_new_name'), s.name);
    if (!n) return;
    try {
      await patch(`/spaces/${s.id}`, { name: n });
      await refreshSpaces();
    } catch (e) {
      errorToast(e);
    }
  }
  async function remove(s) {
    confirm = null;
    try {
      await del(`/spaces/${s.id}`);
      await refreshSpaces();
      toast(tx('spaces_deleted_the_space'), 'success');
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<div class="page">
  <header>
    <h1>{tx('common_spaces')}</h1>
    <p>{tx('spaces_a_space_holds_folders_and_documents')}</p>
    <div class="row" style="margin-top:12px"><button onclick={() => (importing = true)}><Icon name="upload" size={16} /> {tx('importdialog_import_a_space')}</button></div>
  </header>
  <div class="table-wrap">
    <table class="plain">
      <thead><tr><th>{tx('common_name')}</th><th>{tx('importdialog_short_name')}</th><th>{tx('common_belongs_to')}</th><th>{tx('spaces_your_role')}</th><th>{tx('common_created')}</th><th></th></tr></thead>
      <tbody>
        {#each session.spaces as s (s.id)}
          <tr>
            <td><button class="link" onclick={() => goto(`/f/${s.root_folder_id}`)}>{s.name}</button></td>
            <td class="mono">{s.slug}</td>
            <td>{s.team_name ? tx('common_team_x', { name: s.team_name }) : s.shared ? tx('spaces_shared_with_you') : tx('spaces_just_you')}</td>
            <td>{s.role}</td>
            <td>{date(s.created_at)}</td>
            <td class="actions">
              <button class="small" onclick={() => (sharing = s)}>{canShareAny(s.caps) ? tx('common_share') : tx('spaces_shared_with')}</button>
              {#if s.caps.includes('export')}
                <a class="btn small ghost" href={`/api/spaces/${s.id}/export?format=zip`} download title={tx('spaces_all_current_files_as_a_zip')}>{tx('c_zip')}</a>
                <a class="btn small ghost" href={`/api/spaces/${s.id}/export?format=bundle`} download title={tx('spaces_everything_incl_versions_tags_and_collections')}>{tx('c_export')}</a>
              {/if}
              {#if s.caps.includes('manage')}<button class="small" onclick={() => (settingsFor = s)}>{tx('spaces_settings')}</button>{/if}
              {#if s.caps.includes('transfer')}<button class="small" onclick={() => (transferring = s)}>{tx('spaces_transfer')}</button>{/if}
              {#if s.caps.includes('manage')}<button class="small" onclick={() => rename(s)}>{tx('common_rename')}</button>{/if}
              {#if s.caps.includes('manage')}<button class="small ghost" onclick={() => (confirm = s)}>{tx('common_delete')}</button>{/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <form class="section" style="margin-top:28px" onsubmit={create}>
    <h2>{tx('spaces_new_space')}</h2>
    <p>{tx('spaces_only_empty_spaces_can_be_deleted')}</p>
    <div class="section-grid">
      <label>{tx('common_name')}<input bind:value={name} required /></label>
      <label>{tx('importdialog_short_name')}<input bind:value={slug} placeholder={tx('importdialog_made_from_the_name')} /><span class="hint">{tx('importdialog_optional_lowercase_letters_digits_and_dashes')}</span></label>
      <label>{tx('common_belongs_to')}
        <select bind:value={team}>
          <option value="">{tx('common_just_me')}</option>
          {#each manageable as t}<option value={t.id}>{tx('common_team_x', { name: t.name })}</option>{/each}
        </select>
      </label>
    </div>
    <button class="primary">{tx('spaces_create_space')}</button>
  </form>
</div>

{#if sharing}<ShareDialog space={sharing} onclose={() => (sharing = null)} />{/if}
{#if settingsFor}<SpaceSettings space={settingsFor} onclose={() => (settingsFor = null)} />{/if}
{#if transferring}<TransferDialog space={transferring} onclose={() => (transferring = null)} />{/if}
{#if importing}<ImportDialog onclose={() => (importing = false)} />{/if}
{#if confirm}
  <Confirm title={tx('spaces_delete_space')} danger confirm="Delete" message={tx('spaces_delete_the_space_x_this_only', { confirm: confirm.name })}
    onconfirm={() => remove(confirm)} onclose={() => (confirm = null)} />
{/if}

