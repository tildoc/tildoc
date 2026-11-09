package s3

import (
	"context"
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tildoc/tildoc/internal/auth"
	"github.com/tildoc/tildoc/internal/blob"
	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/vfs"
)

const xmlns = "http://s3.amazonaws.com/doc/2006-03-01/"

// metaContentType stores the client supplied Content-Type in version meta.
// ':' cannot occur in header names, so it never clashes with x-amz-meta-*.
const metaContentType = "tildoc:content-type"

// Server is the S3 gateway.
type Server struct {
	Svc            *core.Service
	FS             *vfs.FS
	Log            *slog.Logger
	Region         string
	TrustedProxies []netip.Prefix
	Limiter        *auth.Limiter
	Now            func() time.Time
}

// New creates an S3 gateway.
func New(svc *core.Service, fs *vfs.FS, log *slog.Logger, region string, trusted []netip.Prefix) *Server {
	return &Server{Svc: svc, FS: fs, Log: log, Region: region, TrustedProxies: trusted,
		Limiter: auth.NewLimiter(30, 15*time.Minute), Now: time.Now}
}

type ctxKey int

const principalKey ctxKey = 1

type reqState struct {
	p       *core.Principal
	payload string
	ai      *authInfo
	secret  string
}

func state(r *http.Request) *reqState { return r.Context().Value(principalKey).(*reqState) }

func (s *Server) clientIP(r *http.Request) string {
	host := r.RemoteAddr
	if h, _, err := splitHostPort(host); err == nil {
		host = h
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return host
	}
	for _, pfx := range s.TrustedProxies {
		if pfx.Contains(addr.Unmap()) {
			if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
				parts := strings.Split(xff, ",")
				return strings.TrimSpace(parts[len(parts)-1])
			}
		}
	}
	return addr.String()
}

func splitHostPort(hp string) (string, string, error) {
	i := strings.LastIndex(hp, ":")
	if i < 0 {
		return hp, "", errors.New("no port")
	}
	return strings.Trim(hp[:i], "[]"), hp[i+1:], nil
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if v := recover(); v != nil {
			if v == http.ErrAbortHandler {
				panic(v)
			}
			s.Log.Error("s3 panic", "panic", v, "path", r.URL.Path)
			s.writeErr(w, r, &Error{Status: 500, Code: "InternalError", Message: "internal error"})
		}
	}()
	w.Header().Set("x-amz-request-id", strconv.FormatInt(time.Now().UnixNano(), 36))
	w.Header().Set("Server", "tildoc")
	ip := s.clientIP(r)
	if !s.Limiter.Allowed("ip:" + ip) {
		s.writeErr(w, r, &Error{Status: 503, Code: "SlowDown", Message: "too many failed authentication attempts"})
		return
	}
	ai, aerr := parseAuth(r)
	if aerr != nil {
		s.Limiter.Fail("ip:" + ip)
		s.writeErr(w, r, aerr)
		return
	}
	secret, p, err := s.Svc.AccessKeySecret(r.Context(), ai.Cred.AccessKey)
	if err != nil {
		s.Limiter.Fail("ip:" + ip)
		if core.KindOf(err) == core.ErrUnauthorized {
			s.writeErr(w, r, &Error{Status: 403, Code: "InvalidAccessKeyId", Message: "the access key id you provided does not exist"})
		} else {
			s.writeErr(w, r, toS3(err, false))
		}
		return
	}
	payload, verr := ai.verify(r, secret, s.Now())
	if verr != nil {
		s.Limiter.Fail("ip:" + ip)
		s.writeErr(w, r, verr)
		return
	}
	s.Svc.TouchAccessKey(r.Context(), ai.Cred.AccessKey)
	ctx := context.WithValue(r.Context(), principalKey, &reqState{p: p, payload: payload, ai: ai, secret: secret})
	r = r.WithContext(ctx)

	bucket, key, err := splitBucketKey(rawPath(r))
	if err != nil {
		s.writeErr(w, r, errInvalidArg(err.Error()))
		return
	}
	var herr error
	switch {
	case bucket == "":
		if r.Method == http.MethodGet {
			herr = s.listBuckets(w, r)
		} else {
			herr = &Error{Status: 405, Code: "MethodNotAllowed", Message: "method not allowed"}
		}
	case key == "":
		herr = s.bucketOp(w, r, bucket)
	default:
		herr = s.objectOp(w, r, bucket, key)
	}
	if herr != nil {
		se := toS3(herr, key != "")
		if se.Status == 500 {
			s.Log.Error("s3 request failed", "method", r.Method, "path", r.URL.Path, "err", herr)
		}
		s.writeErr(w, r, se)
	}
}

