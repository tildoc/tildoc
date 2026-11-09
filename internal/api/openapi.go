package api

import (
	"encoding"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/tildoc/tildoc/internal/blob"
)

// OpenAPI builds the contract from the route table and the Go types. It is
// exported by `tildoc openapi`; the file itself is never committed.
var OpenAPI = sync.OnceValues(generateOpenAPI)

// openAPI serves the contract. It is public and holds no build version.
func (s *Server) openAPI(w http.ResponseWriter, r *http.Request) error {
	spec, err := OpenAPI()
	if err != nil {
		return err
	}
	return writeJSON(w, 200, json.RawMessage(spec))
}

const apiDescription = `The tildoc REST API. Everything lives under /api/v1.

Authentication: send "Authorization: Bearer <token>" with a session token (POST /api/v1/auth/login) or an API key (tildoc_...), or use the session cookie. Unsafe methods with the cookie need the header ` + CSRFHeader + ` (any value).

API keys hold no instance permissions, so routes that need one require a signed-in user. Routes described as "session only" reject API keys altogether.

Errors are JSON: {"error": message, "code": code}. JSON GET responses carry an ETag and answer 304 to a matching If-None-Match.`

// errorCodes are the values of Error.code (see StatusFor).
var errorCodes = []string{"not_found", "forbidden", "unauthorized", "conflict", "invalid", "too_large", "too_many", "precondition_failed", "checksum_mismatch", "internal"}

// errorResponses describes the shared error answers by status.
var errorResponses = map[int]string{
	400: "The request is invalid (code invalid).",
	401: "Not authenticated (code unauthorized).",
	403: "Not allowed (code forbidden).",
	404: "Not found, or not visible to the caller (code not_found).",
	409: "Conflict with the current state (code conflict).",
	412: "An upload condition failed (code precondition_failed).",
	413: "Larger than the upload limit (code too_large).",
	422: "The upload does not match X-Content-SHA256 (code checksum_mismatch).",
	429: "Too many requests (code too_many).",
	500: "Internal error (code internal).",
}

var (
	timeType       = reflect.TypeFor[time.Time]()
	sha256Type     = reflect.TypeFor[blob.SHA256]()
	rawMessageType = reflect.TypeFor[json.RawMessage]()
	textMarshaler  = reflect.TypeFor[encoding.TextMarshaler]()
	jsonMarshaler  = reflect.TypeFor[json.Marshaler]()
)

var pathParamRe = regexp.MustCompile(`\{([^}]+)\}`)

// pathParams lists the {name} parameters of a route path in order.
func pathParams(path string) []string {
	var out []string
	for _, m := range pathParamRe.FindAllStringSubmatch(path, -1) {
		out = append(out, m[1])
	}
	return out
}

// generateOpenAPI builds the OpenAPI 3.1 document from apiDocs.
// Maps are encoded with sorted keys, so the output is deterministic.
func generateOpenAPI() ([]byte, error) {
	b := &specBuilder{schemas: map[string]any{}, seen: map[reflect.Type]schemaUse{}, names: map[string]reflect.Type{}}
	paths := map[string]map[string]any{}
	tags := map[string]bool{}
	for pattern, d := range apiDocs {
		method, path, ok := strings.Cut(pattern, " ")
		if !ok {
			return nil, fmt.Errorf("%s: pattern without method", pattern)
		}
		op, err := b.operation(method, path, d)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pattern, err)
		}
		if paths[path] == nil {
			paths[path] = map[string]any{}
		}
		paths[path][strings.ToLower(method)] = op
		tags[d.Tag] = true
	}
	tagList := []map[string]string{}
	for _, t := range slices.Sorted(mapKeys(tags)) {
		tagList = append(tagList, map[string]string{"name": t})
	}
	b.schemas["Error"] = map[string]any{
		"type":     "object",
		"required": []string{"code", "error"},
		"properties": map[string]any{
			"error": map[string]any{"type": "string", "description": "Message for people."},
			"code":  map[string]any{"type": "string", "enum": errorCodes},
		},
	}
	responses := map[string]any{}
	for status, desc := range errorResponses {
		responses[fmt.Sprintf("Error%d", status)] = map[string]any{
			"description": desc,
			"content":     jsonContent(map[string]any{"$ref": "#/components/schemas/Error"}),
		}
	}
	doc := map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "tildoc REST API", "version": "1", "description": apiDescription},
		"tags":    tagList,
		"paths":   paths,
		"components": map[string]any{
			"schemas":   b.schemas,
			"responses": responses,
			"securitySchemes": map[string]any{
				"bearer": map[string]any{"type": "http", "scheme": "bearer",
					"description": "A session token or an API key (tildoc_...)."},
				"cookieSession": map[string]any{"type": "apiKey", "in": "cookie", "name": SessionCookie,
					"description": "The session cookie. Unsafe methods also need the header " + CSRFHeader + "."},
			},
		},
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func mapKeys[V any](m map[string]V) func(func(string) bool) {
	return func(yield func(string) bool) {
		for k := range m {
			if !yield(k) {
				return
			}
		}
	}
}

