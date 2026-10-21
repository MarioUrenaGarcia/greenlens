// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (C) 2026 Mario Ureña García

package agent

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// contentTypes covers the files of the interface. The table is fixed so
// the answer does not depend on the MIME database of the host.
var contentTypes = map[string]string{
	".html":        "text/html; charset=utf-8",
	".js":          "text/javascript; charset=utf-8",
	".css":         "text/css; charset=utf-8",
	".json":        "application/json",
	".webmanifest": "application/manifest+json",
	".svg":         "image/svg+xml",
	".png":         "image/png",
	".woff2":       "font/woff2",
	".txt":         "text/plain; charset=utf-8",
}

type asset struct {
	data, gz []byte
	etag     string
	ctype    string
}

// static serves the compiled interface from memory, compressed once at
// start. Files keep fixed names, so browsers revalidate them by ETag.
type static struct {
	files map[string]asset
}

func newStatic(fsys fs.FS) *static {
	s := &static{files: map[string]asset{}}
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ctype, ok := contentTypes[path.Ext(p)]
		if !ok {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		a := asset{data: data, etag: `"` + hex.EncodeToString(sum[:12]) + `"`, ctype: ctype}
		// woff2 and png are compressed already.
		if !strings.HasPrefix(ctype, "font/") && ctype != "image/png" {
			var b bytes.Buffer
			zw, _ := gzip.NewWriterLevel(&b, gzip.BestCompression)
			_, _ = zw.Write(data)
			if zw.Close() == nil && b.Len() < len(data) {
				a.gz = b.Bytes()
			}
		}
		s.files[p] = a
		return nil
	})
	return s
}

func (s *static) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	a, ok := s.files[p]
	if !ok {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	h := w.Header()
	h.Set("Content-Type", a.ctype)
	h.Set("ETag", a.etag)
	h.Set("Cache-Control", "no-cache")
	h.Add("Vary", "Accept-Encoding")
	if match := r.Header.Get("If-None-Match"); match == a.etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := a.data
	if a.gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		body = a.gz
	}
	_, _ = w.Write(body)
}
