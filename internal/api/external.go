package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/tildoc/tildoc/internal/core"
)

// uploadConditions reads the optional checks of an upload.
func uploadConditions(r *http.Request, opt *core.PutOptions) {
	opt.SHA256 = r.Header.Get("X-Content-SHA256")
	opt.IfMatch = r.Header.Get("If-Match")
	opt.CreateOnly = r.Header.Get("If-None-Match") == "*"
}

func (s *Server) putExternal(w http.ResponseWriter, r *http.Request) error {
	segs, err := core.SplitPath(r.URL.Query().Get("path"))
	if err != nil {
		return err
	}
	if s.Cfg.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.MaxUploadBytes)
	}
	opt := core.PutOptions{Protocol: "api"}
	uploadConditions(r, &opt)
	res, err := s.Svc.PutExternal(r.Context(), principal(r), r.PathValue("id"), r.PathValue("source"), r.PathValue("ref"), segs, r.Body, opt)
	var mbe *http.MaxBytesError
	if errors.As(err, &mbe) {
		return &core.Error{Kind: core.ErrTooLarge, Msg: "file exceeds the upload limit"}
	}
	if err != nil {
		return err
	}
	status := 200
	if res.NewLink {
		status = 201
	}
	return writeJSON(w, status, uploadResult{File: res.File, NewLink: res.NewLink, NewVersion: res.NewVersion,
		NewDocument: res.NewDocument, Deduplicated: res.Deduplicated, Forked: res.Forked})
}

func (s *Server) getExternal(w http.ResponseWriter, r *http.Request) error {
	e, err := s.Svc.GetExternal(r.Context(), principal(r), r.PathValue("id"), r.PathValue("source"), r.PathValue("ref"))
	if err != nil {
		return err
	}
	d, err := s.Svc.GetDocument(r.Context(), principal(r), e.DocumentID)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]any{"reference": e, "document": d})
}

func (s *Server) deleteExternal(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteExternal(r.Context(), principal(r), r.PathValue("id"), r.PathValue("source"), r.PathValue("ref")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) listExternal(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit")) //nolint:errcheck // not a number: the default
	items, next, err := s.Svc.ListExternal(r.Context(), principal(r), r.PathValue("id"), q.Get("source"), q.Get("cursor"), limit)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]any{"items": items, "next_cursor": next})
}
