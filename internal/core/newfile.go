package core

import (
	"bytes"
	"context"
	"fmt"
	"mime"
	"path"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/tildoc/tildoc/internal/blob"
)

// Office types by extension. The container image has no /etc/mime.types, and
// office files are sniffed as plain ZIP (or nothing), so their names decide.
func init() {
	const (
		ms  = "application/vnd.openxmlformats-officedocument."
		oas = "application/vnd.oasis.opendocument."
	)
	for ext, t := range map[string]string{
		".docx": ms + "wordprocessingml.document", ".dotx": ms + "wordprocessingml.template",
		".xlsx": ms + "spreadsheetml.sheet", ".xltx": ms + "spreadsheetml.template",
		".pptx": ms + "presentationml.presentation", ".potx": ms + "presentationml.template", ".ppsx": ms + "presentationml.slideshow",
		".docm": "application/vnd.ms-word.document.macroEnabled.12", ".dotm": "application/vnd.ms-word.template.macroEnabled.12",
		".xlsm": "application/vnd.ms-excel.sheet.macroEnabled.12", ".xltm": "application/vnd.ms-excel.template.macroEnabled.12",
		".xlsb": "application/vnd.ms-excel.sheet.binary.macroEnabled.12",
		".pptm": "application/vnd.ms-powerpoint.presentation.macroEnabled.12", ".potm": "application/vnd.ms-powerpoint.template.macroEnabled.12",
		".ppsm": "application/vnd.ms-powerpoint.slideshow.macroEnabled.12",
		".doc":  "application/msword", ".xls": "application/vnd.ms-excel", ".ppt": "application/vnd.ms-powerpoint",
		".odt": oas + "text", ".ott": oas + "text-template", ".ods": oas + "spreadsheet", ".ots": oas + "spreadsheet-template",
		".odp": oas + "presentation", ".otp": oas + "presentation-template", ".odg": oas + "graphics", ".epub": "application/epub+zip",
	} {
		mime.AddExtensionType(ext, t)
	}
}

// CreateEmptyFile adds an empty file to a folder. It is always a document of
// its own: unlike an upload, it must not merge with other empty files, or
// editing one would change the others. The office server fills an empty
// office file from its template when it is opened.
func (s *Service) CreateEmptyFile(ctx context.Context, p *Principal, folderID, name string) (*FileInfo, error) {
	if err := requireSession(p); err != nil {
		return nil, err
	}
	name, err := NormalizeName(name)
	if err != nil {
		return nil, err
	}
	folder, err := s.GetFolder(ctx, p, folderID)
	if err != nil {
		return nil, err
	}
	if err := s.requireCap(ctx, s.DB, p, folder.SpaceID, CapAdd); err != nil {
		return nil, err
	}
	st, err := s.Blobs.Stage(bytes.NewReader(nil), 0)
	if err != nil {
		return nil, err
	}
	return s.createStaged(ctx, p, folder, name, st, "web", false)
}

// createStaged adds staged content as a new document of its own. With
// uniqueName an existing name gets a counter instead of an error.
func (s *Service) createStaged(ctx context.Context, p *Principal, folder *FolderInfo, name string, st *blob.Staged, source string, uniqueName bool) (*FileInfo, error) {
	c := s.claim(st)
	defer c.discardUnused()
	var linkID string
	err := s.tx(ctx, func(tx pgx.Tx) error {
		if err := c.commit(ctx, tx); err != nil {
			return err
		}
		if err := registerBlobTx(ctx, tx, st); err != nil {
			return err
		}
		if _, err := lockFolder(ctx, tx, folder.ID); err != nil {
			return err
		}
		final := name
		for i := 2; ; i++ {
			fold, link, err := child(ctx, tx, folder.ID, final)
			if err != nil {
				return err
			}
			if fold == "" && link == "" {
				break
			}
			if !uniqueName {
				return errf(ErrConflict, "%q already exists in this folder", name)
			}
			ext := path.Ext(name)
			final = fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), i, ext)
		}
		docID, err := createDocTx(ctx, tx, p, folder.SpaceID, st.SHA256, source, nil)
		if err != nil {
			return err
		}
		linkID, err = createLinkTx(ctx, tx, p, docID, folder.ID, final, nil, strings.TrimRight(folder.Path, "/")+"/"+final)
		return err
	})
	if err != nil {
		return nil, err
	}
	return scanFile(s.DB.QueryRow(ctx, fileSelect+` WHERE l.id=$1`, linkID))
}
