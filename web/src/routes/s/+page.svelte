<script>
  import { tx } from '#lib/i18n/index.js';
  // A share link. The token is in the URL fragment, so it never reaches the
  // server's logs; it is exchanged for a cookie bound to this one link.
  import { onMount } from 'svelte';
  import { api } from '#lib/api.js';
  import { SOURCE_URL, size as fmtSize, date } from '#lib/format.js';
  import DocView from '#lib/components/viewer/DocView.svelte';
  import Icon from '#lib/components/Icon.svelte';

  let info = $state(null);
  let need = $state(''); // password
  let error = $state('');
  let password = $state('');
  let listing = $state(null), folderId = $state('');
  let file = $state(null); // what is being viewed
  let dropped = $state([]), busy = $state(false);
  let token = '';

  const stored = (t) => {
    try {
      return sessionStorage.getItem(`share:${t}`) ?? '';
    } catch {
      return '';
    }
  };

  async function open(e) {
    e?.preventDefault();
    error = '';
    try {
      const i = await api('POST', '/public/open', { token, password, link_id: stored(token) }, { noRedirect: true });
      info = i;
      need = '';
      try { sessionStorage.setItem(`share:${token}`, i.id); } catch {}
      if (i.root_folder_id) go(i.root_folder_id);
      if (i.file) file = i.file;
    } catch (err) {
      if (err.code === 'login') {
        try { sessionStorage.setItem('afterLogin', location.href); } catch {}
        location.replace('/login');
      } else if (err.code === 'password') {
        if (need && password) error = tx('s_wrong_password');
        need = 'password';
      } else error = err.message;
    }
  }
  async function go(id) {
    try {
      listing = await api('GET', `/public/${info.id}/folders/${id}`, undefined, { noRedirect: true });
      folderId = id;
      file = null;
    } catch (err) {
      error = err.message;
    }
  }
  async function upload(files) {
    busy = true;
    for (const f of files) {
      try {
        await api('PUT', `/public/${info.id}/upload?name=${encodeURIComponent(f.name)}`, f, { noRedirect: true });
        dropped = [...dropped, f.name];
      } catch (err) {
        error = `${f.name}: ${err.message}`;
      }
    }
    busy = false;
  }
  onMount(() => {
    token = location.hash.slice(1);
    if (!token) error = tx('s_this_link_is_not_complete');
    else open();
  });

  const params = $derived(file?.link_id ? { file: file.link_id } : {});
  const download = $derived(info && file ? `/api/public/${info.id}/content${file.link_id ? `?file=${file.link_id}` : ''}` : '');
</script>

<svelte:head><title>{tx('s_x_tildoc', { info: info?.name ?? tx('s_shared') })}</title><meta name="robots" content="noindex" /></svelte:head>

<div class="share">
  <header class="top">
    <p class="brand"><img src="/icon-64.png" alt="" width="22" height="22" /><a href={SOURCE_URL} target="_blank" rel="noopener noreferrer" title={tx('common_source_code_on_github')}>tildoc</a></p>
    {#if info}<h1>{file?.name ?? info.name}</h1>{/if}
    <span class="grow"></span>
    {#if file && listing}<button onclick={() => (file = null)}>{tx('s_back_to_the_list')}</button>{/if}
    {#if file && info?.can_download}<a class="btn" href={download} download><Icon name="download" size={16} /> {tx('common_download')}</a>{/if}
  </header>

  {#if need === 'password'}
    <form class="center" onsubmit={open}>
      <label>{tx('s_this_link_needs_a_password')}<input type="password" bind:value={password} autocomplete="off" required /></label>
      {#if error}<p class="err">{error}</p>{/if}
      <button class="primary">{tx('common_open')}</button>
    </form>
  {:else if error && !info}
    <p class="center err">{error}</p>
  {:else if info?.kind === 'drop'}
    <div class="center">
      <h2>{tx('s_upload_to_x', { info: info.name })}</h2>
      <p class="muted">{tx('s_you_can_add_files_here_you')}</p>
      <input type="file" multiple aria-label={tx('s_files_to_upload')} disabled={busy} onchange={(e) => upload([...e.currentTarget.files])} />
      {#if error}<p class="err">{error}</p>{/if}
      {#if dropped.length}<ul>{#each dropped as n}<li>{tx('s_uploaded_x', { file: n })}</li>{/each}</ul>{/if}
    </div>
  {:else if file}
    <main class="stage">
      <DocView file={{ ...file, document_id: '' }} contentBase={`/api/public/${info.id}/content`} contentParams={params} noDownload={!info.can_download} mode="full" />
    </main>
  {:else if listing}
    <div class="list">
      {#if listing.parent_id}<button class="link" onclick={() => go(listing.parent_id)}>{tx('s_up_one_folder')}</button>{/if}
      <table class="plain">
        <thead><tr><th>{tx('common_name')}</th><th>{tx('common_size')}</th><th>{tx('common_changed')}</th></tr></thead>
        <tbody>
          {#each listing.folders as f (f.id)}<tr><td><button class="link" onclick={() => go(f.id)}>{f.name}/</button></td><td></td><td></td></tr>{/each}
          {#each listing.files as f (f.link_id)}<tr><td><button class="link" onclick={() => (file = f)}>{f.name}</button></td><td>{fmtSize(f.size)}</td><td>{date(f.modified_at)}</td></tr>{/each}
          {#if !listing.folders.length && !listing.files.length}<tr><td colspan="3" class="muted">{tx('s_this_folder_is_empty')}</td></tr>{/if}
        </tbody>
      </table>
    </div>
  {/if}
</div>

<style>
  .share { height: 100vh; display: flex; flex-direction: column; background: var(--bg, var(--surface)); }
  .top { display: flex; align-items: center; gap: 12px; padding: 8px 16px; border-bottom: 1px solid var(--line); }
  .brand { display: flex; align-items: center; gap: 8px; font-weight: 600; margin: 0; }
  .brand a { color: inherit; }
  h1 { font-size: var(--t-md); margin: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .grow { flex: 1; }
  .stage { flex: 1; min-height: 0; background: var(--stage); }
  .list { padding: 16px; overflow: auto; }
  .center { margin: auto; max-width: 420px; width: 100%; padding: 24px; display: flex; flex-direction: column; gap: 12px; }
  .err { color: var(--danger, #b3261e); margin: 0; }
</style>
