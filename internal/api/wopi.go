package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/tildoc/tildoc/internal/core"
	"github.com/tildoc/tildoc/internal/wopi"
)

// officeRoutes registers the UI's calls and the WOPI host Collabora calls.
func (s *Server) officeRoutes(h func(string, http.Handler)) {
	h("GET /api/v1/office", s.authed(s.officeInfo))
	h("POST /api/v1/documents/{id}/office", s.authed(s.officeOpen))
	h("GET /wopi/files/{id}", s.wopiAuth(s.wopiFileInfo))
	h("GET /wopi/files/{id}/contents", s.wopiAuth(s.wopiGetFile))
	h("POST /wopi/files/{id}/contents", s.wopiAuth(s.wopiPutFile))
	h("POST /wopi/files/{id}", s.wopiAuth(s.wopiLock))
}

func newOfficeClient(internalURL string) *wopi.Client {
	if internalURL == "" {
		return nil
	}
	return wopi.NewClient(internalURL)
}

// officeInfo tells the UI which file extensions the office server opens,
// edits and creates.
func (s *Server) officeInfo(w http.ResponseWriter, r *http.Request) error {
	info := map[string][]string{"extensions": {}, "editable": {}, "create": {}}
	if s.Office != nil {
		if d, err := s.Office.Get(r.Context()); err == nil {
			info = map[string][]string{"extensions": d.Extensions(), "editable": d.Editable(), "create": d.Creatable()}
		} else {
			s.Log.Warn("office discovery", "err", err)
		}
	}
	return writeJSON(w, 200, info)
}

// officeOpen issues a token and the URL the browser posts it to.
func (s *Server) officeOpen(w http.ResponseWriter, r *http.Request) error {
	if s.Office == nil {
		return core.ErrNotFound
	}
	d, err := s.Office.Get(r.Context())
	if err != nil {
		return &core.Error{Kind: core.ErrNotFound, Msg: "the office server is not available"}
	}
	id := r.PathValue("id")
	token, expires, doc, canWrite, err := s.Svc.CreateOfficeToken(r.Context(), principal(r), id)
	if err != nil {
		return err
	}
	action, ok := d.ActionPath(strings.TrimPrefix(path.Ext(doc.Name), "."), canWrite, doc.Size == 0)
	if !ok {
		return &core.Error{Kind: core.ErrInvalid, Msg: "this file type does not open in Office"}
	}
	q := url.Values{"WOPISrc": {s.Cfg.WOPIURL + "/wopi/files/" + id}}
	return writeJSON(w, 200, map[string]any{
		"url":              s.Cfg.OfficeURL + action + "?" + q.Encode(),
		"access_token":     token,
		"access_token_ttl": expires.UnixMilli(),
	})
}

// wopiAuth authenticates Collabora: the signed proof when the server
// publishes a key, then the token, which is bound to one document.
func (s *Server) wopiAuth(h handlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Office == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		token := r.URL.Query().Get("access_token")
		if err := s.wopiVerify(r, token); err != nil {
			s.Log.Warn("wopi request rejected", "path", r.URL.Path, "err", err)
			s.writeError(w, r, core.ErrUnauthorized)
			return
		}
		p, err := s.Svc.OfficePrincipal(r.Context(), token, r.PathValue("id"))
		if err != nil {
			s.writeError(w, r, err)
			return
		}
		if err := h(w, r.WithContext(context.WithValue(r.Context(), principalKey, p))); err != nil {
			var lc *core.OfficeLockConflict
			if errors.As(err, &lc) {
				w.Header().Set("X-WOPI-Lock", lc.Current)
			}
			s.writeError(w, r, err)
		}
	})
}

func (s *Server) wopiVerify(r *http.Request, token string) error {
	d, err := s.Office.Get(r.Context())
	if err != nil {
		return err
	}
	check := func(d *wopi.Discovery) error {
		return d.Verify(token, s.Cfg.WOPIURL+r.URL.RequestURI(), r.Header.Get("X-WOPI-TimeStamp"),
			r.Header.Get("X-WOPI-Proof"), r.Header.Get("X-WOPI-ProofOld"), time.Now())
	}
	if err := check(d); err == nil {
		return nil
	}
	if d, err = s.Office.Refresh(r.Context()); err != nil { // the key may have changed
		return err
	}
	return check(d)
}

func wopiTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func (s *Server) wopiFileInfo(w http.ResponseWriter, r *http.Request) error {
	f, err := s.Svc.OfficeFileInfo(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]any{
		"BaseFileName":            f.Name,
		"OwnerId":                 f.OwnerID,
		"Size":                    f.Size,
		"Version":                 f.Version,
		"UserId":                  f.UserID,
		"UserFriendlyName":        f.UserName,
		"UserCanWrite":            f.CanWrite,
		"LastModifiedTime":        wopiTime(f.Modified),
		"SupportsLocks":           true,
		"SupportsGetLock":         true,
		"SupportsUpdate":          true,
		"SupportsRename":          false,
		"UserCanRename":           false,
		"UserCanNotWriteRelative": true, // no "Save as": files are managed in tildoc
		"EnableShare":             false,
		"DisablePrint":            !f.CanDownload,
		"DisableExport":           !f.CanDownload,
		"DisableCopy":             !f.CanDownload,
		"HidePrintOption":         !f.CanDownload,
		"HideExportOption":        !f.CanDownload,
		"HideSaveOption":          !f.CanDownload,
	})
}

func (s *Server) wopiGetFile(w http.ResponseWriter, r *http.Request) error {
	f, err := s.Svc.OpenOfficeFile(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	defer f.Close()
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, "", time.Time{}, f)
	return nil
}

func (s *Server) wopiPutFile(w http.ResponseWriter, r *http.Request) error {
	if r.Header.Get("X-WOPI-Override") != "PUT" {
		return &core.Error{Kind: core.ErrInvalid, Msg: "unsupported operation"}
	}
	// Collabora sends X-COOL-, ONLYOFFICE the older X-LOOL- prefix
	exit := strings.EqualFold(r.Header.Get("X-COOL-WOPI-IsExitSave"), "true") || strings.EqualFold(r.Header.Get("X-LOOL-WOPI-IsExitSave"), "true")
	saved, err := s.Svc.SaveOffice(r.Context(), principal(r), r.PathValue("id"), r.Header.Get("X-WOPI-Lock"), r.Body, exit)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]any{"LastModifiedTime": wopiTime(saved)})
}

func (s *Server) wopiLock(w http.ResponseWriter, r *http.Request) error {
	ctx, p, id, lock := r.Context(), principal(r), r.PathValue("id"), r.Header.Get("X-WOPI-Lock")
	var err error
	switch r.Header.Get("X-WOPI-Override") {
	case "LOCK":
		err = s.Svc.LockOffice(ctx, p, id, lock, r.Header.Get("X-WOPI-OldLock"))
	case "REFRESH_LOCK":
		err = s.Svc.RefreshOfficeLock(ctx, p, id, lock)
	case "UNLOCK":
		err = s.Svc.UnlockOffice(ctx, p, id, lock)
	case "GET_LOCK":
		var cur string
		if cur, err = s.Svc.OfficeLockOf(ctx, p, id); err == nil {
			w.Header().Set("X-WOPI-Lock", cur)
		}
	default:
		w.WriteHeader(http.StatusNotImplemented)
		return nil
	}
	if err != nil {
		return err
	}
	w.WriteHeader(http.StatusOK)
	return nil
}
