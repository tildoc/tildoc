<script>
  import { tx } from '#lib/i18n/index.js';
  // Sharing a space or a single file: with people and teams (matrix), or by link.
  import Modal from './Modal.svelte';
  import GrantRows from './GrantRows.svelte';
  import { get, post, del } from '../api.js';
  import { errorToast, refreshSpaces, toast } from '../state.svelte.js';
  import { date } from '../format.js';

  let { space = null, doc = null, name = '', folder = null, onclose } = $props(); // folder: where a drop link lands
  const isFile = $derived(!!doc);
  const caps = $derived((isFile ? doc.caps : space.caps) ?? []);
  const prefix = $derived(isFile ? 'share.file' : 'share.space');
  const can = (x) => caps.includes(`${prefix}.${x}`);
  const title = $derived(name || space?.name || doc?.name);

  // what can be made here: kinds the caller may create
  const kinds = $derived(
    [['login', tx('sharedialog_link_for_signed_in_users'), 'login_link'], ['public', tx('sharedialog_public_link_no_account_needed'), 'public_link'], ['drop', tx('sharedialog_drop_link_others_can_only_upload'), 'drop_link']].filter(
      ([, , c]) => can(c)
    )
  );
  const presets = $derived([
    ...(caps.includes('view') ? [['restricted', tx('sharedialog_view_only')]] : []),
    ...(caps.includes('download') ? [['view', tx('sharedialog_view_and_download')]] : [])
  ]);

  let links = $state([]);
  let kind = $state(''), preset = $state('restricted'), label = $state(''), password = $state(''), expires = $state('');
  let created = $state('');
  const linksUrl = $derived(isFile ? `/documents/${doc.id}/share-links` : `/spaces/${space.id}/share-links`);

  async function load() {
    try {
      links = await get(linksUrl);
    } catch (e) {
      errorToast(e);
    }
  }
  $effect(() => { if (kinds.length) load(); });
  $effect(() => {
    if (!kinds.some(([k]) => k === kind)) kind = kinds[0]?.[0] ?? '';
  });

  async function create(e) {
    e.preventDefault();
    try {
      const r = await post('/share-links', {
        ...(isFile ? { document_id: doc.id } : { space_id: space.id, folder_id: kind === 'drop' ? (folder?.id ?? '') : '' }),
        kind, preset: kind === 'drop' ? 'drop' : preset, label, password,
        expires_at: expires ? new Date(expires).toISOString() : null
      });
      created = r.url;
      label = password = expires = '';
      load();
    } catch (err) {
      errorToast(err);
    }
  }
  // Drop-In token: for scripts and devices
  let dropLabel = $state(''), dropCmd = $state('');
  async function createDropIn(e) {
    e.preventDefault();
    try {
      const r = await post('/me/drop-in-keys', { label: dropLabel, space_id: space.id, folder_id: folder?.id ?? '' });
      dropCmd = `curl -T file.pdf -H "Authorization: Bearer ${r.token}" ${r.url}`;
      dropLabel = '';
    } catch (err) {
      errorToast(err);
    }
  }
  async function copyCmd() {
    try {
      await navigator.clipboard.writeText(dropCmd);
      toast(tx('sharedialog_command_copied'), 'success');
    } catch {
      toast(tx('sharedialog_select_the_command_and_copy_it'), 'info');
    }
  }
  async function revoke(l) {
    try {
      await del(`/share-links/${l.id}`);
      load();
    } catch (err) {
      errorToast(err);
    }
  }
  async function copy() {
    try {
      await navigator.clipboard.writeText(created);
      toast(tx('sharedialog_link_copied'), 'success');
    } catch {
      toast(tx('sharedialog_select_the_link_and_copy_it'), 'info');
    }
  }
  const kindName = { login: tx('sharedialog_signed_in_users'), public: tx('sharedialog_public'), drop: tx('sharedialog_drop') };
  const presetName = { restricted: tx('sharedialog_view_only'), view: tx('sharedialog_view_and_download'), drop: tx('sharedialog_upload_only') };
</script>

