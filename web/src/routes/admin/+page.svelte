<script>
  import Rich from '#lib/i18n/Rich.svelte';
  import { tx } from '#lib/i18n/index.js';
  import { get, post, patch, del } from '#lib/api.js';
  import { session, toast, errorToast } from '#lib/state.svelte.js';
  import { size, date, statusLabel } from '#lib/format.js';

  const can = (p) => session.user?.instance_permissions?.includes(p);
  let stats = $state(null);
  let tags = $state([]);
  let backups = $state([]);
  let settings = $state(null);

  async function loadBackups() {
    if (can('backups.manage')) backups = await get('/admin/backups');
    if (can('settings.manage')) settings = await get('/admin/settings');
  }
  $effect(() => { loadBackups().catch(errorToast); });
  // refresh while a backup runs
  $effect(() => {
    if (!backups.some((b) => b.status === 'queued' || b.status === 'running')) return;
    const t = setTimeout(() => loadBackups().catch(() => {}), 2000);
    return () => clearTimeout(t);
  });

  async function startBackup(mode) {
    try {
      await post('/admin/backups', { mode });
      toast(mode === 'full' ? tx('admin_full_backup_started') : tx('admin_differential_backup_started'), 'success');
      loadBackups();
    } catch (e) {
      errorToast(e);
    }
  }
  async function deleteBackup(b) {
    if (!confirm(tx('admin_delete_the_x_backup_x_the', { mode: b.mode, backup: b.file || b.id }))) return;
    try {
      await del(`/admin/backups/${b.id}`);
      loadBackups();
    } catch (e) {
      errorToast(e);
    }
  }
  async function saveSetting(patchBody, msg) {
    try {
      settings = await patch('/admin/settings', patchBody);
      toast(msg, 'success');
    } catch (e) {
      errorToast(e);
    }
  }
  const baseOf = (b) => backups.find((x) => x.id === b.base_id);
  let mergeFrom = $state(null), mergeInto = $state('');

  async function loadTags() {
    tags = await get('/tags');
  }
  $effect(() => { loadTags().catch(errorToast); });

  async function renameTag(t) {
    const name = prompt(tx('admin_new_name_for_the_tag'), t.name);
    if (!name || name === t.name) return;
    try {
      await patch(`/tags/${t.id}`, { name });
      loadTags();
    } catch (e) {
      errorToast(e);
    }
  }
  async function colorTag(t, color) {
    try {
      await patch(`/tags/${t.id}`, { color });
      loadTags();
    } catch (e) {
      errorToast(e);
    }
  }
  async function mergeTag() {
    try {
      await post(`/tags/${mergeFrom.id}/merge`, { into: mergeInto });
      toast(tx('admin_merged_x_into_x', { mergeFrom: mergeFrom.name, mergeInto: tags.find((x) => x.id === mergeInto)?.name }), 'success');
      mergeFrom = null;
      mergeInto = '';
      loadTags();
    } catch (e) {
      errorToast(e);
    }
  }
  async function deleteTag(t) {
    if (!confirm(tx('admin_delete_the_tag_x_it_is', { name: t.name }))) return;
    try {
      await del(`/tags/${t.id}`);
      loadTags();
    } catch (e) {
      errorToast(e);
    }
  }

  async function load() {
    if (can('settings.manage')) stats = await get('/admin/stats');
  }
  $effect(() => { load().catch(errorToast); });

  async function reprocess(statuses) {
    try {
      const r = await post('/admin/reprocess', { statuses });
      toast(tx('admin_queued_for_processing', { count: r.queued }), 'success');
      load();
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<div class="page">
  <header>
    <h1>{tx('admin_administration')}</h1>
    <p>{tx('admin_storage_backups_and_background_processing_for')}</p>
  </header>
  {#if stats}
    <section class="section">
      <h2>{tx('admin_storage')}</h2>
      <dl class="facts">
        <dt>{tx('admin_documents')}</dt><dd>{stats.documents}</dd>
        <dt>{tx('admin_in_the_trash')}</dt><dd>{stats.trashed}</dd>
        <dt>{tx('admin_stored_files')}</dt><dd>{tx('admin_x_together_x', { blobs: stats.blobs, blob_bytes: size(stats.blob_bytes) })}</dd>
        <dt>{tx('access_people')}</dt><dd>{stats.users}</dd>
        <dt>{tx('common_spaces')}</dt><dd>{stats.spaces}</dd>
      </dl>
    </section>
    <section class="section">
      <h2>{tx('admin_text_extraction')}</h2>
      <div class="row">
        {#each Object.entries(stats.processing) as [k, v]}<span class="pill status {k}">{statusLabel[k] ?? k}: {v}</span>{/each}
        {#each Object.entries(stats.jobs) as [k, v]}<span class="pill">{tx('admin_jobs_x_x', { state: k, number: v })}</span>{/each}
      </div>
      <div class="row" style="margin-top:12px">
        <button onclick={() => reprocess(['failed'])}>{tx('admin_retry_failed')}</button>
        <button onclick={() => reprocess(['ocr_pending'])}>{tx('admin_run_ocr_again_where_pending')}</button>
        <button class="ghost" onclick={() => reprocess([])}>{tx('admin_process_everything_again')}</button>
      </div>
      {#if stats.failed_jobs.length}
        <h3 style="margin:18px 0 8px">{tx('admin_failed_jobs')}</h3>
        <div class="table-wrap">
          <table class="plain"><thead><tr><th>{tx('admin_id')}</th><th>{tx('admin_kind')}</th><th>{tx('admin_attempts')}</th><th>{tx('admin_error')}</th></tr></thead>
            <tbody>{#each stats.failed_jobs as j}<tr><td>{j.id}</td><td>{j.kind}</td><td>{j.attempts}</td><td class="muted">{j.last_error}</td></tr>{/each}</tbody></table>
        </div>
      {/if}
    </section>
  {/if}
    {#if can('settings.manage') && settings}
  <section class="section">
    <h2>{tx('admin_sharing')}</h2>
    <label class="inline"><input type="checkbox" checked={settings.public_links} onchange={(e) => saveSetting({ public_links: e.currentTarget.checked }, tx('admin_saved_the_sharing_setting'))} /> {tx('admin_allow_public_and_drop_links_which')}</label>
    <p class="hint">{tx('admin_turning_this_off_stops_all_existing')}</p>
  </section>
  {/if}
  {#if can('backups.manage')}
  <section class="section">
    <h2>{tx('admin_backups')}</h2>
    <p><Rich key="admin_backup_kinds" /> {settings ? tx('admin_backup_location_dir', { dir: settings.backup_dir }) : tx('admin_backup_location')} <Rich key="admin_backup_restore" vars={{ command: 'tildoc restore FULL [DIFFERENTIAL]', secret: 'TILDOC_MASTER_KEY' }} /></p>
    <div class="row">
      <button class="primary" onclick={() => startBackup('full')}>{tx('admin_create_full_backup')}</button>
      <button onclick={() => startBackup('differential')} disabled={!backups.some((b) => b.mode === 'full' && b.status === 'done')}>{tx('admin_create_differential_backup')}</button>
      {#if settings}
        <label class="inline keep">{tx('admin_keep_the_last')}
          <input type="number" min="1" max="100" value={settings.backup_keep}
            onchange={(e) => saveSetting({ backup_keep: +e.currentTarget.value }, tx('admin_saved_how_many_full_backups_are'))} aria-label={tx('admin_full_backups_to_keep')} />
          {tx('admin_full_backups_with_their_differentials')}</label>
      {/if}
    </div>
    {#if backups.length}
      <div class="table-wrap" style="margin-top:12px">
        <table class="plain">
          <thead><tr><th>{tx('admin_started')}</th><th>{tx('admin_kind')}</th><th>{tx('access_status')}</th><th>{tx('common_size')}</th><th>{tx('admin_files')}</th><th>{tx('admin_file')}</th><th></th></tr></thead>
          <tbody>
            {#each backups as b (b.id)}
              <tr>
                <td>{date(b.started_at)}</td>
                <td>{b.mode === 'full' ? tx('admin_full_2') : tx('admin_differential_of_x', { started_at: date(baseOf(b)?.started_at) || tx('admin_a_deleted_backup') })}</td>
                <td><span class="pill status {b.status === 'done' ? 'done' : b.status === 'failed' ? 'failed' : 'pending'}">{b.status}</span>{#if b.error}<small class="err"> {b.error}</small>{/if}</td>
                <td>{b.status === 'done' ? size(b.size) : ''}</td>
                <td>{b.status === 'done' ? b.blob_count : ''}</td>
                <td class="mono small">{b.file}</td>
                <td class="actions">{#if b.status === 'done' || b.status === 'failed'}<button class="small ghost" onclick={() => deleteBackup(b)}>{tx('common_delete')}</button>{/if}</td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <p class="muted">{tx('admin_no_backups_yet')}</p>
    {/if}
  </section>
  {/if}

  {#if can('settings.manage')}
  <section class="section">
    <h2>{tx('admin_ocr')}</h2>
    <p>{tx('admin_some_pdfs_arrive_as_scans_that')}</p>
    {#if settings}
      <div class="seg" role="radiogroup" aria-label={tx('admin_preferred_ocr')}>
        <button role="radio" aria-checked={settings.ocr_preference === 'existing'} class:active={settings.ocr_preference === 'existing'}
          onclick={() => saveSetting({ ocr_preference: 'existing' }, tx('admin_search_now_uses_the_existing_ocr'))}>{tx('admin_existing_ocr_in_the_file')}</button>
        <button role="radio" aria-checked={settings.ocr_preference === 'tildoc'} class:active={settings.ocr_preference === 'tildoc'}
          onclick={() => saveSetting({ ocr_preference: 'tildoc' }, tx('admin_search_now_uses_tildoc_s_ocr'))}>{tx('admin_tildoc_ocr')}</button>
      </div>
    {/if}
  </section>
  {/if}

  {#if can('tags.manage')}
  <section class="section">
    <h2>{tx('admin_tags')}</h2>
    <p>{tx('admin_one_tag_list_for_the_whole')}</p>
    {#if tags.length}
      <div class="table-wrap">
        <table class="plain">
          <thead><tr><th>{tx('admin_tag')}</th><th>{tx('admin_colour')}</th><th>{tx('admin_used_in_your_spaces')}</th><th></th></tr></thead>
          <tbody>
            {#each tags as t (t.id)}
              <tr>
                <td><span class="tagname"><span class="dot" style:background={t.color || null}></span>{t.name}</span></td>
                <td><input type="color" aria-label={tx('admin_colour_of', { tag: t.name })} value={t.color || '#56636b'} onchange={(e) => colorTag(t, e.currentTarget.value)} /></td>
                <td>{t.count}</td>
                <td class="actions">
                  <button class="small" onclick={() => renameTag(t)}>{tx('common_rename')}</button>
                  <button class="small" onclick={() => { mergeFrom = t; mergeInto = ''; }} disabled={tags.length < 2}>{tx('admin_merge_into')}</button>
                  <button class="small ghost" onclick={() => deleteTag(t)}>{tx('common_delete')}</button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if mergeFrom}
        <form class="row merge" onsubmit={(e) => { e.preventDefault(); mergeTag(); }}>
          <span><Rich key="admin_merge_into" vars={{ tag: mergeFrom.name }} /></span>
          <select bind:value={mergeInto} required aria-label={tx('admin_target_tag')}>
            <option value="" disabled>{tx('admin_choose_a_tag')}</option>
            {#each tags.filter((x) => x.id !== mergeFrom.id) as x (x.id)}<option value={x.id}>{x.name}</option>{/each}
          </select>
          <button class="primary" disabled={!mergeInto}>{tx('admin_merge_tags')}</button>
          <button type="button" onclick={() => (mergeFrom = null)}>{tx('common_cancel')}</button>
        </form>
      {/if}
    {:else}
      <p class="muted">{tx('admin_no_tags_yet_tags_are_added')}</p>
    {/if}
  </section>
  {/if}

  
</div>

<style>
  .keep { margin: 0 0 0 12px; font-size: var(--t-sm); }
  .keep input { width: 4.5em; min-height: 28px; padding: 2px 6px; }
  .tagname { display: inline-flex; align-items: center; gap: 8px; font-weight: 600; }
  .dot { width: 9px; height: 9px; border-radius: 50%; background: var(--ink-2); }
  input[type='color'] { width: 44px; min-height: 28px; padding: 2px; }
  .merge { margin-top: 12px; }
  .merge select { width: auto; }
  .facts { display: grid; grid-template-columns: max-content 1fr; gap: 6px 24px; margin: 8px 0 0; }
  .facts dt { color: var(--ink-2); }
  .facts dd { margin: 0; font-weight: 600; }
</style>