// splitBucketKey decodes /bucket/key from the raw path.
func splitBucketKey(raw string) (string, string, error) {
	raw = strings.TrimPrefix(raw, "/")
	b, k, _ := strings.Cut(raw, "/")
	bucket, err := url.PathUnescape(b)
	if err != nil {
		return "", "", err
	}
	key, err := url.PathUnescape(k)
	if err != nil {
		return "", "", err
	}
	return bucket, key, nil
}

func (s *Server) writeErr(w http.ResponseWriter, r *http.Request, e *Error) {
	if r.Method == http.MethodHead {
		w.WriteHeader(e.Status)
		return
	}
	writeXML(w, e.Status, errorBody{Code: e.Code, Message: e.Message, Resource: r.URL.Path, RequestID: w.Header().Get("x-amz-request-id")}) //nolint:errcheck // the client is gone
}

func writeXML(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	io.WriteString(w, xml.Header)
	return xml.NewEncoder(w).Encode(v)
}

func vpath(bucket, key string) string { return "/" + bucket + "/" + key }

// validKey rejects keys the folder model cannot represent.
func validKey(key string) error {
	k := strings.TrimSuffix(key, "/")
	if k == "" || strings.HasPrefix(k, "/") || strings.Contains(k, "//") {
		return errInvalidArg("object keys must not be empty or contain empty path segments")
	}
	if len(key) > 1024 {
		return &Error{Status: 400, Code: "KeyTooLongError", Message: "your key is too long"}
	}
	for _, seg := range strings.Split(k, "/") {
		if _, err := core.NormalizeName(seg); err != nil {
			return errInvalidArg(err.Error())
		}
	}
	return nil
}

// ---------------------------------------------------------------- service / bucket

type owner struct {
	ID          string `xml:"ID"`
	DisplayName string `xml:"DisplayName"`
}

type bucketEntry struct {
	Name         string `xml:"Name"`
	CreationDate string `xml:"CreationDate"`
}

type listAllMyBucketsResult struct {
	XMLName xml.Name      `xml:"ListAllMyBucketsResult"`
	Xmlns   string        `xml:"xmlns,attr"`
	Owner   owner         `xml:"Owner"`
	Buckets []bucketEntry `xml:"Buckets>Bucket"`
}

func isoTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func (s *Server) listBuckets(w http.ResponseWriter, r *http.Request) error {
	st := state(r)
	spaces, err := s.Svc.ListSpaces(r.Context(), st.p)
	if err != nil {
		return err
	}
	res := listAllMyBucketsResult{Xmlns: xmlns, Owner: owner{ID: st.p.UserID, DisplayName: st.p.Username}}
	for _, sp := range spaces {
		res.Buckets = append(res.Buckets, bucketEntry{Name: sp.Slug, CreationDate: isoTime(sp.CreatedAt)})
	}
	return writeXML(w, 200, res)
}

func (s *Server) space(r *http.Request, bucket string) (*core.Space, error) {
	sp, err := s.Svc.GetSpace(r.Context(), state(r).p, bucket)
	if err != nil {
		if core.KindOf(err) == core.ErrNotFound {
			return nil, errNoSuchBucket()
		}
		return nil, err
	}
	return sp, nil
}

func (s *Server) bucketOp(w http.ResponseWriter, r *http.Request, bucket string) error {
	q := r.URL.Query()
	switch r.Method {
	case http.MethodHead:
		if _, err := s.space(r, bucket); err != nil {
			return err
		}
		w.WriteHeader(200)
		return nil
	case http.MethodPut:
		// Spaces are created in the UI. Creating an existing own bucket succeeds
		// (us-east-1 semantics, clients like rclone rely on it).
		if _, err := s.space(r, bucket); err != nil {
			if toS3(err, false).Code == "NoSuchBucket" {
				return errAccessDenied("buckets (spaces) are created in the tildoc web UI")
			}
			return err
		}
		w.Header().Set("Location", "/"+bucket)
		w.WriteHeader(200)
		return nil
	case http.MethodDelete:
		return errAccessDenied("spaces are deleted in the tildoc web UI")
	case http.MethodPost:
		if _, ok := q["delete"]; ok {
			return s.deleteObjects(w, r, bucket)
		}
	case http.MethodGet:
		sp, err := s.space(r, bucket)
		if err != nil {
			return err
		}
		switch {
		case has(q, "location"):
			return writeXML(w, 200, struct {
				XMLName xml.Name `xml:"LocationConstraint"`
				Xmlns   string   `xml:"xmlns,attr"`
				Value   string   `xml:",chardata"`
			}{Xmlns: xmlns, Value: s.Region})
		case has(q, "versioning"):
			return writeXML(w, 200, struct {
				XMLName xml.Name `xml:"VersioningConfiguration"`
				Xmlns   string   `xml:"xmlns,attr"`
			}{Xmlns: xmlns})
		case has(q, "uploads"):
			return s.listUploads(w, r, sp)
		case has(q, "acl"):
			return s.acl(w, r)
		case has(q, "policy"), has(q, "lifecycle"), has(q, "cors"), has(q, "tagging"), has(q, "encryption"), has(q, "object-lock"):
			return &Error{Status: 404, Code: "NoSuchConfiguration", Message: "the specified configuration does not exist"}
		}
		return s.listObjects(w, r, sp)
	}
	return errNotImplemented()
}

