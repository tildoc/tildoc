package core

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (OWASP recommendation: m=19 MiB, t=2, p=1).
const (
	argonTime    = 2
	argonMemory  = 19 * 1024
	argonThreads = 1
	argonKeyLen  = 32
)

// HashPassword returns a PHC formatted argon2id hash.
func HashPassword(pw string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	h := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime,
		argonThreads, b64.EncodeToString(salt), b64.EncodeToString(h)), nil
}

// VerifyPassword checks pw against a PHC argon2id hash in constant time.
func VerifyPassword(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// dummyHash is used to equalize timing for unknown users.
var dummyHash, _ = HashPassword("tildoc-dummy-password")

// randomToken returns n random bytes, base64url encoded.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func tokenHash(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// newAccessKeyID returns an AWS-style 20 char upper case key id.
func newAccessKeyID() string {
	b := make([]byte, 12)
	rand.Read(b)
	return "DK" + base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(b)[:18]
}

// newSecret returns a 40 char alphanumeric secret (about 238 bits). No '-'
// or '_': a leading dash breaks command line tools that take the secret as
// an argument (e.g. `rclone obscure`).
func newSecret() string {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	out := make([]byte, 40)
	buf := make([]byte, 64)
	for i := 0; i < len(out); {
		rand.Read(buf)
		for _, b := range buf {
			// rejection sampling keeps the distribution uniform (248 = 4*62)
			if b < 248 && i < len(out) {
				out[i] = alphabet[int(b)%62]
				i++
			}
		}
	}
	return string(out)
}

func (s *Service) gcm() (cipher.AEAD, error) {
	if len(s.Cfg.MasterKey) != 32 {
		return nil, errors.New("master key not configured")
	}
	blk, err := aes.NewCipher(s.Cfg.MasterKey)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(blk)
}

// masterKeyVersion is the first byte of every sealed secret, so that the
// master key can be rotated: rows name the key that sealed them.
const masterKeyVersion = 1

// sealSecret encrypts a secret with AES-256-GCM; aad binds it to its key id.
func (s *Service) sealSecret(secret, aad string) ([]byte, error) {
	g, err := s.gcm()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, g.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return append([]byte{masterKeyVersion}, g.Seal(nonce, nonce, []byte(secret), []byte(aad))...), nil
}

func (s *Service) openSecret(enc []byte, aad string) (string, error) {
	g, err := s.gcm()
	if err != nil {
		return "", err
	}
	if len(enc) < 1+g.NonceSize() {
		return "", errors.New("ciphertext too short")
	}
	if enc[0] != masterKeyVersion {
		return "", errors.New("secret was sealed with an unknown master key version")
	}
	enc = enc[1:]
	pt, err := g.Open(nil, enc[:g.NonceSize()], enc[g.NonceSize():], []byte(aad))
	if err != nil {
		return "", errors.New("cannot decrypt secret (wrong master key?)")
	}
	return string(pt), nil
}
