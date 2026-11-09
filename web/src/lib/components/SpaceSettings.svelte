<script>
  import { tx } from '#lib/i18n/index.js';
  // Settings of one space: all off by default.
  import { untrack } from 'svelte';
  import Modal from './Modal.svelte';
  import { patch } from '../api.js';
  import { errorToast, refreshSpaces, toast } from '../state.svelte.js';

  let { space, onclose } = $props();
  let st = $state(untrack(() => ({ ...space.settings })));
  const groups = [
    [tx('admin_sharing'), [
      ['allow_login_links', tx('spacesettings_sign_in_links'), 'Links to the space or a file that work for people signed in to tildoc.'],
      ['allow_public_links', tx('spacesettings_public_links'), tx('spacesettings_links_that_work_for_anyone_who')],
      ['allow_drop_links', tx('spacesettings_drop_links'), tx('spacesettings_links_where_anyone_can_upload_into')],
      ['allow_file_shares', tx('spacesettings_file_shares'), tx('spacesettings_sharing_single_files_with_people_and')],
      ['allow_drop_in', tx('spacesettings_drop_in_tokens'), tx('spacesettings_keys_that_scripts_and_devices_use')]
    ]],
    [tx('spacesettings_protection'), [
      ['no_ocr', tx('spacesettings_no_tildoc_ocr'), tx('spacesettings_tildoc_adds_no_ocr_version_and')]
    ]]
  ];
  const policies = [
    ['open', tx('spacesettings_policy_open'), tx('spacesettings_policy_open_help')],
    ['append_only', tx('spacesettings_policy_append_only'), tx('spacesettings_policy_append_only_help')],
    ['read_only', tx('spacesettings_write_protection'), tx('spacesettings_policy_read_only_help')],
    ['frozen', tx('spacesettings_policy_frozen'), tx('spacesettings_policy_frozen_help')]
  ];
  async function save(e) {
    e.preventDefault();
    try {
      await patch(`/spaces/${space.id}/settings`, st);
      await refreshSpaces();
      toast(tx('spacesettings_saved_the_settings_of_the_space'), 'success');
      onclose();
    } catch (err) {
      errorToast(err);
    }
  }
</script>

<Modal title={tx('spacesettings_settings_of_x', { space: space.name })} {onclose}>
  <form onsubmit={save}>
    {#each groups as [title, items] (title)}
      <h3>{title}</h3>
      {#if title === tx('spacesettings_protection')}
        {#each policies as [value, label, help] (value)}
          <label class="inline opt"><input type="radio" name="write_policy" {value} bind:group={st.write_policy} aria-label={label} /><span><strong>{label}</strong><small>{help}</small></span></label>
        {/each}
      {/if}
      {#each items as [key, label, help] (key)}
        <label class="inline opt"><input type="checkbox" bind:checked={st[key]} aria-label={label} /><span><strong>{label}</strong><small>{help}</small></span></label>
      {/each}
    {/each}
    <div class="row end"><button type="button" onclick={onclose}>{tx('common_cancel')}</button><button class="primary">{tx('collectionform_save')}</button></div>
  </form>
</Modal>

<style>
  h3 { margin: 12px 0 6px; font-size: var(--t-base); }
  .opt { align-items: flex-start; gap: 10px; margin-bottom: 10px; }
  .opt span { display: flex; flex-direction: column; gap: 2px; }
  .opt small { color: var(--ink-2); font-size: var(--t-sm); }
</style>
