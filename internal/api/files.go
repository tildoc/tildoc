package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/tildoc/tildoc/internal/core"
)

func (s *Server) createFolder(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		ParentID string `json:"parent_id"`
		Name     string `json:"name"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	f, err := s.Svc.Mkdir(r.Context(), principal(r), b.ParentID, b.Name)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, f)
}

type folderListing struct {
	Folder      *core.FolderInfo  `json:"folder"`
	Space       *core.Space       `json:"space"`
	Breadcrumbs []core.FolderInfo `json:"breadcrumbs"`
	Folders     []core.FolderInfo `json:"folders"`
	Files       []core.FileInfo   `json:"files"`
}

func (s *Server) getFolder(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	f, folders, files, err := s.Svc.ListFolder(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	sp, err := s.Svc.GetSpace(r.Context(), p, f.SpaceID)
	if err != nil {
		return err
	}
	// breadcrumbs from root to the folder
	crumbs := []core.FolderInfo{}
	cur := f
	for cur != nil {
		crumbs = append([]core.FolderInfo{*cur}, crumbs...)
		if cur.ParentID == nil {
			break
		}
		cur, err = s.Svc.GetFolder(r.Context(), p, *cur.ParentID)
		if err != nil {
			return err
		}
	}
	return writeJSON(w, 200, folderListing{Folder: f, Space: sp, Breadcrumbs: crumbs, Folders: folders, Files: files})
}

func (s *Server) moveFolder(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Name     string `json:"name"`
		ParentID string `json:"parent_id"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	f, err := s.Svc.MoveFolder(r.Context(), principal(r), r.PathValue("id"), b.ParentID, b.Name)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, f)
}

func (s *Server) deleteFolder(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteFolder(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) copyFolder(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		ParentID string `json:"parent_id"`
		Name     string `json:"name"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	f, err := s.Svc.CopyFolder(r.Context(), principal(r), r.PathValue("id"), b.ParentID, b.Name)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, f)
}

type uploadResult struct {
	File         *core.FileInfo `json:"file"`
	NewLink      bool           `json:"new_link"`
	NewVersion   bool           `json:"new_version"`
	NewDocument  bool           `json:"new_document"`
	Deduplicated bool           `json:"deduplicated"`
	Forked       bool           `json:"forked"`
}

// upload streams the raw request body into folder {id} at ?path=rel/path/name
// (intermediate folders are created, preserving dropped structures).
func (s *Server) upload(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	folder, err := s.Svc.GetFolder(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	rel := r.URL.Query().Get("path")
	relSegs, err := core.SplitPath(rel)
	if err != nil {
		return err
	}
	if len(relSegs) == 0 {
		return &core.Error{Kind: core.ErrInvalid, Msg: "query parameter path (file name) required"}
	}
	base, err := core.SplitPath(folder.Path)
	if err != nil {
		return err
	}
	if s.Cfg.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.MaxUploadBytes)
	}
	res, err := s.Svc.PutFile(r.Context(), p, folder.SpaceID, append(base, relSegs...), r.Body, core.PutOptions{
		Protocol: "web", ImportID: r.URL.Query().Get("import"), CreateParents: true,
	})
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

func (s *Server) getFile(w http.ResponseWriter, r *http.Request) error {
	f, err := s.Svc.GetFile(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, f)
}

func (s *Server) moveFile(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		Name      string `json:"name"`
		FolderID  string `json:"folder_id"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	f, err := s.Svc.MoveLink(r.Context(), principal(r), r.PathValue("id"), b.FolderID, b.Name, b.Overwrite)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, f)
}

