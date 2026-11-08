package core

import (
	"context"
	"encoding/json"
)

// PDF viewer choices.
const (
	PDFViewerPDFJS  = "pdfjs"  // in-app viewer (pdf.js), default
	PDFViewerNative = "native" // the browser's built-in viewer
)

// Prefs are per-user UI preferences.
type Prefs struct {
	PDFViewer string `json:"pdf_viewer"`
}

func (p *Prefs) normalize() {
	if p.PDFViewer != PDFViewerNative {
		p.PDFViewer = PDFViewerPDFJS
	}
}

// GetPrefs returns the preferences of a user with defaults applied.
func (s *Service) GetPrefs(ctx context.Context, userID string) (Prefs, error) {
	var raw []byte
	var p Prefs
	if err := s.DB.QueryRow(ctx, `SELECT prefs FROM users WHERE id=$1`, userID).Scan(&raw); err != nil {
		return p, err
	}
	_ = json.Unmarshal(raw, &p)
	p.normalize()
	return p, nil
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
	cur, err := s.GetPrefs(ctx, p.UserID)
	if err != nil {
		return cur, err
	}
	if upd.PDFViewer != nil {
		switch *upd.PDFViewer {
		case PDFViewerPDFJS, PDFViewerNative:
			cur.PDFViewer = *upd.PDFViewer
		default:
			return cur, errf(ErrInvalid, "pdf_viewer must be %q or %q", PDFViewerPDFJS, PDFViewerNative)
		}
	}
	raw, _ := json.Marshal(cur)
	_, err = s.DB.Exec(ctx, `UPDATE users SET prefs=$2 WHERE id=$1`, p.UserID, raw)
	return cur, err
}
