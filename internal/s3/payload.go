package s3

import (
	"bufio"
	"bytes"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"hash"
	"hash/crc32"
	"hash/crc64"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const maxChunkSize = 64 << 20

var crc64NVME = crc64.MakeTable(0x9a6c9329ac4bc9b5)

// checksumHash returns a hash for an x-amz-checksum-* algorithm.
func checksumHash(algo string) hash.Hash {
	switch strings.ToLower(algo) {
	case "crc32":
		return crc32.NewIEEE()
	case "crc32c":
		return crc32.New(crc32.MakeTable(crc32.Castagnoli))
	case "crc64nvme":
		return crc64.New(crc64NVME)
	case "sha1":
		return sha1.New()
	case "sha256":
		return sha256.New()
	}
	return nil
}

func encodeChecksum(h hash.Hash) string {
	sum := h.Sum(nil)
	return base64.StdEncoding.EncodeToString(sum)
}

// verifier wraps the request body, verifying the declared payload hash,
// Content-MD5 and x-amz-checksum-* while the body is streamed. On mismatch
// Read returns an *Error instead of io.EOF, so nothing gets committed.
type verifier struct {
	r        io.Reader
	sha      hash.Hash // payload sha256 (if declared)
	wantSHA  string
	md5      hash.Hash
	wantMD5  string
	cks      hash.Hash
	ckName   string
	wantCk   string
	trailers func() map[string]string // trailer values from aws-chunked
	done     bool
}

func (v *verifier) Read(p []byte) (int, error) {
	n, err := v.r.Read(p)
	if n > 0 {
		if v.sha != nil {
			v.sha.Write(p[:n])
		}
		if v.md5 != nil {
			v.md5.Write(p[:n])
		}
		if v.cks != nil {
			v.cks.Write(p[:n])
		}
	}
	if err == io.EOF && !v.done {
		v.done = true
		if verr := v.check(); verr != nil {
			return n, verr
		}
	}
	return n, err
}

func (v *verifier) check() *Error {
	if v.sha != nil && hex.EncodeToString(v.sha.Sum(nil)) != v.wantSHA {
		return &Error{Status: 400, Code: "XAmzContentSHA256Mismatch", Message: "the provided x-amz-content-sha256 header does not match what was computed"}
	}
	if v.md5 != nil && base64.StdEncoding.EncodeToString(v.md5.Sum(nil)) != v.wantMD5 {
		return &Error{Status: 400, Code: "BadDigest", Message: "the Content-MD5 you specified did not match what we received"}
	}
	if v.cks != nil {
		want := v.wantCk
		if want == "" && v.trailers != nil {
			want = v.trailers()["x-amz-checksum-"+v.ckName]
		}
		if want == "" {
			return &Error{Status: 400, Code: "InvalidRequest", Message: "missing checksum trailer"}
		}
		if encodeChecksum(v.cks) != want {
			return &Error{Status: 400, Code: "BadDigest", Message: "the " + v.ckName + " checksum did not match"}
		}
	}
	return nil
}

// bodyReader returns the verified, decoded request body and its decoded
// length (-1 if unknown).
func bodyReader(r *http.Request, payload string, ai *authInfo, secret string) (io.Reader, int64, *Error) {
	v := &verifier{}
	length := r.ContentLength
	switch payload {
	case unsignedPayload:
		v.r = r.Body
	case streamingSigned, streamingSignedT, streamingUnsignT:
		cr := &chunkedReader{br: bufio.NewReaderSize(r.Body, 64<<10), trailer: payload != streamingSigned}
		if payload != streamingUnsignT {
			cr.signed = true
			cr.key = signingKey(secret, ai.Cred.Date, ai.Cred.Region, ai.Cred.Service)
			cr.amzDate = ai.AmzDate
			cr.scope = ai.Cred.scope()
			cr.prevSig = ai.Signature
		}
		v.r = cr
		v.trailers = func() map[string]string { return cr.trailers }
		length = -1
		if dl := r.Header.Get("X-Amz-Decoded-Content-Length"); dl != "" {
			if n, err := strconv.ParseInt(dl, 10, 64); err == nil {
				length = n
			}
		}
	default:
		if len(payload) != 64 {
			return nil, 0, &Error{Status: 400, Code: "InvalidArgument", Message: "invalid x-amz-content-sha256"}
		}
		if _, err := hex.DecodeString(payload); err != nil {
			return nil, 0, &Error{Status: 400, Code: "InvalidArgument", Message: "invalid x-amz-content-sha256"}
		}
		v.r, v.sha, v.wantSHA = r.Body, sha256.New(), strings.ToLower(payload)
	}
	if m := r.Header.Get("Content-MD5"); m != "" {
		v.md5, v.wantMD5 = md5.New(), m
	}
	algo := strings.ToLower(r.Header.Get("X-Amz-Sdk-Checksum-Algorithm"))
	if t := r.Header.Get("X-Amz-Trailer"); t != "" {
		algo = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(t)), "x-amz-checksum-")
	} else {
		for _, a := range []string{"crc32", "crc32c", "crc64nvme", "sha1", "sha256"} {
			if val := r.Header.Get("X-Amz-Checksum-" + a); val != "" {
				algo, v.wantCk = a, val
			}
		}
	}
	if algo != "" {
		if h := checksumHash(algo); h != nil && (v.wantCk != "" || v.trailers != nil) {
			v.cks, v.ckName = h, algo
		}
	}
	return v, length, nil
}

// chunkedReader decodes aws-chunked bodies, verifying chunk signatures
// (signed variants) and collecting trailing headers.
type chunkedReader struct {
	br       *bufio.Reader
	signed   bool
	trailer  bool
	key      []byte
	amzDate  string
	scope    string
	prevSig  string
	chunk    []byte
	off      int
	eof      bool
	trailers map[string]string
}

