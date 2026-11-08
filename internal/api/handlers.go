package api

import (
	"net/http"
	"strings"

	"github.com/tildoc/tildoc/internal/core"
)

func (s *Server) routes(mux *http.ServeMux) {
	h := func(pattern string, handler http.Handler) { mux.Handle(pattern, handler) }

	h("GET /api/health", s.public(func(w http.ResponseWriter, r *http.Request) error {
		if err := s.Svc.DB.Ping(r.Context()); err != nil {
			return err
		}
		return writeJSON(w, 200, map[string]string{"status": "ok"})
	}))
	h("GET /api/info", s.public(s.info))
	h("GET /api/setup", s.public(s.setupStatus))
	h("POST /api/setup", s.public(s.setup))
	h("POST /api/auth/login", s.public(s.login))
	h("POST /api/auth/logout", s.public(s.logout))

	h("GET /api/me", s.authed(s.me))
	h("PATCH /api/me", s.authed(s.updateMe))
	h("POST /api/me/password", s.authed(s.changePassword))
	h("GET /api/me/access-keys", s.authed(s.listAccessKeys))
	h("POST /api/me/access-keys", s.authed(s.createAccessKey))
	h("DELETE /api/me/access-keys/{id}", s.authed(s.revokeAccessKey))
	h("GET /api/me/ssh-keys", s.authed(s.listSSHKeys))
	h("POST /api/me/ssh-keys", s.authed(s.addSSHKey))
	h("DELETE /api/me/ssh-keys/{id}", s.authed(s.deleteSSHKey))

	h("GET /api/users", s.authed(s.listUsers))
	h("POST /api/users", s.authed(s.createUser))
	h("PATCH /api/users/{id}", s.authed(s.updateUser))
	h("DELETE /api/users/{id}", s.authed(s.deleteUser))

	h("GET /api/teams", s.authed(s.listTeams))
	h("POST /api/teams", s.authed(s.createTeam))
	h("GET /api/teams/{id}", s.authed(s.getTeam))
	h("PATCH /api/teams/{id}", s.authed(s.renameTeam))
	h("DELETE /api/teams/{id}", s.authed(s.deleteTeam))
	h("GET /api/teams/{id}/members", s.authed(s.listMembers))
	h("PUT /api/teams/{id}/members/{user}", s.authed(s.setMember))
	h("DELETE /api/teams/{id}/members/{user}", s.authed(s.removeMember))

	h("GET /api/spaces", s.authed(s.listSpaces))
	h("POST /api/spaces", s.authed(s.createSpace))
	h("GET /api/spaces/{id}", s.authed(s.getSpace))
	h("PATCH /api/spaces/{id}", s.authed(s.updateSpace))
	h("DELETE /api/spaces/{id}", s.authed(s.deleteSpace))
	h("GET /api/spaces/{id}/tree", s.authed(s.spaceTree))

	h("POST /api/folders", s.authed(s.createFolder))
	h("GET /api/folders/{id}", s.authed(s.getFolder))
	h("PATCH /api/folders/{id}", s.authed(s.moveFolder))
	h("DELETE /api/folders/{id}", s.authed(s.deleteFolder))
	h("POST /api/folders/{id}/copy", s.authed(s.copyFolder))
	h("PUT /api/folders/{id}/files", s.authed(s.upload))

	h("GET /api/files/{id}", s.authed(s.getFile))
	h("PATCH /api/files/{id}", s.authed(s.moveFile))
	h("DELETE /api/files/{id}", s.authed(s.deleteFile))
	h("POST /api/files/{id}/copy", s.authed(s.copyFile))
	h("GET /api/files/{id}/content", s.authed(s.fileContent))
	h("POST /api/links", s.authed(s.addLink))

	h("GET /api/documents/{id}", s.authed(s.getDocument))
	h("GET /api/documents/{id}/content", s.authed(s.documentContent))
	h("GET /api/documents/{id}/text", s.authed(s.documentText))
	h("POST /api/documents/{id}/versions", s.authed(s.addVersion))
	h("POST /api/documents/{id}/versions/{v}/restore", s.authed(s.restoreVersion))
	h("POST /api/documents/{id}/reprocess", s.authed(s.reprocess))

	h("POST /api/bulk/move", s.authed(s.bulkMove))
	h("POST /api/bulk/delete", s.authed(s.bulkDelete))
	h("POST /api/bulk/restore-original", s.authed(s.bulkRestoreOriginal))

	h("GET /api/search", s.authed(s.search))

	h("GET /api/trash", s.authed(s.listTrash))
	h("DELETE /api/trash", s.authed(s.emptyTrash))
	h("POST /api/trash/{id}/restore", s.authed(s.restoreTrash))
	h("DELETE /api/trash/{id}", s.authed(s.purgeTrash))

	h("GET /api/imports", s.authed(s.listImports))
	h("POST /api/imports", s.authed(s.createImport))

	h("GET /api/admin/stats", s.authed(s.adminStats))
	h("POST /api/admin/reprocess", s.authed(s.adminReprocess))

	mux.Handle("/api/", s.public(func(w http.ResponseWriter, r *http.Request) error {
		return &core.Error{Kind: core.ErrNotFound, Msg: "no such endpoint"}
	}))
}