func jsonContent(schema any) map[string]any {
	return map[string]any{"application/json": map[string]any{"schema": schema}}
}

// operationID derives a stable name: GET /api/v1/spaces/{id}/tree -> getSpacesByIdTree.
func operationID(method, path string) string {
	var sb strings.Builder
	sb.WriteString(strings.ToLower(method))
	for _, seg := range strings.Split(strings.TrimPrefix(path, "/api/v1/"), "/") {
		if name, ok := strings.CutPrefix(seg, "{"); ok {
			sb.WriteString("By")
			seg = strings.TrimSuffix(name, "}")
		}
		for _, part := range strings.FieldsFunc(seg, func(r rune) bool { return r == '-' || r == '.' }) {
			sb.WriteString(strings.ToUpper(part[:1]) + part[1:])
		}
	}
	return sb.String()
}

func (b *specBuilder) operation(method, path string, d routeDoc) (map[string]any, error) {
	if d.Summary == "" || d.Tag == "" {
		return nil, fmt.Errorf("summary and tag are required")
	}
	op := map[string]any{"summary": d.Summary, "tags": []string{d.Tag}, "operationId": operationID(method, path)}
	if desc := d.description(); desc != "" {
		op["description"] = desc
	}
	if d.Auth == authPublic || d.Auth == authShare {
		op["security"] = []map[string][]string{}
	} else {
		op["security"] = []map[string][]string{{"bearer": {}}, {"cookieSession": {}}}
	}
	params := []any{}
	for _, name := range pathParams(path) {
		params = append(params, map[string]any{"name": name, "in": "path", "required": true,
			"description": pathParamDesc(name), "schema": map[string]any{"type": pathParamType(name)}})
	}
	if d.Auth == authShare && method != "GET" {
		params = append(params, map[string]any{"name": CSRFHeader, "in": "header", "required": true,
			"description": "Any value; guards the share cookie.", "schema": map[string]any{"type": "string"}})
	}
	for _, p := range d.Query {
		params = append(params, p.spec("query"))
	}
	for _, p := range d.Headers {
		params = append(params, p.spec("header"))
	}
	if len(params) > 0 {
		op["parameters"] = params
	}
	switch {
	case d.RawBody != "":
		op["requestBody"] = map[string]any{"required": true, "content": map[string]any{
			d.RawBody: map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}}}}
	case d.Body != nil:
		schema, err := b.schema(d.Body, useRequest)
		if err != nil {
			return nil, err
		}
		op["requestBody"] = map[string]any{"required": true, "content": jsonContent(schema)}
	}
	responses := map[string]any{}
	status := d.Status
	switch {
	case d.Redirect != "":
		responses["302"] = map[string]any{"description": d.Redirect}
	case d.Content != "":
		if status == 0 {
			status = 200
		}
		responses[fmt.Sprint(status)] = map[string]any{"description": d.ContentDesc, "content": map[string]any{
			d.Content: map[string]any{"schema": contentSchema(d.Content)}}}
		if d.Content == "application/json" {
			responses["304"] = map[string]any{"description": "Not modified (If-None-Match matched the ETag)."}
		}
		if d.Ranged {
			responses["206"] = map[string]any{"description": "Partial content for a Range request."}
			responses["304"] = map[string]any{"description": "Not modified."}
			responses["416"] = map[string]any{"description": "The requested range cannot be satisfied."}
		}
	case d.Resp != nil:
		if status == 0 {
			status = 200
		}
		schema, err := b.schema(d.Resp, useResponse)
		if err != nil {
			return nil, err
		}
		if d.RespAlt != nil {
			alt, err := b.schema(d.RespAlt, useResponse)
			if err != nil {
				return nil, err
			}
			schema = map[string]any{"oneOf": []any{schema, alt}}
		}
		responses[fmt.Sprint(status)] = map[string]any{"description": "Success.", "content": jsonContent(schema)}
		if d.AltStatus != 0 {
			responses[fmt.Sprint(d.AltStatus)] = map[string]any{"description": d.AltStatusDesc, "content": jsonContent(schema)}
		}
		if method == "GET" {
			responses["304"] = map[string]any{"description": "Not modified (If-None-Match matched the ETag)."}
		}
	default:
		if status == 0 {
			status = 204
		}
		responses[fmt.Sprint(status)] = map[string]any{"description": "Success, no content."}
	}
	for _, code := range d.errorStatuses(method, path) {
		if t, ok := d.ErrBody[code]; ok {
			r, err := b.errorOverride(t)
			if err != nil {
				return nil, err
			}
			responses[fmt.Sprint(code)] = r
			continue
		}
		if _, ok := errorResponses[code]; !ok {
			return nil, fmt.Errorf("no description for status %d", code)
		}
		responses[fmt.Sprint(code)] = map[string]any{"$ref": fmt.Sprintf("#/components/responses/Error%d", code)}
	}
	op["responses"] = responses
	return op, nil
}