func has(q url.Values, k string) bool { _, ok := q[k]; return ok }

func (s *Server) acl(w http.ResponseWriter, r *http.Request) error {
	st := state(r)
	type grantee struct {
		XMLName     xml.Name `xml:"Grantee"`
		XSI         string   `xml:"xmlns:xsi,attr"`
		Type        string   `xml:"xsi:type,attr"`
		ID          string   `xml:"ID"`
		DisplayName string   `xml:"DisplayName"`
	}
	type grant struct {
		Grantee    grantee `xml:"Grantee"`
		Permission string  `xml:"Permission"`
	}
	return writeXML(w, 200, struct {
		XMLName xml.Name `xml:"AccessControlPolicy"`
		Xmlns   string   `xml:"xmlns,attr"`
		Owner   owner    `xml:"Owner"`
		Grants  []grant  `xml:"AccessControlList>Grant"`
	}{Xmlns: xmlns, Owner: owner{ID: st.p.UserID, DisplayName: st.p.Username},
		Grants: []grant{{Grantee: grantee{XSI: "http://www.w3.org/2001/XMLSchema-instance", Type: "CanonicalUser",
			ID: st.p.UserID, DisplayName: st.p.Username}, Permission: "FULL_CONTROL"}}})
}

// ---------------------------------------------------------------- listing

type object struct {
	Key          string `xml:"Key"`
	LastModified string `xml:"LastModified"`
	ETag         string `xml:"ETag"`
	Size         int64  `xml:"Size"`
	StorageClass string `xml:"StorageClass"`
	Owner        *owner `xml:"Owner,omitempty"`
}

type commonPrefix struct {
	Prefix string `xml:"Prefix"`
}

type listBucketResult struct {
	XMLName               xml.Name       `xml:"ListBucketResult"`
	Xmlns                 string         `xml:"xmlns,attr"`
	Name                  string         `xml:"Name"`
	Prefix                string         `xml:"Prefix"`
	Marker                *string        `xml:"Marker,omitempty"`
	NextMarker            string         `xml:"NextMarker,omitempty"`
	StartAfter            string         `xml:"StartAfter,omitempty"`
	ContinuationToken     string         `xml:"ContinuationToken,omitempty"`
	NextContinuationToken string         `xml:"NextContinuationToken,omitempty"`
	KeyCount              *int           `xml:"KeyCount,omitempty"`
	MaxKeys               int            `xml:"MaxKeys"`
	Delimiter             string         `xml:"Delimiter,omitempty"`
	EncodingType          string         `xml:"EncodingType,omitempty"`
	IsTruncated           bool           `xml:"IsTruncated"`
	Contents              []object       `xml:"Contents"`
	CommonPrefixes        []commonPrefix `xml:"CommonPrefixes"`
}

type listEntry struct {
	key    string
	prefix bool // common prefix
	obj    object
}

