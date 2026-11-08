<script>
  import { get, post, patch, del } from '#lib/api.js';
  import { session, toast, errorToast } from '#lib/state.svelte.js';
  import { date } from '#lib/format.js';
  import Modal from '#lib/components/Modal.svelte';

  let keys = $state([]);
  let sshKeys = $state([]);
  let display = $state(session.user.display_name);
  let oldPw = $state(''), newPw = $state('');
  let label = $state(''), scope = $state(''), readOnly = $state(false);
  let created = $state(null);
  let sshKey = $state(''), sshLabel = $state('');

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
      toast('Saved', 'success');
    } catch (err) {
      errorToast(err);
    }
  }
  async function changePw(e) {
    e.preventDefault();
    try {
      await post('/me/password', { old_password: oldPw, new_password: newPw });
      toast('Password changed – please sign in again', 'success');
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
    if (!confirm(`Revoke access key ${k.label || k.id}? Clients using it stop working.`)) return;
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
  <h1>Settings & access</h1>

  <div class="grid">
    <form class="card" onsubmit={saveProfile}>
      <h2 style="margin-top:0">Profile</h2>
      <label>Username<input value={session.user.username} disabled /></label>
      <label>Display name<input bind:value={display} /></label>
      <button class="primary">Save</button>
    </form>
    <form class="card" onsubmit={changePw}>
      <h2 style="margin-top:0">Password</h2>
      <label>Current password<input type="password" autocomplete="current-password" bind:value={oldPw} required /></label>
      <label>New password<input type="password" autocomplete="new-password" minlength="8" bind:value={newPw} required /></label>
      <button class="primary">Change password</button>
    </form>
  </div>

  <div class="card">
    <h2 style="margin-top:0">Access keys (app passwords)</h2>
    <p class="muted">Used by S3 clients (key id + secret), and as password for WebDAV and SFTP. Your account password is never accepted by these protocols. A key can be limited to one space and/or read-only.</p>
    <table class="plain">
      <thead><tr><th>Label</th><th>Key ID</th><th>Scope</th><th>Created</th><th>Last used</th><th></th></tr></thead>
      <tbody>
        {#each keys as k (k.id)}
          <tr><td>{k.label}</td><td class="mono">{k.id}</td><td>{k.space_id ? spaceName(k.space_id) : 'all spaces'}{k.read_only ? ', read-only' : ''}</td>
            <td>{date(k.created_at)}</td><td>{date(k.last_used_at) || 'never'}</td><td class="row end"><button class="small" onclick={() => revoke(k)}>Revoke</button></td></tr>
        {:else}<tr><td colspan="6" class="muted">No access keys yet.</td></tr>{/each}
      </tbody>
    </table>
    <form class="row" style="margin-top:12px" onsubmit={createKey}>
      <input placeholder="Label, e.g. laptop-dolphin" bind:value={label} style="flex:1;min-width:160px" />
      <select bind:value={scope} style="width:auto">
        <option value="">All spaces</option>
        {#each session.spaces as s}<option value={s.id}>{s.slug}</option>{/each}
      </select>
      <label class="inline" style="margin:0"><input type="checkbox" bind:checked={readOnly} /> read-only</label>
      <button class="primary">Create key</button>
    </form>
  </div>

  <div class="card">
    <h2 style="margin-top:0">SSH keys (SFTP)</h2>
    <table class="plain">
      <thead><tr><th>Label</th><th>Type</th><th>Fingerprint</th><th>Added</th><th>Last used</th><th></th></tr></thead>
      <tbody>
        {#each sshKeys as k (k.id)}
          <tr><td>{k.label}</td><td>{k.type}</td><td class="mono">{k.fingerprint}</td><td>{date(k.created_at)}</td><td>{date(k.last_used_at) || 'never'}</td>
            <td class="row end"><button class="small" onclick={() => delSSH(k)}>Remove</button></td></tr>
        {:else}<tr><td colspan="6" class="muted">No SSH keys.</td></tr>{/each}
      </tbody>
    </table>
    <form style="margin-top:12px" onsubmit={addSSH}>
      <label>Public key (contents of ~/.ssh/id_ed25519.pub)<textarea rows="2" bind:value={sshKey} required></textarea></label>
      <div class="row"><input placeholder="Label (optional)" bind:value={sshLabel} style="flex:1" /><button class="primary">Add key</button></div>
    </form>
  </div>

  <div class="card">
    <h2 style="margin-top:0">Connect from Dolphin / KDE (Arch Linux)</h2>
    <p><strong>WebDAV</strong> – enter in Dolphin's location bar (password = access key secret):</p>
    <pre class="snippet">webdavs://{session.user.username}@{webdavHost}</pre>
    <p><strong>SFTP</strong> – with a registered SSH key or an access key secret as password:</p>
    <pre class="snippet">sftp://{session.user.username}@{sftpHost}/</pre>
    <p><strong>S3 via rclone mount</strong> (<code>pacman -S rclone fuse3</code>): create an access key, then use the generated config and</p>
    <pre class="snippet">rclone mount tildoc: ~/tildoc --vfs-cache-mode writes</pre>
    <p class="muted">Top-level folders are your spaces. Copying a file that is already stored only creates a link; overwriting creates a new version.</p>
  </div>
</div>

{#if created}
  <Modal title="Access key created" wide onclose={() => (created = null)}>
    <p><strong>Copy the secret now – it is shown only once.</strong></p>
    <label>Access key ID<div class="row"><input readonly value={created.key.id} /><button onclick={() => copy(created.key.id)}>Copy</button></div></label>
    <label>Secret<div class="row"><input readonly value={created.secret} /><button onclick={() => copy(created.secret)}>Copy</button></div></label>
    <p>rclone config (<code>~/.config/rclone/rclone.conf</code>):</p>
    <pre class="snippet">{rcloneConf}</pre>
    <div class="row end"><button class="primary" onclick={() => (created = null)}>Done</button></div>
  </Modal>
{/if}

<style>
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(300px, 1fr)); gap: 14px; }
  .card { overflow-x: auto; }
</style>
