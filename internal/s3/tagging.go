package s3

import (
	"encoding/xml"
	"io"
	"net/http"
	"strings"
)

// Object tagging maps S3 tags onto tildoc tags of the object's document:
// the tag key is the tag name, values are not stored (tildoc tags are
// plain labels). PUT replaces the document's tags and creates unknown tags.

type s3Tag struct {
	Key   string `xml:"Key"`
	Value string `xml:"Value"`
}

type tagging struct {
	XMLName xml.Name `xml:"Tagging"`
	Xmlns   string   `xml:"xmlns,attr,omitempty"`
	TagSet  []s3Tag  `xml:"TagSet>Tag"`
}

func (s *Server) taggedObject(r *http.Request, bucket, key string) (string, []s3Tag, error) {
	st := state(r)
	node, err := s.FS.Stat(r.Context(), st.p, vpath(bucket, key))
	if err != nil {
		return "", nil, err
	}
	if node.IsDir() || strings.HasSuffix(key, "/") {
		return "", nil, errNoSuchKey()
	}
	tags := []s3Tag{}
	for _, t := range node.File.Tags {
		tags = append(tags, s3Tag{Key: t.Name})
	}
	return node.File.DocumentID, tags, nil
}

func (s *Server) getTagging(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	_, tags, err := s.taggedObject(r, bucket, key)
	if err != nil {
		return err
	}
	return writeXML(w, 200, tagging{Xmlns: xmlns, TagSet: tags})
}

func (s *Server) putTagging(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	st := state(r)
	docID, _, err := s.taggedObject(r, bucket, key)
	if err != nil {
		return err
	}
	body, _, berr := bodyReader(r, st.payload, st.ai, st.secret)
	if berr != nil {
		return berr
	}
	var req tagging
	if err := xml.NewDecoder(io.LimitReader(body, 1<<20)).Decode(&req); err != nil || len(req.TagSet) > 50 {
		return errMalformedXML()
	}
	names := make([]string, 0, len(req.TagSet))
	for _, t := range req.TagSet {
		names = append(names, t.Key)
	}
	if err := s.FS.Svc.SetDocumentTagNames(r.Context(), st.p, docID, names); err != nil {
		return err
	}
	w.WriteHeader(http.StatusOK)
	return nil
}

func (s *Server) deleteTagging(w http.ResponseWriter, r *http.Request, bucket, key string) error {
	st := state(r)
	docID, _, err := s.taggedObject(r, bucket, key)
	if err != nil {
		return err
	}
	if err := s.FS.Svc.SetDocumentTagNames(r.Context(), st.p, docID, nil); err != nil {
		return err
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}
