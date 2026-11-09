# Office documents: Collabora or ONLYOFFICE

tildoc opens office files in one of two office servers. Both run as a separate
container and connect through the same interface (WOPI), so you choose one per
deployment and can switch at any time.

| | Collabora (CODE 26.04.6.1.1) | ONLYOFFICE (10.0.0.1) |
|---|---|---|
| Compose profile | `collabora` | `onlyoffice` |
| Upstream | `collabora:9980` | `onlyoffice:80` |
| Licence | MPL-2.0 | AGPL-3.0 |
| Image size | about 1.9 GB | about 3.1 GB |
| RAM | about 0.5 GB idle | about 0.5 GB idle, 4 GB recommended by ONLYOFFICE |
| Rendering | on the server | in the browser |
| `.doc`, `.xls`, `.ppt` | can be edited | read-only |

The behaviour in tildoc is the same for both: several people can edit at once,
and a session creates one version (source "Office") when the last person
closes the document.

## First setup

⚠️ The database schema changed and the certificate needs the new subdomain, so
the first start deletes the stack's data:

```
docker compose -f deploy/docker-compose.yml --env-file deploy/.env down -v
```

Add **one** of these two blocks to `deploy/.env`.

Collabora:

```
COMPOSE_PROFILES=collabora
TILDOC_OFFICE_UPSTREAM=collabora:9980
TILDOC_OFFICE_SERVER_NAME=office.localhost
TILDOC_OFFICE_URL=https://office.localhost:8443
```

ONLYOFFICE:

```
COMPOSE_PROFILES=onlyoffice
TILDOC_OFFICE_UPSTREAM=onlyoffice:80
TILDOC_OFFICE_SERVER_NAME=office.localhost
TILDOC_OFFICE_URL=https://office.localhost:8443
```

Then start the stack:

```
docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d --build
```

The office server needs a minute or two on first start.

**Accept the certificate once.** With the generated self-signed certificate,
open this page in the browser and accept the warning, otherwise the editor
stays blank (a frame cannot show the warning). When it shows XML, the office
server is ready:

```
https://office.localhost:8443/hosting/discovery
```

Browsers resolve `*.localhost` by themselves, so a local test needs no DNS
entry. On a server, use a real subdomain that resolves to the host and is
covered by the certificate, for example:

```
TILDOC_OFFICE_SERVER_NAME=office.example.org
TILDOC_OFFICE_URL=https://office.example.org:8443
```

`TILDOC_OFFICE_URL` must include the port when it isn't 443, and must be a
different origin than `TILDOC_PUBLIC_URL`; tildoc refuses to start otherwise.

## Switching

No documents are lost. Close open documents first; a session still open in the
old server is saved as a version 30 minutes later, from its last save
(ONLYOFFICE sends nothing until everyone has left, so edits still open there
may be lost).

1. Remove the running office server:

   ```
   docker compose -f deploy/docker-compose.yml --env-file deploy/.env --profile collabora --profile onlyoffice rm -sf collabora onlyoffice
   ```

2. In `deploy/.env`, change `COMPOSE_PROFILES` and `TILDOC_OFFICE_UPSTREAM` to
   the other block.

3. Start again (this also restarts tildoc and nginx with the new upstream):

   ```
   docker compose -f deploy/docker-compose.yml --env-file deploy/.env up -d
   ```

4. Reload tildoc in the browser. The certificate is the same, so it needs no
   new confirmation.

## Turning it off

Remove the lines from `deploy/.env`, then run steps 1 and 3 above.

## What to test

Use a real `.docx` or `.odt`; tildoc's automated tests only use a stand-in for
these servers, so this is the first run against the real ones.

| # | Do | Expect |
|---|---|---|
| 1 | Upload a `.docx` and open it | the editor loads in the viewer, with your name as user |
| 2 | Type something, wait 1 minute, reload the file list | still the old version number; the preview says it is open in Office |
| 3 | Close the viewer, wait 20 seconds | a new version with source "Office" in History |
| 4 | Open it again without changing, close it | no new version |
| 5 | Open it as two users in two browsers | both edit and see each other; one version after both closed |
| 6 | Open it as a user with read access only | read-only editor, nothing saved |
| 7 | Upload a new version over WebDAV while the editor is open | a notice in the viewer; after closing, the editor's text is the current version and the WebDAV version stays in History |
| 8 | Open a `.txt` and a `.pdf` | the text editor and the PDF viewer as before |

## If something fails

| Symptom | Check |
|---|---|
| No editor, the file sheet with "Download" appears | the office server isn't reachable: `docker compose -f deploy/docker-compose.yml --env-file deploy/.env logs server \| grep -i office` |
| Blank frame | accept the certificate (see above) |
| "wopi request rejected" in the server log | proof signature check failed: send me that log line |
| Editor shows a load or connection error | `docker compose -f deploy/docker-compose.yml --env-file deploy/.env logs collabora` (or `onlyoffice`) and the server log |

Access tokens never appear in tildoc's or nginx's logs, but Collabora's and
ONLYOFFICE's own logs may contain them, so check before sharing those.

## Security

- The office server runs on its own origin and cannot reach tildoc's login
  cookie or API. `/wopi` is reachable only inside the compose network.
- Access tokens are stored hashed, bound to one document and login session, and
  permissions are checked again on every call.
- Both servers sign their requests with a key generated per installation, and
  tildoc verifies it.
- ONLYOFFICE is set to `ALLOW_PRIVATE_IP_ADDRESS=true` because tildoc is on the
  private compose network. Its address filter allows only `server` as a WOPI
  host, but it can also reach private addresses for other requests.
