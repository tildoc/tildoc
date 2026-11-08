<script>
  import { get, post, patch, del } from '#lib/api.js';
  import { session, refreshSpaces, toast, errorToast } from '#lib/state.svelte.js';
  import { date } from '#lib/format.js';
  import { goto } from '$app/navigation';
  import Confirm from '#lib/components/Confirm.svelte';
  import ShareDialog from '#lib/components/ShareDialog.svelte';
  import ImportDialog from '#lib/components/ImportDialog.svelte';
  import Icon from '#lib/components/Icon.svelte';

  let teams = $state([]);
  let name = $state(''), slug = $state(''), team = $state('');
  let confirm = $state(null);
  let sharing = $state(null);
  let importing = $state(false);
  $effect(() => { get('/teams').then((t) => (teams = t)).catch(() => {}); });
  let manageable = $derived(teams.filter((t) => t.role === 'owner' || t.role === 'admin'));

  async function create(e) {
    e.preventDefault();
    try {
      const s = await post('/spaces', { name, slug, team_id: team });
      await refreshSpaces();
      name = slug = '';
      toast(`Created space ${s.slug}`, 'success');
    } catch (err) {
      errorToast(err);
    }
  }
  async function rename(s) {
    const n = prompt('New name', s.name);
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
      toast('Deleted the space', 'success');
    } catch (e) {
      errorToast(e);
    }
  }
</script>

<div class="page">
  <header>
    <h1>Spaces</h1>
    <p>A space holds folders and documents, for you alone or for a team, and can be shared with more people. Its short name is the S3 bucket and the top folder in WebDAV and SFTP.</p>
    <div class="row" style="margin-top:12px"><button onclick={() => (importing = true)}><Icon name="upload" size={16} /> Import a space</button></div>
  </header>
  <div class="table-wrap">
    <table class="plain">
      <thead><tr><th>Name</th><th>Short name</th><th>Belongs to</th><th>Your role</th><th>Created</th><th></th></tr></thead>
      <tbody>
        {#each session.spaces as s (s.id)}
          <tr>
            <td><button class="link" onclick={() => goto(`/f/${s.root_folder_id}`)}>{s.name}</button></td>
            <td class="mono">{s.slug}</td>
            <td>{s.team_name ? `Team ${s.team_name}` : s.shared ? 'Shared with you' : 'Just you'}</td>
            <td>{s.role}</td>
            <td>{date(s.created_at)}</td>
            <td class="actions">
              <button class="small" onclick={() => (sharing = s)}>{s.caps.includes('share') ? 'Share' : 'Shared with'}</button>
              {#if s.caps.includes('export')}
                <a class="btn small ghost" href={`/api/spaces/${s.id}/export?format=zip`} download title="All current files as a ZIP archive">ZIP</a>
                <a class="btn small ghost" href={`/api/spaces/${s.id}/export?format=bundle`} download title="Everything incl. versions, tags and collections, to import into a tildoc server">Export</a>
              {/if}
              {#if s.caps.includes('manage')}<button class="small" onclick={() => rename(s)}>Rename</button>{/if}
              {#if s.caps.includes('manage')}<button class="small ghost" onclick={() => (confirm = s)}>Delete</button>{/if}
            </td>
          </tr>
        {/each}
      </tbody>
    </table>
  </div>
  <form class="section" style="margin-top:28px" onsubmit={create}>
    <h2>New space</h2>
    <p>Only empty spaces can be deleted later.</p>
    <div class="section-grid">
      <label>Name<input bind:value={name} required /></label>
      <label>Short name<input bind:value={slug} placeholder="made from the name" /><span class="hint">Optional. Lowercase letters, digits and dashes.</span></label>
      <label>Belongs to
        <select bind:value={team}>
          <option value="">Just me</option>
          {#each manageable as t}<option value={t.id}>Team {t.name}</option>{/each}
        </select>
      </label>
    </div>
    <button class="primary">Create space</button>
  </form>
</div>

{#if sharing}<ShareDialog space={sharing} onclose={() => (sharing = null)} />{/if}
{#if importing}<ImportDialog onclose={() => (importing = false)} />{/if}
{#if confirm}
  <Confirm title="Delete space" danger confirm="Delete" message={`Delete the space “${confirm.name}”? This only works when it contains no documents.`}
    onconfirm={() => remove(confirm)} onclose={() => (confirm = null)} />
{/if}

