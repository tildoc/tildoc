# HISTORY

Project history of tildoc, a self-hosted document management system:

1. [Decisions by the owner](#decisions-by-the-owner)
2. [Project history](#project-history)
3. [Security-relevant refactorings](#security-relevant-refactorings)

Sources: git log, `PLAN.md`, the owner's messages during development. Dates of
decisions come from those messages (UTC).

## Decisions by the owner

"By" says how a decision came about:

- **O**: the owner decided: their own requirement, a choice among options
  with no recommendation, or a choice against the recommendation.
- **R**: the owner accepted a recommendation made during planning.

"Reason" holds the owner's reason. A reason that came with an accepted
recommendation is marked "Rec.:". An empty field means no reason was stated.
"#n" refers to the Decisions table in `PLAN.md`.

### Scope and foundation

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D1 | 2026-11-08 | O | Documents stay the file types that get the most care; add an S3-compatible interface so the system can be used like a filesystem from Dolphin on Arch Linux. | |
| D2 | 2026-11-08 | O | Accept every file type; supported types get advanced services (#1). | |
| D3 | 2026-11-08 | O | Real folders and subfolders; imported data stays grouped as the user dropped it and can be remapped inside the system (#2). | |
| D4 | 2026-11-08 | R | Overwriting creates a new version (#3). | |
| D5 | 2026-11-08 | O | Drop sharing for now if that is easier; access is per space only (#4). | Replaced by D111. |
| D6 | 2026-11-08 | R | Own S3 implementation on the Go stdlib (#5). | |
| D7 | 2026-11-08 | O | Never upload twice: a file is recognized by its SHA-256 and, if known, only mapped to the place the uploader wanted (#6). | |
| D8 | 2026-11-08 | O | Both WebDAV and SFTP for desktop access (#7). | The recommendation was WebDAV first. |
| D9 | 2026-11-08 | O | `golang.org/x/*` packages are acceptable (#8). | |
| D10 | 2026-11-08 | O | TLS through nginx (#9). | |
| D11 | 2026-11-08 | O | The same file dropped twice into one space is one document with several links (#10). | |
| D12 | 2026-11-08 | O | Deleting removes only that link, the document goes to the trash with its last link; a file shows everywhere it is linked (#11). | |
| D13 | 2026-11-08 | R | Terminology: Space, Team, Folder, Document, Link (#12). | The owner questioned the old wording ("silo"). |
| D14 | 2026-11-08 | R | PDF text extraction with poppler `pdftotext` (#13). | |
| D15 | 2026-11-08 | R | PostgreSQL as job queue instead of Redis (#14). | Rec.: one component less; jobs are enqueued in the ingest transaction. |
| D16 | 2026-11-08 | O | Scope of the first build includes OCR (Phase 6); embeddings and ML stay out (#15). | The recommendation was Phases 0-5. Extended by D20. |
| D17 | 2026-11-08 | O | git with one commit per phase; tests via Docker; the offered AWS SDK test dependency was not selected (#16). | |
| D18 | 2026-11-08 | R | Writing through a link of a multi-linked document forks it copy-on-write (#17). | Rec.: behaves like a normal filesystem. |
| D19 | 2026-11-08 | O | Add integration tests after each larger step and run them; do not stop before the system is fully functional. | |
| D20 | 2026-11-08 | O | Implement all remaining phases (Phases 11-13 at that point). | |
| D21 | 2026-11-08 | O | The data model is one of the most important things next to security; development mode, no support for older versions, breaking changes are fine. Recorded in `AGENTS.md`. | "More important is, that the data model is clean." |

### Data model

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D22 | 2026-11-08 | O | Moving between spaces only remaps references, nothing is physically relocated; content is stored once on the server, users see only what they can access, indices included. Both covered by integration tests. | |
| D23 | 2026-11-08 | R | One namespace table `entries` for folders and links, one name per folder (#21). | |
| D24 | 2026-11-08 | R | Trash invariant enforced by a constraint trigger. | |
| D25 | 2026-11-08 | R | Moving into a space that already holds the same content merges into the existing document, histories combined chronologically (#22). | |
| D26 | 2026-11-08 | O | Shared processing state accepted as a documented side channel (#23). | Option text: every system that stores identical files once has this weakness. |

### Tags and collections

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D27 | 2026-11-08 | O | Tags on files, held in the database, one central vocabulary per server instance (#24). | |
| D28 | 2026-11-08 | O | Users may create tags; admins rename, merge and delete (#24). | |
| D29 | 2026-11-08 | O | Collections: virtual grouping of documents that belong together across spaces (#25). | |
| D30 | 2026-11-08 | O | Collections can be personal or team-owned (#25). | |
| D31 | 2026-11-08 | O | Collections appear read-only in WebDAV and SFTP (#25). | |

### Export, import, backup, encryption

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D32 | 2026-11-08 | O | Three forms: export of a space in the shown structure and names, bundle for another server, whole-server backup in Administration. | |
| D33 | 2026-11-08 | O | Import always goes the same way as any other way to add files (deduplication check); structures like spaces can be imported. | |
| D34 | 2026-11-08 | R | Backups stay on the server, no download in the UI; a person with file system access takes the copy (#27). | |
| D35 | 2026-11-08 | O | Backups are full or differential (#27). | |
| D36 | 2026-11-08 | O | All versions of a file are exported, with the derived variants (#30). | |
| D37 | 2026-11-08 | O | Trash excluded from standard exports, included in server backups (#30). | |
| D38 | 2026-11-08 | R | For now, anyone who can read a space may export it (#26). | Replaced by D101 (separate `export` permission). |
| D39 | 2026-11-08 | R | Full server restore only from the command line, into an empty instance (#28). | |
| D40 | 2026-11-08 | O | Users can import a space and remap its content to a new space (#28). | |
| D41 | 2026-11-08 | O | UUIDs wherever possible (#31). | "So we are not bound to static values." |
| D42 | 2026-11-08 | R | `age` as the later export encryption format (#29). | |
| D43 | 2026-11-08 | O | Add `age` encryption for exports and backups. | Planned as Phase 14, see D44. |
| D44 | 2026-11-08 | O | Keep the Phase 14 plan for later work. | Not implemented yet. |
| D45 | 2026-11-09 | O | During development only the current export/import format version is accepted. From the moment of productivation, to be defined by the owner, importing older exports is mandatory. | Changes the first item of the data model review (see D123), which proposed accepting older versions right away. |

### OCR variants

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D46 | 2026-11-08 | O | A file can have a tree of variants: no OCR gets tildoc's OCR; existing OCR gets an image-only variant with tildoc's OCR on it. Administration chooses the preferred OCR; the tree shows in the file's metadata (#32). | |
| D47 | 2026-11-08 | O | Selecting or opening a file shows the latest version by default. | Narrows D46: the preference now decides only search and the text view (#32). |
| D48 | 2026-11-09 | O | Use Tesseract 5.5.4 instead of 5.5.0, if possible. | Built from source, Debian ships only 5.5.0. |

### UI and viewer

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D49 | 2026-11-08 | O | Rework the whole frontend, modern, keeping the preview and adding a way to read a whole PDF. | Became Phase 8. |
| D50 | 2026-11-08 | R | `pdfjs-dist` as a new runtime dependency (#18). | |
| D51 | 2026-11-08 | R | Bundle the fonts on the own server. | Two families proposed; reduced to one by D53. |
| D52 | 2026-11-08 | O | The PDF viewer is selectable in the profile, pdf.js by default (#18). | |
| D53 | 2026-11-08 | O | One font everywhere, Atkinson Hyperlegible Next; Literata removed (#19). | "The other font is not nice." |
| D54 | 2026-11-08 | O | Form hints below the input; subpages use the full width. | |
| D55 | 2026-11-08 | O | Clicking the row opens the preview, clicking the file name opens the viewer. | |
| D56 | 2026-11-09 | O | Same PDF viewer choice for preview and full view, pdf.js as the default suggestion. | Was already the behaviour; a test was added. |
| D57 | 2026-11-09 | O | The selected file is in the URL, without reloading. | Users send this URL to colleagues, who open the same file if they have access. |
| D58 | 2026-11-09 | O | Tags shown inside the text box; "Also show in..." removed; Places lists identical content in other spaces, only where the user has access. | |
| D59 | 2026-11-08 | R | All nine UI performance suggestions: compression, live updates, parallel startup, viewer start, server text for find, large folders, cache, ETag, preloading (#34, #35). | |

### History graph

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D60 | 2026-11-08 | O | Merge versions and content variants into one tree with parallel lines and a diagonal branch-out as in git; a cheap Svelte solution rather than Mermaid (#36). | |
| D61 | 2026-11-08 | R | Oldest at the top; one "History" tab; `restored_from` as a column with foreign key; tildoc's own OCR output is not OCRed again (#36). | |
| D62 | 2026-11-08 | O | Graph nodes are selectable; only the selected one is marked, no "current" label. | |
| D63 | 2026-11-08 | O | The same tree horizontally in the open document instead of the buttons. | Replaced by D64. |
| D64 | 2026-11-09 | O | Remove the horizontal history from the open file. | It is available under properties anyway. |
| D65 | 2026-11-09 | O | History metadata order: date, size, source of upload or program and version. | |

### Deletion

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D66 | 2026-11-09 | O | Deleted files, also from the trash, are really deleted; re-uploading triggers indexing again. | Old data came back on re-upload. Replaces content staying until GC; see the security section. |

### Text editor

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D67 | 2026-11-09 | R | CodeMirror 6 as editor (#37). | |
| D68 | 2026-11-09 | O | Editable: anything `text/*`, size limit 5 MB. | |
| D69 | 2026-11-09 | R | Each save creates a new version. | |
| D70 | 2026-11-09 | O | Live read-only for others while one client edits; released when the locking client is gone for 15 seconds (#37). | |
| D71 | 2026-11-09 | O | The preview stays read-only; opening a file goes straight to the editor. | |
| D72 | 2026-11-09 | O | Saving is acknowledged and shows the differences. | |
| D73 | 2026-11-09 | O | Syntax highlighting on. | |
| D74 | 2026-11-09 | R | WebDAV, SFTP and S3 writes are not blocked during editing (#37). | |
| D75 | 2026-11-09 | O | The last save wins; at worst there are two versions (#37). | Replaced the proposal to refuse the save on a version conflict. |
| D76 | 2026-11-09 | R | The review before saving is always shown (#37). | |

### Office documents

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D77 | 2026-11-09 | O | Only the end of an editing session creates a version (#38). | Asked for "only exit-saves"; widened in planning to UNLOCK and lock expiry, since an exit save may write nothing. |
| D78 | 2026-11-09 | O | Always the Collabora CODE edition for now. | |
| D79 | 2026-11-09 | O | Storing PDFs of office documents for archiving: later. | |
| D80 | 2026-11-09 | R | A session ending without exit save is promoted after the lock expires. | Rec.: discarding it loses work. |
| D81 | 2026-11-09 | R | Several users can edit together. | |
| D82 | 2026-11-09 | O | Collabora runs on its own subdomain. | The recommendation was its own port on the same hostname. |
| D83 | 2026-11-09 | O | Stub tests are fine for now. | The recommendation also had an optional stage against the real server. |
| D84 | 2026-11-09 | O | Choice between Collabora and ONLYOFFICE per deployment (#39). | |
| D85 | 2026-11-09 | R | ONLYOFFICE through WOPI only; the Docs API stays a later option (#39). | The owner wanted the same interface to keep servers exchangeable. |
| D86 | 2026-11-09 | O | ONLYOFFICE is the default office server. | It also creates, for example, presentations and PDFs. |
| D87 | 2026-11-09 | O | "New File" next to "New Folder": a text file with any ending, or any format the office server supports. | |
| D88 | 2026-11-09 | O | PDFs open in the default PDF viewer and can be edited with the office server. | Neither offered option. |
| D89 | 2026-11-09 | R | The dropdown offers everything the server can create. | |
| D90 | 2026-11-09 | O | The preview notes that office formats need the full view. | |

### Name, licence, version

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D91 | 2026-11-09 | O | The application is called tildoc, path `github.com/tildoc/tildoc`. | |
| D92 | 2026-11-09 | O | BSD 3-Clause licence. | Replaced by D96. |
| D93 | 2026-11-09 | O | An image supplied by the owner is the application icon. | |
| D94 | 2026-11-09 | O | Version from git (tag, distance, commit), shown after login next to the program name. | |
| D95 | 2026-11-09 | O | The name tildoc links to the GitHub repository, on the login page and in the app. | AGPL section 13, and the owner wanted the link anyway. |
| D96 | 2026-11-09 | O | Dual licence: GNU AGPL-3.0-only and commercial, contact tildoc@0x7e.eu. | Replaces D92. |
| D97 | 2026-11-09 | O | Copyright holder: Matthias Robert Wiora. | From the licensing note in the owner's message: the holder must be able to grant the commercial licence. |
| D98 | 2026-11-09 | O | Outside contributions need a CLA (`CLA.md`). | From the same note: without one, their code cannot be relicensed commercially. |
| D99 | 2026-11-09 | O | The old publication is removed; rewrite the git history with the new licence and name so the repository can be published fresh. | |
| D100 | 2026-11-09 | O | Check the licences of the dependencies. | Go modules MIT or BSD, web MIT and Apache-2.0. |

### Permissions, sharing, authentication

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D101 | 2026-11-09 | O | A solid permission model: roles and groups, matrix-like grants; ownership of a space can be transferred; further login providers kept in mind (#40). | Replaces #33 and D38. |
| D102 | 2026-11-09 | O | Teams keep their name and take the meaning of groups. | |
| D103 | 2026-11-09 | R | A matrix row is a role plus individual permissions (#40). | |
| D104 | 2026-11-09 | R | No deny, permissions only add up (#40). | Rec.: stays predictable. |
| D105 | 2026-11-09 | O | Ownership transfers need no acceptance for now. | The recommendation was acceptance by the recipient. |
| D106 | 2026-11-09 | R | Personal spaces can be transferred. | |
| D107 | 2026-11-09 | R | Permissions per space only; a folder that needs other rights moves as a whole to a new space (#40). | |
| D108 | 2026-11-09 | O | A restricted viewer who can view but neither download nor export. | |
| D109 | 2026-11-09 | R | The restricted viewer is kept simple: no download or export offered, no server-rendered pages. | "Do not overengineer it." |
| D110 | 2026-11-09 | O | API keys. | |
| D111 | 2026-11-09 | O | Sharing with each variant allowed individually for spaces and files (#41). | Replaces D5. |
| D112 | 2026-11-09 | O | Upload-only drop links are included (#41). | |
| D113 | 2026-11-09 | O | Settings per space: sharing, disabling modification, disabling tildoc's processing (#42). | |
| D114 | 2026-11-09 | O | All space settings off by default; enabling one adds share links, prevents writing or disables tildoc's OCR (#42). | |
| D115 | 2026-11-09 | O | With OCR off, the image-only step is not done either (#42). | |
| D116 | 2026-11-09 | O | Built-in roles stay as they are for now. | |
| D117 | 2026-11-09 | O | A short hover description for every permission. | |
| D118 | 2026-11-09 | O | Settings as an icon next to sign-out; people and permissions move to a new Access page. | |
| D119 | 2026-11-09 | R | Drop-In API token instead of drop links over REST: uploads only, one space, optionally one folder. | The owner asked for this split and accepted the proposal. |
| D120 | 2026-11-09 | O | Google is the first OAuth provider. | As a solid reference point. |
| D121 | 2026-11-09 | O | Registered users are always approved by the server admin first, for now. | |

### 2026-11-09: lint and data model review

| ID | Date | By | Decision | Reason, status |
|---|---|---|---|---|
| D122 | 2026-11-09 | O | Fix the real golangci-lint bugs. | The recommendation also proposed excluding the harmless findings (284 in total) in the config. |
| D123 | 2026-11-09 | R | Completeness test for backups (every column exported). | Accepted, done in Phase 28. Version policy: D45. |
| D124 | 2026-11-09 | R | Version purge with a purge log. | Accepted, done in Phase 28. |
| D125 | 2026-11-09 | R | Audit trail. | Accepted, done in Phase 28. |
| D126 | 2026-11-09 | R | Google identities bound to the issuer. | Accepted, done in Phase 28. |
| D127 | 2026-11-09 | R | Database check against folder cycles. | Accepted, done in Phase 28. |
| D128 | 2026-11-09 | R | Shares revoked on every cross-space move. | Accepted, done in Phase 28. |
| D129 | 2026-11-09 | R | One user status column. | Accepted, done in Phase 28. |
| D130 | 2026-11-09 | R | Database constraints for folder targets. | Accepted, done in Phase 28. |
| D131 | 2026-11-09 | R | Key version on stored secrets; no per-space encryption keys. | Accepted, done in Phase 28. |

### Still open

- Phase 14 (`age`): backups without recipients, exports to a public key in the
  user's profile, post-quantum recipients.
- The moment of productivation (D45).
- Manual tests by the owner: a real editing session with each office server
  (Phase 20.5), a real Google sign-in (Phase 27.4).

## Project history

The owner started from a top-level plan written a few months before. On
2026-11-08 it was reworked (S3, WebDAV, SFTP, folders, deduplication,
terminology) and development began. The commits below were rewritten once
(Phase 23); commit hashes are the current ones. Phase 5 was committed after
Phase 6.

| Date | Commit | Phase | What it brought |
|---|---|---|---|
| 2026-11-08 | `fc3e3cb` | 0/1: Foundation, data model, blob store | Repository, config, migrations, test harness; schema; content-addressed blob store. |
| 2026-11-08 | `3dff517` | 2: Auth | Users, argon2id, sessions, access keys, SSH keys, teams, spaces, authorization. |
| 2026-11-08 | `9ed78a6` | 3: Core | Ingest with dedup, versioning, copy-on-write, provenance; folders, links, trash, GC; VFS layer. |
| 2026-11-08 | `4d849b1` | 4: REST API | API, full-text search, uploads, downloads, bulk operations, administration. |
| 2026-11-08 | `2bbe9fe` | 4b: Protocols | S3 (SigV4, aws-chunked, multipart), WebDAV, SFTP; end-to-end tests with aws-cli and rclone. |
| 2026-11-08 | `1b9be9a` | 6: Worker and OCR | Job queue in PostgreSQL, text, PDF and OCR processors, maintenance jobs, Docker test image. |
| 2026-11-08 | `d1bcee8` | 5: Web UI | SvelteKit UI: folder browser, details list, search, document panel, trash, teams, settings, administration; UI smoke test. |
| 2026-11-08 | `0e6e836` | 7: Deployment | Docker images, compose stack with nginx TLS, CSP, README, full-stack test. |
| 2026-11-08 | `465313a`, `e19f5f3` | 8: Frontend rework | "Light table" design, three viewing levels (list, peek panel, full viewer), pdf.js, find with highlights, inline PDFs sandboxed; follow-up: one typeface, full-width pages. |
| 2026-11-08 | `e29e60a` | Storage tests | Cross-space moves only remap references; content stored once and visible only with access. |
| 2026-11-08 | `1bcce5f` | 9: Clean data model | `entries` table, composite foreign keys, trash invariant by trigger, CHECK constraints, merge on cross-space move. |
| 2026-11-08 | `ae14d54` | 10: Tags and collections | Central tags, personal and team collections, read-only `/@collections/`, S3 tagging, UI. |
| 2026-11-08 | `ee3ba31` | 11-13: Export, OCR variants, permissions | UUIDv7; ZIP export, bundle format, import, full and differential backups; foreign OCR detection, variant tree, OCR preference; capabilities, roles, space sharing. |
| 2026-11-08 | `549a4a6` | Fix | PDF search highlight no longer covers the text; lists refresh while documents process. |
| 2026-11-08 | `23b7a5d` | 15: Live updates | PostgreSQL triggers, LISTEN/NOTIFY, `EventHub`, SSE; ETag and gzip; precompressed UI; virtualized file table. |
| 2026-11-08 | `c93a171` | Plan | Open topics collected in `PLAN.md`. |
| 2026-11-08 | `78a399c`, `55785df` | 16: History graph | One git-like graph of versions and variants, `restored_from`, bundle format 2; selectable nodes. |
| 2026-11-08 | `566cbfe`, `64acce2` | Fix, viewer | Name dialogs kept only the last typed character; horizontal history strip in the viewer (later removed). |
| 2026-11-09 | `f9ab81c`, `ccea74f` | History follow-up | Latest node opens by default; variants branch once and continue on their lane. |
| 2026-11-09 | `8f04757`, `32da956` | Test, plan | PDF viewer preference also checked in the full viewer; step order in `PLAN.md`. |
| 2026-11-09 | `67d7440` | Tesseract | Tesseract 5.5.4 built from source; tests and worker share one Dockerfile. |
| 2026-11-09 | `e787113` | UI cleanup | Places across spaces, tags in the box, history metadata order; history strip and "Also show in..." removed. |
| 2026-11-09 | `c6a1228`, `980865f` | UI | Selected file in the URL; multi-selection no longer restarts on a slightly moving Ctrl-click. |
| 2026-11-09 | `fce4e4a` | Immediate purge | Purge deletes content at once; uploads and GC serialized by the blob lock. |
| 2026-11-09 | `ca22733` | 17: Text editor | CodeMirror 6, edit lease over SSE, review diff before saving. |
| 2026-11-09 | `4a158ee` | 18: Name, licence, icon | Rename to tildoc, licence file, application icon. The licence at this point was BSD 3-Clause (D92); the rewritten commit shows AGPL. |
| 2026-11-09 | `0f08121` | 19: Office with Collabora | WOPI host, office sessions with a working copy, one version per session, own origin, stub tests. |
| 2026-11-09 | `5e5bddb` | 20: Choice of office server | Collabora or ONLYOFFICE as compose profiles, both through WOPI; `docs/office-servers.md`. |
| 2026-11-09 | `ea40671` | 21: New files | "New file" menu, ONLYOFFICE as default, office MIME types in code, "In Office" state. |
| 2026-11-09 | `3cdef75` | 22: Version from git | `git describe` version shown after login; first tag `v0.1.0`. |
| 2026-11-09 | `adc1ea9` | 23: Dual licence, fresh history | AGPL-3.0-only and commercial, `CLA.md`, source link; dependencies checked; history rewritten (all 37 commits of that time, tag moved). The owner then pushed the repository. |
| 2026-11-09 | `3561719` | 24: Permission model | Permission catalogue, editable roles, grants on spaces, files and the instance; restricted viewer with `download` enforced on every channel; ownership transfer; sharing links; API keys; `identities`; bundle format 3. |
| 2026-11-09 | `e4a3948` | 25: Space settings | Per-space settings, off by default; permission help; Access page. |
| 2026-11-09 | `3cf92b5` | CI | golangci-lint workflow. |
| 2026-11-09 | `541f054` | 26, 27: Drop-In tokens, Google login | `PUT /api/drop/{name}`, `drop_in`; OIDC with PKCE, pending accounts approved on the Access page. Fix: an API key whose only space was deleted covered all of its owner's spaces (`all_spaces` flag). |

Phase 28 (2026-11-09): the lint fixes (D122) and the data model changes
(D123-D131).

Planned, not done yet: Phase 14 (`age` encryption, D43, D44).

## Security-relevant refactorings


### API keys could create wider credentials

Before: `POST /me/access-keys` and `POST /me/ssh-keys` rejected access keys and SSH sessions, but not API keys. A key limited to one space and to viewing could create an access key or SSH key with its owner's full rights.
After: both need a login session (`requireSession`), like creating API keys. `TestAPIKeyCannotMintCredentials` covers it.

### 2026-11-08: inline PDFs are sandboxed

Before: `?inline=1` responses for PDFs were served without `Content-Security-Policy: sandbox`
so the browser's built-in viewer could render them inside the UI (an iframe on the UI origin).
Every other inline type was already sandboxed.

Now: the UI renders PDFs itself with pdf.js (PDF scripting off, no XFA, no JS eval allowed by
the CSP) from bytes fetched over the API, so every content response carries the sandbox CSP. The
browser's viewer remains available as an opt-in preference (`prefs.pdf_viewer = "native"`);
only for users with that preference does `?inline=1&viewer=native` drop the sandbox for PDFs. The
server checks the preference itself, so a crafted link cannot opt other users out.

### 2026-11-08: capability based permissions, space sharing

Before: access to a space came only from personal ownership or membership in
the owning team; checks compared numeric levels in several places.

Now: `space_role()` in the database computes the strongest role (ownership,
owning team, direct share, team share capped by the member's team role) and
`internal/core/permissions.go` maps roles to capabilities; every check goes
through `requireCap`. Shares never grant ownership and nobody can grant more
than their own role. Access keys still restrict scope and writing.

### 2026-11-08: blob garbage collection vs. exports

Exports and backups stream blob files after reading a snapshot. A shared
advisory lock now keeps garbage collection from deleting files while they
are being streamed.

### 2026-11-09: deleted content is deleted at once; GC race with uploads

Before: purging a document (emptying the trash, deleting from it, retention)
removed only the document; its content (blob file, extracted text, processing
state, OCR variants) stayed until garbage collection, which skipped blobs
used within the last hour. Uploading the same bytes in that hour silently
reused the old text and OCR results. The grace period was also the only
protection for uploads: a file written to the store before its database row
existed could be deleted by a GC run in between, leaving a row without file.

Now: every writer records a blob under the shared blob lock, in the same
transaction that writes the file into place (uploads, new versions, OCR
variants; bundle imports hold the lock for the whole import). GC takes the
lock exclusively, so no grace period is needed and `blobs.last_used_at` is
gone. Purging deletes the content of the purged documents immediately unless
another document still uses it; if an export holds the lock, a GC job
follows shortly.
