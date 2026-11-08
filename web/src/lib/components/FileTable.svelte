<script>
  import { untrack } from 'svelte';
  import Icon from './Icon.svelte';
  import { COLUMNS, loadView, saveView } from '../columns.js';
  import { iconFor, size, date } from '../format.js';

  // rows: [{key, kind: 'folder'|'file', name, ...}]
  let { rows, view = 'folder', selected = $bindable([]), onopen, onpeek, ondropon, sortable = true, keyboard = true, empty } = $props();

  let cfg = $state(loadView(untrack(() => view)));
  let chooser = $state(false);
  let anchor = null;

  $effect(() => saveView(view, $state.snapshot(cfg)));

  let sorted = $derived.by(() => {
    if (!sortable) return rows;
    const col = COLUMNS[cfg.sort] ?? COLUMNS.name;
    const dir = cfg.desc ? -1 : 1;
    return [...rows].sort((a, b) => {
      // folders always first
      if (a.kind !== b.kind) return a.kind === 'folder' ? -1 : 1;
      const x = col.sort(a), y = col.sort(b);
      if (x < y) return -dir;
      if (x > y) return dir;
      return a.name.localeCompare(b.name);
    });
  });

  function sortBy(c) {
    if (!sortable) return;
    if (cfg.sort === c) cfg.desc = !cfg.desc;
    else {
      cfg.sort = c;
      cfg.desc = c === 'modified' || c === 'created' || c === 'size';
    }
  }

  function click(e, row, i) {
    if (e.shiftKey && anchor !== null) {
      const a = Math.min(anchor, i), b = Math.max(anchor, i);
      selected = sorted.slice(a, b + 1).map((r) => r.key);
    } else if (e.ctrlKey || e.metaKey) {
      selected = selected.includes(row.key) ? selected.filter((k) => k !== row.key) : [...selected, row.key];
      anchor = i;
    } else {
      selected = [row.key];
      anchor = i;
    }
  }

  // keyboard: arrows move, Shift extends, Enter opens, Space peeks
  let cursor = $derived(selected.length ? sorted.findIndex((r) => r.key === selected[selected.length - 1]) : -1);
  let tbody = $state();

  function reveal(key) {
    tbody?.querySelector(`[data-key="${CSS.escape(key)}"]`)?.scrollIntoView({ block: 'nearest' });
  }

  function keydown(e) {
    if (!keyboard || !sorted.length || e.altKey || e.defaultPrevented) return;
    if (document.querySelector('[role="dialog"]')) return;
    const tag = document.activeElement?.tagName;
    if (['INPUT', 'TEXTAREA', 'SELECT'].includes(tag)) return;
    if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'a') {
      e.preventDefault();
      selected = sorted.map((r) => r.key);
      return;
    }
    if (e.ctrlKey || e.metaKey) return;
    let next = null;
    if (e.key === 'ArrowDown') next = Math.min(sorted.length - 1, cursor + 1);
    else if (e.key === 'ArrowUp') next = Math.max(0, cursor < 0 ? 0 : cursor - 1);
    else if (e.key === 'Home') next = 0;
    else if (e.key === 'End') next = sorted.length - 1;
    else if (e.key === 'Enter' && cursor >= 0 && (tag !== 'BUTTON' && tag !== 'A')) {
      e.preventDefault();
      onopen?.(sorted[cursor]);
      return;
    } else if (e.key === ' ' && cursor >= 0 && tag !== 'BUTTON') {
      e.preventDefault();
      onpeek?.(sorted[cursor]);
      return;
    }
    if (next === null) return;
    e.preventDefault();
    const row = sorted[next];
    if (e.shiftKey && anchor !== null) {
      const a = Math.min(anchor, next), b = Math.max(anchor, next);
      selected = sorted.slice(a, b + 1).map((r) => r.key);
      selected = [...selected.filter((k) => k !== row.key), row.key];
    } else {
      selected = [row.key];
      anchor = next;
    }
    reveal(row.key);
  }

  function toggle(row, i) {
    selected = selected.includes(row.key) ? selected.filter((k) => k !== row.key) : [...selected, row.key];
    anchor = i;
  }

  function toggleAll() {
    selected = selected.length === rows.length ? [] : rows.map((r) => r.key);
  }

  function toggleColumn(c) {
    if (COLUMNS[c].always) return;
    cfg.columns = cfg.columns.includes(c) ? cfg.columns.filter((x) => x !== c) : [...cfg.columns, c];
  }

  function moveColumn(c, d) {
    const i = cfg.columns.indexOf(c), j = i + d;
    if (i < 1 || j < 1 || j >= cfg.columns.length) return;
    const cols = [...cfg.columns];
    [cols[i], cols[j]] = [cols[j], cols[i]];
    cfg.columns = cols;
  }

  // drag rows onto folder rows
  let dragOver = $state(null);
  function dragstart(e, row) {
    if (!selected.includes(row.key)) selected = [row.key];
    e.dataTransfer.setData('application/x-tildoc-keys', JSON.stringify(selected));
    e.dataTransfer.effectAllowed = 'move';
  }
  function dragover(e, row) {
    if (row.kind !== 'folder' || !e.dataTransfer.types.includes('application/x-tildoc-keys')) return;
    e.preventDefault();
    dragOver = row.key;
  }
  function drop(e, row) {
    dragOver = null;
    const raw = e.dataTransfer.getData('application/x-tildoc-keys');
    if (!raw || row.kind !== 'folder') return;
    e.preventDefault();
    e.stopPropagation();
    const keys = JSON.parse(raw).filter((k) => k !== row.key);
    if (keys.length) ondropon?.(keys, row);
  }
