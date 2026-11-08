<script>
  import '../app.css';
  import { onMount } from 'svelte';
  import { goto, onNavigate } from '$app/navigation';
  import { page } from '$app/state';
  import { get, post } from '#lib/api.js';
  import { session, ui, loadSession, endSession, errorToast, applyTheme } from '#lib/state.svelte.js';
  import Icon from '#lib/components/Icon.svelte';
  import FolderTree from '#lib/components/FolderTree.svelte';
  import QuickSearch from '#lib/components/QuickSearch.svelte';
  import CollectionForm from '#lib/components/CollectionForm.svelte';
  import { moveKeys } from '#lib/actions.js';

  let { children } = $props();
  let newCollection = $state(false);
  const publicRoutes = ['/login', '/setup'];

  onMount(async () => {
    applyTheme();
    // setup check and session load in parallel: one round trip on start
    const [s] = await Promise.all([get('/setup', { noRedirect: true }).catch(() => ({})), loadSession()]);
    if (s.needs_setup) {
      goto('/setup');
      return;
    }
    if (!session.user && !publicRoutes.includes(page.url.pathname)) goto('/login');
  });

  // the one orchestrated motion: opening a document grows the preview into the viewer
  onNavigate((nav) => {
    if (!document.startViewTransition || !nav.to?.url.pathname.startsWith('/view/') || nav.from?.url.pathname.startsWith('/view/')) return;
    if (matchMedia('(prefers-reduced-motion: reduce)').matches) return;
    return new Promise((resolve) => {
      // going back before the viewer finished opening aborts the navigation:
      // the transition then just ends (its promises must not reject unhandled)
      const vt = document.startViewTransition(async () => {
        resolve();
        await nav.complete.catch(() => {});
      });
      for (const p of [vt.updateCallbackDone, vt.ready, vt.finished]) p.catch(() => {});
    });
  });

  async function logout() {
    await post('/auth/logout').catch(() => {});
    endSession();
    goto('/login');
  }

  let currentFolder = $derived(page.url.pathname.startsWith('/f/') ? page.params.id : null);
  let isPublic = $derived(publicRoutes.includes(page.url.pathname));
  let isViewer = $derived(page.url.pathname.startsWith('/view/'));
  let path = $derived(page.url.pathname);

  async function dropOnTree(keys, folderId) {
    try {
      await moveKeys(keys, folderId);
    } catch (e) {
      errorToast(e);
    }
  }

  function keydown(e) {
    if (e.defaultPrevented) return;
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'k' && session.user && !isPublic) {
      e.preventDefault();
      ui.palette = !ui.palette;
    }
  }

  const nav = [
    ['/search', 'search', 'All documents'],
    ['/trash', 'trash', 'Trash'],
    ['/spaces', 'space', 'Spaces'],
    ['/teams', 'users', 'Teams'],
    ['/settings', 'settings', 'Settings and access']
  ];
</script>

<svelte:window onkeydown={keydown} />

