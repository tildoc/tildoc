package api

import (
	"reflect"
	"strings"
	"time"

	"github.com/tildoc/tildoc/internal/core"
)

// authKind is how a route authenticates.
type authKind int

const (
	authSessionOrKey authKind = iota // session or API key
	authSession                      // signed-in user; API keys are rejected
	authPublic                       // no authentication
	authShare                        // the cookie of a share link
	authDropKey                      // only a Drop-In API key
)

// param is a query or header parameter.
type param struct {
	Name     string
	Desc     string
	Type     string // default string
	Required bool
	Multi    bool // may repeat
}

// routeDoc documents one route of the REST API. See openapi.go.
type routeDoc struct {
	Summary string
	Tag     string
	Auth    authKind
	Inst    string // instance permission the route needs
	Note    string

	Body    reflect.Type // JSON request body
	RawBody string       // media type of a raw request body
	Query   []param
	Headers []param

	Resp          reflect.Type // JSON response
	RespAlt       reflect.Type // alternative JSON response (oneOf)
	Status        int          // success status (default 200, 204 without a body)
	AltStatus     int          // further success status with the same body
	AltStatusDesc string
	Content       string // media type of a streamed response
	ContentDesc   string
	Ranged        bool   // the stream honours Range and conditional headers
	Redirect      string // the route answers with a redirect

	Errs    []int                // error statuses besides the derived ones
	ErrBody map[int]reflect.Type // error statuses with their own body
}

// description is the operation text: what the table says about access.
func (d routeDoc) description() string {
	var parts []string
	if d.Note != "" {
		parts = append(parts, d.Note)
	}
	switch {
	case d.Inst != "":
		parts = append(parts, "Needs the instance permission "+d.Inst+", which API keys never hold.")
	case d.Auth == authSession:
		parts = append(parts, "Session only: API keys are rejected.")
	case d.Auth == authDropKey:
		parts = append(parts, "Only a Drop-In API key is accepted.")
	case d.Auth == authShare:
		parts = append(parts, "Authenticated by the cookie set by POST /api/v1/public/open for this link.")
	}
	return strings.Join(parts, " ")
}

func typ[T any]() reflect.Type { return reflect.TypeFor[T]() }

func pathParamDesc(name string) string {
	switch name {
	case "user":
		return "User id or username."
	case "v":
		return "Version number."
	case "source":
		return "Namespace of the reference: lower-case letters, digits, dots, dashes, underscores, at most 64."
	case "ref":
		return "The reference: 1 to 255 bytes without control characters, URL-encoded."
	case "name":
		return "File name."
	case "link":
		return "Share link id."
	}
	return "Identifier."
}

func pathParamType(name string) string {
	if name == "v" {
		return "integer"
	}
	return "string"
}

func q(name, desc string) param  { return param{Name: name, Desc: desc} }
func qr(name, desc string) param { return param{Name: name, Desc: desc, Required: true} }
func qi(name, desc string) param { return param{Name: name, Desc: desc, Type: "integer"} }

var uploadHeaders = []param{
	{Name: "X-Content-SHA256", Desc: "Hex SHA-256 the content must hash to; otherwise 422."},
	{Name: "If-Match", Desc: "Replace only the file with this ETag; * requires that a file exists. Otherwise 412."},
	{Name: "If-None-Match", Desc: "* stores only if nothing exists at the path; otherwise 412."},
}

var (
	uploadErrs = []int{400, 403, 404, 409, 412, 413, 422}
	writeErrs  = []int{400, 403, 404, 409}
)

const (
	tagSystem      = "System"
	tagAuth        = "Authentication"
	tagAccount     = "Account"
	tagUsers       = "Users"
	tagTeams       = "Teams"
	tagSpaces      = "Spaces"
	tagExternal    = "External references"
	tagSharing     = "Sharing"
	tagRoles       = "Roles and permissions"
	tagKeys        = "Keys"
	tagFolders     = "Folders"
	tagFiles       = "Files"
	tagDocuments   = "Documents"
	tagEditing     = "Editing"
	tagTags        = "Tags"
	tagCollections = "Collections"
	tagBulk        = "Bulk"
	tagSearch      = "Search"
	tagTrash       = "Trash"
	tagImports     = "Imports"
	tagTransfers   = "Transfers"
	tagAdmin       = "Administration"
)

const (
	contentStream = "application/octet-stream"
	contentDesc   = "The file. Type and Content-Disposition follow the file and the inline parameter."
)

