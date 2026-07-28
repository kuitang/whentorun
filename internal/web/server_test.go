package web

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// populated mirrors the web/ layout with both static and mockup files.
var populated = fstest.MapFS{
	"static/css/tokens.css":     {Data: []byte(":root{--ink:#16191B}")},
	"static/js/htmx.min.js":     {Data: []byte("/*htmx*/")},
	"mockups/a-broadsheet.html": {Data: []byte("<!doctype html><p>mockup a</p>")},
}

// staticOnly mirrors the early-phase repo: web/mockups exists but is empty
// (or is entirely absent from the embed), and must 404, never 500.
var staticOnly = fstest.MapFS{
	"static/css/tokens.css": {Data: []byte(":root{}")},
}

func TestRoutes(t *testing.T) {
	tests := []struct {
		name         string
		assets       fstest.MapFS
		method, path string
		wantStatus   int
		wantCache    string // Cache-Control, "" = don't check
		wantBodySub  string // substring, "" = don't check
	}{
		{
			name: "healthz ok", assets: populated,
			method: "GET", path: "/healthz",
			wantStatus: http.StatusOK, wantCache: "no-store",
		},
		{
			name: "static file served with cache header", assets: populated,
			method: "GET", path: "/static/css/tokens.css",
			wantStatus: http.StatusOK, wantCache: "public, max-age=300",
			wantBodySub: "--ink",
		},
		{
			name: "static missing file 404s", assets: populated,
			method: "GET", path: "/static/nope.css",
			wantStatus: http.StatusNotFound,
		},
		{
			name: "mockup file served no-cache", assets: populated,
			method: "GET", path: "/mockups/a-broadsheet.html",
			wantStatus: http.StatusOK, wantCache: "no-cache",
			wantBodySub: "mockup a",
		},
		{
			name: "mockups dir listing when populated", assets: populated,
			method: "GET", path: "/mockups/",
			wantStatus: http.StatusOK, wantBodySub: "a-broadsheet.html",
		},
		{
			name: "empty mockups dir 404s not 500s", assets: staticOnly,
			method: "GET", path: "/mockups/",
			wantStatus: http.StatusNotFound,
		},
		{
			name: "missing mockup file 404s when dir absent", assets: staticOnly,
			method: "GET", path: "/mockups/a-broadsheet.html",
			wantStatus: http.StatusNotFound,
		},
		{
			name: "index stub", assets: populated,
			method: "GET", path: "/",
			wantStatus: http.StatusOK, wantBodySub: "whentorun",
		},
		{
			name: "unknown path 404s", assets: populated,
			method: "GET", path: "/definitely-not-a-route",
			wantStatus: http.StatusNotFound,
		},
		{
			name: "healthz rejects POST", assets: populated,
			method: "POST", path: "/healthz",
			wantStatus: http.StatusMethodNotAllowed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewServer(Options{Assets: tt.assets})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(tt.method, tt.path, nil))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if tt.wantCache != "" {
				if got := rec.Header().Get("Cache-Control"); got != tt.wantCache {
					t.Errorf("Cache-Control = %q, want %q", got, tt.wantCache)
				}
			}
			if tt.wantBodySub != "" {
				body, _ := io.ReadAll(rec.Body)
				if !strings.Contains(string(body), tt.wantBodySub) {
					t.Errorf("body does not contain %q; body: %s", tt.wantBodySub, body)
				}
			}
		})
	}
}

func TestHealthzShape(t *testing.T) {
	h := NewServer(Options{Assets: staticOnly})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}

	var resp struct {
		Status  string `json:"status"`
		Sources map[string]struct {
			Fresh     bool    `json:"fresh"`
			FetchedAt *string `json:"fetched_at"`
			Note      string  `json:"note"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("invalid JSON: %v; body: %s", err, rec.Body.String())
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want ok", resp.Status)
	}
	for _, src := range []string{"nws", "airnow", "open-meteo"} {
		s, ok := resp.Sources[src]
		if !ok {
			t.Errorf("sources missing %q", src)
			continue
		}
		if s.Fresh {
			t.Errorf("source %q claims fresh in stub phase", src)
		}
		if s.FetchedAt != nil {
			t.Errorf("source %q fetched_at = %v, want null placeholder", src, *s.FetchedAt)
		}
	}
}

// TestNilAssets guards the degenerate wiring case: a nil FS must serve 404s,
// not panic — the page never 500s over assets.
func TestNilAssets(t *testing.T) {
	h := NewServer(Options{Assets: nil})
	for _, path := range []string{"/static/css/tokens.css", "/mockups/"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
	}
}
