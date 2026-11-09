<script>
  // History of a document as one graph: versions on the main line, derived
  // variants branching off diagonally, joins where a version continues from a
  // variant or restores an older version. Layout: lib/history.js. The SVG is
  // decoration; the list next to it carries names, details and actions.
  // Every node is selectable: selecting shows it (version + variant) in the
  // preview or viewer; only the shown node is marked.
  import Icon from './Icon.svelte';
  import { layoutHistory, edgePath, nodeVariant, nodeSelection, nodeName, VARIANT_LABEL } from '../history.js';
  import { size, date, shownVariant } from '../format.js';

  // viewVersion: 0 = current; viewVariant: '' = the default for that version
  let { doc, canWrite = false, viewVersion = $bindable(0), viewVariant = $bindable(''), onrestore } = $props();

  const ROW = 54, LANE = 16, PAD = 10, R = 5;
  const graph = $derived(layoutHistory(doc.versions));
  const width = $derived(PAD * 2 + (graph.lanes - 1) * LANE);
  const x = (lane) => PAD + lane * LANE;
  const y = (row) => row * ROW + 19; // centre of the title line
  const color = (lane) => `var(--lane-${lane % 4})`;
  const shownVer = $derived(viewVersion || doc.version);
  const shownVar = $derived(shownVariant(doc, viewVersion, viewVariant));
  const isShown = (r) => r.version === shownVer && nodeVariant(r) === shownVar;

  function select(r) {
    ({ viewVersion, viewVariant } = nodeSelection(doc, r));
  }
  const contentURL = $derived(`/api/documents/${doc.id}/content`);

  const ORIGINAL = { 'pdf-ocred': 'scan with an existing OCR layer', 'pdf-scanned': 'scan without text', 'pdf-mixed': 'partly scanned',
    'pdf-digital': 'with a text layer', 'pdf-tildoc-ocr': 'tildoc OCR result' };

  function relation(v) {
    if (v.restored_from) return `restored from version ${v.restored_from}`;
    const c = v.continued_from;
    if (!c) return '';
    return c.variant ? `continued from ${VARIANT_LABEL[c.variant] ?? c.variant} of version ${c.version}` : `same content as version ${c.version}`;
  }

  // Row metadata, always in this order: date | size | where it came from
  // (upload channel of a version, program and version that made a variant).
  // Further details go into the tooltip.
  const SOURCE = { web: 'Web upload', api: 'API', webdav: 'WebDAV', sftp: 'SFTP', s3: 'S3', rename: 'Rename', copy: 'Copy', restore: 'Restore', editor: 'Editor', office: 'Office' };
  const versionInfo = (v) => [date(v.created_at), size(v.size), SOURCE[v.source] ?? v.source].join(' | ');
  const variantInfo = (x) => [date(x.created_at), size(x.size), x.engine || 'unknown program'].join(' | ');

  function versionTip(v) {
    return [versionInfo(v), v.created_by ? `by ${v.created_by}` : '', v.variants?.length ? ORIGINAL[v.kind] : ''].filter(Boolean).join(', ');
  }
  function variantTip(x) {
    const what = x.kind === 'image_only_pdf' ? 'existing text removed' : x.meta?.mode === 'skip-text' ? 'pages without text recognised' : 'all pages recognised';
    return [variantInfo(x), what, x.meta?.langs].filter(Boolean).join(', ');
  }

  function variantQuery(r) {
    const q = new URLSearchParams();
    if (r.version !== doc.version) q.set('version', String(r.version));
    q.set('variant', nodeVariant(r));
    return q;
  }
</script>

