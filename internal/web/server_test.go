package web_test

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/airnow"
	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
	"github.com/kuitang/whentorun/internal/nws"
	"github.com/kuitang/whentorun/internal/openmeteo"
	"github.com/kuitang/whentorun/internal/web"
	webassets "github.com/kuitang/whentorun/web"
)

var update = flag.Bool("update", false, "regenerate golden files")

var nyLoc = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return loc
}()

// testNow sits inside the recorded fixtures' coverage window (recorded
// 2026-07-27 evening ET), matching merge's own fixture test.
var testNow = time.Date(2026, 7, 27, 20, 30, 0, 0, nyLoc)

var centralPark = *domain.PathBySlug("central-park")

func okIn[T any](data T, fetched time.Time) merge.Input[T] {
	return merge.Input[T]{Data: data, FetchedAt: fetched, OK: true}
}

// loadFixtures replays the recorded upstream responses through each client
// package, exactly as internal/merge's fixture test does (the fixtures are
// this package's own copies, keeping testdata decoupled).
func loadFixtures(t *testing.T, fetched time.Time) merge.Sources {
	t.Helper()
	ctx := context.Background()

	var gp nws.Gridpoint
	mustDecode(t, "testdata/gridpoint_okx_34_45.json", &gp)

	var fc nws.TextForecast
	mustDecode(t, "testdata/forecast_okx_34_45.json", &fc)

	alertsSrv := fixtureServer(t, "testdata/alerts_nyz072.json")
	nwsClient := &nws.Client{BaseURL: alertsSrv.URL, RetryWait: time.Millisecond}
	alerts, err := nwsClient.ActiveAlerts(ctx, "NYZ072")
	if err != nil {
		t.Fatalf("ActiveAlerts over fixture: %v", err)
	}

	weatherSrv := fixtureServer(t, "testdata/weather_nyc.json")
	airSrv := fixtureServer(t, "testdata/airquality_nyc.json")
	om := openmeteo.New(openmeteo.Config{
		WeatherURL:    weatherSrv.URL,
		AirQualityURL: airSrv.URL,
		Timezone:      "America/New_York",
		ForecastDays:  3,
	}, nil)
	omWeather, err := om.Weather(ctx, centralPark.Lat, centralPark.Lon)
	if err != nil {
		t.Fatalf("openmeteo Weather over fixture: %v", err)
	}
	omAir, err := om.AirQuality(ctx, centralPark.Lat, centralPark.Lon)
	if err != nil {
		t.Fatalf("openmeteo AirQuality over fixture: %v", err)
	}

	airnowSrv := fixtureServer(t, "testdata/observation_nyc.json")
	cfg := airnow.DefaultConfig()
	cfg.BaseURL = airnowSrv.URL
	an := airnow.New(cfg, "test-key-not-real", nil)
	obs, err := an.Observation(ctx, centralPark.Lat, centralPark.Lon)
	if err != nil {
		t.Fatalf("airnow Observation over fixture: %v", err)
	}

	return merge.Sources{
		Grid:      okIn(&gp, fetched),
		Forecast:  okIn(&fc, fetched),
		Alerts:    okIn(alerts, fetched),
		AirNowObs: okIn(obs, fetched),
		OMWeather: okIn(omWeather, fetched),
		OMAir:     okIn(omAir, fetched),
	}
}

func fixtureServer(t *testing.T, path string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustDecode(t *testing.T, path string, out any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
}

// newTestServer builds the production handler over fixture-backed merge
// data and a fixed clock.
func newTestServer(t *testing.T) http.Handler {
	t.Helper()
	fetched := testNow.Add(-2 * time.Minute)
	src := loadFixtures(t, fetched)
	return web.NewServer(web.Options{
		Merger: func(ctx context.Context, p domain.Path) (merge.Result, error) {
			return merge.Merge(p, testNow, src), nil
		},
		Clock:  func() time.Time { return testNow },
		Assets: webassets.Files,
	})
}

func get(t *testing.T, h http.Handler, target string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", target, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func postForm(t *testing.T, h http.Handler, target string, form url.Values, htmx bool, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", target, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// --- golden rendering ---

func checkGolden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", "golden", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", path, err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden %s (run with -update to create): %v", path, err)
	}
	if string(want) != string(got) {
		gotPath := path + ".got"
		_ = os.WriteFile(gotPath, got, 0o644)
		t.Errorf("%s differs from golden; wrote %s (regenerate with -update)", name, gotPath)
	}
}