// apiDocs has one entry per REST route; openapi_test.go keeps it complete.
var apiDocs = map[string]routeDoc{
	// system
	"GET /api/v1/health":       {Summary: "Check that the server and its database respond.", Tag: tagSystem, Auth: authPublic, Resp: typ[healthResponse]()},
	"GET /api/v1/info":         {Summary: "Read the public addresses of this instance.", Tag: tagSystem, Auth: authPublic, Resp: typ[infoResponse]()},
	"GET /api/v1/openapi.json": {Summary: "Read this contract.", Tag: tagSystem, Auth: authPublic, Content: "application/json", ContentDesc: "The OpenAPI document."},
	"GET /api/v1/events": {Summary: "Stream live changes as Server-Sent Events.", Tag: tagSystem, Content: "text/event-stream",
		ContentDesc: "Starts with the event ready; then data lines with JSON arrays of change events holding only ids. Comments keep the connection alive. The server ends the stream after five minutes; reconnect.",
		Note:        "429 carries Retry-After.", Errs: []int{429}},

	// authentication
	"GET /api/v1/setup": {Summary: "Tell whether the first administrator is still missing.", Tag: tagAuth, Auth: authPublic, Resp: typ[setupStatusResponse]()},
	"POST /api/v1/setup": {Summary: "Create the first administrator and sign in.", Tag: tagAuth, Auth: authPublic, Body: typ[credentialsBody](), Resp: typ[loginResponse](),
		Note: "Also sets the session cookie.", Errs: []int{403}},
	"POST /api/v1/auth/login": {Summary: "Sign in with a username and password.", Tag: tagAuth, Auth: authPublic, Body: typ[credentialsBody](), Resp: typ[loginResponse](),
		Note: "Also sets the session cookie. Repeated failures answer 403 for a while.", Errs: []int{401, 403}},
	"POST /api/v1/auth/logout":   {Summary: "End the session of the cookie or bearer token.", Tag: tagAuth, Auth: authPublic},
	"GET /api/v1/auth/providers": {Summary: "List the external login providers.", Tag: tagAuth, Auth: authPublic, Resp: typ[[]authProvider]()},
	"GET /api/v1/auth/google/start": {Summary: "Start a Google sign-in.", Tag: tagAuth, Auth: authPublic, Redirect: "To Google, or to /login?error=provider.",
		ErrBody: map[int]reflect.Type{404: typ[plainNotFound]()}, Errs: []int{404}},
	"GET /api/v1/auth/google/callback": {Summary: "Finish a Google sign-in.", Tag: tagAuth, Auth: authPublic, Redirect: "To / when signed in, otherwise to /login with error=... or pending=1.",
		Query:   []param{q("state", "State issued by the start route."), q("code", "Authorization code."), q("error", "Error reported by the provider.")},
		ErrBody: map[int]reflect.Type{404: typ[plainNotFound]()}, Errs: []int{404}},

	// account
	"GET /api/v1/me":           {Summary: "Read the caller's account and preferences.", Tag: tagAccount, Resp: typ[meResponse]()},
	"PATCH /api/v1/me":         {Summary: "Change the caller's display name or preferences.", Tag: tagAccount, Body: typ[updateMeBody](), Resp: typ[meResponse]()},
	"POST /api/v1/me/password": {Summary: "Change the caller's password.", Tag: tagAccount, Body: typ[changePasswordBody](), Note: "A wrong current password answers 403."},

	// keys
	"GET /api/v1/me/access-keys":         {Summary: "List the caller's access keys for WebDAV, SFTP and S3.", Tag: tagKeys, Resp: typ[[]core.AccessKey]()},
	"POST /api/v1/me/access-keys":        {Summary: "Create an access key.", Tag: tagKeys, Auth: authSession, Body: typ[createAccessKeyBody](), Resp: typ[accessKeyCreated](), Status: 201, Note: "The secret is returned only here.", Errs: []int{404}},
	"DELETE /api/v1/me/access-keys/{id}": {Summary: "Revoke an access key.", Tag: tagKeys},
	"GET /api/v1/me/ssh-keys":            {Summary: "List the caller's SSH keys.", Tag: tagKeys, Resp: typ[[]core.SSHKey]()},
	"POST /api/v1/me/ssh-keys":           {Summary: "Register an SSH public key.", Tag: tagKeys, Auth: authSession, Body: typ[addSSHKeyBody](), Resp: typ[core.SSHKey](), Status: 201, Errs: []int{409}},
	"DELETE /api/v1/me/ssh-keys/{id}":    {Summary: "Remove an SSH key.", Tag: tagKeys},
	"GET /api/v1/me/api-keys":            {Summary: "List the caller's API keys.", Tag: tagKeys, Auth: authSession, Resp: typ[[]core.APIKey]()},
	"POST /api/v1/me/api-keys":           {Summary: "Create an API key.", Tag: tagKeys, Auth: authSession, Body: typ[createAPIKeyBody](), Resp: typ[apiKeyCreated](), Status: 201, Note: "The token is returned only here.", Errs: []int{404}},
	"DELETE /api/v1/me/api-keys/{id}":    {Summary: "Revoke an API key.", Tag: tagKeys, Auth: authSession},
	"POST /api/v1/me/drop-in-keys":       {Summary: "Create a Drop-In key for one space or folder.", Tag: tagKeys, Auth: authSession, Body: typ[createDropInKeyBody](), Resp: typ[dropInKeyCreated](), Status: 201, Note: "The token is returned only here.", Errs: []int{404}},
	"PUT /api/v1/drop/{name}":            {Summary: "Upload one new file with a Drop-In key.", Tag: tagKeys, Auth: authDropKey, RawBody: contentStream, Errs: []int{400, 409, 413}},

	// users
	"GET /api/v1/users":               {Summary: "List users.", Tag: tagUsers, Resp: typ[[]core.User](), Note: "Users waiting for approval are listed only with users.manage."},
	"POST /api/v1/users":              {Summary: "Create a user.", Tag: tagUsers, Auth: authSession, Inst: "users.manage", Body: typ[createUserBody](), Resp: typ[core.User](), Status: 201, Errs: []int{409}},
	"POST /api/v1/users/{id}/approve": {Summary: "Approve a user waiting for approval.", Tag: tagUsers, Auth: authSession, Inst: "users.manage", Resp: typ[core.User]()},
	"PATCH /api/v1/users/{id}":        {Summary: "Change a user.", Tag: tagUsers, Body: typ[core.UserUpdate](), Resp: typ[core.User](), Note: "Without users.manage only the caller's own display name.", Errs: []int{403}},
	"DELETE /api/v1/users/{id}":       {Summary: "Delete a user.", Tag: tagUsers, Auth: authSession, Inst: "users.manage", Errs: []int{400, 409}},

	// teams
	"GET /api/v1/teams":                        {Summary: "List the caller's teams.", Tag: tagTeams, Resp: typ[[]core.Team]()},
	"POST /api/v1/teams":                       {Summary: "Create a team.", Tag: tagTeams, Auth: authSession, Body: typ[teamNameBody](), Resp: typ[core.Team](), Status: 201, Errs: []int{409}},
	"GET /api/v1/teams/{id}":                   {Summary: "Read a team.", Tag: tagTeams, Resp: typ[core.Team]()},
	"PATCH /api/v1/teams/{id}":                 {Summary: "Rename a team.", Tag: tagTeams, Auth: authSession, Body: typ[teamNameBody](), Resp: typ[core.Team](), Errs: []int{409}},
	"DELETE /api/v1/teams/{id}":                {Summary: "Delete a team that owns nothing.", Tag: tagTeams, Auth: authSession, Errs: []int{409}},
	"GET /api/v1/teams/{id}/members":           {Summary: "List the members of a team.", Tag: tagTeams, Resp: typ[[]core.Member]()},
	"PUT /api/v1/teams/{id}/members/{user}":    {Summary: "Add a member or change their role.", Tag: tagTeams, Auth: authSession, Body: typ[setMemberBody](), Resp: typ[[]core.Member]()},
	"DELETE /api/v1/teams/{id}/members/{user}": {Summary: "Remove a member.", Tag: tagTeams, Auth: authSession, Errs: []int{400}},

	// spaces
	"GET /api/v1/spaces":                 {Summary: "List the spaces the caller can read.", Tag: tagSpaces, Resp: typ[[]core.Space]()},
	"POST /api/v1/spaces":                {Summary: "Create a space.", Tag: tagSpaces, Auth: authSession, Body: typ[createSpaceBody](), Resp: typ[core.Space](), Status: 201, Note: "Needs spaces.create, or a team the caller may create spaces for.", Errs: []int{409}},
	"GET /api/v1/spaces/{id}":            {Summary: "Read a space by id or slug.", Tag: tagSpaces, Resp: typ[core.Space]()},
	"PATCH /api/v1/spaces/{id}":          {Summary: "Rename a space or change its slug.", Tag: tagSpaces, Auth: authSession, Body: typ[updateSpaceBody](), Resp: typ[core.Space](), Errs: []int{409}},
	"DELETE /api/v1/spaces/{id}":         {Summary: "Delete an empty space.", Tag: tagSpaces, Auth: authSession, Errs: []int{400, 409}},
	"GET /api/v1/spaces/{id}/tree":       {Summary: "List all folders of a space.", Tag: tagSpaces, Resp: typ[[]core.FolderInfo]()},
	"POST /api/v1/spaces/{id}/transfer":  {Summary: "Give a space to another user or team.", Tag: tagSpaces, Auth: authSession, Body: typ[transferSpaceBody](), Errs: []int{400}},
	"PATCH /api/v1/spaces/{id}/settings": {Summary: "Replace a space's settings.", Tag: tagSpaces, Auth: authSession, Body: typ[spaceSettingsBody](), Resp: typ[core.Space]()},
	"GET /api/v1/spaces/{id}/export": {Summary: "Download a space as ZIP or bundle.", Tag: tagSpaces, Content: "application/zip", ContentDesc: "The archive (application/zip, or application/x-tar for a bundle). Errors after the start abort the stream.",
		Query: []param{q("format", "zip (default) or bundle.")}, Errs: []int{400, 403}},

	// external references
	"GET /api/v1/spaces/{id}/external": {Summary: "List references page by page.", Tag: tagExternal, Resp: typ[externalList](),
		Query: []param{q("source", "Only this source."), qi("limit", "Page size, 1 to 1000 (default 100)."), q("cursor", "next_cursor of the previous page.")}, Errs: []int{400, 403}},
	"PUT /api/v1/spaces/{id}/external/{source}/{ref}": {Summary: "Store content under a reference.", Tag: tagExternal, RawBody: contentStream, Resp: typ[uploadResult](), AltStatus: 201, AltStatusDesc: "A new link was created.",
		Note:  "A new reference needs path. A known reference adds a version to its document and ignores path. The same content again changes nothing.",
		Query: []param{q("path", "Path relative to the space root; required for a new reference.")}, Headers: uploadHeaders, Errs: uploadErrs},
	"GET /api/v1/spaces/{id}/external/{source}/{ref}":    {Summary: "Read a reference and its document.", Tag: tagExternal, Resp: typ[externalDetail](), Errs: []int{400, 403}},
	"DELETE /api/v1/spaces/{id}/external/{source}/{ref}": {Summary: "Remove a reference; the document stays.", Tag: tagExternal, Errs: []int{400, 403}},

	// sharing
	"GET /api/v1/spaces/{id}/grants":               {Summary: "List a space's grants.", Tag: tagSharing, Resp: typ[[]core.Grant](), Errs: []int{403}},
	"POST /api/v1/spaces/{id}/grants":              {Summary: "Create or update a grant on a space.", Tag: tagSharing, Auth: authSession, Body: typ[grantBody](), Resp: typ[core.Grant]()},
	"DELETE /api/v1/spaces/{id}/grants/{grant}":    {Summary: "Delete a grant on a space.", Tag: tagSharing, Auth: authSession},
	"GET /api/v1/documents/{id}/grants":            {Summary: "List a document's grants.", Tag: tagSharing, Resp: typ[[]core.Grant](), Errs: []int{403}},
	"POST /api/v1/documents/{id}/grants":           {Summary: "Share one document with a user or team.", Tag: tagSharing, Auth: authSession, Body: typ[grantBody](), Resp: typ[core.Grant]()},
	"DELETE /api/v1/documents/{id}/grants/{grant}": {Summary: "Delete a grant on a document.", Tag: tagSharing},
	"GET /api/v1/spaces/{id}/share-links":          {Summary: "List a space's share links.", Tag: tagSharing, Auth: authSession, Resp: typ[[]core.ShareLink]()},
	"GET /api/v1/documents/{id}/share-links":       {Summary: "List a document's share links.", Tag: tagSharing, Auth: authSession, Resp: typ[[]core.ShareLink]()},
	"POST /api/v1/share-links":                     {Summary: "Create a share link.", Tag: tagSharing, Auth: authSession, Body: typ[core.NewShareLink](), Resp: typ[shareLinkCreated](), Status: 201, Note: "The URL is returned only here; its fragment holds the token.", Errs: []int{404}},
	"DELETE /api/v1/share-links/{id}":              {Summary: "Revoke a share link.", Tag: tagSharing, Auth: authSession},
	"GET /api/v1/shared":                           {Summary: "List files shared with the caller.", Tag: tagSharing, Resp: typ[[]core.FileInfo]()},
	"POST /api/v1/public/open": {Summary: "Open a share link with its token.", Tag: tagSharing, Auth: authPublic, Body: typ[shareOpenBody](), Resp: typ[core.ShareInfo](),
		Note: "Sets the cookie of this link. 401 has the code password or login.", Errs: []int{401, 404, 429}, ErrBody: map[int]reflect.Type{401: typ[shareOpenError]()}},
	"GET /api/v1/public/{link}":              {Summary: "Read what a share link shows.", Tag: tagSharing, Auth: authShare, Resp: typ[core.ShareInfo]()},
	"GET /api/v1/public/{link}/folders/{id}": {Summary: "List a folder of a share link.", Tag: tagSharing, Auth: authShare, Resp: typ[core.ShareListing]()},
	"GET /api/v1/public/{link}/content": {Summary: "Read a file through a share link.", Tag: tagSharing, Auth: authShare, Content: contentStream, ContentDesc: contentDesc, Ranged: true,
		Query: []param{q("file", "Link id of the file; omit for a link to one file."), q("inline", "1 shows the file in the browser.")}, Errs: []int{403}},
	"PUT /api/v1/public/{link}/upload": {Summary: "Upload a file through a drop link.", Tag: tagSharing, Auth: authShare, RawBody: contentStream,
		Query: []param{qr("name", "File name.")}, Errs: []int{400, 413}},

	// roles and permissions
	"GET /api/v1/permissions":                {Summary: "List all permission names.", Tag: tagRoles, Resp: typ[core.PermissionCatalogue]()},
	"GET /api/v1/roles":                      {Summary: "List roles.", Tag: tagRoles, Resp: typ[[]core.Role]()},
	"POST /api/v1/roles":                     {Summary: "Create a role.", Tag: tagRoles, Auth: authSession, Inst: "roles.manage", Body: typ[createRoleBody](), Resp: typ[core.Role](), Status: 201, Errs: []int{409}},
	"PATCH /api/v1/roles/{id}":               {Summary: "Change a role.", Tag: tagRoles, Auth: authSession, Inst: "roles.manage", Body: typ[updateRoleBody](), Resp: typ[core.Role](), Errs: []int{409}},
	"DELETE /api/v1/roles/{id}":              {Summary: "Delete a role.", Tag: tagRoles, Auth: authSession, Inst: "roles.manage", Errs: []int{400, 409}},
	"GET /api/v1/instance/grants":            {Summary: "List instance grants.", Tag: tagRoles, Auth: authSession, Inst: "roles.manage", Resp: typ[[]core.Grant]()},
	"POST /api/v1/instance/grants":           {Summary: "Create or update an instance grant.", Tag: tagRoles, Auth: authSession, Inst: "roles.manage", Body: typ[grantBody](), Resp: typ[core.Grant]()},
	"DELETE /api/v1/instance/grants/{grant}": {Summary: "Delete an instance grant.", Tag: tagRoles, Auth: authSession, Inst: "roles.manage"},

	// folders
	"POST /api/v1/folders":           {Summary: "Create a folder.", Tag: tagFolders, Body: typ[createFolderBody](), Resp: typ[core.FolderInfo](), Status: 201, Errs: writeErrs},
	"GET /api/v1/folders/{id}":       {Summary: "List a folder with its breadcrumbs.", Tag: tagFolders, Resp: typ[folderListing]()},
	"PATCH /api/v1/folders/{id}":     {Summary: "Rename or move a folder.", Tag: tagFolders, Body: typ[moveFolderBody](), Resp: typ[core.FolderInfo](), Errs: writeErrs},
	"DELETE /api/v1/folders/{id}":    {Summary: "Move a folder to the trash.", Tag: tagFolders, Errs: []int{400}},
	"POST /api/v1/folders/{id}/copy": {Summary: "Copy a folder.", Tag: tagFolders, Body: typ[copyFolderBody](), Resp: typ[core.FolderInfo](), Status: 201, Errs: writeErrs},
	"PUT /api/v1/folders/{id}/files": {Summary: "Upload a file into a folder.", Tag: tagFolders, RawBody: contentStream, Resp: typ[uploadResult](), AltStatus: 201, AltStatusDesc: "A new link was created.",
		Note:  "Intermediate folders are created.",
		Query: []param{qr("path", "File name or relative path."), q("import", "Import batch id.")}, Headers: uploadHeaders, Errs: uploadErrs},
	"POST /api/v1/folders/{id}/new-file": {Summary: "Create an empty file.", Tag: tagFolders, Auth: authSession, Body: typ[teamNameBody](), Resp: typ[core.FileInfo](), Status: 201, Errs: writeErrs},
	"GET /api/v1/folders/{id}/export": {Summary: "Download a folder as ZIP.", Tag: tagFolders, Content: "application/zip", ContentDesc: "The archive. Errors after the start abort the stream.",
		Query: []param{q("format", "zip (default).")}, Errs: []int{400, 403}},

	// files
	"GET /api/v1/files/{id}": {Summary: "Read a file (a link to a document).", Tag: tagFiles, Resp: typ[core.FileInfo](), RespAlt: typ[fileWithDocument](),
		Query: []param{q("include", "document adds the document in the same answer.")}},
	"PATCH /api/v1/files/{id}":     {Summary: "Rename or move a file.", Tag: tagFiles, Body: typ[moveFileBody](), Resp: typ[core.FileInfo](), Errs: writeErrs},
	"DELETE /api/v1/files/{id}":    {Summary: "Move a file to the trash.", Tag: tagFiles, Errs: []int{403}},
	"POST /api/v1/files/{id}/copy": {Summary: "Copy a file.", Tag: tagFiles, Body: typ[copyFileBody](), Resp: typ[core.FileInfo](), Status: 201, Errs: writeErrs},
	"GET /api/v1/files/{id}/content": {Summary: "Download or show a file.", Tag: tagFiles, Content: contentStream, ContentDesc: contentDesc, Ranged: true, Errs: []int{403},
		Query: []param{q("inline", "1 shows it in the browser (needs view; a download needs download)."), q("viewer", "native: unsandboxed PDF for users who chose the browser's viewer.")}},
	"POST /api/v1/links": {Summary: "Place a document in another folder.", Tag: tagFiles, Body: typ[addLinkBody](), Resp: typ[core.FileInfo](), Status: 201, Errs: writeErrs},

	// documents
	"GET /api/v1/documents/{id}": {Summary: "Read a document with versions and places.", Tag: tagDocuments, Resp: typ[core.Document]()},
	"GET /api/v1/documents/{id}/content": {Summary: "Download or show a document, a version or a variant.", Tag: tagDocuments, Content: contentStream, ContentDesc: contentDesc, Ranged: true, Errs: []int{400, 403},
		Query: []param{qi("version", "Version number (default: the current one)."), q("variant", "searchable or image_only."), q("inline", "1 shows it in the browser."), q("viewer", "native: unsandboxed PDF for users who chose the browser's viewer.")}},
	"GET /api/v1/documents/{id}/text":                  {Summary: "Read the extracted text.", Tag: tagDocuments, Resp: typ[documentTextResponse](), Errs: []int{403}},
	"POST /api/v1/documents/{id}/versions":             {Summary: "Add a version.", Tag: tagDocuments, RawBody: contentStream, Resp: typ[core.Document](), Status: 201, Errs: []int{400, 403, 413}},
	"POST /api/v1/documents/{id}/versions/{v}/restore": {Summary: "Make an earlier version the current one.", Tag: tagDocuments, Resp: typ[core.Document](), Errs: []int{400, 403}},
	"DELETE /api/v1/documents/{id}/versions/{v}":       {Summary: "Delete a version for good.", Tag: tagDocuments, Errs: []int{400, 403}},
	"POST /api/v1/documents/{id}/reprocess":            {Summary: "Queue the document for processing again.", Tag: tagDocuments, Status: 202, Errs: []int{403}},
	"POST /api/v1/documents/{id}/tags":                 {Summary: "Tag a document by tag id or by name.", Tag: tagDocuments, Body: typ[tagDocumentBody](), Resp: typ[core.Document](), Note: "A name is created when missing.", Errs: []int{403}},
	"DELETE /api/v1/documents/{id}/tags/{tag}":         {Summary: "Remove a tag from a document.", Tag: tagDocuments, Resp: typ[core.Document]()},
	"POST /api/v1/documents/{id}/office":               {Summary: "Open a document in the office server.", Tag: tagDocuments, Auth: authSession, Resp: typ[officeOpenResponse](), Errs: []int{400}},
	"GET /api/v1/office":                               {Summary: "List the file types the office server handles.", Tag: tagDocuments, Resp: typ[officeInfoResponse]()},

	// editing
	"PUT /api/v1/documents/{id}/text": {Summary: "Save the editor's text as a new version.", Tag: tagEditing, Auth: authSession, RawBody: "text/plain", Resp: typ[core.Document](),
		Query: []param{qr("client", "client_id of the window that holds the lease.")}, Errs: []int{400, 409, 413}},
	"POST /api/v1/documents/{id}/edit-lease": {Summary: "Take the edit lease of a document.", Tag: tagEditing, Auth: authSession, Body: typ[acquireLeaseBody](), Resp: typ[core.EditLease](),
		Errs: []int{409}, ErrBody: map[int]reflect.Type{409: typ[leaseConflictResponse]()}},
	"PUT /api/v1/documents/{id}/edit-lease": {Summary: "Renew the edit lease.", Tag: tagEditing, Auth: authSession, Body: typ[renewLeaseBody](), Errs: []int{409}},
	"DELETE /api/v1/documents/{id}/edit-lease": {Summary: "Release the edit lease.", Tag: tagEditing, Auth: authSession,
		Query: []param{qr("client", "client_id of the window that holds the lease.")}},

	// tags
	"GET /api/v1/tags":             {Summary: "List tags.", Tag: tagTags, Resp: typ[[]core.Tag]()},
	"POST /api/v1/tags":            {Summary: "Create a tag.", Tag: tagTags, Body: typ[createTagBody](), Resp: typ[core.Tag](), Status: 201, Note: "An existing name returns that tag.", Errs: []int{403}},
	"PATCH /api/v1/tags/{id}":      {Summary: "Rename or recolour a tag.", Tag: tagTags, Auth: authSession, Inst: "tags.manage", Body: typ[updateTagBody](), Resp: typ[core.Tag](), Errs: []int{409}},
	"DELETE /api/v1/tags/{id}":     {Summary: "Delete a tag.", Tag: tagTags, Auth: authSession, Inst: "tags.manage"},
	"POST /api/v1/tags/{id}/merge": {Summary: "Merge a tag into another.", Tag: tagTags, Auth: authSession, Inst: "tags.manage", Body: typ[mergeTagBody](), Resp: typ[core.Tag]()},

	// collections
	"GET /api/v1/collections":                     {Summary: "List collections.", Tag: tagCollections, Resp: typ[[]core.Collection]()},
	"POST /api/v1/collections":                    {Summary: "Create a collection.", Tag: tagCollections, Auth: authSession, Body: typ[createCollectionBody](), Resp: typ[core.Collection](), Status: 201, Errs: []int{409}},
	"GET /api/v1/collections/{id}":                {Summary: "Read a collection and its items.", Tag: tagCollections, Resp: typ[collectionDetail]()},
	"PATCH /api/v1/collections/{id}":              {Summary: "Change a collection.", Tag: tagCollections, Body: typ[core.CollectionUpdate](), Resp: typ[core.Collection](), Errs: []int{403, 409}},
	"DELETE /api/v1/collections/{id}":             {Summary: "Delete a collection.", Tag: tagCollections, Errs: []int{403}},
	"POST /api/v1/collections/{id}/items":         {Summary: "Add documents to a collection.", Tag: tagCollections, Body: typ[addCollectionItemsBody](), Resp: typ[collectionDetail](), Errs: []int{403}},
	"DELETE /api/v1/collections/{id}/items/{doc}": {Summary: "Remove a document from a collection.", Tag: tagCollections, Resp: typ[collectionDetail](), Errs: []int{403}},
	"GET /api/v1/collections/{id}/export": {Summary: "Download a collection as ZIP or bundle.", Tag: tagCollections, Content: "application/zip", ContentDesc: "The archive (application/zip, or application/x-tar for a bundle). Errors after the start abort the stream.",
		Query: []param{q("format", "zip (default) or bundle.")}, Errs: []int{400, 403}},

	// bulk
	"POST /api/v1/bulk/move":             {Summary: "Move files and folders.", Tag: tagBulk, Body: typ[bulkBody](), Resp: typ[bulkResult](), Note: "Failures per item are listed in errors."},
	"POST /api/v1/bulk/delete":           {Summary: "Move files and folders to the trash.", Tag: tagBulk, Body: typ[bulkBody](), Resp: typ[bulkResult](), Note: "Failures per item are listed in errors."},
	"POST /api/v1/bulk/restore-original": {Summary: "Move files back to their original paths.", Tag: tagBulk, Body: typ[bulkBody](), Resp: typ[[]core.FileInfo](), Note: "Takes link_ids and, with import_id, the files of that import.", Errs: []int{403, 404}},

	// search
	"GET /api/v1/search": {Summary: "Search documents.", Tag: tagSearch, Resp: typ[core.SearchResult](), Errs: []int{400, 404},
		Query: []param{
			q("q", "Search text (web search syntax) over content and names."), q("space", "Limit to a space."), q("folder", "Limit to a folder and its subfolders."),
			q("mime", "MIME type prefix."), q("status", "Processing status."), q("sort", "relevance, name, modified, created or size."),
			q("order", "desc for descending."), qi("limit", "Page size (default 100)."), qi("offset", "Rows to skip."),
			{Name: "tag", Desc: "Tag id; documents must carry all given tags.", Multi: true}, q("collection", "Limit to a collection."),
		}},

	// trash
	"GET /api/v1/trash":               {Summary: "List deleted documents.", Tag: tagTrash, Resp: typ[[]core.TrashItem](), Query: []param{q("space", "Limit to a space.")}, Errs: []int{403, 404}},
	"DELETE /api/v1/trash":            {Summary: "Empty a space's trash.", Tag: tagTrash, Resp: typ[purgedResponse](), Query: []param{qr("space", "The space.")}, Errs: []int{400, 404}},
	"POST /api/v1/trash/{id}/restore": {Summary: "Restore a deleted document.", Tag: tagTrash, Resp: typ[core.Document](), Errs: []int{400, 403}},
	"DELETE /api/v1/trash/{id}":       {Summary: "Delete a document for good.", Tag: tagTrash, Errs: []int{400, 403}},

	// imports
	"GET /api/v1/imports":  {Summary: "List import batches.", Tag: tagImports, Resp: typ[[]core.Import](), Query: []param{qi("limit", "Page size (default 100).")}},
	"POST /api/v1/imports": {Summary: "Start an import batch.", Tag: tagImports, Body: typ[createImportBody](), Resp: typ[core.Import](), Status: 201, Errs: []int{403, 404}},

	// transfers
	"POST /api/v1/transfers": {Summary: "Upload a ZIP or bundle for import.", Tag: tagTransfers, Auth: authSession, RawBody: contentStream, Resp: typ[core.Transfer](), Status: 202, Errs: []int{400, 404, 413},
		Query: []param{qr("kind", "import_bundle or import_zip."), q("space_name", "Name of a new space."), q("space_slug", "Slug of a new space."), q("team_id", "Team that owns a new space."), q("folder_id", "Target folder; required for import_zip.")}},
	"GET /api/v1/transfers":      {Summary: "List the caller's transfers.", Tag: tagTransfers, Resp: typ[[]core.Transfer]()},
	"GET /api/v1/transfers/{id}": {Summary: "Read a transfer.", Tag: tagTransfers, Resp: typ[core.Transfer]()},

	// administration
	"GET /api/v1/admin/stats": {Summary: "Read instance statistics.", Tag: tagAdmin, Auth: authSession, Inst: "settings.manage", Resp: typ[core.Stats]()},
	"GET /api/v1/admin/audit": {Summary: "Read the audit log.", Tag: tagAdmin, Auth: authSession, Inst: "audit.view", Resp: typ[[]core.AuditEvent](),
		Query: []param{q("before", "Only events before this event id."), qi("limit", "Page size (default 100).")}},
	"GET /api/v1/admin/backups":         {Summary: "List backups.", Tag: tagAdmin, Auth: authSession, Inst: "backups.manage", Resp: typ[[]core.Backup]()},
	"POST /api/v1/admin/backups":        {Summary: "Start a backup.", Tag: tagAdmin, Auth: authSession, Inst: "backups.manage", Body: typ[startBackupBody](), Resp: typ[core.Backup](), Status: 202},
	"DELETE /api/v1/admin/backups/{id}": {Summary: "Delete a backup.", Tag: tagAdmin, Auth: authSession, Inst: "backups.manage", Errs: []int{409}},
	"GET /api/v1/admin/settings":        {Summary: "Read the instance settings.", Tag: tagAdmin, Auth: authSession, Inst: "settings.manage", Resp: typ[core.Settings]()},
	"PATCH /api/v1/admin/settings":      {Summary: "Change the instance settings.", Tag: tagAdmin, Auth: authSession, Inst: "settings.manage", Body: typ[core.SettingsUpdate](), Resp: typ[core.Settings]()},
	"POST /api/v1/admin/reprocess":      {Summary: "Queue documents for processing again.", Tag: tagAdmin, Auth: authSession, Inst: "settings.manage", Body: typ[reprocessBody](), Resp: typ[queuedResponse]()},
}

