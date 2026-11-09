package api

import (
	"net/http"

	"github.com/tildoc/tildoc/internal/core"
)

// ---------------------------------------------------------------- tags

func (s *Server) listTags(w http.ResponseWriter, r *http.Request) error {
	t, err := s.Svc.ListTags(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, t)
}

func (s *Server) createTag(w http.ResponseWriter, r *http.Request) error {
	var b struct{ Name, Color string }
	if err := readJSON(r, &b); err != nil {
		return err
	}
	t, err := s.Svc.CreateTag(r.Context(), principal(r), b.Name, b.Color)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, t)
}

func (s *Server) updateTag(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Name  *string `json:"name"`
		Color *string `json:"color"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	t, err := s.Svc.UpdateTag(r.Context(), principal(r), r.PathValue("id"), b.Name, b.Color)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, t)
}

func (s *Server) deleteTag(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteTag(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

func (s *Server) mergeTag(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Into string `json:"into"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	t, err := s.Svc.MergeTags(r.Context(), principal(r), r.PathValue("id"), b.Into)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, t)
}

// tagDocument attaches a tag by id, or by name (created when missing).
func (s *Server) tagDocument(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		TagID string `json:"tag_id"`
		Name  string `json:"name"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	p := principal(r)
	if b.TagID == "" {
		t, err := s.Svc.CreateTag(r.Context(), p, b.Name, "")
		if err != nil {
			return err
		}
		b.TagID = t.ID
	}
	if err := s.Svc.TagDocument(r.Context(), p, r.PathValue("id"), b.TagID); err != nil {
		return err
	}
	return s.getDocument(w, r)
}

func (s *Server) untagDocument(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.UntagDocument(r.Context(), principal(r), r.PathValue("id"), r.PathValue("tag")); err != nil {
		return err
	}
	return s.getDocument(w, r)
}

// ---------------------------------------------------------------- collections

func (s *Server) listCollections(w http.ResponseWriter, r *http.Request) error {
	c, err := s.Svc.ListCollections(r.Context(), principal(r))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, c)
}

func (s *Server) createCollection(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Name   string  `json:"name"`
		TeamID string  `json:"team_id"`
		TagID  *string `json:"tag_id"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	c, err := s.Svc.CreateCollection(r.Context(), principal(r), b.Name, b.TeamID, b.TagID)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, c)
}

func (s *Server) getCollection(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	c, err := s.Svc.GetCollection(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	items, err := s.Svc.CollectionItems(r.Context(), p, c.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]any{"collection": c, "items": items})
}

func (s *Server) updateCollection(w http.ResponseWriter, r *http.Request) error {
	var b core.CollectionUpdate
	if err := readJSON(r, &b); err != nil {
		return err
	}
	c, err := s.Svc.UpdateCollection(r.Context(), principal(r), r.PathValue("id"), b)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, c)
}

func (s *Server) deleteCollection(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteCollection(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}

func (s *Server) addCollectionItems(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		DocumentIDs []string `json:"document_ids"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	if err := s.Svc.AddToCollection(r.Context(), principal(r), r.PathValue("id"), b.DocumentIDs); err != nil {
		return err
	}
	return s.getCollection(w, r)
}

func (s *Server) removeCollectionItem(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.RemoveFromCollection(r.Context(), principal(r), r.PathValue("id"), []string{r.PathValue("doc")}); err != nil {
		return err
	}
	return s.getCollection(w, r)
}

// ---------------------------------------------------------------- sharing

func (s *Server) listGrants(w http.ResponseWriter, r *http.Request) error {
	g, err := s.Svc.ListGrants(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, g)
}

func (s *Server) setGrant(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		User        string   `json:"user"`
		TeamID      string   `json:"team_id"`
		Role        string   `json:"role"`
		Permissions []string `json:"permissions"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	g, err := s.Svc.SetGrant(r.Context(), principal(r), r.PathValue("id"), b.User, b.TeamID, b.Role, b.Permissions...)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, g)
}

func (s *Server) deleteGrant(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteGrant(r.Context(), principal(r), r.PathValue("id"), r.PathValue("grant")); err != nil {
		return err
	}
	w.WriteHeader(204)
	return nil
}
