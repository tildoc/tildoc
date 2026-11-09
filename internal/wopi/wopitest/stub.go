// Package wopitest is a stand-in for Collabora Online: it publishes a
// discovery document, signs its requests and speaks the WOPI host protocol
// the way Collabora does. Used by the API tests and the UI smoke test.
package wopitest

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/tildoc/tildoc/internal/wopi"
)

// Stub is the fake Collabora server.
type Stub struct {
	Key *rsa.PrivateKey
	mu  sync.Mutex
	// open sessions by token (UI flow only)
	sessions map[string]*Session
}

// New generates a proof key.
func New() (*Stub, error) {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	return &Stub{Key: k, sessions: map[string]*Session{}}, nil
}

// Handler serves the discovery document and a minimal document page.
func (s *Stub) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hosting/discovery", s.discovery)
	mux.HandleFunc("POST /browser/stub/cool.html", s.open)
	mux.HandleFunc("POST /browser/stub/do", s.do)
	return mux
}

func (s *Stub) discovery(w http.ResponseWriter, r *http.Request) {
	mod := base64.StdEncoding.EncodeToString(s.Key.N.Bytes())
	exp := base64.StdEncoding.EncodeToString(big.NewInt(int64(s.Key.E)).Bytes())
	fmt.Fprintf(w, `<wopi-discovery><net-zone name="external-http"><app name="writer">
<action default="true" ext="docx" name="edit" urlsrc="https://ignored.example/browser/stub/cool.html?"/>
<action ext="docx" name="view" urlsrc="https://ignored.example/browser/stub/cool.html?"/>
<action ext="txt" name="edit" urlsrc="https://ignored.example/browser/stub/cool.html?"/>
<action ext="odt" name="edit" urlsrc="https://ignored.example/browser/stub/cool.html?"/>
</app></net-zone><proof-key exponent="%s" modulus="%s"/></wopi-discovery>`, exp, mod)
}

// open is what the iframe's form POST reaches: load the file, take the lock,
// show the name and two controls.
func (s *Stub) open(w http.ResponseWriter, r *http.Request) {
	token := r.PostFormValue("access_token")
	sess := &Session{Stub: s, WOPISrc: r.URL.Query().Get("WOPISrc"), Token: token, Lock: "stub-lock"}
	var info map[string]any
	if st, err := sess.Info(&info); err != nil || st != 200 {
		http.Error(w, fmt.Sprintf("CheckFileInfo: %d %v", st, err), http.StatusBadGateway)
		return
	}
	if info["UserCanWrite"] == true {
		sess.Call("LOCK", nil)
	}
	s.mu.Lock()
	s.sessions[token] = sess
	s.mu.Unlock()
	mode := "read-only"
	if info["UserCanWrite"] == true {
		mode = "editing"
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><h1>%s</h1><p>%s as %s</p>`, html.EscapeString(fmt.Sprint(info["BaseFileName"])), mode,
		html.EscapeString(fmt.Sprint(info["UserFriendlyName"])))
	controls(w, token)
}

func controls(w io.Writer, token string) {
	fmt.Fprintf(w, `<form method=post action="/browser/stub/do"><input type=hidden name=t value="%s">
<button name=a value=save>Autosave</button> <button name=a value=exit>Close document</button></form>`, html.EscapeString(token))
}

func (s *Stub) do(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sess := s.sessions[r.PostFormValue("t")]
	s.mu.Unlock()
	if sess == nil {
		http.NotFound(w, r)
		return
	}
	switch r.PostFormValue("a") {
	case "save":
		sess.Put(Doc("autosaved "+strconv.FormatInt(time.Now().UnixNano(), 10)), false)
	case "exit":
		sess.Put(Doc("final text"), true)
		sess.Call("UNLOCK", nil)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, "<!doctype html><p>done: %s</p>", html.EscapeString(r.PostFormValue("a")))
	if r.PostFormValue("a") == "save" {
		controls(w, r.PostFormValue("t"))
	}
}

// Doc makes bytes that are not detected as text (a zip header).
func Doc(text string) []byte { return append([]byte("PK\x03\x04\x14\x00"), text...) }

// Session is one client of a WOPI host.
type Session struct {
	Stub    *Stub
	WOPISrc string
	Token   string
	Lock    string
	// NoProof leaves out the signature, BadProof signs another URL.
	NoProof, BadProof bool
	// HeaderPrefix replaces X-COOL-WOPI- (ONLYOFFICE: X-LOOL-WOPI-).
	HeaderPrefix string
}

// Do sends one WOPI request; path is appended to WOPISrc.
func (c *Session) Do(method, path string, hdr map[string]string, body []byte) (*http.Response, []byte, error) {
	u := c.WOPISrc + path + "?access_token=" + url.QueryEscape(c.Token) + "&access_token_ttl=0"
	req, err := http.NewRequest(method, u, bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if !c.NoProof {
		ticks := time.Now().UnixMicro()*10 + 621355968000000000
		signed := u
		if c.BadProof {
			signed += "x"
		}
		sum := sha256.Sum256(wopi.Message(c.Token, signed, ticks))
		sig, err := rsa.SignPKCS1v15(rand.Reader, c.Stub.Key, crypto.SHA256, sum[:])
		if err != nil {
			return nil, nil, err
		}
		req.Header.Set("X-WOPI-TimeStamp", strconv.FormatInt(ticks, 10))
		req.Header.Set("X-WOPI-Proof", base64.StdEncoding.EncodeToString(sig))
		req.Header.Set("X-WOPI-ProofOld", base64.StdEncoding.EncodeToString(sig))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return resp, b, err
}

// Info calls CheckFileInfo.
func (c *Session) Info(v any) (int, error) {
	resp, b, err := c.Do(http.MethodGet, "", nil, nil)
	if err != nil {
		return 0, err
	}
	if resp.StatusCode == 200 && v != nil {
		err = json.Unmarshal(b, v)
	}
	return resp.StatusCode, err
}

// Get fetches the file.
func (c *Session) Get() (int, []byte, error) {
	resp, b, err := c.Do(http.MethodGet, "/contents", nil, nil)
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, b, nil
}

// Call sends a lock operation; it returns the status and the X-WOPI-Lock answer.
func (c *Session) Call(op string, extra map[string]string) (int, string, error) {
	h := map[string]string{"X-WOPI-Override": op, "X-WOPI-Lock": c.Lock}
	for k, v := range extra {
		h[k] = v
	}
	resp, _, err := c.Do(http.MethodPost, "", h, nil)
	if err != nil {
		return 0, "", err
	}
	return resp.StatusCode, resp.Header.Get("X-WOPI-Lock"), nil
}

// Put saves the file as Collabora does: exit marks the save on close.
func (c *Session) Put(data []byte, exit bool) (int, string, error) {
	prefix := c.HeaderPrefix
	if prefix == "" {
		prefix = "X-COOL-WOPI-"
	}
	h := map[string]string{"X-WOPI-Override": "PUT", "X-WOPI-Lock": c.Lock,
		prefix + "IsAutosave": strconv.FormatBool(!exit), prefix + "IsExitSave": strconv.FormatBool(exit)}
	resp, _, err := c.Do(http.MethodPost, "/contents", h, data)
	if err != nil {
		return 0, "", err
	}
	return resp.StatusCode, resp.Header.Get("X-WOPI-Lock"), nil
}
