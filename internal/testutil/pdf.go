package testutil

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// PDFPage describes one page of a generated test PDF: either a text layer
// (digital PDF) or a rendered image of text (scanned page without text layer).
type PDFPage struct {
	Text  string // text layer lines (separated by \n)
	Image string // text rendered into a raster image
}

func pdfEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `(`, `\(`, `)`, `\)`)
	return r.Replace(s)
}

// RenderTextImage renders text lines as a high resolution grayscale image
// (bitmap font, upscaled) suitable for OCR.
func RenderTextImage(text string) *image.Gray {
	const scale = 6
	lines := strings.Split(text, "\n")
	face := basicfont.Face7x13
	w, h := 0, len(lines)*18+20
	for _, l := range lines {
		if n := len(l)*7 + 40; n > w {
			w = n
		}
	}
	small := image.NewGray(image.Rect(0, 0, w, h))
	draw.Draw(small, small.Bounds(), &image.Uniform{color.White}, image.Point{}, draw.Src)
	d := &font.Drawer{Dst: small, Src: image.NewUniform(color.Black), Face: face}
	for i, l := range lines {
		d.Dot = fixed.P(20, 24+i*18)
		d.DrawString(l)
	}
	big := image.NewGray(image.Rect(0, 0, w*scale, h*scale))
	for y := 0; y < h*scale; y++ {
		for x := 0; x < w*scale; x++ {
			big.SetGray(x, y, small.GrayAt(x/scale, y/scale))
		}
	}
	return big
}

// MakePDF builds a minimal valid PDF with the given pages.
func MakePDF(pages ...PDFPage) []byte {
	var objs [][]byte
	add := func(b []byte) int { objs = append(objs, b); return len(objs) }
	add(nil) // 1: catalog (filled later)
	add(nil) // 2: pages
	font := add([]byte("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>"))
	var kids []string
	for _, pg := range pages {
		var content string
		resources := fmt.Sprintf("/Font << /F1 %d 0 R >>", font)
		if pg.Image != "" {
			img := RenderTextImage(pg.Image)
			var jb bytes.Buffer
			jpeg.Encode(&jb, img, &jpeg.Options{Quality: 90})
			b := img.Bounds()
			obj := []byte(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /DCTDecode /Length %d >>\nstream\n",
				b.Dx(), b.Dy(), jb.Len()))
			obj = append(obj, jb.Bytes()...)
			obj = append(obj, []byte("\nendstream")...)
			im := add(obj)
			resources += fmt.Sprintf(" /XObject << /Im1 %d 0 R >>", im)
			// scale image to page width keeping aspect ratio
			pw := 540.0
			ph := pw * float64(b.Dy()) / float64(b.Dx())
			content = fmt.Sprintf("q %.2f 0 0 %.2f 36 %.2f cm /Im1 Do Q", pw, ph, 800-ph)
		} else {
			var sb strings.Builder
			sb.WriteString("BT /F1 14 Tf 72 760 Td 18 TL ")
			for _, l := range strings.Split(pg.Text, "\n") {
				sb.WriteString("(" + pdfEscape(l) + ") Tj T* ")
			}
			sb.WriteString("ET")
			content = sb.String()
		}
		cs := add([]byte(fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(content), content)))
		page := add([]byte(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 842] /Contents %d 0 R /Resources << %s >> >>", cs, resources)))
		kids = append(kids, fmt.Sprintf("%d 0 R", page))
	}
	objs[0] = []byte("<< /Type /Catalog /Pages 2 0 R >>")
	objs[1] = []byte(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(kids)))

	var out bytes.Buffer
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objs))
	for i, o := range objs {
		offsets[i] = out.Len()
		fmt.Fprintf(&out, "%d 0 obj\n", i+1)
		out.Write(o)
		out.WriteString("\nendobj\n")
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, off := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", off)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, xref)
	return out.Bytes()
}