func (b *specBuilder) errorOverride(t reflect.Type) (map[string]any, error) {
	if t == reflect.TypeFor[plainNotFound]() {
		return map[string]any{"description": "The feature is not configured (plain text).",
			"content": map[string]any{"text/plain": map[string]any{"schema": map[string]any{"type": "string"}}}}, nil
	}
	schema, err := b.schema(t, useResponse)
	if err != nil {
		return nil, err
	}
	return map[string]any{"description": "See the schema for the extra fields.", "content": jsonContent(schema)}, nil
}

func contentSchema(media string) map[string]any {
	if media == "application/json" || strings.HasPrefix(media, "text/") {
		return map[string]any{"type": "string"}
	}
	return map[string]any{"type": "string", "format": "binary"}
}

// errorStatuses lists the error statuses of a route, sorted: those that
// follow from how it is reached, plus the ones its table entry adds.
func (d routeDoc) errorStatuses(method, path string) []int {
	set := map[int]bool{500: true}
	unsafe := !safeMethod(method)
	switch d.Auth {
	case authSessionOrKey, authSession, authDropKey:
		set[401] = true
		if unsafe || d.Auth != authSessionOrKey || d.Inst != "" {
			set[403] = true
		}
	case authShare:
		set[404] = true
		if unsafe {
			set[403] = true
		}
	}
	if len(pathParams(path)) > 0 {
		set[404] = true
	}
	if d.Body != nil {
		set[400] = true
	}
	for _, c := range d.Errs {
		set[c] = true
	}
	out := make([]int, 0, len(set))
	for c := range set {
		out = append(out, c)
	}
	slices.Sort(out)
	return out
}

func (p param) spec(in string) map[string]any {
	schema := map[string]any{"type": p.Type}
	if p.Type == "" {
		schema["type"] = "string"
	}
	out := map[string]any{"name": p.Name, "in": in, "required": p.Required, "description": p.Desc}
	if p.Multi {
		out["schema"] = map[string]any{"type": "array", "items": schema}
		out["style"], out["explode"] = "form", true
		return out
	}
	out["schema"] = schema
	return out
}

// ---------------------------------------------------------------- schemas

// schemaUse says whether a type is decoded from requests or encoded into
// responses. Request fields are optional unless tagged doc:"required", and
// requests reject unknown fields; response fields are present unless
// omitempty. One Go type can only serve one side.
type schemaUse int

const (
	useResponse schemaUse = iota + 1
	useRequest
)

type specBuilder struct {
	schemas map[string]any
	seen    map[reflect.Type]schemaUse
	names   map[string]reflect.Type
}