func (s *Server) listObjects(w http.ResponseWriter, r *http.Request, sp *core.Space) error {
	q := r.URL.Query()
	v2 := q.Get("list-type") == "2"
	prefix, delim := q.Get("prefix"), q.Get("delimiter")
	maxKeys := 1000
	if v := q.Get("max-keys"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return errInvalidArg("invalid max-keys")
		}
		if n < maxKeys {
			maxKeys = n
		}
	}
	after := q.Get("marker")
	if v2 {
		after = q.Get("start-after")
		if tok := q.Get("continuation-token"); tok != "" {
			b, err := base64.RawURLEncoding.DecodeString(tok)
			if err != nil {
				return errInvalidArg("invalid continuation token")
			}
			after = string(b)
		}
	}
	fetchOwner := !v2 || q.Get("fetch-owner") == "true"
	st := state(r)
	own := &owner{ID: st.p.UserID, DisplayName: st.p.Username}

	entries, truncated, err := s.collect(r.Context(), st.p, sp, prefix, delim, after, maxKeys)
	if err != nil {
		return err
	}
	urlEnc := q.Get("encoding-type") == "url"
	enc := func(s string) string {
		if urlEnc {
			return awsEncode(s, true)
		}
		return s
	}
	res := listBucketResult{Xmlns: xmlns, Name: sp.Slug, Prefix: enc(prefix), MaxKeys: maxKeys, Delimiter: enc(delim),
		IsTruncated: truncated, Contents: []object{}, CommonPrefixes: []commonPrefix{}}
	if urlEnc {
		res.EncodingType = "url"
	}
	for _, e := range entries {
		if e.prefix {
			res.CommonPrefixes = append(res.CommonPrefixes, commonPrefix{Prefix: enc(e.key)})
			continue
		}
		o := e.obj
		o.Key = enc(o.Key)
		if fetchOwner {
			o.Owner = own
		}
		res.Contents = append(res.Contents, o)
	}
	last := ""
	if len(entries) > 0 {
		last = entries[len(entries)-1].key
	}
	if v2 {
		n := len(entries)
		res.KeyCount = &n
		res.ContinuationToken = q.Get("continuation-token")
		res.StartAfter = enc(q.Get("start-after"))
		if truncated {
			res.NextContinuationToken = base64.RawURLEncoding.EncodeToString([]byte(last))
		}
	} else {
		m := enc(q.Get("marker"))
		res.Marker = &m
		if truncated {
			res.NextMarker = enc(last)
		}
	}
	return writeXML(w, 200, res)
}

// collect produces up to max listing entries (objects + common prefixes)
// after the given key, in S3 order.
func (s *Server) collect(ctx context.Context, p *core.Principal, sp *core.Space, prefix, delim, after string, max int) ([]listEntry, bool, error) {
	dirPart := ""
	if i := strings.LastIndex(prefix, "/"); i >= 0 {
		dirPart = prefix[:i+1]
	}
	namePrefix := prefix[len(dirPart):]
	segs, err := core.SplitPath(dirPart)
	if err != nil || strings.Contains(dirPart, "//") {
		return nil, false, nil
	}
	folder, err := s.Svc.FolderByPath(ctx, p, sp.ID, segs)
	if err != nil {
		if core.KindOf(err) == core.ErrNotFound {
			return nil, false, nil
		}
		return nil, false, err
	}
	if max == 0 {
		return nil, false, nil
	}
	if delim == "/" {
		_, folders, files, err := s.Svc.ListFolder(ctx, p, folder.ID)
		if err != nil {
			return nil, false, err
		}
		var all []listEntry
		for _, f := range folders {
			if strings.HasPrefix(f.Name, namePrefix) {
				all = append(all, listEntry{key: dirPart + f.Name + "/", prefix: true})
			}
		}
		for _, f := range files {
			if strings.HasPrefix(f.Name, namePrefix) {
				all = append(all, listEntry{key: dirPart + f.Name, obj: fileObject(dirPart+f.Name, &f)})
			}
		}
		sort.Slice(all, func(i, j int) bool { return all[i].key < all[j].key })
		var out []listEntry
		for _, e := range all {
			if e.key <= after {
				continue
			}
			if len(out) == max {
				return out, true, nil
			}
			out = append(out, e)
		}
		return out, false, nil
	}
	relAfter := ""
	switch {
	case strings.HasPrefix(after, dirPart):
		relAfter = after[len(dirPart):]
	case after > dirPart:
		return nil, false, nil
	}
	var out []listEntry
	seenPrefix := map[string]bool{}
	for {
		keys, err := s.Svc.ListKeys(ctx, p, sp.ID, folder.ID, namePrefix, relAfter, max+1)
		if err != nil {
			return nil, false, err
		}
		for _, k := range keys {
			full := dirPart + k.Key
			relAfter = k.Key
			if delim != "" {
				rest := full[len(prefix):]
				if i := strings.Index(rest, delim); i >= 0 {
					cp := prefix + rest[:i+len(delim)]
					if seenPrefix[cp] || cp <= after {
						continue
					}
					seenPrefix[cp] = true
					if len(out) == max {
						return out, true, nil
					}
					out = append(out, listEntry{key: cp, prefix: true})
					continue
				}
			}
			if len(out) == max {
				return out, true, nil
			}
			out = append(out, listEntry{key: full, obj: object{Key: full, LastModified: isoTime(k.ModTime),
				ETag: `"` + k.MD5 + `"`, Size: k.Size, StorageClass: "STANDARD"}})
			if k.IsDir {
				out[len(out)-1].obj.ETag = `"d41d8cd98f00b204e9800998ecf8427e"`
			}
		}
		if len(keys) <= max {
			return out, false, nil
		}
	}
}

func fileObject(key string, f *core.FileInfo) object {
	return object{Key: key, LastModified: isoTime(f.ModifiedAt), ETag: f.ETag(), Size: f.Size, StorageClass: "STANDARD"}
}