<div class="hist" style:--row="{ROW}px">
  <svg class="graph" width={width} height={graph.rows.length * ROW} aria-hidden="true">
    {#each graph.edges as e, i (i)}
      <path d={edgePath(e, x, y)} class="edge {e.kind}"
        style:stroke={color(e.kind === 'branch' ? e.to.lane : e.kind === 'merge' ? e.from.lane : e.kind === 'restore' ? e.via : 0)} />
    {/each}
    {#each graph.rows as r (r.id)}
      {#if isShown(r)}<circle cx={x(r.lane)} cy={y(r.row)} r={R + 3.5} class="ring" style:stroke={color(r.lane)} />{/if}
      {#if r.kind === 'version'}
        <circle cx={x(r.lane)} cy={y(r.row)} r={R} style:fill={color(0)} style:stroke={color(0)} />
      {:else}
        <circle cx={x(r.lane)} cy={y(r.row)} r={R} class="variant" class:search={r.data.search_text && r.version === doc.version}
          style:stroke={color(r.lane)} />
      {/if}
    {/each}
  </svg>

  <ol class="rows" style:padding-left="{width + 6}px" aria-label="History: versions and their variants, oldest first">
    {#each graph.rows as r (r.id)}
      <li class={r.kind} class:shown={isShown(r)}>
        <button class="pick" aria-pressed={isShown(r)} onclick={() => select(r)}
          aria-label="Show {nodeName(r).replace(/^Version/, 'version')}"></button>
        {#if r.kind === 'version'}
          {@const v = r.data}
          <div class="title">
            <strong>Version {v.version}</strong>
            {#if relation(v)}<span class="rel">{relation(v)}</span>{/if}
            <span class="acts">
              {#if v.version !== doc.version && canWrite}<button class="small ghost" onclick={() => onrestore?.(v.version)}>Restore</button>{/if}
              <a class="btn small ghost" href={`${contentURL}?version=${v.version}`} download title="Download version {v.version}" aria-label="Download version {v.version}"><Icon name="download" size={14} /></a>
            </span>
          </div>
          <span class="meta" title={versionTip(v)}>{versionInfo(v)}</span>
        {:else}
          {@const vx = r.data}
          <div class="title">
            <span class="vname">{VARIANT_LABEL[r.variant] ?? r.variant}</span>
            {#if vx.search_text && r.version === doc.version}<span class="pill search">used for search</span>{/if}
            <span class="acts">
              <a class="btn small ghost" href={`${contentURL}?${variantQuery(r)}`} download
                title="Download this variant" aria-label="Download {VARIANT_LABEL[r.variant]} of version {r.version}"><Icon name="download" size={14} /></a>
            </span>
          </div>
          <span class="meta" title={variantTip(vx)}>{variantInfo(vx)}</span>
        {/if}
      </li>
    {/each}
  </ol>
</div>

<style>
  .hist {
    position: relative;
    min-width: 0;
    --lane-0: var(--accent);
    --lane-1: var(--manila);
    --lane-2: var(--ok);
    --lane-3: var(--ink-2);
  }
  .graph { position: absolute; left: 0; top: 0; overflow: visible; }
  .edge { fill: none; stroke-width: 2; stroke-linecap: round; }
  .edge.restore { stroke-dasharray: 4 4; }
  .ring { fill: none; stroke-width: 2; }
  .variant { fill: var(--surface); stroke-width: 2.5; }
  .variant.search { fill: var(--highlight); }
  .rows { list-style: none; margin: 0; padding: 0; }
  .rows li { position: relative; height: var(--row); display: flex; flex-direction: column; justify-content: flex-start; padding-top: 8px; min-width: 0; border-radius: var(--r-control); }
  .rows li:hover { background: var(--surface-2); }
  .rows li.shown { background: var(--accent-soft); }
  /* the whole row selects; texts let clicks through, actions stay on top */
  .pick { position: absolute; inset: 0 0 0 -8px; width: auto; min-height: 0; border: 0; border-radius: var(--r-control); background: none; padding: 0; cursor: pointer; }
  .pick:hover:not(:disabled) { background: none; }
  .pick:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .title, .meta { pointer-events: none; position: relative; }
  .acts { pointer-events: auto; position: relative; z-index: 1; }
  .title { display: flex; align-items: center; gap: 6px; min-width: 0; height: 22px; }
  .title strong, .vname { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .vname { font-weight: 600; color: var(--ink-2); }
  .acts { margin-left: auto; display: inline-flex; gap: 2px; flex: none; }
  .acts .btn, .acts button { min-height: 24px; padding: 0 6px; }
  .meta { color: var(--ink-2); font-size: var(--t-sm); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; padding-right: 4px; }
  .rel { color: var(--ink-2); font-size: var(--t-sm); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; min-width: 0; }
  .pill.search { background: var(--highlight); color: #1d2a35; border-color: transparent; }
</style>