func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.DeleteLink(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) copyFile(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		FolderID  string `json:"folder_id"`
		Name      string `json:"name"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	f, err := s.Svc.CopyLink(r.Context(), principal(r), r.PathValue("id"), b.FolderID, b.Name, b.Overwrite)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, f)
}

func (s *Server) fileContent(w http.ResponseWriter, r *http.Request) error {
	p := principal(r)
	f, err := s.Svc.GetFile(r.Context(), p, r.PathValue("id"))
	if err != nil {
		return err
	}
	rd, err := s.Svc.OpenFile(r.Context(), p, f)
	if err != nil {
		return err
	}
	defer rd.Close()
	ServeContent(w, r, rd, f.Name, f.MIME, f.MD5, f.ModifiedAt, r.URL.Query().Get("inline") == "1")
	return nil
}

func (s *Server) addLink(w http.ResponseWriter, r *http.Request) error {
	var b struct {
		DocumentID string `json:"document_id"`
		FolderID   string `json:"folder_id"`
		Name       string `json:"name"`
	}
	if err := readJSON(r, &b); err != nil {
		return err
	}
	f, err := s.Svc.AddLink(r.Context(), principal(r), b.DocumentID, b.FolderID, b.Name)
	if err != nil {
		return err
	}
	return writeJSON(w, 201, f)
}

// ---------------------------------------------------------------- documents

func (s *Server) getDocument(w http.ResponseWriter, r *http.Request) error {
	d, err := s.Svc.GetDocument(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, d)
}

func (s *Server) documentContent(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	version, _ := strconv.Atoi(q.Get("version"))
	f, ci, err := s.Svc.OpenDocument(r.Context(), principal(r), r.PathValue("id"), version, q.Get("variant"))
	if err != nil {
		return err
	}
	defer f.Close()
	name := ci.Name
	if q.Get("variant") == "searchable" {
		name = strings.TrimSuffix(name, ".pdf") + ".searchable.pdf"
	}
	ServeContent(w, r, f, name, ci.MIME, ci.MD5, ci.ModTime, q.Get("inline") == "1")
	return nil
}

func (s *Server) documentText(w http.ResponseWriter, r *http.Request) error {
	text, method, err := s.Svc.DocumentText(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]string{"text": text, "method": method})
}

func (s *Server) addVersion(w http.ResponseWriter, r *http.Request) error {
	if s.Cfg.MaxUploadBytes > 0 {
		r.Body = http.MaxBytesReader(w, r.Body, s.Cfg.MaxUploadBytes)
	}
	d, err := s.Svc.AddDocumentVersion(r.Context(), principal(r), r.PathValue("id"), r.Body, "web")
	if err != nil {
		return err
	}
	return writeJSON(w, 201, d)
}

func (s *Server) restoreVersion(w http.ResponseWriter, r *http.Request) error {
	v, err := strconv.Atoi(r.PathValue("v"))
	if err != nil {
		return &core.Error{Kind: core.ErrInvalid, Msg: "invalid version"}
	}
	d, err := s.Svc.RestoreVersion(r.Context(), principal(r), r.PathValue("id"), v)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, d)
}

func (s *Server) reprocess(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.Reprocess(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusAccepted)
	return nil
}

// ---------------------------------------------------------------- bulk

type bulkBody struct {
	LinkIDs        []string `json:"link_ids"`
	FolderIDs      []string `json:"folder_ids"`
	TargetFolderID string   `json:"target_folder_id"`
	ImportID       string   `json:"import_id"`
}

type bulkResult struct {
	Done   int          `json:"done"`
	Errors []bulkFailed `json:"errors"`
}

type bulkFailed struct {
	ID    string `json:"id"`
	Error string `json:"error"`
}

func (b *bulkResult) record(id string, err error) {
	if err != nil {
		msg := err.Error()
		var ce *core.Error
		if errors.As(err, &ce) {
			msg = ce.Msg
		}
		b.Errors = append(b.Errors, bulkFailed{ID: id, Error: msg})
		return
	}
	b.Done++
}

func (s *Server) bulkMove(w http.ResponseWriter, r *http.Request) error {
	var b bulkBody
	if err := readJSON(r, &b); err != nil {
		return err
	}
	p := principal(r)
	res := bulkResult{Errors: []bulkFailed{}}
	for _, id := range b.LinkIDs {
		_, err := s.Svc.MoveLink(r.Context(), p, id, b.TargetFolderID, "", false)
		res.record(id, err)
	}
	for _, id := range b.FolderIDs {
		_, err := s.Svc.MoveFolder(r.Context(), p, id, b.TargetFolderID, "")
		res.record(id, err)
	}
	return writeJSON(w, 200, res)
}

func (s *Server) bulkDelete(w http.ResponseWriter, r *http.Request) error {
	var b bulkBody
	if err := readJSON(r, &b); err != nil {
		return err
	}
	p := principal(r)
	res := bulkResult{Errors: []bulkFailed{}}
	for _, id := range b.LinkIDs {
		res.record(id, s.Svc.DeleteLink(r.Context(), p, id))
	}
	for _, id := range b.FolderIDs {
		res.record(id, s.Svc.DeleteFolder(r.Context(), p, id))
	}
	return writeJSON(w, 200, res)
}

func (s *Server) bulkRestoreOriginal(w http.ResponseWriter, r *http.Request) error {
	var b bulkBody
	if err := readJSON(r, &b); err != nil {
		return err
	}
	p := principal(r)
	ids := b.LinkIDs
	if b.ImportID != "" {
		more, err := s.Svc.ImportLinks(r.Context(), p, b.ImportID)
		if err != nil {
			return err
		}
		ids = append(ids, more...)
	}
	files, err := s.Svc.RestoreOriginal(r.Context(), p, ids)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, files)
}

// ---------------------------------------------------------------- search, trash

func (s *Server) search(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	res, err := s.Svc.Search(r.Context(), principal(r), core.SearchQuery{
		Q: q.Get("q"), SpaceID: q.Get("space"), FolderID: q.Get("folder"), MIME: q.Get("mime"),
		Status: q.Get("status"), Sort: q.Get("sort"), Desc: q.Get("order") == "desc",
		Limit: queryInt(r, "limit", 100), Offset: queryInt(r, "offset", 0),
	})
	if err != nil {
		return err
	}
	return writeJSON(w, 200, res)
}

func (s *Server) listTrash(w http.ResponseWriter, r *http.Request) error {
	t, err := s.Svc.ListTrash(r.Context(), principal(r), r.URL.Query().Get("space"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, t)
}

func (s *Server) emptyTrash(w http.ResponseWriter, r *http.Request) error {
	space := r.URL.Query().Get("space")
	if space == "" {
		return &core.Error{Kind: core.ErrInvalid, Msg: "query parameter space required"}
	}
	n, err := s.Svc.EmptyTrash(r.Context(), principal(r), space)
	if err != nil {
		return err
	}
	return writeJSON(w, 200, map[string]int{"purged": n})
}

func (s *Server) restoreTrash(w http.ResponseWriter, r *http.Request) error {
	d, err := s.Svc.RestoreDocument(r.Context(), principal(r), r.PathValue("id"))
	if err != nil {
		return err
	}
	return writeJSON(w, 200, d)
}

func (s *Server) purgeTrash(w http.ResponseWriter, r *http.Request) error {
	if err := s.Svc.PurgeDocument(r.Context(), principal(r), r.PathValue("id")); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
