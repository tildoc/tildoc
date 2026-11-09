<script>
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
    [['login', 'Link for signed-in users', 'login_link'], ['public', 'Public link, no account needed', 'public_link'], ['drop', 'Drop link: others can only upload', 'drop_link']].filter(
      ([, , c]) => can(c)
    )
  );
  const presets = $derived([
    ...(caps.includes('view') ? [['restricted', 'View only']] : []),
    ...(caps.includes('download') ? [['view', 'View and download']] : [])
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
      toast('Link copied', 'success');
    } catch {
      toast('Select the link and copy it', 'info');
    }
  }
  const kindName = { login: 'Signed-in users', public: 'Public', drop: 'Drop' };
  const presetName = { restricted: 'View only', view: 'View and download', drop: 'Upload only' };
</script>

<Modal title={`Share ${title}`} wide {onclose}>
  <h3>People and teams</h3>
  {#if isFile}
    <GrantRows url={`/documents/${doc.id}/grants`} scope="file" canEdit={can('people')} held={caps} />
  {:else}
    <p class="muted">The owner{space.team_name ? ` (managers of team ${space.team_name})` : ''} always has every permission. Permissions only add up: someone gets everything their own row and their teams' rows give.</p>
    <GrantRows url={`/spaces/${space.id}/grants`} scope="space" canEdit={can('people')} held={caps} onchanged={refreshSpaces} />
  {/if}

  <h3>Links</h3>
  {#if links.length}
    <ul class="links">
      {#each links as l (l.id)}
        <li>
          <span class="what">{l.label || kindName[l.kind]}</span>
          <span class="pill">{kindName[l.kind]}</span><span class="pill">{presetName[l.preset]}</span>
          {#if l.has_password}<span class="pill">Password</span>{/if}
          {#if l.expires_at}<span class="muted small">until {date(l.expires_at)}</span>{/if}
          <button class="small ghost" onclick={() => revoke(l)}>Revoke</button>
        </li>
      {/each}
    </ul>
  {/if}
  {#if created}
    <div class="created">
      <p>Copy this link now, it is shown only once:</p>
      <input readonly value={created} aria-label="New link" onfocus={(e) => e.currentTarget.select()} />
      <button onclick={copy}>Copy</button>
    </div>
  {/if}
  {#if kinds.length}
    <form class="newlink" onsubmit={create}>
      <select bind:value={kind} aria-label="Type of link">{#each kinds as [k, l] (k)}<option value={k}>{l}</option>{/each}</select>
      {#if kind !== 'drop'}
        <select bind:value={preset} aria-label="What it allows">{#each presets as [p, l] (p)}<option value={p}>{l}</option>{/each}</select>
      {/if}
      <input bind:value={label} placeholder="Label (optional)" aria-label="Label" />
      <input type="password" bind:value={password} placeholder="Password (optional)" aria-label="Link password" autocomplete="new-password" />
      <input type="datetime-local" bind:value={expires} aria-label="Expires" title="Expires (optional)" />
      <button class="primary">Create link</button>
    </form>
    {#if kind === 'drop'}<p class="hint">Uploads land in {folder ? `the folder ${folder.name}` : 'the top of the space'}. Visitors see nothing that is already there.</p>{/if}
  {:else if !links.length}
    <p class="muted">You may not create links here.</p>
  {/if}
  <div class="row end"><button onclick={onclose}>Done</button></div>
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