func TestGoldenPages(t *testing.T) {
	h := newTestServer(t)
	for _, tc := range []struct{ target, golden string }{
		{"/", "index.html"},
		{"/p/central-park", "central-park.html"},
	} {
		w := get(t, h, tc.target)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s = %d, want 200; body: %.300s", tc.target, w.Code, w.Body.String())
		}
		body := w.Body.Bytes()
		checkGolden(t, tc.golden, body)

		// Design invariants from the locked mockup.
		s := string(body)
		for _, want := range []string{
			`class="wordmark"`, `class="current"`, `class="hours"`,
			`data-sticky-head`, `class="wbgt-num`, "when to run",
			`aria-current="true"`, `id="explainers"`,
		} {
			if !strings.Contains(s, want) {
				t.Errorf("GET %s: output missing %q", tc.target, want)
			}
		}
	}
}

func TestGoldenFragments(t *testing.T) {
	h := newTestServer(t)
	for _, frag := range []string{"current", "hourly", "windows", "alerts"} {
		w := get(t, h, "/fragment/"+frag+"?path=central-park")
		if w.Code != http.StatusOK {
			t.Fatalf("fragment %s = %d; body: %.300s", frag, w.Code, w.Body.String())
		}
		checkGolden(t, "fragment-"+frag+".html", w.Body.Bytes())
	}
}

// --- handlers ---

func TestIndexRedirectsWithPathCookie(t *testing.T) {
	h := newTestServer(t)
	w := get(t, h, "/", &http.Cookie{Name: "path", Value: "brooklyn"})
	if w.Code != http.StatusFound {
		t.Fatalf("code = %d, want 302", w.Code)
	}
	if loc := w.Header().Get("Location"); loc != "/p/brooklyn" {
		t.Errorf("Location = %q", loc)
	}
	// An invalid cookie renders the default page instead.
	w = get(t, h, "/", &http.Cookie{Name: "path", Value: "nope"})
	if w.Code != http.StatusOK {
		t.Errorf("invalid cookie: code = %d, want 200", w.Code)
	}
}

func TestUnknownPath404(t *testing.T) {
	h := newTestServer(t)
	if w := get(t, h, "/p/nowhere"); w.Code != http.StatusNotFound {
		t.Errorf("code = %d, want 404", w.Code)
	}
}

func cookieFrom(t *testing.T, w *httptest.ResponseRecorder, name string) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no %q cookie in response", name)
	return nil
}

func TestPrefsUnitsToggle(t *testing.T) {
	h := newTestServer(t)
	w := postForm(t, h, "/prefs/units", url.Values{}, true)
	if w.Code != http.StatusNoContent {
		t.Fatalf("code = %d, want 204", w.Code)
	}
	if w.Header().Get("HX-Refresh") != "true" {
		t.Error("missing HX-Refresh")
	}
	if c := cookieFrom(t, w, "units"); c.Value != "C" {
		t.Errorf("units = %q, want C (toggled from default F)", c.Value)
	}
	// Toggling back from C.
	w = postForm(t, h, "/prefs/units", url.Values{}, true, &http.Cookie{Name: "units", Value: "C"})
	if c := cookieFrom(t, w, "units"); c.Value != "F" {
		t.Errorf("units = %q, want F", c.Value)
	}
	// Non-HTMX post redirects back.
	w = postForm(t, h, "/prefs/units", url.Values{"units": {"C"}}, false)
	if w.Code != http.StatusSeeOther {
		t.Errorf("plain post: code = %d, want 303", w.Code)
	}
}

func TestPrefsThemeCycle(t *testing.T) {
	h := newTestServer(t)
	w := postForm(t, h, "/prefs/theme", url.Values{}, true)
	if c := cookieFrom(t, w, "theme"); c.Value != "light" {
		t.Errorf("theme = %q, want light (system → light)", c.Value)
	}
	w = postForm(t, h, "/prefs/theme", url.Values{}, true, &http.Cookie{Name: "theme", Value: "light"})
	if c := cookieFrom(t, w, "theme"); c.Value != "dark" {
		t.Errorf("theme = %q, want dark", c.Value)
	}
	w = postForm(t, h, "/prefs/theme", url.Values{}, true, &http.Cookie{Name: "theme", Value: "dark"})
	if c := cookieFrom(t, w, "theme"); c.Value != "" || c.MaxAge >= 0 {
		t.Errorf("theme cookie = %q maxage %d, want cleared", c.Value, c.MaxAge)
	}
	// The page stamps data-theme from the cookie.
	if body := get(t, h, "/p/central-park", &http.Cookie{Name: "theme", Value: "dark"}).Body.String(); !strings.Contains(body, `data-theme="dark"`) {
		t.Error("page missing data-theme=dark stamp")
	}
}

