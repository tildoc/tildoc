package api

import (
	"net/http"

	"github.com/tildoc/tildoc/internal/core"
)

// export streams a ZIP or bundle. Permissions are checked before the
// response starts; later errors can only abort the stream.
func (s *Server) export(kind string) func(http.ResponseWriter, *http.Request) error {
	return func(w http.ResponseWriter, r *http.Request) error {
		p := principal(r)
		scope := core.ExportScope{Kind: kind, ID: r.PathValue("id")}
		format := r.URL.Query().Get("format")
		if format == "" {
			format = "zip"
		}
		ext, ctype := ".zip", "application/zip"
		switch {
		case format == "bundle" && kind != "folder":
			ext, ctype = ".tildoc", "application/x-tar"
		case format != "zip":
			return &core.Error{Kind: core.ErrInvalid, Msg: "format must be zip or bundle (bundles: spaces and collections)"}
		}
		name, err := s.Svc.ExportName(r.Context(), p, scope, ext)
		if err != nil {
			return err
		}
		h := w.Header()
		h.Set("Content-Type", ctype)
		h.Set("Content-Disposition", contentDisposition("attachment", name))
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Cache-Control", "no-store")
		if format == "bundle" {
			err = s.Svc.ExportBundle(r.Context(), p, scope, w, nil)
		} else {
			err = s.Svc.ExportZip(r.Context(), p, scope, w, nil)
		}
		if err != nil {
			s.Log.Warn("export aborted", "scope", kind, "id", scope.ID, "err", err)
		}
		return nil
	}
}

func (s *Server) createTransfer(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	t, err := s.Svc.CreateTransfer(r.Context(), principal(r), q.Get("kind"), r.Body, core.ImportOptions{
		NewSpaceName: q.Get("space_name"), NewSpaceSlug: q.Get("space_slug"), TeamID: q.Get("team_id"), TargetFolderID: q.Get("folder_id"),
	})
	if err != nil {
		return err
	}
	return writeJSON(w, 202, t)
}

func (s *Server) listTransfers(w http.ResponseWriter, r *http.Request) error {
	t, err := s.Svc.ListTransfers(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, t)
}

func (s *Server) getTransfer(w http.ResponseWriter, r *http.Request) error {
	t, err := s.Svc.GetTransfer(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, t)
}

func (s *Server) listBackups(w http.ResponseWriter, r *http.Request) error {
	b, err := s.Svc.ListBackups(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, b)
}

func (s *Server) startBackup(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	bk, err := s.Svc.StartBackup(r.Context(), principal(r), b.Mode)
	if err != nil {
		return err
	}
	return writeJSON(w, 202, bk)
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteBackup(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) error {
	st, err := s.Svc.GetSettings(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, st)
}

func (s *Server) updateSettings(w http.ResponseWriter, r *http.Request) error {
	var u core.SettingsUpdate
	if err := readJSON(r, &u); err != nil {
		return err
	}
	st, err := s.Svc.UpdateSettings(r.Context(), principal(r), u)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, st)
}