</script>

<svelte:window onkeydown={keydown} />

<div class="wrap">
  <div class="tools">
    <span class="count">{rows.length} item{rows.length === 1 ? '' : 's'}{selected.length ? `, ${selected.length} selected` : ''}</span>
    <div class="chooser">
      <button class="ghost small" onclick={() => (chooser = !chooser)} aria-expanded={chooser}><Icon name="columns" size={15} /> Columns</button>
      {#if chooser}
        <div class="menu" role="menu">
          {#each [...cfg.columns, ...Object.keys(COLUMNS).filter((c) => !cfg.columns.includes(c))] as c (c)}
            <div class="opt">
              <label><input type="checkbox" checked={cfg.columns.includes(c)} disabled={COLUMNS[c].always} onchange={() => toggleColumn(c)} /> {COLUMNS[c].label}</label>
              {#if cfg.columns.includes(c) && !COLUMNS[c].always}
                <span><button class="icon" onclick={() => moveColumn(c, -1)} aria-label="Move left"><Icon name="up" size={14} /></button><button class="icon" onclick={() => moveColumn(c, 1)} aria-label="Move right"><Icon name="down" size={14} /></button></span>
              {/if}
            </div>
          {/each}
          <button class="ghost small" onclick={() => (chooser = false)}>Done</button>
        </div>
      {/if}
    </div>
  </div>

  <div class="scroll">
    <table>
      <thead>
        <tr>
          <th class="check"><input type="checkbox" aria-label="Select all" checked={rows.length > 0 && selected.length === rows.length} onchange={toggleAll} /></th>
          {#each cfg.columns as c (c)}
            <th class="c-{c}" class:right={COLUMNS[c].align === 'right'} class:secondary={c !== 'name'}>
              <button class="sort" onclick={() => sortBy(c)}>{COLUMNS[c].label}{#if sortable && cfg.sort === c}<span class="arrow">{cfg.desc ? '▾' : '▴'}</span>{/if}</button>
            </th>
          {/each}
        </tr>
      </thead>
      <tbody bind:this={tbody}>
        {#each sorted as row, i (row.key)}
          <tr data-key={row.key} aria-selected={selected.includes(row.key)} class:sel={selected.includes(row.key)} class:over={dragOver === row.key} draggable="true"
            onclick={(e) => click(e, row, i)} ondblclick={() => onopen?.(row)}
            ondragstart={(e) => dragstart(e, row)} ondragover={(e) => dragover(e, row)} ondragleave={() => (dragOver = null)} ondrop={(e) => drop(e, row)}>
            <td class="check"><input type="checkbox" checked={selected.includes(row.key)} onclick={(e) => e.stopPropagation()} onchange={() => toggle(row, i)} aria-label="Select" /></td>
            {#each cfg.columns as c (c)}
              {#if c === 'name'}
                <td class="name"><div class="namecell">
                  <span class="ic {iconFor(row)}"><Icon name={iconFor(row)} /></span>
                  <span class="nm">
                    <button class="link" onclick={(e) => { e.stopPropagation(); onopen?.(row); }}>{row.name}</button>
                    <small class="mobile-meta">{row.kind === 'folder' ? 'Folder' : `${size(row.size)}, ${date(row.modified_at)}`}</small>
                  </span>
                  {#if row.link_count > 1}<span class="badge" title="Appears in {row.link_count} places"><Icon name="link" size={12} />{row.link_count}</span>{/if}
                </div></td>
              {:else}
                <td class="c-{c} secondary" class:right={COLUMNS[c].align === 'right'} class:muted={c === 'path' || c === 'original'}>
                  {#if c === 'status' && row.kind === 'file'}<span class="pill status {row.status}">{COLUMNS[c].value(row)}</span>{:else}{COLUMNS[c].value(row)}{/if}
                </td>
              {/if}
            {/each}
          </tr>
        {:else}
          <tr><td class="empty" colspan={cfg.columns.length + 1}>{#if empty}{@render empty()}{:else}Nothing here.{/if}</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

<style>
  .wrap { display: flex; flex-direction: column; min-height: 0; flex: 1; }
  .tools { display: flex; align-items: center; justify-content: space-between; padding: 0 2px 6px; }
  .count { color: var(--ink-2); font-size: var(--t-sm); }
  .chooser { position: relative; }
  .menu { position: absolute; right: 0; top: 110%; z-index: 20; background: var(--surface); border: 1px solid var(--line); border-radius: var(--r-panel);
    box-shadow: var(--pop-shadow); padding: 8px; width: 250px; display: flex; flex-direction: column; gap: 2px; }
  .opt { display: flex; justify-content: space-between; align-items: center; padding: 0 4px; }
  .opt label { display: flex; gap: 8px; align-items: center; margin: 0; font-weight: 400; font-size: var(--t-base); }
  .opt button.icon { min-height: 26px; min-width: 26px; }
  .scroll { overflow: auto; flex: 1; border: 1px solid var(--line); border-radius: var(--r-panel); background: var(--surface); }
  table { width: 100%; border-collapse: collapse; }
  thead th { position: sticky; top: 0; background: var(--surface); z-index: 1; text-align: left; font-weight: 600; border-bottom: 1px solid var(--line); white-space: nowrap; height: 36px; }
  th, td { padding: 0 12px; }
  td { height: 38px; border-bottom: 1px solid var(--line-soft); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 360px; }
  .right { text-align: right; }
  .check { width: 36px; padding-right: 0; }
  .sort { border: 0; background: none; padding: 0; min-height: 0; font: inherit; font-size: var(--t-sm); font-weight: 600; color: var(--ink-2); cursor: pointer; }
  .sort:hover:not(:disabled) { background: none; color: var(--ink); }
  .arrow { margin-left: 4px; color: var(--accent); }
  tbody tr { cursor: default; user-select: none; }
  tbody tr:hover { background: var(--surface-2); }
  tr.sel, tr.sel:hover { background: var(--accent-soft); }
  tr.sel td.name .link { color: var(--accent-ink); }
  tr.over { outline: 2px dashed var(--accent); outline-offset: -2px; }
  td.name { max-width: 560px; }
  .namecell { display: flex; align-items: center; gap: 10px; min-width: 0; }
  .nm { display: flex; flex-direction: column; min-width: 0; }
  .nm .link { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ic { display: inline-flex; color: var(--ink-2); flex: none; }
  .ic.folder { color: var(--manila); }
  .ic.pdf { color: var(--err); }
  .badge { display: inline-flex; align-items: center; gap: 3px; font-size: 0.75rem; border: 1px solid var(--line); border-radius: 99px; padding: 0 6px; color: var(--ink-2); flex: none; }
  .muted { color: var(--ink-2); }
  .secondary { color: var(--ink-2); font-size: var(--t-sm); }
  .empty { height: auto; text-align: center; color: var(--ink-2); padding: 0; white-space: normal; }
  .mobile-meta { display: none; color: var(--ink-2); }
  @media (max-width: 760px) {
    .secondary { display: none; }
    .mobile-meta { display: block; }
    td { max-width: none; height: 46px; }
    td.name { max-width: calc(100vw - 90px); }
  }
</style>