<Modal title={tx('sharedialog_share_x', { title })} wide {onclose}>
  <h3>{tx('sharedialog_people_and_teams')}</h3>
  {#if isFile}
    <GrantRows url={`/documents/${doc.id}/grants`} scope="file" canEdit={can('people')} held={caps} />
  {:else}
    <p class="muted">{space.team_name ? tx('sharedialog_owner_team', { team: space.team_name }) : tx('sharedialog_owner')}</p>
    <GrantRows url={`/spaces/${space.id}/grants`} scope="space" canEdit={can('people')} held={space.held_caps ?? caps} onchanged={refreshSpaces} />
  {/if}

  <h3>{tx('sharedialog_links')}</h3>
  {#if links.length}
    <ul class="links">
      {#each links as l (l.id)}
        <li>
          <span class="what">{l.label || kindName[l.kind]}</span>
          <span class="pill">{kindName[l.kind]}</span><span class="pill">{presetName[l.preset]}</span>
          {#if l.has_password}<span class="pill">{tx('common_password')}</span>{/if}
          {#if l.expires_at}<span class="muted small">{tx('sharedialog_until_x', { expires_at: date(l.expires_at) })}</span>{/if}
          <button class="small ghost" onclick={() => revoke(l)}>{tx('settings_revoke')}</button>
        </li>
      {/each}
    </ul>
  {/if}
  {#if created}
    <div class="created">
      <p>{tx('sharedialog_copy_this_link_now_it_is')}</p>
      <input readonly value={created} aria-label={tx('sharedialog_new_link')} onfocus={(e) => e.currentTarget.select()} />
      <button onclick={copy}>{tx('common_copy')}</button>
    </div>
  {/if}
  {#if kinds.length}
    <form class="newlink" onsubmit={create}>
      <select bind:value={kind} aria-label={tx('sharedialog_type_of_link')}>{#each kinds as [k, l] (k)}<option value={k}>{l}</option>{/each}</select>
      {#if kind !== 'drop'}
        <select bind:value={preset} aria-label={tx('sharedialog_what_it_allows')}>{#each presets as [p, l] (p)}<option value={p}>{l}</option>{/each}</select>
      {/if}
      <input bind:value={label} placeholder={tx('settings_label_optional')} aria-label={tx('settings_label')} />
      <input type="password" bind:value={password} placeholder={tx('sharedialog_password_optional')} aria-label={tx('sharedialog_link_password')} autocomplete="new-password" />
      <input type="datetime-local" bind:value={expires} aria-label={tx('sharedialog_expires')} title={tx('sharedialog_expires_optional')} />
      <button class="primary">{tx('sharedialog_create_link')}</button>
    </form>
    {#if kind === 'drop'}<p class="hint">{folder ? tx('sharedialog_uploads_land_in_folder', { folder: folder.name }) : tx('sharedialog_uploads_land_at_top')}</p>{/if}
  {:else if !links.length}
    <p class="muted">{isFile ? tx('sharedialog_no_links_can_be_made_here') : tx('sharedialog_links_are_off_for_this_space')}</p>
  {/if}
  {#if !isFile && caps.includes('drop_in')}
    <h3>{tx('sharedialog_drop_in_token')}</h3>
    <p class="hint">{folder ? tx('sharedialog_for_scripts_folder', { folder: folder.name }) : tx('sharedialog_for_scripts')}</p>
    <form class="newlink" onsubmit={createDropIn}>
      <input bind:value={dropLabel} placeholder={tx('sharedialog_label_for_example_office_scanner')} aria-label={tx('sharedialog_drop_in_label')} required />
      <button class="primary">{tx('sharedialog_create_token')}</button>
    </form>
    {#if dropCmd}
      <p>{tx('sharedialog_copy_this_command_now_the_token')}</p>
      <pre class="snippet" aria-label="Drop-In command">{dropCmd}</pre>
      <button onclick={copyCmd}>{tx('common_copy')}</button>
    {/if}
  {/if}
  <div class="row end"><button onclick={onclose}>{tx('common_done')}</button></div>
</Modal>

<style>
  h3 { margin: 4px 0 8px; font-size: var(--t-base); }
  .links { list-style: none; padding: 0; margin: 0 0 12px; }
  .links li { display: flex; align-items: center; gap: 8px; padding: 6px 0; border-bottom: 1px solid var(--line-soft); flex-wrap: wrap; }
  .what { flex: 1; min-width: 120px; }
  .newlink { display: flex; flex-wrap: wrap; gap: 8px; margin-bottom: 8px; }
  .newlink input, .newlink select { width: auto; flex: 1; min-width: 150px; }
  .created { display: flex; flex-wrap: wrap; gap: 8px; align-items: center; margin-bottom: 12px; }
  .created p { width: 100%; margin: 0; }
  .created input { flex: 1; min-width: 200px; }
  .hint { color: var(--ink-2); font-size: var(--t-sm); margin: 0 0 12px; }
  .small { font-size: var(--t-sm); }
</style>