// ---------------------------------------------------------------- objects

func (s *Server) objectOp(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	q := r.URL.Query()
	if _, err := s.space(r, bucket); err != nil {
		return err
	}
	if err := validKey(key); err != nil {
		return err
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		switch {
		case q.Get("uploadId") != "":
			return s.listParts(w, r, bucket, key, q.Get("uploadId"))
		case has(q, "acl"):
			return s.acl(w, r)
		case has(q, "tagging"):
			return s.getTagging(w, r, bucket, key)
		}
		return s.getObject(w, r, bucket, key)
	case http.MethodPut:
		switch {
		case q.Get("uploadId") != "" && q.Get("partNumber") != "":
			if r.Header.Get("X-Amz-Copy-Source") != "" {
				return errNotImplemented()
			}
			return s.uploadPart(w, r, bucket, key, q.Get("uploadId"), q.Get("partNumber"))
		case has(q, "tagging"):
			return s.putTagging(w, r, bucket, key)
		case has(q, "acl"):
			w.WriteHeader(200)
			return nil
		case r.Header.Get("X-Amz-Copy-Source") != "":
			return s.copyObject(w, r, bucket, key)
		}
		return s.putObject(w, r, bucket, key)
	case http.MethodDelete:
		if id := q.Get("uploadId"); id != "" {
			return s.abortUpload(w, r, id)
		}
		if has(q, "tagging") {
			return s.deleteTagging(w, r, bucket, key)
		}
		return s.deleteObject(w, r, bucket, key)
	case http.MethodPost:
		switch {
		case has(q, "uploads"):
			return s.createUpload(w, r, bucket, key)
		case q.Get("uploadId") != "":
			return s.completeUpload(w, r, bucket, key, q.Get("uploadId"))
		}
	}
	return errNotImplemented()
}

