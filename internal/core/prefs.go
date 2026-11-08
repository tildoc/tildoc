package core

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// PDF viewer choices (users.pdf_viewer).
const (
	PDFViewerPDFJS  = "pdfjs"  // in-app viewer (pdf.js), default
	PDFViewerNative = "native" // the browser's built-in viewer
)

// Prefs are per-user UI preferences.
type Prefs struct {
	PDFViewer string `json:"pdf_viewer"`
}

// GetPrefs returns the preferences of a user.
func (s *Service) GetPrefs(ctx context.Context, userID string) (Prefs, error) {
	var p Prefs
	err := s.DB.QueryRow(ctx, `SELECT pdf_viewer FROM users WHERE id=$1`, userID).Scan(&p.PDFViewer)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, errf(ErrNotFound, "user not found")
	}
	return p, err
}

// PrefsUpdate changes individual preferences; nil fields stay unchanged.
type PrefsUpdate struct {
	PDFViewer *string `json:"pdf_viewer"`
}

// SetPrefs updates the caller's own preferences.
func (s *Service) SetPrefs(ctx context.Context, p *Principal, upd PrefsUpdate) (Prefs, error) {
	if p == nil {
		return Prefs{}, ErrUnauthorized
	}
	if upd.PDFViewer != nil {
		switch *upd.PDFViewer {
		case PDFViewerPDFJS, PDFViewerNative:
		default:
			return Prefs{}, errf(ErrInvalid, "pdf_viewer must be %q or %q", PDFViewerPDFJS, PDFViewerNative)
		}
		if _, err := s.DB.Exec(ctx, `UPDATE users SET pdf_viewer=$2 WHERE id=$1`, p.UserID, *upd.PDFViewer); err != nil {
			return Prefs{}, err
		}
	}
	return s.GetPrefs(ctx, p.UserID)
}
