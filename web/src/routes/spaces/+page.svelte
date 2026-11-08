<script>
  import { get, post, patch, del } from '#lib/api.js';
  import { session, refreshSpaces, toast, errorToast } from '#lib/state.svelte.js';
  import { date } from '#lib/format.js';
  import { goto } from '$app/navigation';
  import Confirm from '#lib/components/Confirm.svelte';

  let teams = $state([]);
  let name = $state(''), slug = $state(''), team = $state('');
  let confirm = $state(null);
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
    <p>A space holds folders and documents, for you alone or for a team. Its short name is the S3 bucket and the top folder in WebDAV and SFTP.</p>
  </header>
  <div class="table-wrap">
    <table class="plain">
      <thead><tr><th>Name</th><th>Short name</th><th>Belongs to</th><th>Your role</th><th>Created</th><th></th></tr></thead>
      <tbody>
        {#each session.spaces as s (s.id)}
          <tr>
            <td><button class="link" onclick={() => goto(`/f/${s.root_folder_id}`)}>{s.name}</button></td>
            <td class="mono">{s.slug}</td>
            <td>{s.team_name ? `Team ${s.team_name}` : 'Just you'}</td>
            <td>{s.role}</td>
            <td>{date(s.created_at)}</td>
            <td class="actions">
              {#if s.perm >= 3}<button class="small" onclick={() => rename(s)}>Rename</button>{/if}
              {#if s.perm >= 3}<button class="small ghost" onclick={() => (confirm = s)}>Delete</button>{/if}
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

{#if confirm}
  <Confirm title="Delete space" danger confirm="Delete" message={`Delete the space “${confirm.name}”? This only works when it contains no documents.`}
    onconfirm={() => remove(confirm)} onclose={() => (confirm = null)} />
{/if}

