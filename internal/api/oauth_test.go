package api_test

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tildoc/tildoc/internal/config"
)

// fakeIssuer is an OpenID Connect provider for one test.
type fakeIssuer struct {
	t         *testing.T
	srv       *httptest.Server
	key       *rsa.PrivateKey
	challenge string
	nonce     string
	sub       string
	aud       string
	badNonce  bool
	verified  bool
}

func (i *fakeIssuer) idToken() string {
	b64 := base64.RawURLEncoding.EncodeToString
	nonce := i.nonce
	if i.badNonce {
		nonce = "other"
	}
	head, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	body, _ := json.Marshal(map[string]any{"iss": i.srv.URL, "aud": i.aud, "sub": i.sub, "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(),
		"nonce": nonce, "email": "neu@example.org", "name": "Neue Person"})
	msg := b64(head) + "." + b64(body)
	sum := sha256.Sum256([]byte(msg))
	sig, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, sum[:])
	if err != nil {
		i.t.Fatal(err)
	}
	return msg + "." + b64(sig)
}

func newIssuer(t *testing.T) *fakeIssuer {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	i := &fakeIssuer{t: t, key: k, sub: "google-sub-1", aud: "client-1"}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"issuer": i.srv.URL, "authorization_endpoint": i.srv.URL + "/auth", "token_endpoint": i.srv.URL + "/token",
			"jwks_uri": i.srv.URL + "/jwks", "id_token_signing_alg_values_supported": []string{"RS256"}})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		b64 := base64.RawURLEncoding.EncodeToString
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "alg": "RS256", "use": "sig",
			"n": b64(k.N.Bytes()), "e": b64(big.NewInt(int64(k.E)).Bytes())}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		i.verified = base64.RawURLEncoding.EncodeToString(sum[:]) == i.challenge
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": i.idToken()})
	})
	i.srv = httptest.NewServer(mux)
	t.Cleanup(i.srv.Close)
	return i
}

// signIn runs the browser's part; returns where tildoc sent it in the end.
func (i *fakeIssuer) signIn(t *testing.T, c *client, tamperState bool) string {
	t.Helper()
	noFollow := *c.http
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := noFollow.Get(c.base + "/api/v1/auth/google/start")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	u, err := url.Parse(resp.Header.Get("Location"))
	if err != nil || !strings.HasPrefix(resp.Header.Get("Location"), i.srv.URL+"/auth") {
		t.Fatalf("redirect %q", resp.Header.Get("Location"))
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("nonce") == "" || !strings.HasSuffix(q.Get("redirect_uri"), "/api/v1/auth/google/callback") {
		t.Fatalf("auth request %v", q)
	}
	i.challenge, i.nonce = q.Get("code_challenge"), q.Get("nonce")
	state := q.Get("state")
	if tamperState {
		state = "forged"
	}
	resp, err = noFollow.Get(c.base + "/api/v1/auth/google/callback?code=abc&state=" + url.QueryEscape(state))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.Header.Get("Location")
}

func TestGoogleLogin(t *testing.T) {
	iss := newIssuer(t)
	ts, srv := liveServerWith(t, "", func(c *config.Config) {
		c.GoogleClientID, c.GoogleClientSecret, c.GoogleIssuer = "client-1", "secret", iss.srv.URL
	})
	srv.Cfg.PublicURL = ts.URL
	alice, _ := aliceClient(t, ts.URL) // also sets the server up
	admin := newClient(t, ts.URL)
	admin.ok("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": "supersecret"}, nil)

	var providers []struct{ ID, Name string }
	newClient(t, ts.URL).ok("GET", "/api/v1/auth/providers", nil, &providers)
	if len(providers) != 1 || providers[0].ID != "google" {
		t.Fatalf("providers %+v", providers)
	}

	// a forged state is refused, and a callback without the browser's cookie too
	b := newClient(t, ts.URL)
	if got := iss.signIn(t, b, true); got != "/login?error=expired" {
		t.Fatalf("forged state: %q", got)
	}
	// a token with the wrong nonce
	iss.badNonce = true
	if got := iss.signIn(t, b, false); got != "/login?error=provider" {
		t.Fatalf("bad nonce: %q", got)
	}
	iss.badNonce = false
	// and one for another application
	iss.aud = "someone-else"
	if got := iss.signIn(t, b, false); got != "/login?error=provider" {
		t.Fatalf("wrong audience: %q", got)
	}
	iss.aud = "client-1"

	// first login waits for approval; no session
	if got := iss.signIn(t, b, false); got != "/login?pending=1" {
		t.Fatalf("first login: %q", got)
	}
	if !iss.verified {
		t.Fatal("PKCE verifier does not match the challenge")
	}
	expect(t, b.do("GET", "/api/v1/me", nil, nil), 401, "pending has no session")

	var users []struct {
		ID, Username, Status string
	}
	admin.ok("GET", "/api/v1/users", nil, &users)
	id := ""
	for _, u := range users {
		if u.Status == "pending" {
			id = u.ID
		}
	}
	if id == "" {
		t.Fatalf("users %+v", users)
	}
	expect(t, alice.do("POST", "/api/v1/users/"+id+"/approve", map[string]any{}, nil), 403, "not an admin")
	admin.ok("POST", "/api/v1/users/"+id+"/approve", map[string]any{}, nil)

	// approved: signed in
	if got := iss.signIn(t, b, false); got != "/" {
		t.Fatalf("after approval: %q", got)
	}
	var me struct{ Username string }
	b.ok("GET", "/api/v1/me", nil, &me)
	if me.Username == "" {
		t.Fatal("no session after approval")
	}
}

