// Package wopi holds the protocol parts of Collabora's WOPI host interface
// that need no database: the discovery document and proof signatures.
package wopi

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxDiscovery = 4 << 20
	refreshAfter = time.Hour
	retryAfter   = time.Minute
	proofWindow  = 20 * time.Minute
	// ticksAtUnix: .NET ticks (100 ns since 0001-01-01) of the Unix epoch.
	ticksAtUnix = 621355968000000000
)

// Discovery is what a Collabora server offers.
type Discovery struct {
	actions map[string]map[string]string // extension -> action -> URL path
	keys    []*rsa.PublicKey             // current key, then the previous one
}

type xmlDiscovery struct {
	Zones []struct {
		Apps []struct {
			Actions []struct {
				Ext    string `xml:"ext,attr"`
				Name   string `xml:"name,attr"`
				URLSrc string `xml:"urlsrc,attr"`
			} `xml:"action"`
		} `xml:"app"`
	} `xml:"net-zone"`
	Proof struct {
		Modulus     string `xml:"modulus,attr"`
		Exponent    string `xml:"exponent,attr"`
		OldModulus  string `xml:"oldmodulus,attr"`
		OldExponent string `xml:"oldexponent,attr"`
	} `xml:"proof-key"`
}

// Parse reads a discovery document. Only the path of an action URL is kept:
// the public origin comes from tildoc's own configuration.
func Parse(data []byte) (*Discovery, error) {
	var x xmlDiscovery
	if err := xml.Unmarshal(data, &x); err != nil {
		return nil, fmt.Errorf("discovery: %w", err)
	}
	d := &Discovery{actions: map[string]map[string]string{}}
	for _, z := range x.Zones {
		for _, a := range z.Apps {
			for _, act := range a.Actions {
				u, err := url.Parse(act.URLSrc)
				if err != nil || !strings.HasPrefix(u.Path, "/") || act.Ext == "" {
					continue
				}
				ext := strings.ToLower(act.Ext)
				if d.actions[ext] == nil {
					d.actions[ext] = map[string]string{}
				}
				d.actions[ext][act.Name] = u.Path
			}
		}
	}
	if len(d.actions) == 0 {
		return nil, errors.New("discovery: no actions")
	}
	if k, err := publicKey(x.Proof.Modulus, x.Proof.Exponent); err == nil {
		d.keys = append(d.keys, k)
	}
	if k, err := publicKey(x.Proof.OldModulus, x.Proof.OldExponent); err == nil {
		d.keys = append(d.keys, k)
	}
	return d, nil
}

