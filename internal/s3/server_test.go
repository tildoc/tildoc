package s3_test

import (
	"bytes"
	"context"
	md5pkg "crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/s3"
	"github.com/tildoc/tildoc/internal/testutil"
	"github.com/tildoc/tildoc/internal/vfs"
)

type s3client struct {
	t          *testing.T
	base       string
	ak, secret string
}

func setup(t *testing.T) (*s3client, *testutil.Env, *core.Principal) {
	env := testutil.New(t)
	ctx := context.Background()
	if _, err := env.Svc.SetupAdmin(ctx, "alice", "alicepass", ""); err != nil {
		t.Fatal(err)
	}
	p, _ := env.Svc.Authenticate(ctx, "alice", "alicepass")
	p.Via = "session"
	key, secret, err := env.Svc.CreateAccessKey(ctx, p, "s3", "", false)
	if err != nil {
		t.Fatal(err)
	}
	srv := s3.New(env.Svc, vfs.New(env.Svc), slog.New(slog.NewTextHandler(io.Discard, nil)), "us-east-1", nil)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &s3client{t: t, base: ts.URL, ak: key.ID, secret: secret}, env, p
}

func hashHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (c *s3client) req(method, path string, body []byte, hdr map[string]string) (*http.Response, []byte) {
	c.t.Helper()
	r, _ := http.NewRequest(method, c.base+path, bytes.NewReader(body))
	if body == nil {
		r.Body = http.NoBody
		r.ContentLength = 0
	}
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	payload := hashHex(body)
	if v, ok := hdr["X-Amz-Content-Sha256"]; ok {
		payload = v
	}
	s3.SignRequest(r, c.ak, c.secret, "us-east-1", payload, time.Now())
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func (c *s3client) must(method, path string, body []byte, hdr map[string]string, want int) []byte {
	c.t.Helper()
	resp, data := c.req(method, path, body, hdr)
	if resp.StatusCode != want {
		c.t.Fatalf("%s %s: %d (want %d): %s", method, path, resp.StatusCode, want, data)
	}
	return data
}

type listResult struct {
	Contents []struct {
		Key  string
		Size int64
		ETag string
	}
	CommonPrefixes []struct{ Prefix string }
	IsTruncated    bool
	NextMarker     string
	NextContinuationToken string
	KeyCount       int
}

func (c *s3client) list(path string) listResult {
	c.t.Helper()
	var lr listResult
	if err := xml.Unmarshal(c.must("GET", path, nil, nil, 200), &lr); err != nil {
		c.t.Fatal(err)
	}
	return lr
}

func keys(lr listResult) string {
	var out []string
	for _, p := range lr.CommonPrefixes {
		out = append(out, p.Prefix)
	}
	for _, o := range lr.Contents {
		out = append(out, o.Key)
	}
	return strings.Join(out, ",")
}

func TestS3Basic(t *testing.T) {
	c, env, p := setup(t)

	// list buckets
	data := c.must("GET", "/", nil, nil, 200)
	if !strings.Contains(string(data), "<Name>alice</Name>") {
		t.Fatalf("buckets: %s", data)
	}
	c.must("HEAD", "/alice", nil, nil, 200)
	c.must("HEAD", "/nope", nil, nil, 404)
	c.must("PUT", "/alice", nil, nil, 200)       // existing own bucket
	c.must("PUT", "/new-bucket", nil, nil, 403) // creation via UI only

	// put / get / head with metadata and range
	body := []byte("hello s3 world")
	resp, _ := c.req("PUT", "/alice/docs/2024/hello.txt", body, map[string]string{"X-Amz-Meta-Mtime": "1700000000.5", "Content-Type": "text/plain"})
	if resp.StatusCode != 200 || resp.Header.Get("ETag") != `"`+fmt.Sprintf("%x", md5sum(body))+`"` {
		t.Fatalf("put: %d %s", resp.StatusCode, resp.Header.Get("ETag"))
	}
	resp, got := c.req("GET", "/alice/docs/2024/hello.txt", nil, nil)
	if string(got) != string(body) || resp.Header.Get("X-Amz-Meta-Mtime") != "1700000000.5" ||
		!strings.Contains(resp.Header.Get("X-Amz-Meta-Tildoc-Links"), "docs/2024/hello.txt") {
		t.Fatalf("get: %q %v", got, resp.Header)
	}
	resp, got = c.req("GET", "/alice/docs/2024/hello.txt", nil, map[string]string{"Range": "bytes=6-7"})
	if resp.StatusCode != 206 || string(got) != "s3" {
		t.Fatalf("range: %d %q", resp.StatusCode, got)
	}
	c.must("GET", "/alice/docs/2024/missing.txt", nil, nil, 404)

	// signed payload mismatch is rejected and nothing is stored
	r, _ := http.NewRequest("PUT", c.base+"/alice/bad.txt", strings.NewReader("tampered"))
	s3.SignRequest(r, c.ak, c.secret, "us-east-1", hashHex([]byte("original")), time.Now())
	bad, _ := http.DefaultClient.Do(r)
	bad.Body.Close()
	if bad.StatusCode != 400 {
		t.Fatalf("payload mismatch: %d", bad.StatusCode)
	}
	c.must("HEAD", "/alice/bad.txt", nil, nil, 404)

	// wrong secret
	c2 := *c
	c2.secret = "wrong"
	c2.must("GET", "/", nil, nil, 403)

	// directory marker
	c.must("PUT", "/alice/empty/", nil, nil, 200)
	c.must("PUT", "/alice/docs/b.txt", []byte("b"), nil, 200)
	c.must("PUT", "/alice/docs/a.txt", []byte("a"), nil, 200)

	// listing with delimiter (v2) and without (v1, recursive)
	lr := c.list("/alice?list-type=2&delimiter=%2F")
	if keys(lr) != "docs/,empty/" {
		t.Fatalf("delimiter root: %s", keys(lr))
	}
	lr = c.list("/alice?list-type=2&delimiter=%2F&prefix=docs%2F")
	if keys(lr) != "docs/2024/,docs/a.txt,docs/b.txt" {
		t.Fatalf("delimiter docs: %s", keys(lr))
	}
	lr = c.list("/alice")
	if keys(lr) != "docs/2024/hello.txt,docs/a.txt,docs/b.txt,empty/" {
		t.Fatalf("recursive: %s", keys(lr))
	}
	lr = c.list("/alice?prefix=docs%2Fa")
	if keys(lr) != "docs/a.txt" {
		t.Fatalf("prefix: %s", keys(lr))
	}
	// pagination v2
	lr = c.list("/alice?list-type=2&max-keys=2")
	if !lr.IsTruncated || lr.KeyCount != 2 || lr.NextContinuationToken == "" {
		t.Fatalf("page1: %+v", lr)
	}
	lr2 := c.list("/alice?list-type=2&max-keys=2&continuation-token=" + lr.NextContinuationToken)
	if keys(lr2) != "docs/b.txt,empty/" || lr2.IsTruncated {
		t.Fatalf("page2: %s %v", keys(lr2), lr2.IsTruncated)
	}
	// pagination v1 with delimiter
	lr = c.list("/alice?delimiter=%2F&prefix=docs%2F&max-keys=1")
	if keys(lr) != "docs/2024/" || lr.NextMarker != "docs/2024/" {
		t.Fatalf("v1 page: %+v", lr)
	}
	lr = c.list("/alice?delimiter=%2F&prefix=docs%2F&marker=docs%2F2024%2F")
	if keys(lr) != "docs/a.txt,docs/b.txt" {
		t.Fatalf("v1 marker: %s", keys(lr))
	}
	// url encoding-type
	c.must("PUT", "/alice/sp%20ace/%C3%BC%20x.txt", []byte("u"), nil, 200)
	data = c.must("GET", "/alice?encoding-type=url&prefix=sp", nil, nil, 200)
	if !strings.Contains(string(data), "<Key>sp%20ace/%C3%BC%20x.txt</Key>") {
		t.Fatalf("url encoding: %s", data)
	}

	// copy (same space → same document) + delete source = rename keeps identity
	c.must("PUT", "/alice/docs/c.txt", nil, map[string]string{"X-Amz-Copy-Source": "/alice/docs/a.txt"}, 200)
	ctx := context.Background()
	sp, _ := env.Svc.GetSpace(ctx, p, "alice")
	a, _ := env.Svc.FileByPath(ctx, p, sp.ID, []string{"docs", "a.txt"})
	cc, _ := env.Svc.FileByPath(ctx, p, sp.ID, []string{"docs", "c.txt"})
	if a.DocumentID != cc.DocumentID {
		t.Fatal("s3 copy must link the same document")
	}
	c.must("DELETE", "/alice/docs/a.txt", nil, nil, 204)
	c.must("DELETE", "/alice/docs/a.txt", nil, nil, 204) // idempotent
	if tr, _ := env.Svc.ListTrash(ctx, p, ""); len(tr) != 0 {
		t.Fatal("rename via copy+delete must not trash the document")
	}
	// metadata replace on self copy
	c.must("PUT", "/alice/docs/c.txt", nil, map[string]string{"X-Amz-Copy-Source": "/alice/docs/c.txt",
		"X-Amz-Metadata-Directive": "REPLACE", "X-Amz-Meta-Mtime": "42"}, 200)
	resp, _ = c.req("HEAD", "/alice/docs/c.txt", nil, nil)
	if resp.Header.Get("X-Amz-Meta-Mtime") != "42" {
		t.Fatalf("meta replace: %v", resp.Header)
	}

	// multi delete incl. directory marker of empty folder
	del := `<Delete><Object><Key>docs/b.txt</Key></Object><Object><Key>empty/</Key></Object><Object><Key>nope</Key></Object></Delete>`
	data = c.must("POST", "/alice?delete", []byte(del), nil, 200)
	if strings.Count(string(data), "<Deleted>") != 3 {
		t.Fatalf("multi delete: %s", data)
	}
	lr = c.list("/alice?list-type=2&delimiter=%2F")
	if keys(lr) != "docs/,sp ace/" {
		t.Fatalf("after delete: %s", keys(lr))
	}
}

func md5sum(b []byte) []byte {
	h := md5New()
	h.Write(b)
	return h.Sum(nil)
}

func TestS3ChunkedAndMultipart(t *testing.T) {
	c, _, _ := setup(t)
	// signed aws-chunked upload
	data := bytes.Repeat([]byte("0123456789"), 20000)
	now := time.Now()
	r, _ := http.NewRequest("PUT", c.base+"/alice/chunked.bin", nil)
	r.Header.Set("Content-Encoding", "aws-chunked")
	r.Header.Set("X-Amz-Decoded-Content-Length", fmt.Sprint(len(data)))
	s3.SignRequest(r, c.ak, c.secret, "us-east-1", "STREAMING-AWS4-HMAC-SHA256-PAYLOAD", now)
	enc := s3.EncodeChunked(data, 65536, s3.SeedSignature(r), c.secret, "us-east-1", r.Header.Get("X-Amz-Date"))
	r.Body = io.NopCloser(bytes.NewReader(enc))
	r.ContentLength = int64(len(enc))
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("chunked put: %d %s", resp.StatusCode, b)
	}
	if got := c.must("GET", "/alice/chunked.bin", nil, nil, 200); !bytes.Equal(got, data) {
		t.Fatal("chunked content mismatch")
	}
	// unsigned payload with crc32 trailer (current AWS SDK default)
	tr := s3.EncodeUnsignedTrailer(data, 8192)
	c.must("PUT", "/alice/trailer.bin", tr, map[string]string{"X-Amz-Content-Sha256": "STREAMING-UNSIGNED-PAYLOAD-TRAILER",
		"Content-Encoding": "aws-chunked", "X-Amz-Trailer": "x-amz-checksum-crc32", "X-Amz-Decoded-Content-Length": fmt.Sprint(len(data))}, 200)
	if got := c.must("GET", "/alice/trailer.bin", nil, nil, 200); !bytes.Equal(got, data) {
		t.Fatal("trailer content mismatch")
	}

	// multipart
	out := c.must("POST", "/alice/big/file.bin?uploads", nil, nil, 200)
	var init struct{ UploadId string }
	xml.Unmarshal(out, &init)
	p1 := bytes.Repeat([]byte("A"), 5<<20)
	p2 := []byte("tail")
	resp1, _ := c.req("PUT", "/alice/big/file.bin?partNumber=1&uploadId="+init.UploadId, p1, nil)
	resp2, _ := c.req("PUT", "/alice/big/file.bin?partNumber=2&uploadId="+init.UploadId, p2, nil)
	if resp1.StatusCode != 200 || resp2.StatusCode != 200 {
		t.Fatalf("parts: %d %d", resp1.StatusCode, resp2.StatusCode)
	}
	lp := c.must("GET", "/alice/big/file.bin?uploadId="+init.UploadId, nil, nil, 200)
	if strings.Count(string(lp), "<Part>") != 2 {
		t.Fatalf("list parts: %s", lp)
	}
	// wrong etag rejected
	badComplete := `<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>"00"</ETag></Part></CompleteMultipartUpload>`
	c.must("POST", "/alice/big/file.bin?uploadId="+init.UploadId, []byte(badComplete), nil, 400)
	complete := fmt.Sprintf(`<CompleteMultipartUpload><Part><PartNumber>1</PartNumber><ETag>%s</ETag></Part><Part><PartNumber>2</PartNumber><ETag>%s</ETag></Part></CompleteMultipartUpload>`,
		resp1.Header.Get("ETag"), resp2.Header.Get("ETag"))
	res := c.must("POST", "/alice/big/file.bin?uploadId="+init.UploadId, []byte(complete), nil, 200)
	if !strings.Contains(string(res), "-2&#34;</ETag>") && !strings.Contains(string(res), `-2"</ETag>`) {
		t.Fatalf("complete: %s", res)
	}
	got := c.must("GET", "/alice/big/file.bin", nil, nil, 200)
	if len(got) != len(p1)+len(p2) || !bytes.Equal(got[len(p1):], p2) {
		t.Fatalf("multipart content: %d", len(got))
	}
	c.must("GET", "/alice/big/file.bin?uploadId="+init.UploadId, nil, nil, 404)

	// abort
	out = c.must("POST", "/alice/x.bin?uploads", nil, nil, 200)
	xml.Unmarshal(out, &init)
	c.must("PUT", "/alice/x.bin?partNumber=1&uploadId="+init.UploadId, []byte("x"), nil, 200)
	c.must("DELETE", "/alice/x.bin?uploadId="+init.UploadId, nil, nil, 204)
	c.must("HEAD", "/alice/x.bin", nil, nil, 404)
}

func TestS3Presigned(t *testing.T) {
	c, _, _ := setup(t)
	c.must("PUT", "/alice/share.txt", []byte("shared"), nil, 200)
	u, err := s3.PresignURL("GET", c.base+"/alice/share.txt", c.ak, c.secret, "us-east-1", time.Now(), 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(b) != "shared" {
		t.Fatalf("presigned: %d %s", resp.StatusCode, b)
	}
	resp, _ = http.Get(c.base + "/alice/share.txt")
	if resp.StatusCode != 403 {
		t.Fatalf("anonymous: %d", resp.StatusCode)
	}
}

func md5New() interface {
	Write([]byte) (int, error)
	Sum([]byte) []byte
} {
	return md5pkg.New()
}