// ---------------------------------------------------------------- auth

func (s *Server) info(w http.ResponseWriter, r *http.Request) error {
	return writeJSON(w, 200, map[string]any{
		"name":       "tildoc",
		"public_url": s.Cfg.PublicURL,
		"webdav_url": s.Cfg.PublicURL + "/dav/",
		"s3_url":     s.Cfg.S3PublicURL,
		"s3_region":  s.Cfg.S3Region,
		"sftp_addr":  s.Cfg.SFTPPublicAddr,
	})
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) error {
	need, err := s.Svc.NeedsSetup(r.Context())
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]bool{"needs_setup": need})
}

type credentials struct {
	Username    string `json:"username"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) error {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		return err
	}
	if _, err := s.Svc.SetupAdmin(r.Context(), c.Username, c.Password, c.DisplayName); err != nil {
		return err
	}
	return s.startSession(w, r, c.Username, c.Password)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) error {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		return err
	}
	return s.startSession(w, r, c.Username, c.Password)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, username, password string) error {
	ip := ClientIP(r, s.Cfg.TrustedProxies)
	keys := []string{"ip:" + ip, "login:" + strings.ToLower(username)}
	if !s.Limiter.Allowed(keys...) {
		return &core.Error{Kind: core.ErrForbidden, Msg: "too many failed attempts, try again later"}
	}
	p, err := s.Svc.Authenticate(r.Context(), username, password)
	if err != nil {
		s.Limiter.Fail(keys...)
		return err
	}
	s.Limiter.Success(keys[1])
	tok, exp, err := s.Svc.CreateSession(r.Context(), p)
	if err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: tok, Path: "/", Expires: exp, HttpOnly: true,
		Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
	u, err := s.Svc.GetUser(r.Context(), p.UserID)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]any{"user": u, "token": tok, "expires_at": exp})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) error {
	if c, err := r.Cookie(SessionCookie); err == nil {
		s.Svc.DeleteSession(r.Context(), c.Value)
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		s.Svc.DeleteSession(r.Context(), strings.TrimPrefix(h, "Bearer "))
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
		Secure: s.Cfg.CookieSecure, SameSite: http.SameSiteStrictMode})
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) error {
	u, err := s.Svc.GetUser(r.Context(), principal(r).UserID)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, u)
}

func (s *Server) updateMe(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		DisplayName *string `json:"display_name"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	p := principal(r)
	u, err := s.Svc.UpdateUser(r.Context(), p, p.UserID, core.UserUpdate{DisplayName: body.DisplayName})
	if err != nil {
		return err
	}
	return writeJSON(w, 200, u)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Old string `json:"old_password"`
		New string `json:"new_password"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if err := s.Svc.ChangePassword(r.Context(), principal(r), body.Old, body.New); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listAccessKeys(w http.ResponseWriter, r *http.Request) error {
	keys, err := s.Svc.ListAccessKeys(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, keys)
}

func (s *Server) createAccessKey(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		Label    string `json:"label"`
		SpaceID  string `json:"space_id"`
		ReadOnly bool   `json:"read_only"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	k, secret, err := s.Svc.CreateAccessKey(r.Context(), principal(r), body.Label, body.SpaceID, body.ReadOnly)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, map[string]any{"key": k, "secret": secret})
}

func (s *Server) revokeAccessKey(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.RevokeAccessKey(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listSSHKeys(w http.ResponseWriter, r *http.Request) error {
	keys, err := s.Svc.ListSSHKeys(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, keys)
}

func (s *Server) addSSHKey(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		PublicKey string `json:"public_key"`
		Label     string `json:"label"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	k, err := s.Svc.AddSSHKey(r.Context(), principal(r), body.PublicKey, body.Label)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, k)
}

func (s *Server) deleteSSHKey(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteSSHKey(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------- users

func (s *Server) listUsers(w http.ResponseWriter, r *http.Request) error {
	users, err := s.Svc.ListUsers(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, users)
}

func (s *Server) createUser(w http.ResponseWriter, r *http.Request) error {
	var body struct {
		credentials
		IsAdmin bool `json:"is_admin"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	u, err := s.Svc.CreateUser(r.Context(), principal(r), body.Username, body.Password, body.DisplayName, body.IsAdmin)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, u)
}

func (s *Server) updateUser(w http.ResponseWriter, r *http.Request) error {
	var upd core.UserUpdate
	if err := readJSON(r, &upd); err != nil {
		return err
	}
	u, err := s.Svc.UpdateUser(r.Context(), principal(r), r.PathValue("id"), upd)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, u)
}

