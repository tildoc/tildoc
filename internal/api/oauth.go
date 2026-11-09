package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/url"

	"github.com/tildoc/tildoc/internal/oidc"
)

const oauthCookie = "tildoc_oauth"

func (s *Server) oauthRoutes(h func(string, http.Handler)) {
	h("GET /api/v1/auth/providers", s.public(s.authProviders))
	h("GET /api/v1/auth/google/start", s.public(s.googleStart))
	h("GET /api/v1/auth/google/callback", s.public(s.googleCallback))
}

// google returns the configured provider, discovering it on first use.
func (s *Server) google(ctx context.Context) (*oidc.Provider, error) {
	s.googleMu.Lock()
	defer s.googleMu.Unlock()
	if s.googleProvider != nil {
		return s.googleProvider, nil
	}
	p, err := oidc.New(ctx, "google", "Google", s.Cfg.GoogleIssuer, s.Cfg.GoogleClientID, s.Cfg.GoogleClientSecret, s.Cfg.PublicURL+"/api/v1/auth/google/callback")
	if err != nil {
		return nil, err
	}
	s.googleProvider = p
	return p, nil
}

func (s *Server) authProviders(w http.ResponseWriter, r *http.Request) error {
	out := []map[string]string{}
	if s.Cfg.GoogleClientID != "" {
		out = append(out, map[string]string{"id": "google", "name": "Google"})
	}
	return writeJSON(w, 200, out)
}

func randomString() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func loginResult(w http.ResponseWriter, r *http.Request, query string) {
	http.Redirect(w, r, "/login?"+query, http.StatusFound)
}

// googleStart sends the browser to Google. The state also goes into a
// cookie, so a callback only counts in the browser that started the sign-in.
func (s *Server) googleStart(w http.ResponseWriter, r *http.Request) error {
	if s.Cfg.GoogleClientID == "" {
		http.NotFound(w, r)
		return nil
	}
	p, err := s.google(r.Context())
	if err != nil {
		s.Log.Warn("google login", "err", err)
		loginResult(w, r, "error=provider")
		return nil
	}
	state, nonce, verifier := randomString(), randomString(), oidc.NewVerifier()
	if err := s.Svc.StartOAuthLogin(r.Context(), p.ID, state, nonce, verifier); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: state, Path: "/api/v1/auth/google", MaxAge: 600, HttpOnly: true,
		Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode}) // must survive the redirect back from Google
	http.Redirect(w, r, p.AuthURL(state, nonce, verifier), http.StatusFound)
	return nil
}

func (s *Server) googleCallback(w http.ResponseWriter, r *http.Request) error {
	if s.Cfg.GoogleClientID == "" {
		http.NotFound(w, r)
		return nil
	}
	http.SetCookie(w, &http.Cookie{Name: oauthCookie, Value: "", Path: "/api/v1/auth/google", MaxAge: -1, HttpOnly: true, Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteLaxMode})
	q := r.URL.Query()
	c, err := r.Cookie(oauthCookie)
	if err != nil || c.Value == "" || c.Value != q.Get("state") {
		loginResult(w, r, "error=expired")
		return nil
	}
	if q.Get("error") != "" || q.Get("code") == "" {
		loginResult(w, r, "error=cancelled")
		return nil
	}
	ctx := r.Context()
	p, err := s.google(ctx)
	if err != nil {
		s.Log.Warn("google login", "err", err)
		loginResult(w, r, "error=provider")
		return nil
	}
	login, err := s.Svc.FinishOAuthLogin(ctx, p.ID, q.Get("state"))
	if err != nil {
		loginResult(w, r, "error=expired")
		return nil
	}
	claims, err := p.Exchange(ctx, q.Get("code"), login.Verifier, login.Nonce)
	if err != nil {
		s.Log.Warn("google login rejected", "err", err)
		loginResult(w, r, "error=provider")
		return nil
	}
	res, err := s.Svc.LoginExternal(ctx, p.ID, claims.Issuer, claims.Subject, claims.Email, claims.Name)
	if err != nil {
		s.Log.Warn("google login rejected", "err", err)
		loginResult(w, r, "error="+url.QueryEscape("disabled"))
		return nil
	}
	if res.Pending {
		loginResult(w, r, "pending=1")
		return nil
	}
	tok, exp, err := s.Svc.CreateSession(ctx, res.Principal)
	if err != nil {
		return err
	}
	s.setSessionCookie(w, tok, exp)
	http.Redirect(w, r, "/", http.StatusFound)
	return nil
}
