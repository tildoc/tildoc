<script>
  import { tx } from '#lib/i18n/index.js';
  let { name, size = 18 } = $props();
  const paths = {
    folder: 'M3 6.5A1.5 1.5 0 0 1 4.5 5h4.2l1.8 2h9A1.5 1.5 0 0 1 21 8.5v9A1.5 1.5 0 0 1 19.5 19h-15A1.5 1.5 0 0 1 3 17.5z',
    file: tx('icon_m6_3h8l4_4v14h6zm14_3v4h4'),
    pdf: tx('icon_m6_3h8l4_4v14h6zm14_3v4h4m8_5_16h7m8'),
    text: tx('icon_m6_3h8l4_4v14h6zm14_3v4h4m8_5_11h7m8'),
    image: tx('icon_m4_5h16v14h4zm4_16l5_5_4_4'),
    upload: 'M12 16V4M7 9l5-5 5 5M4 20h16',
    download: 'M12 4v12M7 11l5 5 5-5M4 20h16',
    search: tx('icon_m10_5_17a6_5_6_5'),
    trash: 'M5 7h14M10 7V4h4v3M7 7l1 13h8l1-13',
    settings: tx('icon_m12_15a3_3_0_1_0'),
    users: tx('icon_m9_11a3_5_3_5_0'),
    shield: 'M12 3l7 3v6c0 4.5-3 7.5-7 9-4-1.5-7-4.5-7-9V6z',
    space: tx('icon_m4_5h16v4h4zm4_11h16v8h4zm8_15h8'),
    chevron: 'M9 6l6 6-6 6',
    plus: 'M12 5v14M5 12h14',
    move: 'M5 12h14M15 8l4 4-4 4M3 5v14',
    copy: tx('icon_m8_8h11v11h8zm5_16v5h11'),
    link: 'M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1',
    edit: tx('icon_m4_20h4l19_9l_4_4l4_16zm13'),
    restore: 'M4 12a8 8 0 1 0 2.3-5.7M4 4v4h4',
    close: 'M6 6l12 12M18 6 6 18',
    columns: tx('icon_m4_5h16v14h4zm10_5v14m15_5v14'),
    menu: 'M4 7h16M4 12h16M4 17h16',
    logout: 'M15 4h4v16h-4M10 8l-4 4 4 4M6 12h10',
    refresh: 'M20 12a8 8 0 1 1-2.3-5.7M20 4v4h-4',
    key: tx('icon_m15_9a4_4_0_1_1'),
    up: 'M12 19V5M6 11l6-6 6 6',
    down: 'M12 5v14M6 13l6 6 6-6',
    history: 'M12 7v5l3 2M4 12a8 8 0 1 0 2.3-5.7M4 4v4h4',
    info: tx('icon_m12_21a9_9_0_1_0'),
    home: 'M4 11l8-7 8 7M6 9.5V20h12V9.5',
    left: 'M15 6l-6 6 6 6',
    right: 'M9 6l6 6-6 6',
    'zoom-in': tx('icon_m10_5_17a6_5_6_5_2'),
    'zoom-out': tx('icon_m10_5_17a6_5_6_5_3'),
    pages: tx('icon_m4_4h6v7h4zm4_14h6v6h4zm14_4h6v16h_6z'),
    panel: tx('icon_m4_5h16v14h4zm15_5v14'),
    open: 'M14 4h6v6M20 4l-8 8M10 5H5v14h14v-5',
    rotate: 'M20 12a8 8 0 1 1-2.3-5.7M20 4v4h-4',
    more: 'M5 12h.01M12 12h.01M19 12h.01',
    check: 'M5 12.5l4.5 4.5L19 7.5',
    lock: tx('icon_m6_11h12v9h6zm8_5_11v8a3_5_3'),
    moon: 'M20 14.5A8 8 0 0 1 9.5 4a8 8 0 1 0 10.5 10.5z',
    tag: tx('icon_m3_5_12_5v4h8_5l8_5'),
    collection: tx('icon_m5_8h14v11h5zm7_5h10m9_2_5h6')
  };
</script>

<svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7"
  stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
  <path d={paths[name] ?? paths.file} />
</svg>
