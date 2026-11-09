# tildoc bundle format, version 3

A bundle moves spaces, collections or a whole server between tildoc
instances. It is a POSIX tar stream (PAX headers); exports use the file
extension `.tildoc`, server backups `.dtbackup`.

```
manifest.json            always the first entry
blobs/<sha256-hex>       file contents, each exactly once, named by their SHA-256
text/<sha256-hex>        extracted text of a blob (optional)
```

Readers reject anything else, verify the SHA-256 of every blob against its
name, and treat the whole bundle as untrusted input.

## manifest.json

| Field | Content |
|-------|---------|
| `format`, `version` | `"tildoc-bundle"`, `3` |
| `kind` | `space` (one space), `collection` (one collection, documents without folders), `server` (backup) |
| `created_at`, `instance` | time and UUID of the exporting instance |
| `backup` | server backups: `{id, mode: full\|differential, base_id}` |
| `settings` | server backups: instance id, OCR preference, backup retention |
| `people` | users referenced by the bundle (`id`, `username`); imports map them by username |
| `users`, `identities`, `teams`, `team_links`, `roles`, `grants`, `share_links`, `api_keys`, `purge_log`, `audit_events`, `imports`, `access_keys`, `ssh_keys` | server backups only (passwords, link tokens and API keys only as hashes; access key secrets stay encrypted with the master key) |
| `tags` | `{id, name, color, created_by, created_at}` |
| `spaces` | `{id, slug, name, owner_user_id, owner_team_id, created_at, folders, documents}` |
| `documents` | collection bundles: documents with one place each |
| `collections` | `{id, name, owner_user_id, owner_team_id, tag_id, items: [{document_id, added_by, added_at}]}` |
| `blobs` | `{sha256, md5, size, mime, created_at, included, processing, text_method}`; `included=false` = bytes not in this archive (differential backups) |
| `derivatives` | `{source, kind: searchable_pdf\|image_only_pdf, derived, input, engine, meta, created_at}`; `input` makes the variant tree |

A folder is `{id, parent_id, name, created_at, created_by}`; exactly one root
per space (`parent_id` null, empty name). A document is `{id, created_at,
updated_at, created_by, forked_from, deleted_at, current_version, versions,
links, tags}`; versions are `{version, sha256, created_at, created_by, source,
meta, restored_from}`, with rising numbers, which may have gaps where a version was purged, referencing blobs;
`source` is one of `web`, `api`, `webdav`, `sftp`, `s3`, `rename`, `copy`,
`restore`, `editor`, `office`; `restored_from` (an earlier version number) can only be set when `source` is
`restore`, and is empty if that version was purged;
links (places) are `{id, folder_id, name, original_path, import_id,
created_at, created_by, updated_at, deleted_at, deleted_path}`.

Every object carries its UUID. Imports into a running server create new
UUIDs (remapping); `tildoc restore` keeps them.

## What goes where

| | Files export (ZIP) | Space/collection bundle | Server backup |
|--|--|--|--|
| Current files in the shown structure | yes | yes | yes |
| All versions, places, original paths | – | yes | yes |
| Tags, collections | – | yes (the exporter's collections) | yes (all) |
| Extracted text, OCR variants | – | yes | yes |
| Trash | – | – | yes |
| Users, teams, shares, keys | – | – | yes |

## Differential backups

A differential backup holds the complete manifest of its moment but only the
blobs that are not in its base full backup (`included=false` for the
others). Restore reads the full backup and the newest differential:
`tildoc restore FULL DIFFERENTIAL`.

## Purged content

A backup keeps what was in it when it was made. `purge_log` (in the manifest)
and `purge.log` next to the backups (it outlives the database) list what was
deleted for good afterwards; `tildoc restore` removes it again.

## Encryption

Reserved: exports pass through an optional sealing stage (planned: `age`)
that wraps the whole stream; the tar format inside stays the same.

## Version history of the format

| Version | Change |
|---------|--------|
| 3 | roles and grants instead of per-space grants; identities, share links, API keys; team members have `is_manager` |
| 2 | `restored_from` on versions (restores are a column now, no longer `meta.restored_from`) |
| 1 | initial format |

During development a server reads only its own format version. **From the
moment of productivation (to be defined), importing older exports is
mandatory.** There are no released bundles
older than version 3.
