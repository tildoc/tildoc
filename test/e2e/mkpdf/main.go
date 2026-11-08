// Command mkpdf writes test PDFs (digital, scanned, scanned with a foreign
// OCR layer) into a directory.
package main

import (
	"os"
	"path/filepath"

	"github.com/tildoc/tildoc/internal/testutil"
)

func main() {
	dir := os.Args[1]
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "digital.pdf"), testutil.MakePDF(
		testutil.PDFPage{Text: "Mietvertrag Wohnung Altona\nVermieter und Mieter vereinbaren"}), 0o644)
	os.WriteFile(filepath.Join(dir, "scanned.pdf"), testutil.MakePDF(
		testutil.PDFPage{Image: "TILDOC SCANNED RECEIPT\nTOTAL AMOUNT 4711 EUR"}), 0o644)
	os.WriteFile(filepath.Join(dir, "foreign-ocr.pdf"), testutil.MakePDF(
		testutil.PDFPage{Image: "INVOICE NUMBER 8150", OCRText: "Quittung Bravo"}), 0o644)
}
