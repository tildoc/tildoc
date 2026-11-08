# Test image: Go toolchain + all document processing tools (poppler, OCR).
FROM golang:1.27-trixie
RUN apt-get update \
 && apt-get install -y --no-install-recommends poppler-utils ocrmypdf tesseract-ocr-deu tesseract-ocr-eng \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /src
