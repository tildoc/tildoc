// Package s3 implements an S3-compatible gateway (path-style addressing)
// on top of the tildoc VFS. Bucket = space slug, key = path in the space.
package s3

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	algorithm        = "AWS4-HMAC-SHA256"
	timeFormat       = "20060102T150405Z"
	unsignedPayload  = "UNSIGNED-PAYLOAD"
	streamingSigned  = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD"
	streamingSignedT = "STREAMING-AWS4-HMAC-SHA256-PAYLOAD-TRAILER"
	streamingUnsignT = "STREAMING-UNSIGNED-PAYLOAD-TRAILER"
	emptySHA256      = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	maxSkew          = 15 * time.Minute
)

// awsEncode URI-encodes per SigV4 rules: unreserved characters stay, every
// other byte becomes %XX (upper case). '/' is kept when keepSlash.
func awsEncode(s string, keepSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			b.WriteString("%")
			b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
		}
	}
	return b.String()
}

// rawPath returns the request path exactly as sent (still escaped).
func rawPath(r *http.Request) string {
	if r.URL.RawPath != "" {
		return r.URL.RawPath
	}
	return r.URL.EscapedPath()
}

// canonicalURI decodes the raw path segment by segment and re-encodes it.
// Decoding per segment keeps "%2F" inside a segment distinct from '/'.
func canonicalURI(raw string) string {
	if raw == "" {
		return "/"
	}
	segs := strings.Split(raw, "/")
	for i, s := range segs {
		if d, err := url.PathUnescape(s); err == nil {
			s = d
		}
		segs[i] = awsEncode(s, false)
	}
	return strings.Join(segs, "/")
}

func canonicalQuery(rawQuery string, skipSignature bool) string {
	if rawQuery == "" {
		return ""
	}
	type kv struct{ k, v string }
	var pairs []kv
	for _, part := range strings.Split(rawQuery, "&") {
		if part == "" {
			continue
		}
		k, v, _ := strings.Cut(part, "=")
		dk, err := url.QueryUnescape(k)
		if err != nil {
			dk = k
		}
		dv, err := url.QueryUnescape(v)
		if err != nil {
			dv = v
		}
		if skipSignature && dk == "X-Amz-Signature" {
			continue
		}
		pairs = append(pairs, kv{awsEncode(dk, false), awsEncode(dv, false)})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].k != pairs[j].k {
			return pairs[i].k < pairs[j].k
		}
		return pairs[i].v < pairs[j].v
	})
	out := make([]string, len(pairs))
	for i, p := range pairs {
		out[i] = p.k + "=" + p.v
	}
	return strings.Join(out, "&")
}

func headerValue(r *http.Request, name string) string {
	switch name {
	case "host":
		return r.Host
	case "content-length":
		if v := r.Header.Get("Content-Length"); v != "" {
			return v
		}
		if r.ContentLength >= 0 && (r.ContentLength > 0 || r.Body != nil && r.Body != http.NoBody) {
			return strconv.FormatInt(r.ContentLength, 10)
		}
	case "transfer-encoding":
		if len(r.TransferEncoding) > 0 {
			return strings.Join(r.TransferEncoding, ",")
		}
	}
	vals := r.Header.Values(name)
	for i, v := range vals {
		vals[i] = strings.Join(strings.Fields(v), " ")
	}
	return strings.Join(vals, ",")
}

func canonicalRequest(r *http.Request, signedHeaders []string, payloadHash string, presigned bool) string {
	var b strings.Builder
	b.WriteString(r.Method)
	b.WriteByte('\n')
	b.WriteString(canonicalURI(rawPath(r)))
	b.WriteByte('\n')
	b.WriteString(canonicalQuery(r.URL.RawQuery, presigned))
	b.WriteByte('\n')
	for _, h := range signedHeaders {
		b.WriteString(h)
		b.WriteByte(':')
		b.WriteString(headerValue(r, h))
		b.WriteByte('\n')
	}
	b.WriteByte('\n')
	b.WriteString(strings.Join(signedHeaders, ";"))
	b.WriteByte('\n')
	b.WriteString(payloadHash)
	return b.String()
}

func hmacSHA256(key []byte, data string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(data))
	return m.Sum(nil)
}

func sha256Hex(data string) string {
	h := sha256.Sum256([]byte(data))
	return hex.EncodeToString(h[:])
}

