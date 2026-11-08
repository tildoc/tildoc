package s3

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// Test vectors from the AWS S3 SigV4 documentation ("Examples: Signature
// Calculations in AWS Signature Version 4").
const (
	exAK     = "AKIAIOSFODNN7EXAMPLE"
	exSecret = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
)

var exTime = time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)

func exRequest(method, target, host string, hdr map[string]string, auth string) *http.Request {
	r, _ := http.NewRequest(method, "http://"+host+target, nil)
	r.Host = host
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	r.Header.Set("Authorization", auth)
	return r
}

func TestAWSVectorGetObject(t *testing.T) {
	r := exRequest("GET", "/test.txt", "examplebucket.s3.amazonaws.com", map[string]string{
		"Range": "bytes=0-9", "X-Amz-Content-Sha256": emptySHA256, "X-Amz-Date": "20130524T000000Z",
	}, "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=host;range;x-amz-content-sha256;x-amz-date,Signature=f0e8bdb87c964420e857bd35b5d6ed310bd44f0170aba48dd91039c6036bdb41")
	ai, err := parseAuth(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ai.verify(r, exSecret, exTime); err != nil {
		t.Fatal(err)
	}
	// wrong secret fails
	if _, err := ai.verify(r, exSecret+"x", exTime); err == nil || err.Code != "SignatureDoesNotMatch" {
		t.Fatalf("expected mismatch, got %v", err)
	}
	// skew
	if _, err := ai.verify(r, exSecret, exTime.Add(time.Hour)); err == nil || err.Code != "RequestTimeTooSkewed" {
		t.Fatalf("expected skew error, got %v", err)
	}
}

func TestAWSVectorPutObject(t *testing.T) {
	r := exRequest("PUT", "/test%24file.text", "examplebucket.s3.amazonaws.com", map[string]string{
		"Date": "Fri, 24 May 2013 00:00:00 GMT", "X-Amz-Date": "20130524T000000Z", "X-Amz-Storage-Class": "REDUCED_REDUNDANCY",
		"X-Amz-Content-Sha256": "44ce7dd67c959e0d3524ffac1771dfbba87d2b6b4b4e99e42034a8b803f8b072",
	}, "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=date;host;x-amz-content-sha256;x-amz-date;x-amz-storage-class,Signature=98ad721746da40c64f1a55b78f14c238d841ea1380cd77a1b5971af0ece108bd")
	ai, err := parseAuth(r)
	if err != nil {
		t.Fatal(err)
	}
	payload, serr := ai.verify(r, exSecret, exTime)
	if serr != nil {
		t.Fatal(serr)
	}
	// body hash verification
	r.Body = io.NopCloser(strings.NewReader("Welcome to Amazon S3."))
	body, _, berr := bodyReader(r, payload, ai, exSecret)
	if berr != nil {
		t.Fatal(berr)
	}
	if b, err := io.ReadAll(body); err != nil || string(b) != "Welcome to Amazon S3." {
		t.Fatalf("body: %q %v", b, err)
	}
	r.Body = io.NopCloser(strings.NewReader("Welcome to Amazon S4."))
	body, _, _ = bodyReader(r, payload, ai, exSecret)
	if _, err := io.ReadAll(body); err == nil {
		t.Fatal("expected sha mismatch")
	}
}

func TestAWSVectorListObjects(t *testing.T) {
	r := exRequest("GET", "/?max-keys=2&prefix=J", "examplebucket.s3.amazonaws.com", map[string]string{
		"X-Amz-Content-Sha256": emptySHA256, "X-Amz-Date": "20130524T000000Z",
	}, "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=host;x-amz-content-sha256;x-amz-date,Signature=34b48302e7b5fa45bde8084f4b7868a86f0a534bc59db6670ed5711ef69dc6f7")
	ai, _ := parseAuth(r)
	if _, err := ai.verify(r, exSecret, exTime); err != nil {
		t.Fatal(err)
	}
}

func TestAWSVectorChunked(t *testing.T) {
	data := bytes.Repeat([]byte("a"), 66560)
	r := exRequest("PUT", "/examplebucket/chunkObject.txt", "s3.amazonaws.com", map[string]string{
		"X-Amz-Date": "20130524T000000Z", "X-Amz-Storage-Class": "REDUCED_REDUNDANCY",
		"X-Amz-Content-Sha256": streamingSigned, "Content-Encoding": "aws-chunked",
		"Content-Length": "66824", "X-Amz-Decoded-Content-Length": "66560",
	}, "AWS4-HMAC-SHA256 Credential=AKIAIOSFODNN7EXAMPLE/20130524/us-east-1/s3/aws4_request,SignedHeaders=content-encoding;content-length;host;x-amz-content-sha256;x-amz-date;x-amz-decoded-content-length;x-amz-storage-class,Signature=4f232c4386841ef735655705268965c44a0e4690baa4adea153f7db9fa80a0a9")
	ai, err := parseAuth(r)
	if err != nil {
		t.Fatal(err)
	}
	payload, serr := ai.verify(r, exSecret, exTime)
	if serr != nil {
		t.Fatal(serr)
	}
	enc := EncodeChunked(data, 65536, ai.Signature, exSecret, "us-east-1", "20130524T000000Z")
	if len(enc) != 66824 {
		t.Fatalf("encoded length %d, want 66824", len(enc))
	}
	if !bytes.Contains(enc, []byte("chunk-signature=ad80c730a21e5b8d04586a2213dd63b9a0e99e0e2307b0ade35a65485a288648")) ||
		!bytes.Contains(enc, []byte("chunk-signature=b6c6ea8a5354eaf15b3cb7646744f4275b71ea724fed81ceb9323e279d449df9")) {
		t.Fatal("chunk signatures differ from AWS example")
	}
	r.Body = io.NopCloser(bytes.NewReader(enc))
	body, n, berr := bodyReader(r, payload, ai, exSecret)
	if berr != nil || n != 66560 {
		t.Fatal(berr, n)
	}
	got, rerr := io.ReadAll(body)
	if rerr != nil || !bytes.Equal(got, data) {
		t.Fatalf("decoded %d bytes, err %v", len(got), rerr)
	}
	// tampered chunk
	bad := append([]byte{}, enc...)
	bad[200] = 'b'
	r.Body = io.NopCloser(bytes.NewReader(bad))
	body, _, _ = bodyReader(r, payload, ai, exSecret)
	if _, err := io.ReadAll(body); err == nil {
		t.Fatal("tampered chunk accepted")
	}
}

func TestUnsignedTrailer(t *testing.T) {
	data := []byte(strings.Repeat("hello trailer ", 5000))
	r, _ := http.NewRequest("PUT", "http://localhost/b/k", bytes.NewReader(EncodeUnsignedTrailer(data, 8192)))
	r.Header.Set("X-Amz-Trailer", "x-amz-checksum-crc32")
	body, _, err := bodyReader(r, streamingUnsignT, &authInfo{}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, rerr := io.ReadAll(body)
	if rerr != nil || !bytes.Equal(got, data) {
		t.Fatalf("decode: %v", rerr)
	}
	// wrong checksum
	enc := EncodeUnsignedTrailer(data, 8192)
	enc = bytes.Replace(enc, []byte("x-amz-checksum-crc32:"), []byte("x-amz-checksum-crc32:AAAA"), 1)
	r.Body = io.NopCloser(bytes.NewReader(enc))
	body, _, _ = bodyReader(r, streamingUnsignT, &authInfo{}, "")
	if _, err := io.ReadAll(body); err == nil {
		t.Fatal("bad checksum accepted")
	}
}

func TestSignRoundTripAndPresign(t *testing.T) {
	now := time.Now()
	r, _ := http.NewRequest("GET", "http://localhost:9000/bucket/a%20b/c+d.txt?list-type=2&prefix=x%2Fy", nil)
	SignRequest(r, "AK", "SECRET", "eu-central-1", emptySHA256, now)
	ai, err := parseAuth(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ai.verify(r, "SECRET", now); err != nil {
		t.Fatal(err)
	}
	u, perr := PresignURL("GET", "http://localhost:9000/bucket/file.pdf", "AK", "SECRET", "us-east-1", now, time.Hour)
	if perr != nil {
		t.Fatal(perr)
	}
	pr, _ := http.NewRequest("GET", u, nil)
	pai, aerr := parseAuth(pr)
	if aerr != nil {
		t.Fatal(aerr)
	}
	if _, err := pai.verify(pr, "SECRET", now); err != nil {
		t.Fatal(err)
	}
	if _, err := pai.verify(pr, "SECRET", now.Add(2*time.Hour)); err == nil {
		t.Fatal("expired presign accepted")
	}
}