func publicKey(mod, exp string) (*rsa.PublicKey, error) {
	if mod == "" || exp == "" {
		return nil, errors.New("no key")
	}
	m, err := base64.StdEncoding.DecodeString(mod)
	if err != nil {
		return nil, err
	}
	e, err := base64.StdEncoding.DecodeString(exp)
	if err != nil {
		return nil, err
	}
	en := new(big.Int).SetBytes(e)
	if !en.IsInt64() || en.Int64() < 3 || en.Int64() > 1<<31 {
		return nil, errors.New("bad exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(m), E: int(en.Int64())}, nil
}

// Extensions lists the file extensions that open for editing or viewing.
func (d *Discovery) Extensions() []string { return d.with("edit", "view") }

// Editable lists the file extensions that can be edited.
func (d *Discovery) Editable() []string { return d.with("edit") }

// Creatable lists the file extensions the server can fill from a template.
// Plain text formats are left out: those belong to tildoc's text editor.
func (d *Discovery) Creatable() []string {
	out := []string{}
	for _, ext := range d.with("editnew") {
		switch ext {
		case "txt", "csv", "tsv", "md", "html", "htm", "xml":
		default:
			out = append(out, ext)
		}
	}
	return out
}

func (d *Discovery) with(actions ...string) []string {
	out := []string{}
	for ext, a := range d.actions {
		for _, name := range actions {
			if a[name] != "" {
				out = append(out, ext)
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// ActionPath returns the URL path that opens ext. An empty file that can be
// edited uses the template action (editnew). Files that cannot be edited, or
// that the user may not edit, use the view action if there is one.
func (d *Discovery) ActionPath(ext string, write, empty bool) (string, bool) {
	a := d.actions[strings.ToLower(ext)]
	if empty && write && a["editnew"] != "" {
		return a["editnew"], true
	}
	first, second := "edit", "view"
	if !write {
		first, second = second, first
	}
	if p := a[first]; p != "" {
		return p, true
	}
	p := a[second]
	return p, p != ""
}

// Verify checks a request's proof headers. A Collabora without proof key
// (none published) passes: the access token remains the authentication.
func (d *Discovery) Verify(token, fullURL, timestamp, proof, proofOld string, now time.Time) error {
	if len(d.keys) == 0 {
		return nil
	}
	ticks, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || ticks < ticksAtUnix {
		return errors.New("wopi proof: bad timestamp")
	}
	at := time.UnixMicro((ticks - ticksAtUnix) / 10)
	if diff := now.Sub(at); diff > proofWindow || diff < -proofWindow {
		return errors.New("wopi proof: timestamp outside the window")
	}
	sum := sha256.Sum256(Message(token, fullURL, ticks))
	check := func(k *rsa.PublicKey, sig string) bool {
		b, err := base64.StdEncoding.DecodeString(sig)
		return err == nil && len(b) > 0 && rsa.VerifyPKCS1v15(k, crypto.SHA256, sum[:], b) == nil
	}
	// the sender or tildoc may be one key rotation behind
	if check(d.keys[0], proof) || check(d.keys[0], proofOld) || (len(d.keys) > 1 && check(d.keys[1], proof)) {
		return nil
	}
	return errors.New("wopi proof: invalid signature")
}

// Message builds the bytes that a proof signs.
func Message(token, fullURL string, ticks int64) []byte {
	u := strings.ToUpper(fullURL)
	b := make([]byte, 0, len(token)+len(u)+20)
	b = binary.BigEndian.AppendUint32(b, uint32(len(token)))
	b = append(b, token...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(u)))
	b = append(b, u...)
	b = binary.BigEndian.AppendUint32(b, 8)
	return binary.BigEndian.AppendUint64(b, uint64(ticks))
}

// Client fetches and caches the discovery document of one Collabora server.
type Client struct {
	url  string
	http *http.Client
	now  func() time.Time

	mu    sync.Mutex
	cur   *Discovery
	at    time.Time
	tried time.Time
}

// NewClient creates a client for the server at internal base URL.
func NewClient(base string) *Client {
	return &Client{url: strings.TrimRight(base, "/") + "/hosting/discovery", http: &http.Client{Timeout: 10 * time.Second}, now: time.Now}
}

// Get returns the discovery, refreshing it when it is old. While Collabora
// is unreachable the last known state stays in use.
func (c *Client) Get(ctx context.Context) (*Discovery, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cur != nil && c.now().Sub(c.at) < refreshAfter {
		return c.cur, nil
	}
	return c.fetchLocked(ctx)
}

// Refresh fetches the discovery again, at most once a minute (proof keys
// may have changed).
func (c *Client) Refresh(ctx context.Context) (*Discovery, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fetchLocked(ctx)
}

func (c *Client) fetchLocked(ctx context.Context) (*Discovery, error) {
	if !c.tried.IsZero() && c.now().Sub(c.tried) < retryAfter {
		if c.cur != nil {
			return c.cur, nil
		}
		return nil, errors.New("office server unavailable")
	}
	c.tried = c.now()
	d, err := c.fetch(ctx)
	if err != nil {
		if c.cur != nil {
			return c.cur, nil
		}
		return nil, err
	}
	c.cur, c.at = d, c.now()
	return d, nil
}

func (c *Client) fetch(ctx context.Context) (*Discovery, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("discovery: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxDiscovery))
	if err != nil {
		return nil, err
	}
	return Parse(data)
}
