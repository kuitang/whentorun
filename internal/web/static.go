package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"
)

func init() {
	// woff2 is not in every system mime table.
	_ = mime.AddExtensionType(".woff2", "font/woff2")
}

// hashedAssets serves /static/ files under content-hashed names computed at
// startup ("css/tokens.css" → "css/tokens-8f14e45f9c.css") so responses can
// be immutable; the plain name still works (no-cache) as a dev fallback.
type hashedAssets struct {
	fsys   fs.FS
	hashed map[string]string // real name → hashed name
	real   map[string]string // hashed name → real name
}

func newHashedAssets(fsys fs.FS) *hashedAssets {
	a := &hashedAssets{fsys: fsys, hashed: map[string]string{}, real: map[string]string{}}
	if fsys == nil {
		return a
	}
	_ = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(data)
		h := hex.EncodeToString(sum[:])[:10]
		ext := path.Ext(p)
		hp := strings.TrimSuffix(p, ext) + "-" + h + ext
		a.hashed[p] = hp
		a.real[hp] = p
		return nil
	})
	return a
}

// URL returns the hashed /static/ URL for a real asset name, or the plain
// URL when the asset is unknown (missing file: the 404 shows up in tests).
func (a *hashedAssets) URL(name string) string {
	if hp, ok := a.hashed[name]; ok {
		return "/static/" + hp
	}
	return "/static/" + name
}

func (s *server) handleStatic(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Path, "/static/")
	a := s.assets
	if real, ok := a.real[name]; ok {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		a.serveFile(w, r, real)
		return
	}
	if _, ok := a.hashed[name]; ok {
		w.Header().Set("Cache-Control", "no-cache")
		a.serveFile(w, r, name)
		return
	}
	http.NotFound(w, r)
}

func (a *hashedAssets) serveFile(w http.ResponseWriter, r *http.Request, name string) {
	data, err := fs.ReadFile(a.fsys, name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, path.Base(name), time.Time{}, bytes.NewReader(data))
}
