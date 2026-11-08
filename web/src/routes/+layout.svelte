<script>
  import '../app.css';
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { page } from '$app/state';
  import { get, post } from '#lib/api.js';
  import { session, ui, loadSession, errorToast, toast } from '#lib/state.svelte.js';
  import Icon from '#lib/components/Icon.svelte';
  import FolderTree from '#lib/components/FolderTree.svelte';
  import { moveKeys } from '#lib/actions.js';

  let { children } = $props();
  let q = $state('');
  const publicRoutes = ['/login', '/setup'];

  onMount(async () => {
    try {
      const s = await get('/setup', { noRedirect: true });
      if (s.needs_setup) {
        session.ready = true;
        goto('/setup');
        return;
      }
    } catch {}
    await loadSession();
    if (!session.user && !publicRoutes.includes(page.url.pathname)) goto('/login');
  });

  $effect(() => {
    if (page.url.pathname === '/search') q = page.url.searchParams.get('q') ?? '';
  });

  function search(e) {
    e.preventDefault();
    const params = new URLSearchParams({ q });
    if (page.url.pathname.startsWith('/f/')) params.set('folder', page.params.id);
    goto(`/search?${params}`);
  }

  async function logout() {
    await post('/auth/logout').catch(() => {});
    session.user = null;
    goto('/login');
  }

  let currentFolder = $derived(page.url.pathname.startsWith('/f/') ? page.params.id : null);
  let isPublic = $derived(publicRoutes.includes(page.url.pathname));

  async function dropOnTree(keys, folderId) {
    try {
      await moveKeys(keys, folderId);
    } catch (e) {
      errorToast(e);
    }
  }
</script>

