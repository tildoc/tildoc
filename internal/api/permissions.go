package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/tildoc/tildoc/internal/core"
)

type grantBody struct {
	User        string   `json:"user"`
	TeamID      string   `json:"team_id"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

func (s *Server) permissionCatalogue(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, 200, core.Catalogue())
}

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) error {
	roles, err := s.Svc.ListRoles(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, roles)
}

func (s *Server) createRole(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Name        string   `json:"name"`
		Scope       string   `json:"scope"`
		Permissions []string `json:"permissions"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	role, err := s.Svc.CreateRole(r.Context(), principal(r), b.Name, b.Scope, b.Permissions)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, role)
}

func (s *Server) updateRole(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Name        *string  `json:"name"`
		Permissions []string `json:"permissions"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	role, err := s.Svc.UpdateRole(r.Context(), principal(r), r.PathValue("id"), b.Name, b.Permissions)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, role)
}

func (s *Server) deleteRole(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteRole(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) transferSpace(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		User     string `json:"user"`
		TeamID   string `json:"team_id"`
		KeepRole string `json:"keep_role"`
		Confirm  string `json:"confirm"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	if err := s.Svc.TransferSpace(r.Context(), principal(r), r.PathValue("id"), b.User, b.TeamID, b.KeepRole, b.Confirm); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) updateSpaceSettings(w http.ResponseWriter, r *http.Request) error {
	var st core.SpaceSettings
	if err := readJSON(r, &st); err != nil {
		return err
	}
	sp, err := s.Svc.UpdateSpaceSettings(r.Context(), principal(r), r.PathValue("id"), st)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, sp)
}

func (s *Server) listDocGrants(w http.ResponseWriter, r *http.Request) error {
	g, err := s.Svc.ListDocumentGrants(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, g)
}

func (s *Server) setDocGrant(w http.ResponseWriter, r *http.Request) error {
	var b grantBody
	if err := readJSON(r, &b); err != nil {
		return err
	}
	g, err := s.Svc.SetDocumentGrant(r.Context(), principal(r), r.PathValue("id"), b.User, b.TeamID, b.Role, b.Permissions...)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, g)
}

func (s *Server) deleteDocGrant(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteDocumentGrant(r.Context(), principal(r), r.PathValue("id"), r.PathValue("grant")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) sharedFiles(w http.ResponseWriter, r *http.Request) error {
	files, err := s.Svc.ListSharedFiles(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, files)
}

func (s *Server) listInstanceGrants(w http.ResponseWriter, r *http.Request) error {
	g, err := s.Svc.ListInstanceGrants(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, g)
}

func (s *Server) setInstanceGrant(w http.ResponseWriter, r *http.Request) error {
	var b grantBody
	if err := readJSON(r, &b); err != nil {
		return err
	}
	g, err := s.Svc.SetInstanceGrant(r.Context(), principal(r), b.User, b.TeamID, b.Role, b.Permissions...)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, g)
}