{#if !session.ready}
  <div class="splash" aria-busy="true"></div>
{:else if isPublic || !session.user}
  {@render children()}
{:else if isViewer}
  {@render children()}
{:else}
  <div class="shell" class:open={ui.sidebarOpen}>
    <header class="mobilebar">
      <button class="icon" onclick={() => (ui.sidebarOpen = !ui.sidebarOpen)} aria-label="Menu" aria-expanded={ui.sidebarOpen}><Icon name="menu" /></button>
      <a class="brand" href="/">tildoc</a>
      <button class="icon" onclick={() => (ui.palette = true)} aria-label="Search"><Icon name="search" /></button>
    </header>

    <nav class="rail" aria-label="Spaces and settings" onclick={(e) => e.target.closest('a,button.lbl') && (ui.sidebarOpen = false)} role="presentation">
      <a class="brand" href="/"><img src="/favicon.svg" alt="" width="22" height="22" />tildoc</a>
      <button class="searchbtn" onclick={() => (ui.palette = true)}>
        <Icon name="search" size={16} /><span>Search</span><kbd>Ctrl K</kbd>
      </button>

      <h2 class="group">Spaces</h2>
      <div class="scrollpart">
        <ul class="tree" role="tree" aria-label="Folders">
          {#each session.spaces as s (s.id)}
            <FolderTree space={s} current={currentFolder} ondropon={dropOnTree} />
          {/each}
        </ul>

        <div class="grouprow">
          <h2 class="group">Collections</h2>
          <button class="icon add" onclick={() => (newCollection = true)} aria-label="New collection" title="New collection"><Icon name="plus" size={15} /></button>
        </div>
        <ul class="colls">
          {#each session.collections as c (c.id)}
            <li>
              <a class="nav coll" class:active={path === `/c/${c.id}`} href={`/c/${c.id}`} aria-current={path === `/c/${c.id}` ? 'page' : undefined}>
                <Icon name="collection" size={16} /><span class="cn">{c.name}</span>
                {#if c.team_name}<small class="team">{c.team_name}</small>{/if}
                <small class="cnt">{c.count}</small>
              </a>
            </li>
          {:else}
            <li class="none">Group documents from several spaces, for example all bills for your tax return.</li>
          {/each}
        </ul>
      </div>

      <div class="foot">
        {#each nav as [href, icon, label] (href)}
          <a class="nav" class:active={path === href} {href} aria-current={path === href ? 'page' : undefined}><Icon name={icon} size={17} /> {label}</a>
        {/each}
        {#if session.user.is_admin}<a class="nav" class:active={path === '/admin'} href="/admin"><Icon name="shield" size={17} /> Administration</a>{/if}
        <div class="me">
          <span class="who" title={session.user.username}>{session.user.display_name || session.user.username}</span>
          <button class="icon" onclick={logout} title="Sign out" aria-label="Sign out"><Icon name="logout" size={17} /></button>
        </div>
      </div>
    </nav>
    <div class="scrim" role="presentation" onclick={() => (ui.sidebarOpen = false)}></div>
    <main>{@render children()}</main>
  </div>
{/if}

{#if ui.palette && session.user}<QuickSearch onclose={() => (ui.palette = false)} />{/if}
{#if newCollection}<CollectionForm onsaved={(c) => { newCollection = false; goto(`/c/${c.id}`); }} onclose={() => (newCollection = false)} />{/if}

<div class="toasts" role="status" aria-live="polite">
  {#each ui.toasts as t (t.id)}<div class="toast {t.kind}">{t.message}</div>{/each}
</div>

{#if ui.uploads.length}
  <div class="uploads" aria-label="Uploads">
    <strong>Uploading</strong>
    {#each ui.uploads.slice(-8) as u (u.id)}
      <div class="up {u.state}">
        <span class="nm" title={u.name}>{u.name}</span>
        <span class="bar"><span style="width:{Math.round(u.progress * 100)}%"></span></span>
        {#if u.note}<small>{u.note}</small>{/if}
      </div>
    {/each}
    {#if ui.uploads.length > 8}<small class="muted">and {ui.uploads.length - 8} more</small>{/if}
  </div>
{/if}

<style>
  .splash { height: 100vh; background: var(--board); }
  .shell { display: grid; grid-template-columns: 264px 1fr; height: 100vh; }
  .mobilebar { display: none; }
  .rail { display: flex; flex-direction: column; min-height: 0; background: var(--surface); border-right: 1px solid var(--line); padding: 14px 10px 10px; }
  .brand { display: flex; align-items: center; gap: 9px; font-weight: 600; font-size: var(--t-md); color: var(--ink); padding: 2px 8px 12px; letter-spacing: -0.01em; }
  .brand:hover { text-decoration: none; }
  .searchbtn { justify-content: flex-start; width: 100%; color: var(--ink-2); background: var(--paper); margin-bottom: 14px; }
  :global(:root[data-theme='dark']) .searchbtn { background: var(--board); }
  @media (prefers-color-scheme: dark) { :global(:root:not([data-theme='light'])) .searchbtn { background: var(--board); } }
  .searchbtn span { flex: 1; text-align: left; }
  .group { font-size: var(--t-sm); font-weight: 600; color: var(--ink-2); margin: 4px 8px 6px; }
  .scrollpart { flex: 1; min-height: 80px; overflow: auto; }
  .tree { list-style: none; margin: 0; padding: 0; }
  .grouprow { display: flex; align-items: center; justify-content: space-between; margin-top: 14px; }
  .grouprow .group { margin-bottom: 2px; }
  .add { min-height: 26px; min-width: 26px; }
  .colls { list-style: none; margin: 0; padding: 0; }
  .coll { padding: 4px 8px 4px 10px; font-size: var(--t-base); }
  .coll :global(svg) { color: var(--ink-2); flex: none; }
  .cn { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .team { color: var(--ink-2); font-size: 0.75rem; max-width: 40%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .cnt { color: var(--ink-2); font-size: 0.75rem; min-width: 1.5em; text-align: right; }
  .none { color: var(--ink-2); font-size: var(--t-sm); padding: 2px 10px 6px; }
  .foot { border-top: 1px solid var(--line); padding-top: 8px; margin-top: 8px; display: flex; flex-direction: column; gap: 1px; }
  .nav { display: flex; align-items: center; gap: 10px; padding: 6px 10px; border-radius: var(--r-control); color: var(--ink); font-size: var(--t-base); }
  .nav:hover { background: var(--surface-2); text-decoration: none; }
  .nav.active { background: var(--accent-soft); color: var(--accent-ink); font-weight: 600; }
  .me { display: flex; align-items: center; gap: 6px; padding: 8px 0 0 10px; margin-top: 6px; border-top: 1px solid var(--line-soft); }
  .who { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--ink-2); font-size: var(--t-sm); }
  main { min-width: 0; min-height: 0; overflow: hidden; }
  .scrim { display: none; }

  .toasts { position: fixed; bottom: 18px; left: 50%; transform: translateX(-50%); display: flex; flex-direction: column; gap: 8px; z-index: 100; width: min(520px, calc(100vw - 32px)); pointer-events: none; }
  .toast { background: var(--ink); color: var(--surface); padding: 10px 14px; border-radius: var(--r-panel); box-shadow: var(--pop-shadow); }
  .toast.error { background: var(--err); color: #fff; }
  .toast.success { background: var(--ok); color: #fff; }
  .uploads { position: fixed; right: 16px; bottom: 16px; width: 320px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--r-panel); padding: 10px 12px; z-index: 90; box-shadow: var(--pop-shadow); display: flex; flex-direction: column; gap: 6px; font-size: var(--t-sm); }
  .up { display: grid; grid-template-columns: 1fr 80px; gap: 2px 8px; align-items: center; }
  .up .nm { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .up small { grid-column: 1 / -1; color: var(--ink-2); }
  .up.error small { color: var(--err); }
  .bar { height: 5px; background: var(--surface-2); border-radius: 99px; overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--accent); transition: width 0.2s; }
  .up.done .bar span { background: var(--ok); }
  .up.error .bar span { background: var(--err); }

  @media (max-width: 1100px) {
    .shell { grid-template-columns: 228px 1fr; }
  }
  @media (max-width: 900px) {
    .shell { grid-template-columns: 1fr; grid-template-rows: 48px 1fr; }
    .mobilebar { display: flex; align-items: center; gap: 6px; padding: 0 8px; background: var(--surface); border-bottom: 1px solid var(--line); }
    .mobilebar .brand { flex: 1; padding: 0; }
    .rail { position: fixed; top: 0; bottom: 0; left: 0; width: min(300px, 86vw); z-index: 41; transform: translateX(-100%); transition: transform 0.2s; box-shadow: var(--pop-shadow); }
    .shell.open .rail { transform: none; }
    .shell.open .scrim { display: block; position: fixed; inset: 0; background: var(--scrim); z-index: 40; }
  }
  @media (prefers-reduced-motion: reduce) { .rail { transition: none; } }
</style>
