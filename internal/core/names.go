package core

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var (
	slugRe     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
	usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,31}$`)
)

// reservedSlugs cannot be used as space slugs.
var reservedSlugs = map[string]bool{"api": true, "dav": true}

// NormalizeName validates a file or folder name and returns its NFC form.
func NormalizeName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", errf(ErrInvalid, "name is not valid UTF-8")
	}
	name = norm.NFC.String(name)
	switch {
	case name == "", name == ".", name == "..":
		return "", errf(ErrInvalid, "invalid name %q", name)
	case len(name) > 255:
		return "", errf(ErrInvalid, "name too long (max 255 bytes)")
	case strings.ContainsRune(name, '/'):
		return "", errf(ErrInvalid, "name must not contain '/'")
	}
	for _, r := range name {
		if r == 0 || unicode.IsControl(r) {
			return "", errf(ErrInvalid, "name contains control characters")
		}
	}
	return name, nil
}

// SplitPath splits a slash separated path into validated, normalized
// segments. Empty segments (leading, trailing, duplicate slashes) are ignored.
func SplitPath(p string) ([]string, error) {
	var out []string
	for _, seg := range strings.Split(p, "/") {
		if seg == "" {
			continue
		}
		n, err := NormalizeName(seg)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

// JoinPath builds a display path ("/a/b") from segments.
func JoinPath(segs ...string) string {
	var b strings.Builder
	for _, s := range segs {
		if s == "" {
			continue
		}
		b.WriteByte('/')
		b.WriteString(strings.Trim(s, "/"))
	}
	if b.Len() == 0 {
		return "/"
	}
	return b.String()
}

// ValidateSlug checks space slug rules (S3 bucket compatible).
func ValidateSlug(s string) error {
	if !slugRe.MatchString(s) || strings.Contains(s, "--") || reservedSlugs[s] {
		return errf(ErrInvalid, "invalid slug %q: 3-63 chars [a-z0-9-], start/end alphanumeric, no '--'", s)
	}
	return nil
}

// ValidateUsername checks username rules.
func ValidateUsername(u string) error {
	if !usernameRe.MatchString(u) {
		return errf(ErrInvalid, "invalid username %q: 2-32 chars [a-z0-9._-], start alphanumeric", u)
	}
	return nil
}

// slugify derives a slug candidate from arbitrary text.
func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 55 {
		out = strings.Trim(out[:55], "-")
	}
	for len(out) < 3 {
		out += "x"
	}
	return out
}
