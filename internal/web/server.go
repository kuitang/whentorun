// Package web is the production HTTP server for whentorun.com: full SSR of
// the locked ledger-v3-synthesis design, HTMX fragments, preference
// cookies, nearest-path lookup, health, and hashed static assets.
//
// Handlers only read the merge layer through Options.Merger (cache-backed
// in production) — never upstream APIs.
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/kuitang/whentorun/internal/chart"
	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
	"github.com/kuitang/whentorun/internal/narrative"
	"github.com/kuitang/whentorun/internal/rank"
	"github.com/kuitang/whentorun/internal/view"
)

//go:embed templates
var templateFS embed.FS

// DefaultSlug is the path shown when no preference cookie is set.
const DefaultSlug = "central-park"

// Options configures the server.
type Options struct {
	// Merger returns the merged 48 h view for a path; production wires a
	// cache-backed closure, tests a fixture-backed one. Required.
	Merger func(ctx context.Context, path domain.Path) (merge.Result, error)
	// Clock is the time source; nil means time.Now. Tests fix it.
	Clock func() time.Time
	// Assets is a filesystem rooted at the repo's web/ directory
	// (embedded in production, os.DirFS("web") with -dev).
	Assets fs.FS
	// Templates optionally overrides the embedded template FS (rooted at
	// the templates dir); with Dev it is re-parsed per request.
	Templates fs.FS
	// Dev marks development mode: templates re-parse on every request.
	Dev bool
}

type server struct {
	opts    Options
	assets  *hashedAssets
	tmpl    *template.Template
	tmplFS  fs.FS
	mockups fs.FS
}

// NewServer returns the site's root http.Handler. It panics on an invalid
// embedded template set (a build defect, caught by any test).
func NewServer(opts Options) http.Handler {
	s := &server{opts: opts}
	if s.opts.Clock == nil {
		s.opts.Clock = time.Now
	}
	s.assets = newHashedAssets(subFS(opts.Assets, "static"))
	s.mockups = subFS(opts.Assets, "mockups")

	s.tmplFS = opts.Templates
	if s.tmplFS == nil {
		sub, err := fs.Sub(templateFS, "templates")
		if err != nil {
			panic(fmt.Sprintf("web: embedded templates: %v", err))
		}
		s.tmplFS = sub
	}
	s.tmpl = template.Must(s.parseTemplates())

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /p/{slug}", s.handlePath)
	mux.HandleFunc("GET /fragment/{frag}", s.handleFragment)
	mux.HandleFunc("POST /prefs/units", s.handlePrefUnits)
	mux.HandleFunc("POST /prefs/theme", s.handlePrefTheme)
	mux.HandleFunc("POST /prefs/path", s.handlePrefPath)
	mux.HandleFunc("GET /api/nearest", s.handleNearest)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /static/", s.handleStatic)
	mux.Handle("GET /mockups/", cacheControl("no-cache",
		http.StripPrefix("/mockups/", http.FileServerFS(s.mockups))))
	return mux
}

func (s *server) parseTemplates() (*template.Template, error) {
	return template.New("").Funcs(s.funcs()).ParseFS(s.tmplFS, "*.html")
}

// templates returns the parsed set, re-parsing from disk in dev mode.
func (s *server) templates() *template.Template {
	if !s.opts.Dev {
		return s.tmpl
	}
	t, err := s.parseTemplates()
	if err != nil {
		log.Printf("web: dev template reparse: %v", err)
		return s.tmpl
	}
	return t
}

// --- page assembly ---

// prefs are the request's cookie preferences.
type prefs struct {
	units string // "F" | "C"
	theme string // "" | "light" | "dark"
	path  string // slug or ""
}

func readPrefs(r *http.Request) prefs {
	p := prefs{units: "F"}
	if c, err := r.Cookie("units"); err == nil && c.Value == "C" {
		p.units = "C"
	}
	if c, err := r.Cookie("theme"); err == nil && (c.Value == "light" || c.Value == "dark") {
		p.theme = c.Value
	}
	if c, err := r.Cookie("path"); err == nil && domain.PathBySlug(c.Value) != nil {
		p.path = c.Value
	}
	return p
}

func (s *server) buildPage(ctx context.Context, path domain.Path, pf prefs) (view.Page, error) {
	if s.opts.Merger == nil {
		return view.Page{}, fmt.Errorf("web: no Merger configured")
	}
	res, err := s.opts.Merger(ctx, path)
	if err != nil {
		return view.Page{}, fmt.Errorf("web: merge %s: %w", path.Slug, err)
	}
	now := s.opts.Clock()
	windows, err := rank.BestWindows(res.Hours, res.Alerts, now)
	if err != nil {
		return view.Page{}, fmt.Errorf("web: rank %s: %w", path.Slug, err)
	}
	in := view.BuildInput{
		Path:    path,
		Units:   pf.units,
		Theme:   pf.theme,
		Res:     res,
		Windows: windows,
		Now:     now,
	}

	// Narrative engine: the above-fold verdict plus the italic ledger
	// divider rows (rule-based, no runtime LLM).
	nout := narrative.Compose(narrative.Input{
		Hours:  res.Hours,
		Alerts: res.Alerts,
		Sun:    res.Sun,
		Now:    now,
	}, narrative.DefaultBank)
	in.Narrative = nout.AboveFold
	for _, b := range nout.TableBreaks {
		in.Breaks = append(in.Breaks, view.BreakVM{After: b.After, Text: b.Text})
	}

	// Signature merged temperature chart; a chart failure degrades to a
	// page without the fig section, never a 500.
	svg, meta, err := chart.Render(res.Hours, chartBands(res, windows, now), res.Sun, view.NYLoc, pf.units)
	if err != nil {
		log.Printf("web: chart %s: %v", path.Slug, err)
	} else {
		in.ChartSVG = svg
		in.ChartMeta = meta
	}
	return view.Build(in), nil
}

