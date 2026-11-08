<script>
  import { get, post, patch, del } from '#lib/api.js';
  import { session, toast, errorToast } from '#lib/state.svelte.js';
  import { size, date, statusLabel } from '#lib/format.js';

  let stats = $state(null);
  let users = $state([]);
  let nu = $state({ username: '', password: '', display_name: '', is_admin: false });

  async function load() {
    stats = await get('/admin/stats');
    users = await get('/users');
  }
  $effect(() => { load().catch(errorToast); });

  async function createUser(e) {
    e.preventDefault();
    try {
      await post('/users', nu);
      toast(`User ${nu.username} created (with personal space)`, 'success');
      nu = { username: '', password: '', display_name: '', is_admin: false };
      load();
    } catch (err) {
      errorToast(err);
    }
  }
  async function update(u, body) {
    try {
      await patch(`/users/${u.id}`, body);
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  async function resetPw(u) {
    const pw = prompt(`New password for ${u.username} (min. 8 characters)`);
    if (pw) update(u, { password: pw });
  }
  async function remove(u) {
    if (!confirm(`Delete user ${u.username} and all personal spaces? This cannot be undone.`)) return;
    try {
      await del(`/users/${u.id}`);
      load();
    } catch (e) {
      errorToast(e);
    }
  }
  async function reprocess(statuses) {
    try {
      const r = await post('/admin/reprocess', { statuses });
      toast(`${r.queued} blob(s) queued for processing`, 'success');
      load();
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<div class="page">
  <h1>Administration</h1>
  {#if stats}
    <div class="tiles">
      <div class="tile"><span>{stats.documents}</span>documents</div>
      <div class="tile"><span>{stats.blobs}</span>stored blobs</div>
      <div class="tile"><span>{size(stats.blob_bytes)}</span>stored</div>
      <div class="tile"><span>{stats.trashed}</span>in trash</div>
      <div class="tile"><span>{stats.users}</span>users</div>
      <div class="tile"><span>{stats.spaces}</span>spaces</div>
    </div>
    <div class="card">
      <h2 style="margin-top:0">Processing</h2>
      <div class="row">
        {#each Object.entries(stats.processing) as [k, v]}<span class="pill">{statusLabel[k] ?? k}: {v}</span>{/each}
        {#each Object.entries(stats.jobs) as [k, v]}<span class="pill muted">jobs {k}: {v}</span>{/each}
      </div>
      <div class="row" style="margin-top:10px">
        <button onclick={() => reprocess(['failed'])}>Retry failed</button>
        <button onclick={() => reprocess(['ocr_pending'])}>Re-run OCR pending</button>
        <button onclick={() => reprocess([])}>Reprocess everything</button>
      </div>
      {#if stats.failed_jobs.length}
        <h2>Failed jobs</h2>
        <table class="plain"><thead><tr><th>ID</th><th>Kind</th><th>Attempts</th><th>Error</th></tr></thead>
          <tbody>{#each stats.failed_jobs as j}<tr><td>{j.id}</td><td>{j.kind}</td><td>{j.attempts}</td><td class="muted">{j.last_error}</td></tr>{/each}</tbody></table>
      {/if}
    </div>
  {/if}
  <div class="card" style="overflow-x:auto">
    <h2 style="margin-top:0">Users</h2>
    <table class="plain">
      <thead><tr><th>User</th><th>Admin</th><th>Status</th><th>Created</th><th></th></tr></thead>
      <tbody>
        {#each users as u (u.id)}
          <tr>
            <td>{u.display_name} <small class="muted">@{u.username}</small></td>
            <td><input type="checkbox" checked={u.is_admin} disabled={u.id === session.user.id} onchange={(e) => update(u, { is_admin: e.target.checked })} /></td>
            <td>{u.disabled ? 'disabled' : 'active'}</td>
            <td>{date(u.created_at)}</td>
            <td class="row end">
              {#if u.id !== session.user.id}
                <button class="small" onclick={() => update(u, { disabled: !u.disabled })}>{u.disabled ? 'Enable' : 'Disable'}</button>
                <button class="small" onclick={() => resetPw(u)}>Reset password</button>
                <button class="small" onclick={() => remove(u)}>Delete</button>
              {/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
    <form onsubmit={createUser} style="margin-top:14px">
      <div class="grid">
        <label>Username<input bind:value={nu.username} required /></label>
        <label>Display name<input bind:value={nu.display_name} /></label>
        <label>Initial password<input type="password" autocomplete="new-password" minlength="8" bind:value={nu.password} required /></label>
      </div>
      <div class="row"><label class="inline"><input type="checkbox" bind:checked={nu.is_admin} /> administrator</label><button class="primary">Create user</button></div>
    </form>
  </div>
</div>

<style>
  .tiles { display: grid; grid-template-columns: repeat(auto-fit, minmax(140px, 1fr)); gap: 12px; margin-bottom: 14px; }
  .tile { background: var(--surface); border: 1px solid var(--border); border-radius: 12px; padding: 12px 14px; color: var(--muted); font-size: 0.85rem; }
  .tile span { display: block; font-size: 1.5rem; font-weight: 700; color: var(--text); }
  .pill { background: var(--surface-2); border-radius: 99px; padding: 2px 10px; font-size: 0.85rem; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 0 12px; }
</style>
