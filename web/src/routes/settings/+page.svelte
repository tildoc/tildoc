<script>
  import Rich from '#lib/i18n/Rich.svelte';
  import { currentLocale, SOURCE_LOCALE } from '#lib/i18n/index.js';
  import { availableLocales } from '#lib/i18n/locales.js';
  import { tx } from '#lib/i18n/index.js';
  import { permLabel, permHelp } from '#lib/perms.js';
  import { get, post, patch, del } from '#lib/api.js';
  import { session, toast, errorToast, prefs, setPref, getTheme, setTheme } from '#lib/state.svelte.js';
  import { date } from '#lib/format.js';
  import Modal from '#lib/components/Modal.svelte';

  let keys = $state([]);
  let sshKeys = $state([]);
  let display = $state(session.user.display_name);
  let oldPw = $state(''), newPw = $state('');
  let label = $state(''), scope = $state(''), readOnly = $state(false);
  let created = $state(null);
  let sshKey = $state(''), sshLabel = $state('');
  let viewer = $state(prefs().pdf_viewer);
  let theme = $state(getTheme());

  let language = $state(prefs().locale ?? '');
  const languageName = (id) => {
    try {
      return new Intl.DisplayNames([currentLocale()], { type: 'language' }).of(id);
    } catch {
      return id;
    }
  };
  async function saveLanguage(v) {
    try {
      await setPref('locale', v);
      location.reload(); // the whole interface changes
    } catch (e) {
      language = prefs().locale ?? '';
      errorToast(e);
    }
  }
  async function saveViewer(v) {
    try {
      await setPref('pdf_viewer', v);
      viewer = v;
      toast(v === 'native' ? tx('settings_pdfs_now_open_in_the_browser') : tx('settings_pdfs_now_open_in_the_tildoc'), 'success');
    } catch (e) {
      viewer = prefs().pdf_viewer;
      errorToast(e);
    }
  }

  async function load() {
    keys = await get('/me/access-keys');
    sshKeys = await get('/me/ssh-keys');
  }
  $effect(() => { load().catch(errorToast); });

  let info = $derived(session.info ?? {});
  let host = $derived((() => { try { return new URL(info.public_url).host; } catch { return location.host; } })());
  let webdavHost = $derived(info.webdav_url ? info.webdav_url.replace(/^https?:\/\//, '') : `${location.host}/dav/`);
  let sftpHost = $derived(info.sftp_addr || 'localhost:2222');

  async function saveProfile(e) {
    e.preventDefault();
    try {
      session.user = await patch('/me', { display_name: display });
      toast(tx('settings_saved_your_display_name'), 'success');
    } catch (err) {
      errorToast(err);
    }
  }
  async function changePw(e) {
    e.preventDefault();
    try {
      await post('/me/password', { old_password: oldPw, new_password: newPw });
      toast(tx('settings_changed_your_password_sign_in_again'), 'success');
      setTimeout(() => (location.href = '/login'), 1200);
      oldPw = newPw = '';
    } catch (err) {
      errorToast(err);
    }
  }
  let apiKeys = $state([]), apiPerms = $state([]), apiCatalogue = $state([]), apiLabel = $state(''), apiSpace = $state(''), apiToken = $state('');
  async function loadApiKeys() {
    apiKeys = await get('/me/api-keys');
  }
  $effect(() => {
    loadApiKeys().catch(errorToast);
    get('/permissions').then((c) => (apiCatalogue = c.space.filter((p) => !p.startsWith('share.') && p !== 'transfer' && p !== 'drop_in'))).catch(() => {});
  });
  async function createApiKey(e) {
    e.preventDefault();
    try {
      const r = await post('/me/api-keys', { label: apiLabel, permissions: apiPerms, space_ids: apiSpace ? [apiSpace] : [] });
      apiToken = r.token;
      apiLabel = '';
      apiPerms = [];
      loadApiKeys();
    } catch (err) {
      errorToast(err);
    }
  }
  let dropLabel = $state(''), dropSpace = $state(''), dropCmd = $state('');
  async function createDropKey(e) {
    e.preventDefault();
    try {
      const r = await post('/me/drop-in-keys', { label: dropLabel, space_id: dropSpace });
      dropCmd = `curl -T file.pdf -H "Authorization: Bearer ${r.token}" ${r.url}`;
      dropLabel = '';
      loadApiKeys();
    } catch (err) {
      errorToast(err);
    }
  }
  async function revokeApiKey(k) {
    if (!confirm(tx('settings_revoke_api_key_x_programs_that', { k_id: k.label || k.id }))) return;
    try {
      await del(`/me/api-keys/${k.id}`);
      loadApiKeys();
    } catch (err) {
      errorToast(err);
    }
  }
  async function createKey(e) {
    e.preventDefault();
    try {
      created = await post('/me/access-keys', { label, space_id: scope, read_only: readOnly });
      label = '';
      load();
    } catch (err) {
      errorToast(err);
    }
  }
  async function revoke(k) {
    if (!confirm(tx('settings_revoke_access_key_x_clients_that', { k_id: k.label || k.id }))) return;
    try {
      await del(`/me/access-keys/${k.id}`);
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  async function addSSH(e) {
    e.preventDefault();
    try {
      await post('/me/ssh-keys', { public_key: sshKey, label: sshLabel });
      sshKey = sshLabel = '';
      load();
    } catch (err) {
      errorToast(err);
    }
  }
  async function delSSH(k) {
    try {
      await del(`/me/ssh-keys/${k.id}`);
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  const spaceName = (id) => session.spaces.find((s) => s.id === id)?.slug ?? id;
  function copy(t) {
    navigator.clipboard?.writeText(t).then(() => toast(tx('settings_copied')));
  }
  let rcloneConf = $derived(created ? `[tildoc]
type = s3
provider = Other
access_key_id = ${created.key.id}
secret_access_key = ${created.secret}
endpoint = ${info.s3_url ?? ''}
region = ${info.s3_region ?? 'us-east-1'}
directory_markers = true` : '');
</script>

<div class="page">
  <header>
    <h1>{tx('layout_settings_and_access')}</h1>
    <p>{tx('settings_your_profile_how_documents_open_and')}</p>
  </header>

  <section class="section">
    <h2>{tx('settings_profile')}</h2>
    <div class="section-grid">
      <form onsubmit={saveProfile}>
        <label>{tx('common_username')}<input value={session.user.username} disabled /></label>
        <label>{tx('common_display_name')}<input bind:value={display} /></label>
        <button class="primary">{tx('settings_save_display_name')}</button>
      </form>
      <form onsubmit={changePw}>
        <label>{tx('settings_current_password')}<input type="password" autocomplete="current-password" bind:value={oldPw} required /></label>
        <label>{tx('settings_new_password')}<input type="password" autocomplete="new-password" minlength="8" bind:value={newPw} required /><span class="hint">{tx('common_at_least_8_characters')}</span></label>
        <button>{tx('settings_change_password')}</button>
      </form>
    </div>
  </section>

  <section class="section">
    <h2>{tx('settings_opening_documents')}</h2>
    <p>{tx('settings_choose_which_viewer_shows_pdfs_in')}</p>
    <div class="choices" role="radiogroup" aria-label={tx('settings_pdf_viewer')}>
      <label class="choice" class:on={viewer === 'pdfjs'}>
        <input type="radio" name="viewer" value="pdfjs" checked={viewer === 'pdfjs'} onchange={() => saveViewer('pdfjs')} />
        <span><span class="name"><strong>{tx('settings_tildoc_viewer')}</strong> <span class="pill">{tx('settings_default')}</span></span>
          <span class="hint">{tx('settings_search_inside_the_document_with_highlighted')}</span></span>
      </label>
      <label class="choice" class:on={viewer === 'native'}>
        <input type="radio" name="viewer" value="native" checked={viewer === 'native'} onchange={() => saveViewer('native')} />
        <span><strong>{tx('settings_browser_s_built_in_viewer')}</strong>
          <span class="hint">{tx('settings_the_pdf_viewer_of_your_browser')}</span></span>
      </label>
    </div>

    <h3 class="sub">{tx('settings_language')}</h3>
    <select bind:value={language} onchange={() => saveLanguage(language)} aria-label={tx('settings_language')} style="width:auto">
      <option value="">{tx('settings_language_auto')}</option>
      {#each [SOURCE_LOCALE, ...availableLocales] as id (id)}<option value={id}>{languageName(id)}</option>{/each}
    </select>
    <h3 class="sub">{tx('settings_appearance')}</h3>
    <div class="seg" role="radiogroup" aria-label={tx('settings_colour_theme')}>
      {#each [['system', tx('settings_match_system')], ['light', tx('settings_light')], ['dark', tx('settings_dark')]] as [k, l] (k)}
        <button role="radio" aria-checked={theme === k} class:active={theme === k} onclick={() => { theme = k; setTheme(k); }}>{l}</button>
      {/each}
    </div>
    <p class="hint small">{tx('settings_saved_in_this_browser_only')}</p>
  </section>

  <section class="section">
    <h2>{tx('settings_access_keys')}</h2>
    <p>{tx('settings_for_s3_clients_key_id_and')}</p>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>{tx('settings_label')}</th><th>{tx('settings_key_id')}</th><th>{tx('common_access')}</th><th>{tx('common_created')}</th><th>{tx('settings_last_used')}</th><th></th></tr></thead>
        <tbody>
          {#each keys as k (k.id)}
            <tr><td>{k.label}</td><td class="mono">{k.id}</td><td>{k.read_only ? tx('settings_scope_read_only', { scope: k.space_id ? spaceName(k.space_id) : tx('search_all_spaces') }) : k.space_id ? spaceName(k.space_id) : tx('search_all_spaces')}</td>
              <td>{date(k.created_at)}</td><td>{date(k.last_used_at) || tx('settings_never')}</td><td class="actions"><button class="small" onclick={() => revoke(k)}>{tx('settings_revoke')}</button></td></tr>
          {:else}<tr><td colspan="6" class="muted">{tx('settings_no_access_keys_yet')}</td></tr>{/each}
        </tbody>
      </table>
    </div>
    <form class="row newkey" onsubmit={createKey}>
      <input placeholder={tx('settings_label_for_example_laptop_dolphin')} bind:value={label} aria-label={tx('settings_label')} />
      <select bind:value={scope} aria-label={tx('common_space')}>
        <option value="">{tx('search_all_spaces')}</option>
        {#each session.spaces as s}<option value={s.id}>{s.slug}</option>{/each}
      </select>
      <label class="inline"><input type="checkbox" bind:checked={readOnly} /> {tx('f_read_only')}</label>
      <button class="primary">{tx('settings_create_key')}</button>
    </form>
  </section>

  <section class="section">
    <h2>{tx('settings_api_keys')}</h2>
    <p><Rich key="settings_api_keys_help" vars={{ header: 'Authorization: Bearer tildoc_…' }} /></p>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>{tx('settings_label')}</th><th>{tx('settings_permissions')}</th><th>{tx('common_spaces')}</th><th>{tx('settings_last_used')}</th><th></th></tr></thead>
        <tbody>
          {#each apiKeys.filter((k) => !k.revoked_at) as k (k.id)}
            <tr><td>{k.label}</td><td>{k.permissions.map(permLabel).join(', ')}</td><td>{k.all_spaces ? tx('search_all_spaces') : k.space_ids.map(spaceName).join(', ')}</td>
              <td>{date(k.last_used_at) || tx('settings_never')}</td><td class="actions"><button class="small" onclick={() => revokeApiKey(k)}>{tx('settings_revoke')}</button></td></tr>
          {:else}<tr><td colspan="5" class="muted">{tx('settings_no_api_keys_yet')}</td></tr>{/each}
        </tbody>
      </table>
    </div>
    <form onsubmit={createApiKey}>
      <div class="perms">{#each apiCatalogue as p (p)}<label class="inline" title={permHelp(p)}><input type="checkbox" bind:group={apiPerms} value={p} /> {permLabel(p)}</label>{/each}</div>
      <div class="row newkey">
        <input placeholder={tx('settings_label_for_example_backup_script')} bind:value={apiLabel} aria-label={tx('settings_api_key_label')} />
        <select bind:value={apiSpace} aria-label={tx('settings_api_key_space')}><option value="">{tx('search_all_spaces')}</option>{#each session.spaces as s}<option value={s.id}>{s.slug}</option>{/each}</select>
        <button class="primary" disabled={!apiPerms.length}>{tx('settings_create_api_key')}</button>
      </div>
    </form>
    <form onsubmit={createDropKey} class="row newkey">
      <input placeholder={tx('settings_drop_in_label_for_example_office')} bind:value={dropLabel} aria-label={tx('settings_drop_in_token_label')} required />
      <select bind:value={dropSpace} aria-label={tx('settings_drop_in_space')} required><option value="" disabled>{tx('common_space')}</option>{#each session.spaces.filter((s) => s.caps.includes('drop_in')) as s}<option value={s.id}>{s.slug}</option>{/each}</select>
      <button>{tx('settings_create_drop_in_token')}</button>
    </form>
    <p class="hint">{tx('settings_a_drop_in_token_sends_files')}</p>
    {#if dropCmd}
      <p><strong>{tx('settings_copy_the_command_now_the_token')}</strong></p>
      <pre class="snippet" aria-label="Drop-In command">{dropCmd}</pre>
      <button onclick={() => copy(dropCmd)}>{tx('common_copy')}</button>
    {/if}
    {#if apiToken}
      <p><strong>{tx('settings_copy_the_key_now_it_is')}</strong></p>
      <div class="row"><input readonly value={apiToken} aria-label={tx('settings_new_api_key')} /><button onclick={() => copy(apiToken)}>{tx('common_copy')}</button></div>
    {/if}
  </section>

  <section class="section">
    <h2>{tx('settings_ssh_keys')}</h2>
    <p>{tx('settings_sign_in_to_sftp_with_a')}</p>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>{tx('settings_label')}</th><th>{tx('common_type')}</th><th>{tx('settings_fingerprint')}</th><th>{tx('docdetails_added')}</th><th>{tx('settings_last_used')}</th><th></th></tr></thead>
        <tbody>
          {#each sshKeys as k (k.id)}
            <tr><td>{k.label}</td><td>{k.type}</td><td class="mono">{k.fingerprint}</td><td>{date(k.created_at)}</td><td>{date(k.last_used_at) || tx('settings_never')}</td>
              <td class="actions"><button class="small" onclick={() => delSSH(k)}>{tx('common_remove')}</button></td></tr>
          {:else}<tr><td colspan="6" class="muted">{tx('settings_no_ssh_keys_yet')}</td></tr>{/each}
        </tbody>
      </table>
    </div>
    <form class="sshform" onsubmit={addSSH}>
      <label>{tx('settings_public_key')}<textarea rows="2" bind:value={sshKey} required></textarea><span class="hint">{tx('settings_the_contents_of_ssh_id_ed25519')}</span></label>
      <div class="row"><input placeholder={tx('settings_label_optional')} bind:value={sshLabel} aria-label={tx('settings_label')} style="flex:1" /><button class="primary">{tx('settings_add_key')}</button></div>
    </form>
  </section>

  <section class="section">
    <h2>{tx('settings_connect_from_dolphin_on_arch_linux')}</h2>
    <p>{tx('settings_each_top_level_folder')}</p>
    <h3 class="sub">{tx('historygraph_webdav')}</h3>
    <p class="small">{tx('settings_type_into_dolphin_s_location_bar')}</p>
    <pre class="snippet">webdavs://{session.user.username}@{webdavHost}</pre>
    <h3 class="sub">{tx('settings_sftp')}</h3>
    <p class="small">{tx('settings_uses_a_registered_ssh_key_or')}</p>
    <pre class="snippet">sftp://{session.user.username}@{sftpHost}/</pre>
    <h3 class="sub">{tx('settings_s3_with_rclone')}</h3>
    <p class="small"><Rich key="settings_rclone_install" vars={{ command: 'pacman -S rclone fuse3' }} /></p>
    <pre class="snippet">rclone mount tildoc: ~/tildoc --vfs-cache-mode writes</pre>
  </section>
</div>

{#if created}
  <Modal title={tx('settings_access_key_created')} wide onclose={() => (created = null)}>
    <p><strong>{tx('settings_copy_the_secret_now_it_is')}</strong></p>
    <label>{tx('settings_access_key_id')}<div class="row"><input readonly value={created.key.id} /><button onclick={() => copy(created.key.id)}>{tx('common_copy')}</button></div></label>
    <label>{tx('settings_secret')}<div class="row"><input readonly value={created.secret} /><button onclick={() => copy(created.secret)}>{tx('common_copy')}</button></div></label>
    <p><Rich key="settings_rclone_config" vars={{ path: '~/.config/rclone/rclone.conf' }} /></p>
    <pre class="snippet">{rcloneConf}</pre>
    <div class="row end"><button class="primary" onclick={() => (created = null)}>{tx('common_done')}</button></div>
  </Modal>
{/if}

<style>
  .hint { color: var(--ink-2); font-size: var(--t-sm); }
  .perms { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr)); gap: 4px 12px; margin: 10px 0; }
  .sub { margin: 18px 0 6px; }
  .choices { display: grid; grid-template-columns: repeat(auto-fit, minmax(320px, 1fr)); gap: 10px; }
  .choice { flex-direction: row; align-items: flex-start; gap: 12px; padding: 12px 14px; border: 1px solid var(--line); border-radius: var(--r-panel); background: var(--surface); margin: 0; font-weight: 400; font-size: var(--t-base); cursor: pointer; }
  .choice.on { border-color: var(--accent); box-shadow: inset 0 0 0 1px var(--accent); }
  .choice input { margin-top: 4px; }
  .choice > span { display: flex; flex-direction: column; gap: 2px; }
  .choice .name { display: flex; align-items: center; gap: 8px; }
  .choice strong { display: inline; }
  .hint { font-size: var(--t-sm); }
  .newkey { margin-top: 12px; }
  .newkey > input:first-child { flex: 1; min-width: 180px; }
  .newkey select { width: auto; }
  .newkey .inline { margin: 0; }
  .sshform { margin-top: 12px; }
</style>