// plainNotFound marks a 404 sent as plain text by http.NotFound.
type plainNotFound struct{}

// ---------------------------------------------------------------- response bodies

type healthResponse struct {
	Status string `json:"status"`
}

type infoResponse struct {
	Name      string `json:"name"`
	PublicURL string `json:"public_url"`
	WebDAVURL string `json:"webdav_url"`
	S3URL     string `json:"s3_url"`
	S3Region  string `json:"s3_region"`
	SFTPAddr  string `json:"sftp_addr"`
}

type setupStatusResponse struct {
	NeedsSetup bool `json:"needs_setup"`
}

type loginResponse struct {
	User      core.User `json:"user"`
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type authProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type accessKeyCreated struct {
	Key    core.AccessKey `json:"key"`
	Secret string         `json:"secret"`
}

type apiKeyCreated struct {
	Key   core.APIKey `json:"key"`
	Token string      `json:"token"`
}

type dropInKeyCreated struct {
	Key   core.APIKey `json:"key"`
	Token string      `json:"token"`
	URL   string      `json:"url"`
}

type externalList struct {
	Items      []core.ExternalRef `json:"items"`
	NextCursor string             `json:"next_cursor"` // empty on the last page
}

type externalDetail struct {
	Reference core.ExternalRef `json:"reference"`
	Document  core.Document    `json:"document"`
}

type shareLinkCreated struct {
	Link core.ShareLink `json:"link"`
	URL  string         `json:"url"`
}

type shareOpenError struct {
	Error  string `json:"error"`
	Code   string `json:"code"` // password or login
	LinkID string `json:"link_id,omitempty"`
}

type leaseConflictResponse struct {
	Error string         `json:"error"`
	Code  string         `json:"code"`
	Lease core.EditLease `json:"lease"`
}

type fileWithDocument struct {
	core.FileInfo
	Document core.Document `json:"document"`
}

type documentTextResponse struct {
	Text   string `json:"text"`
	Method string `json:"method"`
}

type officeInfoResponse struct {
	Extensions []string `json:"extensions"`
	Editable   []string `json:"editable"`
	Create     []string `json:"create"`
}

type officeOpenResponse struct {
	URL            string `json:"url"`
	AccessToken    string `json:"access_token"`
	AccessTokenTTL int64  `json:"access_token_ttl"` // expiry, Unix milliseconds
}

type collectionDetail struct {
	Collection core.Collection       `json:"collection"`
	Items      []core.CollectionItem `json:"items"`
}

type purgedResponse struct {
	Purged int `json:"purged"`
}

type queuedResponse struct {
	Queued int `json:"queued"`
}

// ---------------------------------------------------------------- request bodies

type credentialsBody struct {
	Username    string `json:"username" doc:"required"`
	Password    string `json:"password" doc:"required"`
	DisplayName string `json:"display_name"`
}

type createUserBody struct {
	credentialsBody
	IsAdmin bool `json:"is_admin"`
}

type updateMeBody struct {
	DisplayName *string           `json:"display_name"`
	Prefs       *core.PrefsUpdate `json:"prefs"`
}

type changePasswordBody struct {
	Old string `json:"old_password" doc:"required"`
	New string `json:"new_password" doc:"required"`
}

type createAccessKeyBody struct {
	Label    string `json:"label"`
	SpaceID  string `json:"space_id"`
	ReadOnly bool   `json:"read_only"`
}

type addSSHKeyBody struct {
	PublicKey string `json:"public_key" doc:"required"`
	Label     string `json:"label"`
}

type createAPIKeyBody struct {
	Label       string     `json:"label"`
	Permissions []string   `json:"permissions" doc:"required"`
	SpaceIDs    []string   `json:"space_ids"`
	ExpiresAt   *time.Time `json:"expires_at"`
}

type createDropInKeyBody struct {
	Label     string     `json:"label"`
	SpaceID   string     `json:"space_id" doc:"required"`
	FolderID  string     `json:"folder_id"`
	ExpiresAt *time.Time `json:"expires_at"`
}

type teamNameBody struct {
	Name string `json:"name" doc:"required"`
}

type setMemberBody struct {
	Role string `json:"role" doc:"required"`
}

type createSpaceBody struct {
	Slug   string `json:"slug"`
	Name   string `json:"name"`
	TeamID string `json:"team_id"`
}

type updateSpaceBody struct {
	Name *string `json:"name"`
	Slug *string `json:"slug"`
}

type transferSpaceBody struct {
	User     string `json:"user"`
	TeamID   string `json:"team_id"`
	KeepRole string `json:"keep_role"`
	Confirm  string `json:"confirm" doc:"required"` // the space's slug
}

type spaceSettingsBody core.SpaceSettings

type createFolderBody struct {
	ParentID string `json:"parent_id" doc:"required"`
	Name     string `json:"name" doc:"required"`
}

type moveFolderBody struct {
	Name     string `json:"name"`
	ParentID string `json:"parent_id"`
}

type copyFolderBody struct {
	ParentID string `json:"parent_id" doc:"required"`
	Name     string `json:"name"`
}

type moveFileBody struct {
	Name      string `json:"name"`
	FolderID  string `json:"folder_id"`
	Overwrite bool   `json:"overwrite"`
}

type copyFileBody struct {
	FolderID  string `json:"folder_id" doc:"required"`
	Name      string `json:"name"`
	Overwrite bool   `json:"overwrite"`
}

type addLinkBody struct {
	DocumentID string `json:"document_id" doc:"required"`
	FolderID   string `json:"folder_id" doc:"required"`
	Name       string `json:"name" doc:"required"`
}

type shareOpenBody struct {
	Token    string `json:"token" doc:"required"`
	Password string `json:"password"`
	LinkID   string `json:"link_id"`
}

type createRoleBody struct {
	Name        string   `json:"name" doc:"required"`
	Scope       string   `json:"scope" doc:"required"`
	Permissions []string `json:"permissions"`
}

type updateRoleBody struct {
	Name        *string  `json:"name"`
	Permissions []string `json:"permissions"`
}

type createTagBody struct {
	Name  string `json:"name" doc:"required"`
	Color string `json:"color"`
}

type updateTagBody struct {
	Name  *string `json:"name"`
	Color *string `json:"color"`
}

type mergeTagBody struct {
	Into string `json:"into" doc:"required"`
}

type tagDocumentBody struct {
	TagID string `json:"tag_id"`
	Name  string `json:"name"`
}

type createCollectionBody struct {
	Name   string  `json:"name" doc:"required"`
	TeamID string  `json:"team_id"`
	TagID  *string `json:"tag_id"`
}

type addCollectionItemsBody struct {
	DocumentIDs []string `json:"document_ids" doc:"required"`
}

type acquireLeaseBody struct {
	ClientID string `json:"client_id" doc:"required"`
	Takeover bool   `json:"takeover"`
}

type renewLeaseBody struct {
	ClientID string `json:"client_id" doc:"required"`
}

type createImportBody struct {
	SpaceID    string `json:"space_id" doc:"required"`
	ClientRoot string `json:"client_root"`
}

type startBackupBody struct {
	Mode string `json:"mode"`
}

type reprocessBody struct {
	Statuses []string `json:"statuses"`
}