func signingKey(secret, date, region, service string) []byte {
	k := hmacSHA256([]byte("AWS4"+secret), date)
	k = hmacSHA256(k, region)
	k = hmacSHA256(k, service)
	return hmacSHA256(k, "aws4_request")
}

func stringToSign(amzDate, scope, canonical string) string {
	return algorithm + "\n" + amzDate + "\n" + scope + "\n" + sha256Hex(canonical)
}

// credential is a parsed SigV4 credential scope.
type credential struct {
	AccessKey, Date, Region, Service string
}

func (c credential) scope() string {
	return c.Date + "/" + c.Region + "/" + c.Service + "/aws4_request"
}

func parseCredential(v string) (credential, bool) {
	parts := strings.Split(v, "/")
	if len(parts) != 5 || parts[4] != "aws4_request" {
		return credential{}, false
	}
	return credential{AccessKey: parts[0], Date: parts[1], Region: parts[2], Service: parts[3]}, true
}

// authInfo is the signature information of a request.
type authInfo struct {
	Cred          credential
	SignedHeaders []string
	Signature     string
	AmzDate       string
	Time          time.Time
	Presigned     bool
	Expires       time.Duration
}

func parseAuth(r *http.Request) (*authInfo, *Error) {
	if h := r.Header.Get("Authorization"); h != "" {
		if !strings.HasPrefix(h, algorithm+" ") {
			return nil, errAccessDenied("unsupported authorization type (only AWS SigV4 is supported)")
		}
		ai := &authInfo{}
		for _, part := range strings.Split(strings.TrimPrefix(h, algorithm+" "), ",") {
			k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
			switch k {
			case "Credential":
				c, ok := parseCredential(v)
				if !ok {
					return nil, errAuthHeaderMalformed()
				}
				ai.Cred = c
			case "SignedHeaders":
				ai.SignedHeaders = strings.Split(v, ";")
			case "Signature":
				ai.Signature = v
			}
		}
		if ai.Cred.AccessKey == "" || ai.Signature == "" || len(ai.SignedHeaders) == 0 {
			return nil, errAuthHeaderMalformed()
		}
		ai.AmzDate = r.Header.Get("X-Amz-Date")
		if ai.AmzDate == "" {
			if d, err := http.ParseTime(r.Header.Get("Date")); err == nil {
				ai.AmzDate = d.UTC().Format(timeFormat)
			}
		}
		t, err := time.Parse(timeFormat, ai.AmzDate)
		if err != nil {
			return nil, errAccessDenied("missing or invalid X-Amz-Date")
		}
		ai.Time = t
		return ai, nil
	}
	q := r.URL.Query()
	if q.Get("X-Amz-Algorithm") == "" {
		return nil, errAccessDenied("anonymous access is not allowed")
	}
	if q.Get("X-Amz-Algorithm") != algorithm {
		return nil, errAccessDenied("unsupported algorithm")
	}
	c, ok := parseCredential(q.Get("X-Amz-Credential"))
	if !ok {
		return nil, errAuthHeaderMalformed()
	}
	ai := &authInfo{Cred: c, SignedHeaders: strings.Split(q.Get("X-Amz-SignedHeaders"), ";"),
		Signature: q.Get("X-Amz-Signature"), AmzDate: q.Get("X-Amz-Date"), Presigned: true}
	t, err := time.Parse(timeFormat, ai.AmzDate)
	if err != nil {
		return nil, errAccessDenied("invalid X-Amz-Date")
	}
	ai.Time = t
	exp, err := strconv.Atoi(q.Get("X-Amz-Expires"))
	if err != nil || exp < 1 || exp > 604800 {
		return nil, errAccessDenied("invalid X-Amz-Expires")
	}
	ai.Expires = time.Duration(exp) * time.Second
	return ai, nil
}

