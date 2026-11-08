<script>
  import Modal from './Modal.svelte';
  let { title, message, confirm = 'Confirm', danger = false, onconfirm, onclose } = $props();
</script>

<Modal {title} {onclose}>
  <p>{message}</p>
  <div class="row end">
    <button onclick={onclose}>Cancel</button>
    <button class={danger ? 'danger' : 'primary'} onclick={onconfirm}>{confirm}</button>
  </div>
</Modal>
