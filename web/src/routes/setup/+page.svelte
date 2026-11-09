<script>
  import { tx } from '#lib/i18n/index.js';
  import { SOURCE_URL } from '#lib/format.js';
  import { goto } from '$app/navigation';
  import { post } from '#lib/api.js';
  import { loadSession } from '#lib/state.svelte.js';
  let username = $state('admin'), display = $state(''), password = $state(''), password2 = $state(''), error = $state('');
  async function submit(e) {
    e.preventDefault();
    if (password !== password2) {
      error = tx('setup_the_two_passwords_are_different');
      return;
    }
    try {
      await post('/setup', { username, password, display_name: display });
      await loadSession();
      goto('/');
    } catch (err) {
      error = err.message;
    }
  }
</script>

<div class="center">
  <form class="auth" onsubmit={submit}>
    <p class="brand"><img src="/icon-64.png" alt="" width="26" height="26" /><a href={SOURCE_URL} target="_blank" rel="noopener noreferrer" title={tx('common_source_code_on_github')}>tildoc</a></p>
    <h1>{tx('setup_create_the_administrator')}</h1>
    <p class="muted">{tx('setup_this_works_only_while_no_account')}</p>
    <label>{tx('common_username')}<input bind:value={username} required /></label>
    <label>{tx('common_display_name')}<input bind:value={display} /></label>
    <label>{tx('common_password')}<input type="password" autocomplete="new-password" bind:value={password} required minlength="8" /><span class="hint">{tx('common_at_least_8_characters')}</span></label>
    <label>{tx('setup_repeat_password')}<input type="password" autocomplete="new-password" bind:value={password2} required /></label>
    {#if error}<p class="err" role="alert">{error}</p>{/if}
    <button class="primary">{tx('setup_create_administrator')}</button>
  </form>
</div>

<style>
  .center { min-height: 100vh; display: grid; place-items: center; padding: 16px; }
  .auth { width: min(420px, 100%); display: flex; flex-direction: column; padding: 32px 32px 28px; background: var(--surface); border: 1px solid var(--line); border-radius: var(--r-panel); }
  .brand a { color: inherit; }
  .brand { display: flex; align-items: center; gap: 10px; font-weight: 600; font-size: var(--t-md); margin: 0 0 22px; }
  h1 { margin-bottom: 10px; }
  .muted { font-size: var(--t-sm); margin-bottom: 18px; }
  button { margin-top: 4px; min-height: 38px; }
</style>