// verify checks the request signature against secret. It returns the
// payload hash value the request declared (or UNSIGNED-PAYLOAD).
func (ai *authInfo) verify(r *http.Request, secret string, now time.Time) (string, *Error) {
	if ai.Cred.Service != "s3" || ai.Cred.Date != ai.AmzDate[:8] {
		return "", errAuthHeaderMalformed()
	}
	if ai.Presigned {
		if now.Before(ai.Time.Add(-maxSkew)) {
			return "", errAccessDenied("request is not yet valid")
		}
		if now.After(ai.Time.Add(ai.Expires)) {
			return "", errAccessDenied("request has expired")
		}
	} else if d := now.Sub(ai.Time); d > maxSkew || d < -maxSkew {
		return "", &Error{Status: 403, Code: "RequestTimeTooSkewed", Message: "the difference between the request time and the server's time is too large"}
	}
	hasHost := false
	for _, h := range ai.SignedHeaders {
		if h == "host" {
			hasHost = true
		}
	}
	if !hasHost {
		return "", errAccessDenied("host header must be signed")
	}
	payload := r.Header.Get("X-Amz-Content-Sha256")
	if payload == "" {
		if !ai.Presigned {
			return "", &Error{Status: 400, Code: "InvalidRequest", Message: "missing x-amz-content-sha256"}
		}
		payload = unsignedPayload
	}
	cr := canonicalRequest(r, ai.SignedHeaders, payload, ai.Presigned)
	key := signingKey(secret, ai.Cred.Date, ai.Cred.Region, ai.Cred.Service)
	want := hex.EncodeToString(hmacSHA256(key, stringToSign(ai.AmzDate, ai.Cred.scope(), cr)))
	if !hmac.Equal([]byte(want), []byte(ai.Signature)) {
		return "", &Error{Status: 403, Code: "SignatureDoesNotMatch",
			Message: "the request signature we calculated does not match the signature you provided"}
	}
	return payload, nil
}

// SignRequest signs r with SigV4 header authentication (used by tests and
// tooling). payloadHash is the hex SHA-256 of the body or a special value.
func SignRequest(r *http.Request, accessKey, secret, region, payloadHash string, t time.Time) {
	amzDate := t.UTC().Format(timeFormat)
	r.Header.Set("X-Amz-Date", amzDate)
	r.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if r.Host == "" {
		r.Host = r.URL.Host
	}
	names := []string{"host"}
	for k := range r.Header {
		lk := strings.ToLower(k)
		if lk == "authorization" || lk == "user-agent" {
			continue
		}
		names = append(names, lk)
	}
	if r.ContentLength > 0 && r.Header.Get("Content-Length") == "" {
		r.Header.Set("Content-Length", strconv.FormatInt(r.ContentLength, 10))
		names = append(names, "content-length")
	}
	sort.Strings(names)
	names = dedupe(names)
	cred := credential{AccessKey: accessKey, Date: amzDate[:8], Region: region, Service: "s3"}
	cr := canonicalRequest(r, names, payloadHash, false)
	key := signingKey(secret, cred.Date, region, "s3")
	sig := hex.EncodeToString(hmacSHA256(key, stringToSign(amzDate, cred.scope(), cr)))
	r.Header.Set("Authorization", algorithm+" Credential="+accessKey+"/"+cred.scope()+
		", SignedHeaders="+strings.Join(names, ";")+", Signature="+sig)
}

// SeedSignature returns the signature of a signed request (for chunk signing in tests).
func SeedSignature(r *http.Request) string {
	h := r.Header.Get("Authorization")
	i := strings.Index(h, "Signature=")
	if i < 0 {
		return ""
	}
	return h[i+len("Signature="):]
}

// PresignURL returns a presigned GET URL valid for expires.
func PresignURL(method, rawURL, accessKey, secret, region string, t time.Time, expires time.Duration) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", err
	}
	amzDate := t.UTC().Format(timeFormat)
	cred := credential{AccessKey: accessKey, Date: amzDate[:8], Region: region, Service: "s3"}
	q := u.Query()
	q.Set("X-Amz-Algorithm", algorithm)
	q.Set("X-Amz-Credential", accessKey+"/"+cred.scope())
	q.Set("X-Amz-Date", amzDate)
	q.Set("X-Amz-Expires", strconv.Itoa(int(expires.Seconds())))
	q.Set("X-Amz-SignedHeaders", "host")
	u.RawQuery = strings.ReplaceAll(q.Encode(), "+", "%20")
	r := &http.Request{Method: method, URL: u, Host: u.Host, Header: http.Header{}}
	cr := canonicalRequest(r, []string{"host"}, unsignedPayload, true)
	key := signingKey(secret, cred.Date, region, "s3")
	sig := hex.EncodeToString(hmacSHA256(key, stringToSign(amzDate, cred.scope(), cr)))
	u.RawQuery += "&X-Amz-Signature=" + sig
	return u.String(), nil
}

func dedupe(s []string) []string {
	out := s[:0]
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}
