<script>
  // History of a document as one graph: versions on the main line, derived
  // variants branching off diagonally, joins where a version continues from a
  // variant or restores an older version. Layout: lib/history.js. The SVG is
  // decoration; the list next to it carries names, details and actions.
  import Icon from './Icon.svelte';
  import { layoutHistory, edgePath, VARIANT_LABEL } from '../history.js';
  import { size, date } from '../format.js';

  let { doc, file = null, canWrite = false, viewVersion = $bindable(0), onrestore } = $props();

  const ROW = 54, LANE = 16, PAD = 10, R = 5;
  const graph = $derived(layoutHistory(doc.versions));
  const width = $derived(PAD * 2 + (graph.lanes - 1) * LANE);
  const x = (lane) => PAD + lane * LANE;
  const y = (row) => row * ROW + 19; // centre of the title line
  const color = (lane) => `var(--lane-${lane % 4})`;
  const viewing = (v) => (viewVersion || doc.version) === v;
  const param = { image_only_pdf: 'image_only', searchable_pdf: 'searchable' };
  const contentURL = $derived(`/api/documents/${doc.id}/content`);

  const ORIGINAL = { 'pdf-ocred': 'scan with an existing OCR layer', 'pdf-scanned': 'scan without text', 'pdf-mixed': 'partly scanned',
    'pdf-digital': 'with a text layer', 'pdf-tildoc-ocr': 'tildoc OCR result' };

  function relation(v) {
    if (v.restored_from) return `Restored from version ${v.restored_from}`;
    const c = v.continued_from;
    if (!c) return '';
    return c.variant ? `Continued from ${VARIANT_LABEL[c.variant] ?? c.variant} of version ${c.version}` : `Same content as version ${c.version}`;
  }

  // relation (restored / continued from) comes first in the row: it is what
  // the lines of the graph show, and must not be the part cut off
  function versionInfo(v) {
    const parts = [date(v.created_at), size(v.size), `${v.created_by || 'system'} via ${v.source}`];
    if (v.variants?.length && ORIGINAL[v.kind]) parts.push(ORIGINAL[v.kind]);
    return parts.join(', ');
  }

  function variantInfo(x) {
    const what = x.kind === 'image_only_pdf' ? 'existing text removed' : x.meta?.mode === 'skip-text' ? 'pages without text recognised' : 'all pages recognised';
    return [what, x.meta?.langs, x.engine, date(x.created_at)].filter(Boolean).join(', ');
  }

  // viewer URLs name the version "v", content URLs "version"
  function variantQuery(r, versionKey) {
    const q = new URLSearchParams();
    if (r.version !== doc.version) q.set(versionKey, String(r.version));
    q.set('variant', param[r.variant]);
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
      {#if r.kind === 'version'}
        {#if r.version === doc.version}<circle cx={x(r.lane)} cy={y(r.row)} r={R + 3.5} class="ring" style:stroke={color(0)} />{/if}
        <circle cx={x(r.lane)} cy={y(r.row)} r={R} style:fill={color(0)} style:stroke={color(0)} />
      {:else}
        <circle cx={x(r.lane)} cy={y(r.row)} r={R} class="variant" class:search={r.data.search_text && r.version === doc.version}
          style:stroke={color(r.lane)} />
      {/if}
    {/each}
  </svg>

  <ol class="rows" style:padding-left="{width + 6}px" aria-label="History: versions and their variants, oldest first">
    {#each graph.rows as r (r.id)}
      <li class={r.kind} class:viewing={r.kind === 'version' && viewing(r.version)}>
        {#if r.kind === 'version'}
          {@const v = r.data}
          <div class="title">
            <strong>Version {v.version}</strong>
            {#if v.version === doc.version}<span class="pill">current</span>{/if}
            {#if viewVersion === v.version}<span class="pill viewing-pill">viewing</span>{/if}
            <span class="acts">
              {#if v.version !== doc.version}
                <button class="small ghost" onclick={() => (viewVersion = viewVersion === v.version ? 0 : v.version)}>{viewVersion === v.version ? 'Show current' : 'View'}</button>
                {#if canWrite}<button class="small ghost" onclick={() => onrestore?.(v.version)}>Restore</button>{/if}
              {/if}
              <a class="btn small ghost" href={`${contentURL}?version=${v.version}`} download title="Download version {v.version}" aria-label="Download version {v.version}"><Icon name="download" size={14} /></a>
            </span>
          </div>
          <span class="meta" title={versionInfo(v)}>{#if relation(v)}<span class="rel">{relation(v)}</span>{', '}{/if}{versionInfo(v)}</span>
        {:else}
          {@const vx = r.data}
          <div class="title">
            <span class="vname">{VARIANT_LABEL[r.variant] ?? r.variant}</span>
            {#if vx.search_text && r.version === doc.version}<span class="pill search">used for search</span>{/if}
            <span class="acts">
              {#if file}<a class="btn small ghost" href={`/view/${file.link_id}?${variantQuery(r, 'v')}`} title="Open this variant" aria-label="Open {VARIANT_LABEL[r.variant]} of version {r.version}"><Icon name="open" size={14} /></a>{/if}
              <a class="btn small ghost" href={`${contentURL}?${variantQuery(r, 'version')}`} download
                title="Download this variant" aria-label="Download {VARIANT_LABEL[r.variant]} of version {r.version}"><Icon name="download" size={14} /></a>
            </span>
          </div>
          <span class="meta" title={variantInfo(vx)}>{variantInfo(vx)}</span>
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
  .rows li { height: var(--row); display: flex; flex-direction: column; justify-content: flex-start; padding-top: 8px; min-width: 0; border-radius: var(--r-control); }
  .rows li.viewing { background: var(--accent-soft); }
  .title { display: flex; align-items: center; gap: 6px; min-width: 0; height: 22px; }
  .title strong, .vname { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .vname { font-weight: 600; color: var(--ink-2); }
  .acts { margin-left: auto; display: inline-flex; gap: 2px; flex: none; }
  .acts .btn, .acts button { min-height: 24px; padding: 0 6px; }
  .meta { color: var(--ink-2); font-size: var(--t-sm); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; padding-right: 4px; }
  .rel { color: var(--ink); }
  .pill.search { background: var(--highlight); color: #1d2a35; border-color: transparent; }
  .viewing-pill { background: var(--accent); color: var(--on-accent); border-color: transparent; }
</style>