func (s *server) renderPage(w http.ResponseWriter, r *http.Request, path domain.Path) {
	pf := readPrefs(r)
	page, err := s.buildPage(r.Context(), path, pf)
	if err != nil {
		log.Printf("web: render %s: %v", path.Slug, err)
		http.Error(w, "temporarily unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.templates().ExecuteTemplate(w, "page", page); err != nil {
		log.Printf("web: execute page %s: %v", path.Slug, err)
	}
}

// --- handlers ---

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if pf := readPrefs(r); pf.path != "" && pf.path != DefaultSlug {
		http.Redirect(w, r, "/p/"+pf.path, http.StatusFound)
		return
	}
	s.renderPage(w, r, *domain.PathBySlug(DefaultSlug))
}

func (s *server) handlePath(w http.ResponseWriter, r *http.Request) {
	p := domain.PathBySlug(r.PathValue("slug"))
	if p == nil {
		http.NotFound(w, r)
		return
	}
	s.renderPage(w, r, *p)
}

// fragmentTemplates maps the URL fragment name to its template.
var fragmentTemplates = map[string]string{
	"current": "current",
	"hourly":  "hourly",
	"windows": "windows",
	"alerts":  "alerts",
}

func (s *server) handleFragment(w http.ResponseWriter, r *http.Request) {
	name, ok := fragmentTemplates[r.PathValue("frag")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	pf := readPrefs(r)
	slug := r.URL.Query().Get("path")
	if domain.PathBySlug(slug) == nil {
		slug = pf.path
	}
	if slug == "" {
		slug = DefaultSlug
	}
	page, err := s.buildPage(r.Context(), *domain.PathBySlug(slug), pf)
	if err != nil {
		log.Printf("web: fragment %s/%s: %v", name, slug, err)
		http.Error(w, "temporarily unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := s.templates().ExecuteTemplate(w, name, page); err != nil {
		log.Printf("web: execute fragment %s: %v", name, err)
	}
}

// --- preferences ---

const cookieMaxAge = 365 * 24 * 60 * 60

func setPrefCookie(w http.ResponseWriter, name, value string) {
	c := &http.Cookie{
		Name: name, Value: value, Path: "/",
		MaxAge: cookieMaxAge, SameSite: http.SameSiteLaxMode,
	}
	if value == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// prefsDone answers a prefs POST: HTMX clients get HX-Refresh (or
// HX-Redirect), plain form posts a 303 back.
func prefsDone(w http.ResponseWriter, r *http.Request, redirect string) {
	if r.Header.Get("HX-Request") == "true" {
		if redirect != "" {
			w.Header().Set("HX-Redirect", redirect)
		} else {
			w.Header().Set("HX-Refresh", "true")
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if redirect == "" {
		redirect = r.Referer()
	}
	if redirect == "" {
		redirect = "/"
	}
	http.Redirect(w, r, redirect, http.StatusSeeOther)
}

func (s *server) handlePrefUnits(w http.ResponseWriter, r *http.Request) {
	next := r.FormValue("units")
	if next != "F" && next != "C" {
		if readPrefs(r).units == "F" {
			next = "C"
		} else {
			next = "F"
		}
	}
	setPrefCookie(w, "units", next)
	prefsDone(w, r, "")
}

func (s *server) handlePrefTheme(w http.ResponseWriter, r *http.Request) {
	next := r.FormValue("theme")
	switch next {
	case "light", "dark":
	case "system":
		next = ""
	default: // cycle system → light → dark → system
		switch readPrefs(r).theme {
		case "":
			next = "light"
		case "light":
			next = "dark"
		default:
			next = ""
		}
	}
	setPrefCookie(w, "theme", next)
	prefsDone(w, r, "")
}

func (s *server) handlePrefPath(w http.ResponseWriter, r *http.Request) {
	slug := r.FormValue("path")
	if domain.PathBySlug(slug) == nil {
		http.Error(w, "unknown path", http.StatusBadRequest)
		return
	}
	setPrefCookie(w, "path", slug)
	prefsDone(w, r, "/p/"+slug)
}

// --- health ---

func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	type payload struct {
		Status  string                        `json:"status"`
		Time    time.Time                     `json:"time"`
		Sources map[string]merge.SourceStatus `json:"sources"`
	}
	resp := payload{Status: "ok", Time: s.opts.Clock().UTC(), Sources: map[string]merge.SourceStatus{}}

	// Worst-case freshness per source across all paths.
	rankOf := func(st merge.SourceStatus) int {
		switch {
		case !st.Available:
			return 2
		case st.Stale:
			return 1
		default:
			return 0
		}
	}
	if s.opts.Merger != nil {
		for _, p := range domain.Paths {
			res, err := s.opts.Merger(r.Context(), p)
			if err != nil {
				resp.Status = "degraded"
				continue
			}
			for k, st := range res.Freshness {
				cur, seen := resp.Sources[k]
				if !seen || rankOf(st) > rankOf(cur) ||
					(rankOf(st) == rankOf(cur) && st.FetchedAt.Before(cur.FetchedAt)) {
					resp.Sources[k] = st
				}
			}
		}
	} else {
		resp.Status = "degraded"
	}
	for _, st := range resp.Sources {
		if !st.Available {
			resp.Status = "degraded"
		}
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("healthz: encode: %v", err)
	}
}

// --- shared helpers ---

// cacheControl sets a Cache-Control header before delegating.
func cacheControl(value string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", value)
		next.ServeHTTP(w, r)
	})
}

// subFS returns fsys rooted at dir; a missing dir serves empty (404s) so
// the server never fails to start over a missing asset directory.
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
