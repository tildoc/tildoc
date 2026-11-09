<script>
  // Settings of one space: all off by default.
  import { untrack } from 'svelte';
  import Modal from './Modal.svelte';
  import { patch } from '../api.js';
  import { errorToast, refreshSpaces, toast } from '../state.svelte.js';

  let { space, onclose } = $props();
  let st = $state(untrack(() => ({ ...space.settings })));
  const groups = [
    ['Sharing', [
      ['allow_login_links', 'Sign-in links', 'Links to the space or a file that work for people signed in to tildoc.'],
      ['allow_public_links', 'Public links', 'Links that work for anyone who has them, without an account. The server must also allow them.'],
      ['allow_drop_links', 'Drop links', 'Links where anyone can upload into this space without seeing what is in it. The server must also allow them.'],
      ['allow_file_shares', 'File shares', 'Sharing single files with people and teams.'],
      ['allow_drop_in', 'Drop-In tokens', 'Keys that scripts and devices use to send files into this space, and nothing else.']
    ]],
    ['Protection', [
      ['write_protected', 'Write protection', 'Nobody, the owner included, can upload, edit, move or delete anything here, by any channel, until this is switched off.'],
      ['no_ocr', 'No tildoc OCR', 'tildoc adds no OCR version and no image-only variant to files here. Originals are never changed, and text already in a file stays searchable.']
    ]]
  ];
  async function save(e) {
    e.preventDefault();
    try {
      await patch(`/spaces/${space.id}/settings`, st);
      await refreshSpaces();
      toast('Saved the settings of the space', 'success');
      onclose();
    } catch (err) {
      errorToast(err);
    }
  }
</script>

<Modal title={`Settings of ${space.name}`} {onclose}>
  <form onsubmit={save}>
    {#each groups as [title, items] (title)}
      <h3>{title}</h3>
      {#each items as [key, label, help] (key)}
        <label class="inline opt"><input type="checkbox" bind:checked={st[key]} aria-label={label} /><span><strong>{label}</strong><small>{help}</small></span></label>
      {/each}
    {/each}
    <div class="row end"><button type="button" onclick={onclose}>Cancel</button><button class="primary">Save</button></div>
  </form>
</Modal>

<style>
  h3 { margin: 12px 0 6px; font-size: var(--t-base); }
  .opt { align-items: flex-start; gap: 10px; margin-bottom: 10px; }
  .opt span { display: flex; flex-direction: column; gap: 2px; }
  .opt small { color: var(--ink-2); font-size: var(--t-sm); }
</style>
