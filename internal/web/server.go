// Package web is the HTTP server for whentorun.com.
//
// STUB — healthz-deploy phase only. The production UI phase extends
// NewServer with the real routes (/, /p/{slug}, /fragment/*, /prefs/*,
// /api/nearest) and template rendering; it should replace handleIndex and
// add routes here rather than rewrite the package.
package web

import (
	"encoding/json"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

// Options configures the server.
type Options struct {
	// Assets is a filesystem rooted at the repo's web/ directory
	// (the embedded copy in production, os.DirFS("web") with -dev).
	Assets fs.FS
	// Dev marks development mode; reserved for the UI phase
	// (template reload, etc.). It has no effect in the stub.
	Dev bool
}

// NewServer returns the site's root http.Handler.
func NewServer(opts Options) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", handleHealthz)

	// Static assets are not content-hashed yet, so keep the cache short;
	// the UI phase switches to hashed names + immutable.
	mux.Handle("GET /static/", cacheControl("public, max-age=300",
		http.StripPrefix("/static/", http.FileServerFS(subFS(opts.Assets, "static")))))

	// Mockups change during design review — always revalidate.
	mux.Handle("GET /mockups/", cacheControl("no-cache",
		http.StripPrefix("/mockups/", http.FileServerFS(subFS(opts.Assets, "mockups")))))

	mux.HandleFunc("GET /{$}", handleIndex)

	return mux
}

// sourceFreshness is the per-source freshness placeholder reported by
// /healthz. The cache phase (B2) replaces the static "not yet wired"
// values with real per-source fetch times and staleness.
type sourceFreshness struct {
	Fresh     bool       `json:"fresh"`
	FetchedAt *time.Time `json:"fetched_at"` // null until the source is wired
	Note      string     `json:"note,omitempty"`
}

type healthResponse struct {
	Status  string                     `json:"status"`
	Time    time.Time                  `json:"time"`
	Sources map[string]sourceFreshness `json:"sources"`
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	resp := healthResponse{
		Status:  "ok",
		Time:    time.Now().UTC(),
		Sources: map[string]sourceFreshness{},
	}
	for _, o := range []domain.Origin{domain.OriginNWS, domain.OriginAirNow, domain.OriginOpenMeteo} {
		resp.Sources[string(o)] = sourceFreshness{
			Fresh: false,
			Note:  "not yet wired",
		}
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("healthz: encode: %v", err)
	}
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(`<!doctype html>
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>whentorun — coming soon</title>
<p>whentorun.com — NYC running conditions. Under construction.</p>
<p><a href="/mockups/">Design mockups</a> · <a href="/healthz">Health</a></p>
`))
}

// cacheControl sets a Cache-Control header before delegating.
func cacheControl(value string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

// subFS returns fsys rooted at dir. If the sub cannot be constructed (or the
// directory does not exist yet, e.g. an empty web/mockups/ during early
// phases), requests into it simply 404 via the file server's ErrNotExist
// handling — the server must never fail to start over a missing asset dir.
func subFS(fsys fs.FS, dir string) fs.FS {
	if fsys == nil {
		return emptyFS{}
	}
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		log.Printf("assets: sub %q: %v (serving empty)", dir, err)
		return emptyFS{}
	}
	return sub
}

// emptyFS is an fs.FS with no files.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
