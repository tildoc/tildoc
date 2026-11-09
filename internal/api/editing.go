package api

import (
	"net/http"

	"github.com/tildoc/tildoc/internal/core"
)

// The web editor's lease on a document: POST takes it (409 + the current
// lease when someone else edits), PUT renews it (heartbeat), DELETE frees it.
func (s *Server) acquireEditLease(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		ClientID string `json:"client_id"`
		Takeover bool   `json:"takeover"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	l, err := s.Svc.AcquireEditLease(r.Context(), principal(r), r.PathValue("id"), b.ClientID, b.Takeover)
	if err != nil && l != nil {
		status, code := StatusFor(err)
		return writeJSON(w, status, map[string]any{"error": err.Error(), "code": code, "lease": l})
	}
	if err != nil {
		return err
	}
	return writeJSON(w, 200, l)
}

func (s *Server) renewEditLease(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		ClientID string `json:"client_id"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	if err := s.Svc.RenewEditLease(r.Context(), principal(r), r.PathValue("id"), b.ClientID); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) releaseEditLease(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.ReleaseEditLease(r.Context(), principal(r), r.PathValue("id"), r.URL.Query().Get("client")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// saveText stores the editor's text (raw UTF-8 body) as a new version.
func (s *Server) saveText(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, core.MaxEditBytes+1)
	d, err := s.Svc.SaveText(r.Context(), principal(r), r.PathValue("id"), r.URL.Query().Get("client"), r.Body)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, d)
}