func TestPrefsPath(t *testing.T) {
	h := newTestServer(t)
	w := postForm(t, h, "/prefs/path", url.Values{"path": {"van-cortlandt"}}, true)
	if w.Code != http.StatusNoContent {
		t.Fatalf("code = %d", w.Code)
	}
	if got := w.Header().Get("HX-Redirect"); got != "/p/van-cortlandt" {
		t.Errorf("HX-Redirect = %q", got)
	}
	if c := cookieFrom(t, w, "path"); c.Value != "van-cortlandt" {
		t.Errorf("path cookie = %q", c.Value)
	}
	if w := postForm(t, h, "/prefs/path", url.Values{"path": {"bogus"}}, true); w.Code != http.StatusBadRequest {
		t.Errorf("bogus path: code = %d, want 400", w.Code)
	}
}

func TestUnitsCookieDrivesCelsius(t *testing.T) {
	h := newTestServer(t)
	body := get(t, h, "/p/central-park", &http.Cookie{Name: "units", Value: "C"}).Body.String()
	if !strings.Contains(body, "&deg;C") {
		t.Error("celsius page missing °C markers")
	}
}

func TestNearest(t *testing.T) {
	h := newTestServer(t)
	// A point in Riverdale, closest to Van Cortlandt Park.
	w := get(t, h, "/api/nearest?lat=40.89&lon=-73.90")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var resp struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Slug != "van-cortlandt" {
		t.Errorf("slug = %q, want van-cortlandt", resp.Slug)
	}
	if w := get(t, h, "/api/nearest?lat=abc&lon=1"); w.Code != http.StatusBadRequest {
		t.Errorf("bad lat: code = %d, want 400", w.Code)
	}
}

func TestHealthz(t *testing.T) {
	h := newTestServer(t)
	w := get(t, h, "/healthz")
	if w.Code != http.StatusOK {
		t.Fatalf("code = %d", w.Code)
	}
	var resp struct {
		Status  string `json:"status"`
		Sources map[string]struct {
			Available bool      `json:"available"`
			Stale     bool      `json:"stale"`
			FetchedAt time.Time `json:"fetched_at"`
		} `json:"sources"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Status != "ok" {
		t.Errorf("status = %q, want ok", resp.Status)
	}
	for _, src := range []string{merge.SrcNWSGrid, merge.SrcNWSForecast, merge.SrcNWSAlerts, merge.SrcAirNow, merge.SrcOMWeather, merge.SrcOMAir} {
		st, ok := resp.Sources[src]
		if !ok || !st.Available || st.Stale {
			t.Errorf("sources[%s] = %+v (present=%v), want available+fresh", src, st, ok)
		}
	}
}

func TestStaticHashedImmutable(t *testing.T) {
	h := newTestServer(t)
	// The page must reference a hashed tokens.css.
	page := get(t, h, "/p/central-park").Body.String()
	i := strings.Index(page, `href="/static/css/tokens-`)
	if i < 0 {
		t.Fatal("page does not reference a hashed tokens.css")
	}
	rest := page[i+len(`href="`):]
	assetURL := rest[:strings.Index(rest, `"`)]

	w := get(t, h, assetURL)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d", assetURL, w.Code)
	}
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("Cache-Control = %q, want immutable", cc)
	}
	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "--tier-0") {
		t.Error("hashed tokens.css content wrong")
	}
	if w := get(t, h, "/static/css/tokens-0000000000.css"); w.Code != http.StatusNotFound {
		t.Errorf("stale hash: code = %d, want 404", w.Code)
	}
}

func TestAlertFeedDownBanner(t *testing.T) {
	fetched := testNow.Add(-2 * time.Minute)
	src := loadFixtures(t, fetched)
	src.Alerts.OK = false // alert feed aged out
	h := web.NewServer(web.Options{
		Merger: func(ctx context.Context, p domain.Path) (merge.Result, error) {
			return merge.Merge(p, testNow, src), nil
		},
		Clock:  func() time.Time { return testNow },
		Assets: webassets.Files,
	})
	body := get(t, h, "/p/central-park").Body.String()
	if !strings.Contains(body, "Alert feed unavailable") {
		t.Error("missing alert-feed-down banner (a down feed must never read as all-clear)")
	}
}