func TestGoogleOffWithoutCredentials(t *testing.T) {
	ts, _ := liveServer(t, "")
	var providers []map[string]string
	newClient(t, ts.URL).ok("GET", "/api/v1/auth/providers", nil, &providers)
	if len(providers) != 0 {
		t.Fatalf("providers %+v", providers)
	}
	resp, err := http.Get(ts.URL + "/api/v1/auth/google/start")
	if err != nil || resp.StatusCode != 404 {
		t.Fatalf("%v %v", resp, err)
	}
}

func TestDropInOverHTTP(t *testing.T) {
	ts, _ := liveServer(t, "")
	alice, sp := aliceClient(t, ts.URL)
	alice.ok("PATCH", "/api/v1/spaces/"+sp.ID+"/settings", map[string]any{"allow_drop_in": true}, nil)
	var r struct {
		Token, URL string
	}
	alice.ok("POST", "/api/v1/me/drop-in-keys", map[string]any{"label": "scan", "space_id": sp.ID}, &r)
	if !strings.HasSuffix(r.URL, "/api/v1/drop/") {
		t.Fatalf("url %q", r.URL)
	}
	put := func(path, token, body string) int {
		req, _ := http.NewRequest("PUT", ts.URL+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	expect(t, put("/api/v1/drop/hello%20world.txt", r.Token, "from curl"), 204, "drop")
	expect(t, put("/api/v1/drop/x.txt", "", "x"), 401, "no token")
	expect(t, put("/api/v1/drop/x.txt", "tildoc_nonsense_x", "x"), 401, "bad token")
	var list struct {
		Files []struct{ Name string }
	}
	alice.ok("GET", "/api/v1/folders/"+sp.RootFolderID, nil, &list)
	if len(list.Files) != 1 || list.Files[0].Name != "hello world.txt" {
		t.Fatalf("files %+v", list.Files)
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/v1/spaces", nil)
	req.Header.Set("Authorization", "Bearer "+r.Token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var seen []any
	json.NewDecoder(resp.Body).Decode(&seen)
	if len(seen) != 0 {
		t.Fatalf("a Drop-In key lists spaces: %v", seen)
	}
}