func (c *chunkedReader) Read(p []byte) (int, error) {
	for c.off >= len(c.chunk) {
		if c.eof {
			return 0, io.EOF
		}
		if err := c.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, c.chunk[c.off:])
	c.off += n
	return n, nil
}

func chunkErr(msg string) *Error {
	return &Error{Status: 400, Code: "IncompleteBody", Message: msg}
}

func (c *chunkedReader) readLine() (string, error) {
	line, err := c.br.ReadString('\n')
	if err != nil {
		if err == io.EOF {
			return "", chunkErr("unexpected end of chunked body")
		}
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func (c *chunkedReader) next() error {
	header, err := c.readLine()
	if err != nil {
		return err
	}
	sizeHex, ext, _ := strings.Cut(header, ";")
	size, err := strconv.ParseInt(strings.TrimSpace(sizeHex), 16, 64)
	if err != nil || size < 0 || size > maxChunkSize {
		return chunkErr("invalid chunk size")
	}
	var sig string
	if c.signed {
		k, v, _ := strings.Cut(ext, "=")
		if strings.TrimSpace(k) != "chunk-signature" {
			return &Error{Status: 403, Code: "SignatureDoesNotMatch", Message: "missing chunk signature"}
		}
		sig = strings.TrimSpace(v)
	}
	if cap(c.chunk) < int(size) {
		c.chunk = make([]byte, size)
	}
	c.chunk = c.chunk[:size]
	c.off = 0
	if _, err := io.ReadFull(c.br, c.chunk); err != nil {
		return chunkErr("chunk shorter than declared")
	}
	if c.signed {
		h := sha256.Sum256(c.chunk)
		sts := "AWS4-HMAC-SHA256-PAYLOAD\n" + c.amzDate + "\n" + c.scope + "\n" + c.prevSig + "\n" + emptySHA256 + "\n" + hex.EncodeToString(h[:])
		want := hex.EncodeToString(hmacSHA256(c.key, sts))
		if !hmac.Equal([]byte(want), []byte(sig)) {
			return &Error{Status: 403, Code: "SignatureDoesNotMatch", Message: "chunk signature mismatch"}
		}
		c.prevSig = sig
	}
	if size > 0 {
		// CRLF after data
		if line, err := c.readLine(); err != nil || line != "" {
			return chunkErr("missing CRLF after chunk")
		}
		return nil
	}
	c.eof = true
	if !c.trailer {
		// optional final CRLF
		c.br.ReadString('\n') //nolint:errcheck // the next read reports a broken stream
		return nil
	}
	c.trailers = map[string]string{}
	var canon bytes.Buffer
	var trailerSig string
	for {
		line, err := c.readLine()
		if err != nil {
			if len(c.trailers) > 0 {
				break
			}
			return err
		}
		if line == "" {
			break
		}
		k, v, _ := strings.Cut(line, ":")
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimSpace(v)
		if k == "x-amz-trailer-signature" {
			trailerSig = v
			continue
		}
		c.trailers[k] = v
		canon.WriteString(k + ":" + v + "\n")
	}
	if c.signed {
		h := sha256.Sum256(canon.Bytes())
		sts := "AWS4-HMAC-SHA256-TRAILER\n" + c.amzDate + "\n" + c.scope + "\n" + c.prevSig + "\n" + hex.EncodeToString(h[:])
		want := hex.EncodeToString(hmacSHA256(c.key, sts))
		if !hmac.Equal([]byte(want), []byte(trailerSig)) {
			return &Error{Status: 403, Code: "SignatureDoesNotMatch", Message: "trailer signature mismatch"}
		}
	}
	return nil
}

// EncodeChunked encodes data as signed aws-chunked (test helper).
func EncodeChunked(data []byte, chunkSize int, seedSig string, secret, region string, amzDate string) []byte {
	key := signingKey(secret, amzDate[:8], region, "s3")
	scope := amzDate[:8] + "/" + region + "/s3/aws4_request"
	var out bytes.Buffer
	prev := seedSig
	write := func(chunk []byte) {
		h := sha256.Sum256(chunk)
		sts := "AWS4-HMAC-SHA256-PAYLOAD\n" + amzDate + "\n" + scope + "\n" + prev + "\n" + emptySHA256 + "\n" + hex.EncodeToString(h[:])
		sig := hex.EncodeToString(hmacSHA256(key, sts))
		out.WriteString(strconv.FormatInt(int64(len(chunk)), 16) + ";chunk-signature=" + sig + "\r\n")
		out.Write(chunk)
		out.WriteString("\r\n")
		prev = sig
	}
	for len(data) > 0 {
		n := chunkSize
		if n > len(data) {
			n = len(data)
		}
		write(data[:n])
		data = data[n:]
	}
	write(nil)
	return out.Bytes()
}

// EncodeUnsignedTrailer encodes data as unsigned aws-chunked with a CRC32 trailer (test helper).
func EncodeUnsignedTrailer(data []byte, chunkSize int) []byte {
	var out bytes.Buffer
	h := crc32.NewIEEE()
	h.Write(data)
	for len(data) > 0 {
		n := chunkSize
		if n > len(data) {
			n = len(data)
		}
		out.WriteString(strconv.FormatInt(int64(n), 16) + "\r\n")
		out.Write(data[:n])
		out.WriteString("\r\n")
		data = data[n:]
	}
	out.WriteString("0\r\n")
	out.WriteString("x-amz-checksum-crc32:" + encodeChecksum(h) + "\r\n\r\n")
	return out.Bytes()
}