{#if !session.ready}
  <div class="splash">tildoc</div>
{:else if isPublic || !session.user}
  {@render children()}
{:else}
  <div class="shell" class:open={ui.sidebarOpen}>
    <header class="top">
      <button class="icon burger" onclick={() => (ui.sidebarOpen = !ui.sidebarOpen)} aria-label="Menu"><Icon name="menu" /></button>
      <a class="brand" href="/"><img src="/favicon.svg" alt="" width="24" /> tildoc</a>
      <form class="search" onsubmit={search}>
        <Icon name="search" size={16} />
        <input placeholder="Search names and content…" bind:value={q} aria-label="Search" />
      </form>
      <div class="user">
        <span class="muted">{session.user.display_name || session.user.username}</span>
        <button class="icon" onclick={logout} title="Log out" aria-label="Log out"><Icon name="logout" /></button>
      </div>
    </header>
    <nav class="side" onclick={(e) => e.target.closest('a,button.lbl') && (ui.sidebarOpen = false)} role="presentation">
      <div class="label">Spaces</div>
      <ul class="tree" role="tree">
        {#each session.spaces as s (s.id)}
          <FolderTree space={s} current={currentFolder} ondropon={dropOnTree} />
        {/each}
      </ul>
      <div class="label">Manage</div>
      <a class="nav" class:active={page.url.pathname === '/trash'} href="/trash"><Icon name="trash" /> Trash</a>
      <a class="nav" class:active={page.url.pathname === '/spaces'} href="/spaces"><Icon name="space" /> Spaces</a>
      <a class="nav" class:active={page.url.pathname === '/teams'} href="/teams"><Icon name="users" /> Teams</a>
      <a class="nav" class:active={page.url.pathname === '/settings'} href="/settings"><Icon name="settings" /> Settings & access</a>
      {#if session.user.is_admin}<a class="nav" class:active={page.url.pathname === '/admin'} href="/admin"><Icon name="shield" /> Administration</a>{/if}
    </nav>
    <main>{@render children()}</main>
  </div>
{/if}

<div class="toasts">
  {#each ui.toasts as t (t.id)}<div class="toast {t.kind}">{t.message}</div>{/each}
</div>

{#if ui.uploads.length}
  <div class="uploads">
    <strong>Uploads</strong>
    {#each ui.uploads.slice(-8) as u (u.id)}
      <div class="up {u.state}">
        <span class="nm" title={u.name}>{u.name}</span>
        <span class="bar"><span style="width:{Math.round(u.progress * 100)}%"></span></span>
        {#if u.note}<small>{u.note}</small>{/if}
      </div>
    {/each}
    {#if ui.uploads.length > 8}<small class="muted">+{ui.uploads.length - 8} more</small>{/if}
  </div>
{/if}

<style>
  .splash { display: grid; place-items: center; height: 100vh; color: var(--muted); font-size: 1.4rem; }
  .shell { display: grid; grid-template-columns: 270px 1fr; grid-template-rows: 56px 1fr; height: 100vh; }
  .top { grid-column: 1 / -1; display: flex; align-items: center; gap: 14px; padding: 0 16px; background: var(--surface); border-bottom: 1px solid var(--border); }
  .brand { display: flex; align-items: center; gap: 8px; font-weight: 700; color: var(--text); font-size: 1.1rem; width: 238px; }
  .search { flex: 1; max-width: 640px; display: flex; align-items: center; gap: 8px; background: var(--surface-2); border-radius: 10px; padding: 0 12px; border: 1px solid var(--border); color: var(--muted); }
  .search input { border: 0; background: none; padding: 8px 0; outline: none; }
  .user { margin-left: auto; display: flex; align-items: center; gap: 8px; }
  .burger { display: none; }
  .side { background: var(--surface); border-right: 1px solid var(--border); overflow: auto; padding: 10px 10px 20px; }
  .label { text-transform: uppercase; font-size: 0.7rem; letter-spacing: 0.06em; color: var(--muted); margin: 12px 8px 6px; }
  .tree { list-style: none; margin: 0; padding: 0; }
  .nav { display: flex; align-items: center; gap: 10px; padding: 7px 10px; border-radius: 8px; color: var(--text); font-size: 0.92rem; }
  .nav:hover { background: var(--hover); }
  .nav.active { background: var(--accent-soft); color: var(--accent-text); }
  main { min-width: 0; min-height: 0; overflow: hidden; }
  .toasts { position: fixed; bottom: 16px; left: 50%; transform: translateX(-50%); display: flex; flex-direction: column; gap: 8px; z-index: 100; width: min(520px, calc(100vw - 32px)); }
  .toast { background: var(--text); color: var(--bg); padding: 10px 14px; border-radius: 10px; box-shadow: 0 8px 24px rgb(0 0 0 / 0.25); font-size: 0.9rem; }
  .toast.error { background: var(--err); color: #fff; }
  .toast.success { background: var(--ok); color: #fff; }
  .uploads { position: fixed; right: 16px; bottom: 16px; width: 320px; background: var(--surface); border: 1px solid var(--border); border-radius: 12px; padding: 10px 12px; z-index: 90; box-shadow: 0 10px 30px rgb(0 0 0 / 0.2); display: flex; flex-direction: column; gap: 6px; font-size: 0.82rem; }
  .up { display: grid; grid-template-columns: 1fr 80px; gap: 2px 8px; align-items: center; }
  .up .nm { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .up small { grid-column: 1 / -1; color: var(--muted); }
  .up.error small { color: var(--err); }
  .bar { height: 6px; background: var(--surface-2); border-radius: 99px; overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--accent); transition: width 0.2s; }
  .up.done .bar span { background: var(--ok); }
  .up.error .bar span { background: var(--err); }
  @media (max-width: 900px) {
    .shell { grid-template-columns: 1fr; }
    .burger { display: inline-flex; }
    .brand { width: auto; }
    .brand { font-size: 0; gap: 0; }
    .side { position: fixed; top: 56px; bottom: 0; left: 0; width: 280px; z-index: 40; transform: translateX(-100%); transition: transform 0.2s; box-shadow: 10px 0 30px rgb(0 0 0 / 0.2); }
    .shell.open .side { transform: none; }
    .user .muted { display: none; }
  }
</style>