func (b *specBuilder) schema(t reflect.Type, use schemaUse) (map[string]any, error) {
	if t == timeType {
		return map[string]any{"type": "string", "format": "date-time"}, nil
	}
	if t == sha256Type {
		return map[string]any{"type": "string", "pattern": "^[0-9a-f]{64}$", "description": "Hex SHA-256."}, nil
	}
	if t == rawMessageType {
		return map[string]any{}, nil
	}
	if t.Implements(textMarshaler) {
		return map[string]any{"type": "string"}, nil
	}
	if t.Implements(jsonMarshaler) {
		return nil, fmt.Errorf("%s has a custom JSON encoding", t)
	}
	switch t.Kind() {
	case reflect.Pointer:
		s, err := b.schema(t.Elem(), use)
		if err != nil {
			return nil, err
		}
		return nullable(s), nil
	case reflect.Struct:
		return b.structRef(t, use)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return map[string]any{"type": "string", "contentEncoding": "base64"}, nil
		}
		items, err := b.schema(t.Elem(), use)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "array", "items": items}, nil
	case reflect.Map:
		if t.Key().Kind() != reflect.String {
			return nil, fmt.Errorf("%s: only string map keys are supported", t)
		}
		if t.Elem().Kind() == reflect.Interface {
			return map[string]any{"type": "object", "additionalProperties": true}, nil
		}
		v, err := b.schema(t.Elem(), use)
		if err != nil {
			return nil, err
		}
		return map[string]any{"type": "object", "additionalProperties": v}, nil
	case reflect.Interface:
		return map[string]any{}, nil
	case reflect.String:
		return map[string]any{"type": "string"}, nil
	case reflect.Bool:
		return map[string]any{"type": "boolean"}, nil
	case reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32:
		return map[string]any{"type": "integer"}, nil
	case reflect.Int64, reflect.Uint64:
		return map[string]any{"type": "integer", "format": "int64"}, nil
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}, nil
	}
	return nil, fmt.Errorf("unsupported type %s", t)
}

// nullable allows null in a schema; a $ref cannot carry a type of its own.
func nullable(s map[string]any) map[string]any {
	if _, ok := s["$ref"]; ok || s["type"] == nil {
		return map[string]any{"oneOf": []any{s, map[string]any{"type": "null"}}}
	}
	out := make(map[string]any, len(s))
	for k, v := range s {
		out[k] = v
	}
	out["type"] = []any{s["type"], "null"}
	return out
}

func (b *specBuilder) structRef(t reflect.Type, use schemaUse) (map[string]any, error) {
	if t.Name() == "" {
		return nil, fmt.Errorf("anonymous struct %s: declare a named doc-only type", t)
	}
	name := strings.ToUpper(t.Name()[:1]) + t.Name()[1:]
	ref := map[string]any{"$ref": "#/components/schemas/" + name}
	if prev, ok := b.seen[t]; ok {
		if prev != use {
			return nil, fmt.Errorf("%s is used for requests and responses: declare a named doc-only type", t)
		}
		return ref, nil
	}
	if other, ok := b.names[name]; ok && other != t {
		return nil, fmt.Errorf("schema name %s is used by %s and %s", name, other, t)
	}
	b.seen[t], b.names[name] = use, t
	props := map[string]any{}
	required := []string{}
	if err := b.addFields(t, use, props, &required, map[string]int{}, 0); err != nil {
		return nil, err
	}
	s := map[string]any{"type": "object", "properties": props}
	slices.Sort(required)
	if len(required) > 0 {
		s["required"] = required
	}
	if use == useRequest {
		s["additionalProperties"] = false
	}
	b.schemas[name] = s
	return ref, nil
}

// addFields follows encoding/json: embedded structs without a tag flatten,
// and a field at a shallower depth wins over a deeper one.
func (b *specBuilder) addFields(t reflect.Type, use schemaUse, props map[string]any, required *[]string, depths map[string]int, depth int) error {
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		name, opts, _ := strings.Cut(tag, ",")
		if name == "-" {
			continue
		}
		if f.Anonymous && name == "" {
			et := f.Type
			if et.Kind() == reflect.Pointer {
				et = et.Elem()
			}
			if et.Kind() == reflect.Struct {
				if err := b.addFields(et, use, props, required, depths, depth+1); err != nil {
					return err
				}
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		if d, ok := depths[name]; ok && d <= depth {
			continue
		}
		depths[name] = depth
		s, err := b.schema(f.Type, use)
		if err != nil {
			return fmt.Errorf("%s.%s: %w", t.Name(), f.Name, err)
		}
		props[name] = s
		*required = slices.DeleteFunc(*required, func(r string) bool { return r == name })
		isRequired := !strings.Contains(opts, "omitempty")
		if use == useRequest {
			isRequired = f.Tag.Get("doc") == "required"
		}
		if isRequired {
			*required = append(*required, name)
		}
	}
	return nil
}