func (s *Server) deleteInstanceGrant(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteInstanceGrant(r.Context(), principal(r), r.PathValue("grant")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listAPIKeys(w http.ResponseWriter, r *http.Request) error {
	k, err := s.Svc.ListAPIKeys(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, k)
}

func (s *Server) createAPIKey(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Label       string     `json:"label"`
		Permissions []string   `json:"permissions"`
		SpaceIDs    []string   `json:"space_ids"`
		ExpiresAt   *time.Time `json:"expires_at"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	k, token, err := s.Svc.CreateAPIKey(r.Context(), principal(r), b.Label, b.Permissions, b.SpaceIDs, b.ExpiresAt)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, map[string]any{"key": k, "token": token})
}

func (s *Server) revokeAPIKey(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.RevokeAPIKey(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listSpaceLinks(w http.ResponseWriter, r *http.Request) error {
	l, err := s.Svc.ListShareLinks(r.Context(), principal(r), r.PathValue("id"), "")
	if err != nil {
		return err
	}
	return writeJSON(w, 200, l)
}

func (s *Server) listDocLinks(w http.ResponseWriter, r *http.Request) error {
	l, err := s.Svc.ListShareLinks(r.Context(), principal(r), "", r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, l)
}

// createLink returns the token once. The URL's fragment carries it, so it
// never reaches server logs or Referer headers.
func (s *Server) createLink(w http.ResponseWriter, r *http.Request) error {
	var n core.NewShareLink
	if err := readJSON(r, &n); err != nil {
		return err
	}
	l, token, err := s.Svc.CreateShareLink(r.Context(), principal(r), n)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, map[string]any{"link": l, "url": s.Cfg.PublicURL + "/s#" + token})
}

func (s *Server) revokeLink(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.RevokeShareLink(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ------------------------------------------------------------ opened links

func shareCookie(linkID string) string { return "tildoc_share_" + strings.ReplaceAll(linkID, "-", "") }

func (s *Server) publicRoutes(h func(string, http.Handler)) {
	h("POST /api/public/open", s.public(s.shareOpen))
	h("GET /api/public/{link}", s.shared(s.shareInfo))
	h("GET /api/public/{link}/folders/{id}", s.shared(s.shareFolder))
	h("GET /api/public/{link}/content", s.shared(s.shareContent))
	h("PUT /api/public/{link}/upload", s.shared(s.shareUpload))
}

// user returns the signed-in user of a request, if any.
func (s *Server) user(r *http.Request) *core.Principal {
	p, _, err := s.authn(r)
	if err != nil {
		return nil
	}
	return p
}

// shareOpen exchanges the token from the URL fragment for a cookie bound to
// this one link. Wrong passwords are rate limited like logins.
func (s *Server) shareOpen(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("X-Robots-Tag", "noindex")
	var b struct {
		Token    string `json:"token"`
		Password string `json:"password"`
		LinkID   string `json:"link_id"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	ip := "share:" + ClientIP(r, s.Cfg.TrustedProxies)
	if !s.Limiter.Allowed(ip) {
		return writeJSON(w, http.StatusTooManyRequests, apiError{Error: "too many attempts, try again later", Code: "too_many"})
	}
	existing := ""
	if b.LinkID != "" {
		if c, err := r.Cookie(shareCookie(b.LinkID)); err == nil {
			existing = c.Value
		}
	}
	a, sess, err := s.Svc.OpenShare(r.Context(), b.Token, b.Password, existing, s.user(r))
	switch {
	case errors.Is(err, core.ErrSharePassword):
		if b.Password != "" {
			s.Limiter.Fail(ip)
		}
		return writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "password required", "code": "password", "link_id": a.Link.ID})
	case errors.Is(err, core.ErrShareLogin):
		return writeJSON(w, http.StatusUnauthorized, map[string]any{"error": err.Error(), "code": "login"})
	case err != nil:
		if core.KindOf(err) == core.ErrNotFound {
			s.Limiter.Fail(ip) // guessing tokens
		}
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: shareCookie(a.Link.ID), Value: sess, Path: "/api/public/" + a.Link.ID, MaxAge: int(core.ShareSessionTTL.Seconds()),
		HttpOnly: true, Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
	info, err := s.Svc.ShareInfo(r.Context(), a)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, info)
}

type shareHandler func(w http.ResponseWriter, r *http.Request, a *core.ShareAccess) error

// shared authenticates a request by the cookie of its link.
func (s *Server) shared(h shareHandler) http.Handler {
	return s.public(func(w http.ResponseWriter, r *http.Request) error {
		w.Header().Set("X-Robots-Tag", "noindex")
		id := r.PathValue("link")
		if !safeMethod(r.Method) && r.Header.Get(CSRFHeader) == "" {
			return &core.Error{Kind: core.ErrForbidden, Msg: "missing " + CSRFHeader + " header"}
		}
		c, err := r.Cookie(shareCookie(id))
		if err != nil {
			return &core.Error{Kind: core.ErrNotFound, Msg: "this link is no longer available"}
		}
		a, err := s.Svc.ResolveShare(r.Context(), id, c.Value, s.user(r))
		if err != nil {
			return err
		}
		return h(w, r, a)
	})
}

func (s *Server) shareInfo(w http.ResponseWriter, r *http.Request, a *core.ShareAccess) error {
	info, err := s.Svc.ShareInfo(r.Context(), a)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, info)
}

func (s *Server) shareFolder(w http.ResponseWriter, r *http.Request, a *core.ShareAccess) error {
	l, err := s.Svc.ShareFolder(r.Context(), a, r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, l)
}

func (s *Server) shareContent(w http.ResponseWriter, r *http.Request, a *core.ShareAccess) error {
	inline := r.URL.Query().Get("inline") == "1"
	f, ci, err := s.Svc.OpenShareContent(r.Context(), a, r.URL.Query().Get("file"), inline)
	if err != nil {
		return err
	}
	defer f.Close()
	disp := Attachment
	if inline {
		disp = Inline
	}
	ServeContent(w, r, f, ci.Name, ci.MIME, ci.MD5, ci.ModTime, disp)
	return nil
}

func (s *Server) shareUpload(w http.ResponseWriter, r *http.Request, a *core.ShareAccess) error {
	if s.Cfg.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.MaxUploadBytes)
	}
	if err := s.Svc.ShareUpload(r.Context(), a, r.URL.Query().Get("name"), r.Body); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
