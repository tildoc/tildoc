<script>
  import { untrack } from 'svelte';
  import Modal from './Modal.svelte';
  let { title, label = 'Name', value = '', confirm = 'OK', onsubmit, onclose } = $props();
  let v = $state(untrack(() => value));
  let input;
  $effect(() => {
    input?.focus();
    const dot = v.lastIndexOf('.');
    input?.setSelectionRange(0, dot > 0 ? dot : v.length);
  });
</script>

<Modal {title} {onclose}>
  <form onsubmit={(e) => { e.preventDefault(); v.trim() && onsubmit(v.trim()); }}>
    <label>{label}<input bind:this={input} bind:value={v} /></label>
    <div class="row end"><button type="button" onclick={onclose}>Cancel</button><button class="primary" type="submit">{confirm}</button></div>
  </form>
</Modal>