func userMeta(r *http.Request) map[string]string {
	meta := map[string]string{}
	for k, vs := range r.Header {
		lk := strings.ToLower(k)
		if strings.HasPrefix(lk, "x-amz-meta-") && len(vs) > 0 {
			meta[strings.TrimPrefix(lk, "x-amz-meta-")] = vs[0]
		}
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && ct != "binary/octet-stream" {
		meta[metaContentType] = ct
	}
	return meta
}

func (s *Server) putObject(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	st := state(r)
	body, _, berr := bodyReader(r, st.payload, st.ai, st.secret)
	if berr != nil {
		return berr
	}
	if strings.HasSuffix(key, "/") {
		// directory marker
		n, _ := io.Copy(io.Discard, io.LimitReader(body, 1))
		if n > 0 {
			return errInvalidArg("directory marker objects must be empty")
		}
		if err := s.FS.MkdirAll(r.Context(), st.p, vpath(bucket, key)); err != nil {
			return err
		}
		w.Header().Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
		w.WriteHeader(200)
		return nil
	}
	n, err := s.FS.Put(r.Context(), st.p, vpath(bucket, key), body, core.PutOptions{Protocol: "s3", Meta: userMeta(r), CreateParents: true})
	if err != nil {
		return err
	}
	w.Header().Set("ETag", n.File.ETag())
	w.WriteHeader(200)
	return nil
}

func (s *Server) getObject(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	st := state(r)
	node, err := s.FS.Stat(r.Context(), st.p, vpath(bucket, key))
	if err != nil {
		return err
	}
	h := w.Header()
	if node.IsDir() {
		if !strings.HasSuffix(key, "/") {
			return errNoSuchKey()
		}
		h.Set("Content-Type", "application/x-directory")
		h.Set("ETag", `"d41d8cd98f00b204e9800998ecf8427e"`)
		h.Set("Last-Modified", node.ModTime.UTC().Format(http.TimeFormat))
		h.Set("Content-Length", "0")
		w.WriteHeader(200)
		return nil
	}
	if strings.HasSuffix(key, "/") {
		return errNoSuchKey()
	}
	f := node.File
	ctype := f.MIME
	for k, v := range f.Meta {
		if k == metaContentType {
			ctype = v
			continue
		}
		if strings.Contains(k, ":") {
			continue
		}
		h.Set("X-Amz-Meta-"+k, v)
	}
	links, err := s.linkPaths(r.Context(), st.p, f.DocumentID, bucket)
	if err == nil && len(links) > 0 {
		h.Set("X-Amz-Meta-Tildoc-Links", strings.Join(links, ","))
	}
	h.Set("X-Amz-Meta-Tildoc-Document", f.DocumentID)
	h.Set("X-Amz-Meta-Tildoc-Version", strconv.Itoa(f.Version))
	h.Set("Content-Type", ctype)
	h.Set("ETag", f.ETag())
	h.Set("Accept-Ranges", "bytes")
	h.Set("X-Content-Type-Options", "nosniff")
	if v := r.URL.Query().Get("response-content-disposition"); v != "" {
		h.Set("Content-Disposition", v)
	}
	rd, err := s.Svc.OpenFile(r.Context(), st.p, f)
	if err != nil {
		return err
	}
	defer rd.Close()
	http.ServeContent(w, r, "", f.ModifiedAt, rd)
	return nil
}

func (s *Server) linkPaths(ctx context.Context, p *core.Principal, docID, bucket string) ([]string, error) {
	d, err := s.Svc.GetDocument(ctx, p, docID)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, l := range d.Links {
		out = append(out, awsEncode(strings.TrimPrefix(l.Path, "/"), true))
	}
	return out, nil
}

func (s *Server) deleteObject(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	st := state(r)
	if err := s.deleteKey(r.Context(), st.p, bucket, key); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// deleteKey deletes an object; missing keys succeed (S3 semantics). A
// directory marker removes the folder only when it is empty.
func (s *Server) deleteKey(ctx context.Context, p *core.Principal, bucket, key string) error {
	path := vpath(bucket, key)
	node, err := s.FS.Stat(ctx, p, path)
	if err != nil {
		if core.KindOf(err) == core.ErrNotFound {
			return nil
		}
		return err
	}
	if node.IsDir() {
		if !strings.HasSuffix(key, "/") {
			return nil
		}
		err := s.FS.RemoveEmptyDir(ctx, p, path)
		if core.KindOf(err) == core.ErrNotEmpty {
			return nil
		}
		return err
	}
	if strings.HasSuffix(key, "/") {
		return nil
	}
	return s.FS.Remove(ctx, p, path)
}

type deleteRequest struct {
	Quiet   bool `xml:"Quiet"`
	Objects []struct {
		Key string `xml:"Key"`
	} `xml:"Object"`
}

type deletedEntry struct {
	Key string `xml:"Key"`
}

type deleteError struct {
	Key     string `xml:"Key"`
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func (s *Server) deleteObjects(w http.ResponseWriter, r *http.Request, bucket string) error {
	st := state(r)
	if _, err := s.space(r, bucket); err != nil {
		return err
	}
	body, _, berr := bodyReader(r, st.payload, st.ai, st.secret)
	if berr != nil {
		return berr
	}
	var req deleteRequest
	if err := xml.NewDecoder(io.LimitReader(body, 2<<20)).Decode(&req); err != nil {
		return errMalformedXML()
	}
	if len(req.Objects) > 1000 {
		return errMalformedXML()
	}
	res := struct {
		XMLName xml.Name       `xml:"DeleteResult"`
		Xmlns   string         `xml:"xmlns,attr"`
		Deleted []deletedEntry `xml:"Deleted"`
		Errors  []deleteError  `xml:"Error"`
	}{Xmlns: xmlns}
	for _, o := range req.Objects {
		err := validKey(o.Key)
		if err == nil {
			err = s.deleteKey(r.Context(), st.p, bucket, o.Key)
		}
		if err != nil {
			se := toS3(err, true)
			res.Errors = append(res.Errors, deleteError{Key: o.Key, Code: se.Code, Message: se.Message})
			continue
		}
		if !req.Quiet {
			res.Deleted = append(res.Deleted, deletedEntry{Key: o.Key})
		}
	}
	return writeXML(w, 200, res)
}

func (s *Server) copyObject(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	st := state(r)
	src := r.Header.Get("X-Amz-Copy-Source")
	if i := strings.Index(src, "?"); i >= 0 {
		src = src[:i]
	}
	src, err := url.PathUnescape(strings.TrimPrefix(src, "/"))
	if err != nil {
		return errInvalidArg("invalid x-amz-copy-source")
	}
	sb, sk, _ := strings.Cut(src, "/")
	if sb == "" || sk == "" || validKey(sk) != nil || strings.HasSuffix(sk, "/") || strings.HasSuffix(key, "/") {
		return errInvalidArg("invalid x-amz-copy-source")
	}
	if _, err := s.space(r, sb); err != nil {
		return err
	}
	from, to := vpath(sb, sk), vpath(bucket, key)
	if _, err := s.FS.Stat(r.Context(), st.p, from); err != nil {
		return err
	}
	if from != to {
		if err := s.FS.MkdirAll(r.Context(), st.p, to[:strings.LastIndex(to, "/")]); err != nil {
			return err
		}
		if err := s.FS.Copy(r.Context(), st.p, from, to, true); err != nil {
			return err
		}
	}
	node, err := s.FS.Stat(r.Context(), st.p, to)
	if err != nil {
		return err
	}
	if strings.EqualFold(r.Header.Get("X-Amz-Metadata-Directive"), "REPLACE") {
		if err := s.Svc.SetFileMeta(r.Context(), st.p, node.File.LinkID, userMeta(r)); err != nil {
			return err
		}
	} else if from == to {
		return &Error{Status: 400, Code: "InvalidRequest", Message: "copying an object to itself requires the REPLACE metadata directive"}
	}
	return writeXML(w, 200, struct {
		XMLName      xml.Name `xml:"CopyObjectResult"`
		Xmlns        string   `xml:"xmlns,attr"`
		LastModified string   `xml:"LastModified"`
		ETag         string   `xml:"ETag"`
	}{Xmlns: xmlns, LastModified: isoTime(node.ModTime), ETag: node.File.ETag()})
}

// ---------------------------------------------------------------- multipart

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	st := state(r)
	sp, err := s.space(r, bucket)
	if err != nil {
		return err
	}
	if strings.HasSuffix(key, "/") {
		return errInvalidArg("cannot upload to a directory key")
	}
	u, err := s.Svc.CreateUpload(r.Context(), st.p, sp.ID, key, userMeta(r))
	if err != nil {
		return err
	}
	return writeXML(w, 200, struct {
		XMLName  xml.Name `xml:"InitiateMultipartUploadResult"`
		Xmlns    string   `xml:"xmlns,attr"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		UploadID string   `xml:"UploadId"`
	}{Xmlns: xmlns, Bucket: bucket, Key: key, UploadID: u.ID})
}

func (s *Server) upload(r *http.Request, bucket, key, id string) (*core.Upload, error) {
	u, err := s.Svc.GetUpload(r.Context(), state(r).p, id)
	if err != nil {
		if core.KindOf(err) == core.ErrNotFound {
			return nil, errNoSuchUpload()
		}
		return nil, err
	}
	sp, err := s.space(r, bucket)
	if err != nil {
		return nil, err
	}
	if u.SpaceID != sp.ID || u.Key != key {
		return nil, errNoSuchUpload()
	}
	return u, nil
}

func (s *Server) uploadPart(w http.ResponseWriter, r *http.Request, bucket, key, id, num string) error {
	st := state(r)
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 || n > 10000 {
		return errInvalidArg("part number must be 1-10000")
	}
	u, err := s.upload(r, bucket, key, id)
	if err != nil {
		return err
	}
	body, _, berr := bodyReader(r, st.payload, st.ai, st.secret)
	if berr != nil {
		return berr
	}
	tmp, err := s.Svc.Blobs.TempFile("mpu-part-*")
	if err != nil {
		return err
	}
	h := md5.New()
	size, cerr := io.Copy(io.MultiWriter(tmp, h), body)
	closeErr := tmp.Close()
	if cerr != nil || closeErr != nil {
		os.Remove(tmp.Name())
		if cerr != nil {
			return cerr
		}
		return closeErr
	}
	final := s.Svc.PartPath(u.ID, n)
	if err := os.Rename(tmp.Name(), final); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	sum := h.Sum(nil)
	if err := s.Svc.RecordPart(r.Context(), u.ID, n, size, sum, final); err != nil {
		return err
	}
	w.Header().Set("ETag", `"`+hex.EncodeToString(sum)+`"`)
	w.WriteHeader(200)
	return nil
}

type completeRequest struct {
	Parts []struct {
		PartNumber int    `xml:"PartNumber"`
		ETag       string `xml:"ETag"`
	} `xml:"Part"`
}

func (s *Server) completeUpload(w http.ResponseWriter, r *http.Request, bucket, key, id string) error {
	st := state(r)
	u, err := s.upload(r, bucket, key, id)
	if err != nil {
		return err
	}
	body, _, berr := bodyReader(r, st.payload, st.ai, st.secret)
	if berr != nil {
		return berr
	}
	var req completeRequest
	if err := xml.NewDecoder(io.LimitReader(body, 4<<20)).Decode(&req); err != nil || len(req.Parts) == 0 {
		return errMalformedXML()
	}
	parts, err := s.Svc.ListParts(r.Context(), u.ID)
	if err != nil {
		return err
	}
	byNum := map[int]core.Part{}
	for _, pt := range parts {
		byNum[pt.Number] = pt
	}
	var readers []io.Reader
	var files []*os.File
	defer func() {
		for _, f := range files {
			f.Close()
		}
	}()
	etagMD5 := md5.New()
	prev := 0
	for _, rp := range req.Parts {
		pt, ok := byNum[rp.PartNumber]
		if rp.PartNumber <= prev {
			return &Error{Status: 400, Code: "InvalidPartOrder", Message: "the list of parts was not in ascending order"}
		}
		prev = rp.PartNumber
		if !ok || strings.Trim(rp.ETag, `"`) != pt.MD5 {
			return &Error{Status: 400, Code: "InvalidPart", Message: "one or more of the specified parts could not be found"}
		}
		f, err := os.Open(pt.Path)
		if err != nil {
			return &Error{Status: 400, Code: "InvalidPart", Message: "part data missing"}
		}
		files = append(files, f)
		readers = append(readers, f)
		raw, _ := hex.DecodeString(pt.MD5)
		etagMD5.Write(raw)
	}
	staged, err := s.Svc.Blobs.Stage(io.MultiReader(readers...), s.Svc.Cfg.MaxUploadBytes)
	if err != nil {
		if errors.Is(err, blob.ErrTooLarge) {
			return &Error{Status: 400, Code: "EntityTooLarge", Message: "object exceeds the upload limit"}
		}
		return err
	}
	if _, err := s.FS.PutStaged(r.Context(), st.p, vpath(bucket, key), staged, core.PutOptions{Protocol: "s3", Meta: u.Meta, CreateParents: true}); err != nil {
		return err
	}
	s.Svc.DeleteUpload(r.Context(), u.ID) //nolint:errcheck // the upload is complete; cleanup also runs periodically
	etag := `"` + hex.EncodeToString(etagMD5.Sum(nil)) + "-" + strconv.Itoa(len(req.Parts)) + `"`
	return writeXML(w, 200, struct {
		XMLName  xml.Name `xml:"CompleteMultipartUploadResult"`
		Xmlns    string   `xml:"xmlns,attr"`
		Location string   `xml:"Location"`
		Bucket   string   `xml:"Bucket"`
		Key      string   `xml:"Key"`
		ETag     string   `xml:"ETag"`
	}{Xmlns: xmlns, Location: "/" + bucket + "/" + awsEncode(key, true), Bucket: bucket, Key: key, ETag: etag})
}

func (s *Server) abortUpload(w http.ResponseWriter, r *http.Request, id string) error {
	u, err := s.Svc.GetUpload(r.Context(), state(r).p, id)
	if err != nil {
		return errNoSuchUpload()
	}
	if err := s.Svc.DeleteUpload(r.Context(), u.ID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listParts(w http.ResponseWriter, r *http.Request, bucket, key, id string) error {
	u, err := s.upload(r, bucket, key, id)
	if err != nil {
		return err
	}
	parts, err := s.Svc.ListParts(r.Context(), u.ID)
	if err != nil {
		return err
	}
	type part struct {
		PartNumber   int    `xml:"PartNumber"`
		LastModified string `xml:"LastModified"`
		ETag         string `xml:"ETag"`
		Size         int64  `xml:"Size"`
	}
	res := struct {
		XMLName     xml.Name `xml:"ListPartsResult"`
		Xmlns       string   `xml:"xmlns,attr"`
		Bucket      string   `xml:"Bucket"`
		Key         string   `xml:"Key"`
		UploadID    string   `xml:"UploadId"`
		MaxParts    int      `xml:"MaxParts"`
		IsTruncated bool     `xml:"IsTruncated"`
		Parts       []part   `xml:"Part"`
	}{Xmlns: xmlns, Bucket: bucket, Key: key, UploadID: u.ID, MaxParts: 10000}
	for _, pt := range parts {
		res.Parts = append(res.Parts, part{PartNumber: pt.Number, LastModified: isoTime(pt.CreatedAt), ETag: `"` + pt.MD5 + `"`, Size: pt.Size})
	}
	return writeXML(w, 200, res)
}

func (s *Server) listUploads(w http.ResponseWriter, r *http.Request, sp *core.Space) error {
	ups, err := s.Svc.ListUploads(r.Context(), state(r).p, sp.ID)
	if err != nil {
		return err
	}
	type upload struct {
		Key       string `xml:"Key"`
		UploadID  string `xml:"UploadId"`
		Initiated string `xml:"Initiated"`
	}
	res := struct {
		XMLName     xml.Name `xml:"ListMultipartUploadsResult"`
		Xmlns       string   `xml:"xmlns,attr"`
		Bucket      string   `xml:"Bucket"`
		IsTruncated bool     `xml:"IsTruncated"`
		Uploads     []upload `xml:"Upload"`
	}{Xmlns: xmlns, Bucket: sp.Slug}
	prefix := r.URL.Query().Get("prefix")
	for _, u := range ups {
		if strings.HasPrefix(u.Key, prefix) {
			res.Uploads = append(res.Uploads, upload{Key: u.Key, UploadID: u.ID, Initiated: isoTime(u.CreatedAt)})
		}
	}
	return writeXML(w, 200, res)
}
