# REST API

The contract is OpenAPI 3.1, served by every instance at `GET /api/v1/openapi.json`
(public, no login). It is built from `internal/api/apidoc.go` and the Go types,
and is never committed. To export it:

```
go run ./cmd/tildoc openapi > openapi.json
```

The `pipeline` workflow does the same for every pull request (as a build artifact)
and attaches the file to every GitHub release.

## Authentication

`Authorization: Bearer <token>` with a session token (`POST /api/v1/auth/login`)
or an API key (`tildoc_…`, created in Settings). The session cookie works too;
unsafe methods with the cookie also need the header `X-Tildoc-CSRF` (any value).

API keys hold no instance permissions, so routes that need one require a
signed-in user. Routes described as "session only" reject API keys altogether.

## Errors

Every error is JSON with a matching HTTP status:

```json
{"error": "message for people", "code": "not_found"}
```

`code` is one of `not_found`, `forbidden`, `unauthorized`, `conflict`, `invalid`,
`too_large`, `too_many`, `precondition_failed`, `checksum_mismatch`, `internal`.
The exceptions are documented on their routes: `POST /api/v1/public/open` answers
401 with `password` or `login`, and a refused edit lease includes the current lease.

## Upload conditions

`PUT /api/v1/folders/{id}/files` and the external reference `PUT` accept:

| Header | Effect |
|---|---|
| `X-Content-SHA256: <hex>` | the upload must hash to this, otherwise 422 |
| `If-Match: <etag>` | replace only this state of the file; `*`: a file must exist; otherwise 412 |
| `If-None-Match: *` | store only if nothing exists at the path, otherwise 412 |

## External references

A reference is an application's own id for a document, unique per space and
`source`. Uploading to the same reference again adds a version to the same
document, and the same content again changes nothing, so retries are safe.
URL-encode `ref` (`/` as `%2F`).

Store content (`path` is needed the first time, then ignored):

```
curl -T invoice.pdf -H "Authorization: Bearer $KEY" \
  "https://host/api/v1/spaces/$SPACE/external/billing/INV-1?path=invoices/INV-1.pdf"
```

Look it up, with its document:

```
curl -H "Authorization: Bearer $KEY" https://host/api/v1/spaces/$SPACE/external/billing/INV-1
```

Remove the reference; the document stays:

```
curl -X DELETE -H "Authorization: Bearer $KEY" https://host/api/v1/spaces/$SPACE/external/billing/INV-1
```

List page by page, passing `next_cursor` until it is empty:

```
curl -H "Authorization: Bearer $KEY" "https://host/api/v1/spaces/$SPACE/external?source=billing&limit=100&cursor=$NEXT"
```

## Versioning

While in development everything is v1 and may change. After that, breaking
changes become v2.
