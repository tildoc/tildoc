<script>
  import { get, post, patch, del } from '#lib/api.js';
  import { session, toast, errorToast } from '#lib/state.svelte.js';
  import { size, date, statusLabel } from '#lib/format.js';

  let stats = $state(null);
  let users = $state([]);
  let nu = $state({ username: '', password: '', display_name: '', is_admin: false });
  let tags = $state([]);
  let mergeFrom = $state(null), mergeInto = $state('');

  async function loadTags() {
    tags = await get('/tags');
  }
  $effect(() => { loadTags().catch(errorToast); });

  async function renameTag(t) {
    const name = prompt('New name for the tag', t.name);
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
      toast(`Merged ${mergeFrom.name} into ${tags.find((x) => x.id === mergeInto)?.name}`, 'success');
      mergeFrom = null;
      mergeInto = '';
      loadTags();
    } catch (e) {
      errorToast(e);
    }
  }
  async function deleteTag(t) {
    if (!confirm(`Delete the tag ${t.name}? It is removed from every document on this server.`)) return;
    try {
      await del(`/tags/${t.id}`);
      loadTags();
    } catch (e) {
      errorToast(e);
    }
  }

  async function load() {
    stats = await get('/admin/stats');
    users = await get('/users');
  }
  $effect(() => { load().catch(errorToast); });

  async function createUser(e) {
    e.preventDefault();
    try {
      await post('/users', nu);
      toast(`Added ${nu.username} with a personal space`, 'success');
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
    if (!confirm(`Delete ${u.username} and all their personal spaces? This can't be undone.`)) return;
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
      toast(`Queued ${r.queued} file${r.queued === 1 ? '' : 's'} for processing`, 'success');
      load();
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<div class="page">
  <header>
    <h1>Administration</h1>
    <p>People, storage and background processing for the whole installation. Administrators cannot open other people’s spaces.</p>
  </header>
  {#if stats}
    <section class="section">
      <h2>Storage</h2>
      <dl class="facts">
        <dt>Documents</dt><dd>{stats.documents}</dd>
        <dt>In the trash</dt><dd>{stats.trashed}</dd>
        <dt>Stored files</dt><dd>{stats.blobs}, together {size(stats.blob_bytes)}</dd>
        <dt>People</dt><dd>{stats.users}</dd>
        <dt>Spaces</dt><dd>{stats.spaces}</dd>
      </dl>
    </section>
    <section class="section">
      <h2>Text extraction</h2>
      <div class="row">
        {#each Object.entries(stats.processing) as [k, v]}<span class="pill status {k}">{statusLabel[k] ?? k}: {v}</span>{/each}
        {#each Object.entries(stats.jobs) as [k, v]}<span class="pill">jobs {k}: {v}</span>{/each}
      </div>
      <div class="row" style="margin-top:12px">
        <button onclick={() => reprocess(['failed'])}>Retry failed</button>
        <button onclick={() => reprocess(['ocr_pending'])}>Run OCR again where pending</button>
        <button class="ghost" onclick={() => reprocess([])}>Process everything again</button>
      </div>
      {#if stats.failed_jobs.length}
        <h3 style="margin:18px 0 8px">Failed jobs</h3>
        <div class="table-wrap">
          <table class="plain"><thead><tr><th>ID</th><th>Kind</th><th>Attempts</th><th>Error</th></tr></thead>
            <tbody>{#each stats.failed_jobs as j}<tr><td>{j.id}</td><td>{j.kind}</td><td>{j.attempts}</td><td class="muted">{j.last_error}</td></tr>{/each}</tbody></table>
        </div>
      {/if}
    </section>
  {/if}
  <section class="section">
    <h2>Tags</h2>
    <p>One tag list for the whole server. Everyone can create tags; only administrators rename, merge or delete them. Usage counts cover your own spaces only.</p>
    {#if tags.length}
      <div class="table-wrap">
        <table class="plain">
          <thead><tr><th>Tag</th><th>Colour</th><th>Used in your spaces</th><th></th></tr></thead>
          <tbody>
            {#each tags as t (t.id)}
              <tr>
                <td><span class="tagname"><span class="dot" style:background={t.color || null}></span>{t.name}</span></td>
                <td><input type="color" aria-label="Colour of {t.name}" value={t.color || '#56636b'} onchange={(e) => colorTag(t, e.currentTarget.value)} /></td>
                <td>{t.count}</td>
                <td class="actions">
                  <button class="small" onclick={() => renameTag(t)}>Rename</button>
                  <button class="small" onclick={() => { mergeFrom = t; mergeInto = ''; }} disabled={tags.length < 2}>Merge into…</button>
                  <button class="small ghost" onclick={() => deleteTag(t)}>Delete</button>
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
      {#if mergeFrom}
        <form class="row merge" onsubmit={(e) => { e.preventDefault(); mergeTag(); }}>
          <span>Merge <strong>{mergeFrom.name}</strong> into</span>
          <select bind:value={mergeInto} required aria-label="Target tag">
            <option value="" disabled>Choose a tag</option>
            {#each tags.filter((x) => x.id !== mergeFrom.id) as x (x.id)}<option value={x.id}>{x.name}</option>{/each}
          </select>
          <button class="primary" disabled={!mergeInto}>Merge tags</button>
          <button type="button" onclick={() => (mergeFrom = null)}>Cancel</button>
        </form>
      {/if}
    {:else}
      <p class="muted">No tags yet. Tags are added on documents, in the details of the preview or viewer.</p>
    {/if}
  </section>

  <section class="section">
    <h2>People</h2>
    <div class="table-wrap">
      <table class="plain">
        <thead><tr><th>Person</th><th>Administrator</th><th>Status</th><th>Created</th><th></th></tr></thead>
        <tbody>
          {#each users as u (u.id)}
            <tr>
              <td>{u.display_name} <small class="muted">@{u.username}</small></td>
              <td><input type="checkbox" aria-label="Administrator" checked={u.is_admin} disabled={u.id === session.user.id} onchange={(e) => update(u, { is_admin: e.target.checked })} /></td>
              <td>{u.disabled ? 'Disabled' : 'Active'}</td>
              <td>{date(u.created_at)}</td>
              <td class="actions">
                {#if u.id !== session.user.id}
                  <button class="small" onclick={() => update(u, { disabled: !u.disabled })}>{u.disabled ? 'Enable' : 'Disable'}</button>
                  <button class="small" onclick={() => resetPw(u)}>Set password</button>
                  <button class="small ghost" onclick={() => remove(u)}>Delete</button>
                {/if}
              </td>
            </tr>
          {/each}
        </tbody>
      </table>
    </div>
    <form onsubmit={createUser} style="margin-top:18px">
      <h3 style="margin-bottom:8px">Add a person</h3>
      <div class="section-grid">
        <label>Username<input bind:value={nu.username} required /></label>
        <label>Display name<input bind:value={nu.display_name} /></label>
        <label>First password<input type="password" autocomplete="new-password" minlength="8" bind:value={nu.password} required /><span class="hint">At least 8 characters.</span></label>
      </div>
      <div class="row"><label class="inline" style="margin:0"><input type="checkbox" bind:checked={nu.is_admin} /> Administrator</label><button class="primary">Add person</button></div>
    </form>
  </section>
</div>

<style>
  .tagname { display: inline-flex; align-items: center; gap: 8px; font-weight: 600; }
  .dot { width: 9px; height: 9px; border-radius: 50%; background: var(--ink-2); }
  input[type='color'] { width: 44px; min-height: 28px; padding: 2px; }
  .merge { margin-top: 12px; }
  .merge select { width: auto; }
  .facts { display: grid; grid-template-columns: max-content 1fr; gap: 6px 24px; margin: 8px 0 0; }
  .facts dt { color: var(--ink-2); }
  .facts dd { margin: 0; font-weight: 600; }
</style>