func (s *Server) deleteUser(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteUser(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------- teams

func (s *Server) listTeams(w http.ResponseWriter, r *http.Request) error {
	t, err := s.Svc.ListTeams(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, t)
}

type nameBody struct {
	Name string `json:"name"`
}

func (s *Server) createTeam(w http.ResponseWriter, r *http.Request) error {
	var b nameBody
	if err := readJSON(r, &b); err != nil {
		return err
	}
	t, err := s.Svc.CreateTeam(r.Context(), principal(r), b.Name)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, t)
}

func (s *Server) getTeam(w http.ResponseWriter, r *http.Request) error {
	t, err := s.Svc.GetTeam(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, t)
}

func (s *Server) renameTeam(w http.ResponseWriter, r *http.Request) error {
	var b nameBody
	if err := readJSON(r, &b); err != nil {
		return err
	}
	if err := s.Svc.RenameTeam(r.Context(), principal(r), r.PathValue("id"), b.Name); err != nil {
		return err
	}
	return s.getTeam(w, r)
}

func (s *Server) deleteTeam(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteTeam(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listMembers(w http.ResponseWriter, r *http.Request) error {
	m, err := s.Svc.ListMembers(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, m)
}

// resolveUser accepts a user id or username.
func (s *Server) resolveUser(r *http.Request, v string) (string, error) {
	if len(v) == 36 && strings.Count(v, "-") == 4 {
		return v, nil
	}
	u, err := s.Svc.UserByName(r.Context(), v)
	if err != nil {
		return "", err
	}
	return u.ID, nil
}

func (s *Server) setMember(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Role string `json:"role"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	uid, err := s.resolveUser(r, r.PathValue("user"))
	if err != nil {
		return err
	}
	if err := s.Svc.SetMember(r.Context(), principal(r), r.PathValue("id"), uid, b.Role); err != nil {
		return err
	}
	return s.listMembers(w, r)
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) error {
	uid, err := s.resolveUser(r, r.PathValue("user"))
	if err != nil {
		return err
	}
	if err := s.Svc.RemoveMember(r.Context(), principal(r), r.PathValue("id"), uid); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// ---------------------------------------------------------------- spaces

func (s *Server) listSpaces(w http.ResponseWriter, r *http.Request) error {
	sp, err := s.Svc.ListSpaces(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, sp)
}

func (s *Server) createSpace(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Slug   string `json:"slug"`
		Name   string `json:"name"`
		TeamID string `json:"team_id"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	sp, err := s.Svc.CreateSpace(r.Context(), principal(r), b.Slug, b.Name, b.TeamID)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, sp)
}

func (s *Server) getSpace(w http.ResponseWriter, r *http.Request) error {
	sp, err := s.Svc.GetSpace(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, sp)
}

func (s *Server) updateSpace(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Name *string `json:"name"`
		Slug *string `json:"slug"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	sp, err := s.Svc.UpdateSpace(r.Context(), principal(r), r.PathValue("id"), b.Name, b.Slug)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, sp)
}

func (s *Server) deleteSpace(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteSpace(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) spaceTree(w http.ResponseWriter, r *http.Request) error {
	sp, err := s.Svc.GetSpace(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	tree, err := s.Svc.ListFolderTree(r.Context(), principal(r), sp.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, tree)
}

// ---------------------------------------------------------------- imports, admin

func (s *Server) listImports(w http.ResponseWriter, r *http.Request) error {
	im, err := s.Svc.ListImports(r.Context(), principal(r), queryInt(r, "limit", 100))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, im)
}

func (s *Server) createImport(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		SpaceID    string `json:"space_id"`
		ClientRoot string `json:"client_root"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	im, err := s.Svc.CreateImport(r.Context(), principal(r), b.SpaceID, "web", b.ClientRoot)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, im)
}

func (s *Server) adminStats(w http.ResponseWriter, r *http.Request) error {
	st, err := s.Svc.AdminStats(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, st)
}

func (s *Server) adminReprocess(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Statuses []string `json:"statuses"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	n, err := s.Svc.ReprocessAll(r.Context(), principal(r), b.Statuses)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]int{"queued": n})
}

