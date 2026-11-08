<script>
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

  async function saveViewer(v) {
    try {
      await setPref('pdf_viewer', v);
      viewer = v;
      toast(v === 'native' ? 'PDFs now open in the browser’s own viewer' : 'PDFs now open in the tildoc viewer', 'success');
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
      toast('Saved your display name', 'success');
    } catch (err) {
      errorToast(err);
    }
  }
  async function changePw(e) {
    e.preventDefault();
    try {
      await post('/me/password', { old_password: oldPw, new_password: newPw });
      toast('Changed your password. Sign in again with the new one.', 'success');
      setTimeout(() => (location.href = '/login'), 1200);
      oldPw = newPw = '';
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
    if (!confirm(`Revoke access key ${k.label || k.id}? Clients that use it stop working right away.`)) return;
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
    navigator.clipboard?.writeText(t).then(() => toast('Copied'));
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
    <h1>Settings and access</h1>
    <p>Your profile, how documents open, and the keys that file managers and sync tools use to connect.</p>
  </header>

  <section class="section">
    <h2>Profile</h2>
    <div class="section-grid">
      <form onsubmit={saveProfile}>
        <label>Username<input value={session.user.username} disabled /></label>
        <label>Display name<input bind:value={display} /></label>
        <button class="primary">Save display name</button>
      </form>
      <form onsubmit={changePw}>
        <label>Current password<input type="password" autocomplete="current-password" bind:value={oldPw} required /></label>
        <label>New password<input type="password" autocomplete="new-password" minlength="8" bind:value={newPw} required /><span class="hint">At least 8 characters.</span></label>
        <button>Change password</button>
      </form>
    </div>
  </section>

  <section class="section">
    <h2>Opening documents</h2>
    <p>Choose which viewer shows PDFs in the preview and in the full-window viewer.</p>
    <div class="choices" role="radiogroup" aria-label="PDF viewer">
      <label class="choice" class:on={viewer === 'pdfjs'}>
        <input type="radio" name="viewer" value="pdfjs" checked={viewer === 'pdfjs'} onchange={() => saveViewer('pdfjs')} />
        <span><span class="name"><strong>tildoc viewer</strong> <span class="pill">default</span></span>
          <span class="hint">Search inside the document with highlighted hits, jumps from search results to the right page, page thumbnails, and switching between the original and the OCR version. PDFs are drawn inside tildoc with scripts in the PDF turned off.</span></span>
      </label>
      <label class="choice" class:on={viewer === 'native'}>
        <input type="radio" name="viewer" value="native" checked={viewer === 'native'} onchange={() => saveViewer('native')} />
        <span><strong>Browser’s built-in viewer</strong>
          <span class="hint">The PDF viewer of your browser, with its own printing and form filling. No highlighted hits and no page jumps from search results. PDFs are then shown without tildoc’s sandbox, so the browser’s own protections apply.</span></span>
      </label>
    </div>

    <h3 class="sub">Appearance</h3>
    <div class="seg" role="radiogroup" aria-label="Colour theme">
      {#each [['system', 'Match system'], ['light', 'Light'], ['dark', 'Dark']] as [k, l] (k)}
        <button role="radio" aria-checked={theme === k} class:active={theme === k} onclick={() => { theme = k; setTheme(k); }}>{l}</button>
      {/each}
    </div>
    <p class="hint small">Saved in this browser only.</p>
  </section>

  <section class="section">
    <h2>Access keys</h2>
    <p>For S3 clients (key ID and secret) and as the password for WebDAV and SFTP. Your account password never works for these. A key can be limited to one space, and to reading only.</p>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>Label</th><th>Key ID</th><th>Access</th><th>Created</th><th>Last used</th><th></th></tr></thead>
        <tbody>
          {#each keys as k (k.id)}
            <tr><td>{k.label}</td><td class="mono">{k.id}</td><td>{k.space_id ? spaceName(k.space_id) : 'All spaces'}{k.read_only ? ', read only' : ''}</td>
              <td>{date(k.created_at)}</td><td>{date(k.last_used_at) || 'Never'}</td><td class="actions"><button class="small" onclick={() => revoke(k)}>Revoke</button></td></tr>
          {:else}<tr><td colspan="6" class="muted">No access keys yet. Create one to connect Dolphin, rclone or another client.</td></tr>{/each}
        </tbody>
      </table>
    </div>
    <form class="row newkey" onsubmit={createKey}>
      <input placeholder="Label, for example laptop-dolphin" bind:value={label} aria-label="Label" />
      <select bind:value={scope} aria-label="Space">
        <option value="">All spaces</option>
        {#each session.spaces as s}<option value={s.id}>{s.slug}</option>{/each}
      </select>
      <label class="inline"><input type="checkbox" bind:checked={readOnly} /> Read only</label>
      <button class="primary">Create key</button>
    </form>
  </section>

  <section class="section">
    <h2>SSH keys</h2>
    <p>Sign in to SFTP with a key instead of an access key secret.</p>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>Label</th><th>Type</th><th>Fingerprint</th><th>Added</th><th>Last used</th><th></th></tr></thead>
        <tbody>
          {#each sshKeys as k (k.id)}
            <tr><td>{k.label}</td><td>{k.type}</td><td class="mono">{k.fingerprint}</td><td>{date(k.created_at)}</td><td>{date(k.last_used_at) || 'Never'}</td>
              <td class="actions"><button class="small" onclick={() => delSSH(k)}>Remove</button></td></tr>
          {:else}<tr><td colspan="6" class="muted">No SSH keys yet.</td></tr>{/each}
        </tbody>
      </table>
    </div>
    <form class="sshform" onsubmit={addSSH}>
      <label>Public key<textarea rows="2" bind:value={sshKey} required></textarea><span class="hint">The contents of ~/.ssh/id_ed25519.pub.</span></label>
      <div class="row"><input placeholder="Label (optional)" bind:value={sshLabel} aria-label="Label" style="flex:1" /><button class="primary">Add key</button></div>
    </form>
  </section>

  <section class="section">
    <h2>Connect from Dolphin on Arch Linux</h2>
    <p>Each top-level folder is one of your spaces. Copying a file that is already stored only adds another place for it; overwriting a file creates a new version.</p>
    <h3 class="sub">WebDAV</h3>
    <p class="small">Type into Dolphin’s location bar. The password is an access key secret.</p>
    <pre class="snippet">webdavs://{session.user.username}@{webdavHost}</pre>
    <h3 class="sub">SFTP</h3>
    <p class="small">Uses a registered SSH key, or an access key secret as the password.</p>
    <pre class="snippet">sftp://{session.user.username}@{sftpHost}/</pre>
    <h3 class="sub">S3 with rclone</h3>
    <p class="small">Install with <code>pacman -S rclone fuse3</code>, create an access key, add the config shown afterwards, then mount:</p>
    <pre class="snippet">rclone mount tildoc: ~/tildoc --vfs-cache-mode writes</pre>
  </section>
</div>

{#if created}
  <Modal title="Access key created" wide onclose={() => (created = null)}>
    <p><strong>Copy the secret now. It is shown only this once.</strong></p>
    <label>Access key ID<div class="row"><input readonly value={created.key.id} /><button onclick={() => copy(created.key.id)}>Copy</button></div></label>
    <label>Secret<div class="row"><input readonly value={created.secret} /><button onclick={() => copy(created.secret)}>Copy</button></div></label>
    <p>rclone configuration for <code>~/.config/rclone/rclone.conf</code>:</p>
    <pre class="snippet">{rcloneConf}</pre>
    <div class="row end"><button class="primary" onclick={() => (created = null)}>Done</button></div>
  </Modal>
{/if}

<style>
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
