// Command stub replays the repo's recorded upstream fixtures as ALL of the
// production server's upstreams on one port, with path-based routing that
// mirrors the real APIs:
//
//	NWS        GET /gridpoints/{office}/{x},{y}            (raw gridpoint)
//	           GET /gridpoints/{office}/{x},{y}/forecast   (narrative forecast)
//	           GET /alerts/active?zone=...                 (active alerts)
//	Open-Meteo GET /v1/forecast                            (hourly weather + daily sun)
//	           GET /v1/air-quality                         (hourly us_aqi)
//	AirNow     GET /aq/observation/current/ziplatlong/     (current observations)
//	           GET /aq/forecast/current/                   (daily AQI forecast)
//
// Any office/x,y and any zone serve the single recorded Central Park / NYZ072
// fixture — the five paths share one OKX recording. Any AirNow API_KEY is
// accepted; never point the stub at a real key.
//
// TIME-SHIFT: every fixture is shifted forward on each request so its data
// covers now..now+48h. The shift is the delta between the fixture's first
// hour and the current hour, rounded DOWN to whole days so time-of-day
// (sunrise, UV curve, overnight lows) stays physically plausible; the
// fixture's first hour therefore lands within the last 24 h and every
// recorded span (72 h Open-Meteo, 7½ d gridpoint) still covers the horizon.
// RLE durations in gridpoint validTime intervals are kept intact — only the
// interval starts move. AirNow observations are stamped to the current hour
// exactly.
//
// DEGRADATION CONTROL (per source, source names match /healthz keys):
//
//	POST /control/{source}/{up|down|stale}   source ∈ nws-grid, nws-forecast,
//	                                         nws-alerts, openmeteo-weather,
//	                                         openmeteo-air, airnow, all
//	GET  /control/status
//
// down => the source answers 503; stale => the source serves data anchored
// 24 h in the past (an upstream that is up but has stopped updating). True
// cache-staleness labels are the server's clock's business and cannot be
// forced from here.
//
// SCENARIOS:
//
//	POST /control/scenario/{live|storm}
//
// "storm" injects a Severe Thunderstorm Warning (onset = current hour, 3 h
// long) into every alerts response and a 6 h thunder block
// (probabilityOfThunder 70 % + "thunderstorms" weather runs) into the
// shifted gridpoint, for veto/banner specs. "live" is the plain recording.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Source names, aligned with the server's /healthz freshness keys.
const (
	srcNWSGrid     = "nws-grid"
	srcNWSForecast = "nws-forecast"
	srcNWSAlerts   = "nws-alerts"
	srcAirNow      = "airnow"
	srcOMWeather   = "openmeteo-weather"
	srcOMAir       = "openmeteo-air"
)

var allSources = []string{srcNWSGrid, srcNWSForecast, srcNWSAlerts, srcAirNow, srcOMWeather, srcOMAir}

// naive layouts used by the Open-Meteo and AirNow fixtures.
const (
	layoutNaiveHour = "2006-01-02T15:04"
	layoutDate      = "2006-01-02"
)

// nyLoc is the fixtures' local timezone (all recordings are NYC).
var nyLoc = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		log.Fatalf("load America/New_York: %v", err)
	}
	return loc
}()

// fixtures holds the raw recorded bodies; each request re-unmarshals its
// fixture so shifting always starts from the pristine recording.
type fixtures struct {
	gridpoint []byte // internal/nws/testdata/gridpoint_okx_34_45.json
	forecast  []byte // internal/nws/testdata/forecast_okx_34_45.json
	alerts    []byte // internal/nws/testdata/alerts_nyz072.json
	omWeather []byte // internal/openmeteo/testdata/weather_nyc.json
	omAir     []byte // internal/openmeteo/testdata/airquality_nyc.json
	anObs     []byte // internal/airnow/testdata/observation_nyc.json
	anFc      []byte // internal/airnow/testdata/forecast_nyc.json
}

func loadFixtures(root string) *fixtures {
	read := func(rel string) []byte {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			log.Fatalf("fixture: %v", err)
		}
		return b
	}
	return &fixtures{
		gridpoint: read("internal/nws/testdata/gridpoint_okx_34_45.json"),
		forecast:  read("internal/nws/testdata/forecast_okx_34_45.json"),
		alerts:    read("internal/nws/testdata/alerts_nyz072.json"),
		omWeather: read("internal/openmeteo/testdata/weather_nyc.json"),
		omAir:     read("internal/openmeteo/testdata/airquality_nyc.json"),
		anObs:     read("internal/airnow/testdata/observation_nyc.json"),
		anFc:      read("internal/airnow/testdata/forecast_nyc.json"),
	}
}

// findRoot walks up from the working directory to the repo root (go.mod).
func findRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for i := 0; i < 8; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no go.mod found above %q; pass -fixtures", dir)
}

// stub is the shared mutable state behind the control API.
type stub struct {
	mu       sync.Mutex
	states   map[string]string // source -> up|down|stale
	scenario string            // live|storm
	fx       *fixtures
}

func newStub(fx *fixtures) *stub {
	s := &stub{states: map[string]string{}, scenario: "live", fx: fx}
	for _, src := range allSources {
		s.states[src] = "up"
	}
	return s
}

// gate applies the source's degradation state: down answers 503 and returns
// ok=false; stale returns an extra -24 h anchor offset.
func (s *stub) gate(w http.ResponseWriter, source string) (staleOffset time.Duration, ok bool) {
	s.mu.Lock()
	st := s.states[source]
	s.mu.Unlock()
	switch st {
	case "down":
		http.Error(w, source+" is down (stub control)", http.StatusServiceUnavailable)
		return 0, false
	case "stale":
		return -24 * time.Hour, true
	}
	return 0, true
}

func (s *stub) storm() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scenario == "storm"
}

// --- time-shift helpers ---

// dayAlign returns the whole-day multiple that moves anchor to within
// (now-24h, now]: floor((now-anchor)/24h) days.
func dayAlign(anchor, now time.Time) time.Duration {
	d := now.Sub(anchor)
	days := d / (24 * time.Hour)
	if d < 0 && d%(24*time.Hour) != 0 {
		days-- // floor, not truncate, for anchors in the future
	}
	return days * 24 * time.Hour
}

// nowHourUTC is the current hour for offset-carrying (RFC3339) fixtures.
func nowHourUTC() time.Time { return time.Now().UTC().Truncate(time.Hour) }

// nowNaiveNY is the current NY wall-clock hour re-expressed in UTC, the
// frame in which the naive Open-Meteo/AirNow timestamps are shifted.
func nowNaiveNY() time.Time {
	n := time.Now().In(nyLoc)
	return time.Date(n.Year(), n.Month(), n.Day(), n.Hour(), 0, 0, 0, time.UTC)
}

// shiftRFC3339 shifts one RFC3339 timestamp, preserving its zone offset.
func shiftRFC3339(s string, delta time.Duration) string {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return s
	}
	return t.Add(delta).Format(time.RFC3339)
}

// shiftInterval shifts the start of an NWS "start/ISO-duration" interval,
// keeping the RLE duration intact.
func shiftInterval(s string, delta time.Duration) string {
	start, dur, ok := strings.Cut(s, "/")
	if !ok {
		return s
	}
	return shiftRFC3339(start, delta) + "/" + dur
}

func intervalStart(s string) (time.Time, error) {
	start, _, _ := strings.Cut(s, "/")
	return time.Parse(time.RFC3339, start)
}

// shiftNaive shifts a zone-less fixture timestamp in the given layout.
func shiftNaive(s, layout string, delta time.Duration) string {
	t, err := time.Parse(layout, s)
	if err != nil {
		return s
	}
	return t.Add(delta).Format(layout)
}

// shiftStrings shifts every string in a JSON array in place.
func shiftStrings(arr []any, layout string, delta time.Duration) {
	for i, v := range arr {
		if s, ok := v.(string); ok {
			arr[i] = shiftNaive(s, layout, delta)
		}
	}
}

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asSlice(v any) []any        { s, _ := v.([]any); return s }
func asString(v any) string      { s, _ := v.(string); return s }

func writeJSON(w http.ResponseWriter, contentType string, v any) {
	w.Header().Set("Content-Type", contentType)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("encode: %v", err)
	}
}

// --- NWS handlers ---

func (s *stub) handleGridpoint(w http.ResponseWriter, r *http.Request) {
	off, ok := s.gate(w, srcNWSGrid)
	if !ok {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(s.fx.gridpoint, &m); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	props := asMap(m["properties"])
	anchor, err := intervalStart(asString(props["validTimes"]))
	if err != nil {
		http.Error(w, "fixture validTimes: "+err.Error(), http.StatusInternalServerError)
		return
	}
	delta := dayAlign(anchor, nowHourUTC()) + off

	props["updateTime"] = shiftRFC3339(asString(props["updateTime"]), delta)
	props["validTimes"] = shiftInterval(asString(props["validTimes"]), delta)
	for _, v := range props {
		layer := asMap(v)
		if layer == nil {
			continue
		}
		for _, rv := range asSlice(layer["values"]) {
			run := asMap(rv)
			if run == nil {
				continue
			}
			if vt, ok := run["validTime"].(string); ok {
				run["validTime"] = shiftInterval(vt, delta)
			}
		}
	}
	if s.storm() {
		injectStormGrid(props)
	}
	writeJSON(w, "application/geo+json", m)
}

// injectStormGrid appends thunder runs covering the next 6 hours. Appended
// runs win: grid expansion applies runs in order, later writes overwrite.
func injectStormGrid(props map[string]any) {
	vt := nowHourUTC().Format(time.RFC3339) + "/PT6H"
	if layer := asMap(props["probabilityOfThunder"]); layer != nil {
		layer["values"] = append(asSlice(layer["values"]),
			map[string]any{"validTime": vt, "value": 70.0})
	}
	if layer := asMap(props["weather"]); layer != nil {
		layer["values"] = append(asSlice(layer["values"]), map[string]any{
			"validTime": vt,
			"value": []any{map[string]any{
				"coverage":   "definite",
				"weather":    "thunderstorms",
				"intensity":  "heavy",
				"visibility": map[string]any{"unitCode": "wmoUnit:km", "value": nil},
				"attributes": []any{},
			}},
		})
	}
}

func (s *stub) handleTextForecast(w http.ResponseWriter, r *http.Request) {
	off, ok := s.gate(w, srcNWSForecast)
	if !ok {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(s.fx.forecast, &m); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	props := asMap(m["properties"])
	periods := asSlice(props["periods"])
	if len(periods) == 0 {
		writeJSON(w, "application/geo+json", m)
		return
	}
	anchor, err := time.Parse(time.RFC3339, asString(asMap(periods[0])["startTime"]))
	if err != nil {
		http.Error(w, "fixture periods[0].startTime: "+err.Error(), http.StatusInternalServerError)
		return
	}
	delta := dayAlign(anchor, nowHourUTC()) + off
	for _, key := range []string{"generatedAt", "updateTime"} {
		props[key] = shiftRFC3339(asString(props[key]), delta)
	}
	props["validTimes"] = shiftInterval(asString(props["validTimes"]), delta)
	for _, pv := range periods {
		p := asMap(pv)
		p["startTime"] = shiftRFC3339(asString(p["startTime"]), delta)
		p["endTime"] = shiftRFC3339(asString(p["endTime"]), delta)
	}
	writeJSON(w, "application/geo+json", m)
}

// alertTimeFields are the alert timestamps that move with the shift.
var alertTimeFields = []string{"sent", "effective", "onset", "expires", "ends"}

func (s *stub) handleAlerts(w http.ResponseWriter, r *http.Request) {
	off, ok := s.gate(w, srcNWSAlerts)
	if !ok {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(s.fx.alerts, &m); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	features := asSlice(m["features"])

	// Anchor on the earliest onset so the recorded alert is active now.
	var anchor time.Time
	for _, fv := range features {
		p := asMap(asMap(fv)["properties"])
		if t, err := time.Parse(time.RFC3339, asString(p["onset"])); err == nil {
			if anchor.IsZero() || t.Before(anchor) {
				anchor = t
			}
		}
	}
	if !anchor.IsZero() {
		delta := dayAlign(anchor, nowHourUTC()) + off
		for _, fv := range features {
			p := asMap(asMap(fv)["properties"])
			for _, key := range alertTimeFields {
				if ts, ok := p[key].(string); ok && ts != "" {
					p[key] = shiftRFC3339(ts, delta)
				}
			}
		}
	}
	if s.storm() {
		features = append(features, stormAlertFeature())
	}
	m["features"] = features
	writeJSON(w, "application/geo+json", m)
}

// stormAlertFeature builds an active Severe Thunderstorm Warning: onset at
// the current hour, three hours long.
func stormAlertFeature() map[string]any {
	onset := time.Now().In(nyLoc).Truncate(time.Hour)
	ends := onset.Add(3 * time.Hour)
	id := "urn:oid:2.49.0.1.840.0.e2e-stub-severe-tstorm-warning.001.1"
	props := map[string]any{
		"@id":       "https://api.weather.gov/alerts/" + id,
		"@type":     "wx:Alert",
		"id":        id,
		"event":     "Severe Thunderstorm Warning",
		"severity":  "Severe",
		"certainty": "Observed",
		"urgency":   "Immediate",
		"status":    "Actual",
		"headline": fmt.Sprintf("Severe Thunderstorm Warning issued %s until %s by NWS Upton NY",
			onset.Format("January 2 at 3:04PM MST"), ends.Format("3:04PM MST")),
		"areaDesc":    "New York (Manhattan); Bronx; Kings (Brooklyn); Northern Queens",
		"sent":        onset.Format(time.RFC3339),
		"effective":   onset.Format(time.RFC3339),
		"onset":       onset.Format(time.RFC3339),
		"expires":     ends.Format(time.RFC3339),
		"ends":        ends.Format(time.RFC3339),
		"senderName":  "NWS Upton NY",
		"description": "The National Weather Service has issued a Severe Thunderstorm Warning. 60 mph wind gusts and quarter size hail possible. (e2e stub scenario)",
		"instruction": "Move indoors immediately.",
		"response":    "Shelter",
	}
	return map[string]any{
		"id":         "https://api.weather.gov/alerts/" + id,
		"type":       "Feature",
		"geometry":   nil,
		"properties": props,
	}
}

// --- Open-Meteo handlers ---

func (s *stub) handleOMWeather(w http.ResponseWriter, r *http.Request) {
	off, ok := s.gate(w, srcOMWeather)
	if !ok {
		return
	}
	s.serveOpenMeteo(w, s.fx.omWeather, off, true)
}

func (s *stub) handleOMAir(w http.ResponseWriter, r *http.Request) {
	off, ok := s.gate(w, srcOMAir)
	if !ok {
		return
	}
	s.serveOpenMeteo(w, s.fx.omAir, off, false)
}

// serveOpenMeteo shifts the naive local hourly.time array (and, for the
// weather API, the daily date/sunrise/sunset arrays) by whole days so the
// 72 h recording covers now..now+48h with time-of-day preserved.
func (s *stub) serveOpenMeteo(w http.ResponseWriter, raw []byte, off time.Duration, daily bool) {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	hourly := asMap(m["hourly"])
	times := asSlice(hourly["time"])
	if len(times) == 0 {
		writeJSON(w, "application/json", m)
		return
	}
	anchor, err := time.Parse(layoutNaiveHour, asString(times[0]))
	if err != nil {
		http.Error(w, "fixture hourly.time[0]: "+err.Error(), http.StatusInternalServerError)
		return
	}
	delta := dayAlign(anchor, nowNaiveNY()) + off
	shiftStrings(times, layoutNaiveHour, delta)
	if daily {
		if d := asMap(m["daily"]); d != nil {
			shiftStrings(asSlice(d["time"]), layoutDate, delta)
			shiftStrings(asSlice(d["sunrise"]), layoutNaiveHour, delta)
			shiftStrings(asSlice(d["sunset"]), layoutNaiveHour, delta)
		}
	}
	writeJSON(w, "application/json", m)
}

// --- AirNow handlers ---

func (s *stub) handleAirNowObs(w http.ResponseWriter, r *http.Request) {
	off, ok := s.gate(w, srcAirNow)
	if !ok {
		return
	}
	var recs []map[string]any
	if err := json.Unmarshal(s.fx.anObs, &recs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// A current observation is stamped to the current hour exactly (or 24 h
	// ago when stale).
	obs := time.Now().In(nyLoc).Truncate(time.Hour).Add(off)
	for _, rec := range recs {
		rec["dateObserved"] = obs.Format(layoutDate)
		rec["hourObserved"] = obs.Format("15:04")
	}
	writeJSON(w, "application/json", recs)
}

func (s *stub) handleAirNowForecast(w http.ResponseWriter, r *http.Request) {
	off, ok := s.gate(w, srcAirNow)
	if !ok {
		return
	}
	var recs []map[string]any
	if err := json.Unmarshal(s.fx.anFc, &recs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var anchor time.Time
	for _, rec := range recs {
		if t, err := time.Parse(layoutDate, asString(rec["dateValid"])); err == nil {
			if anchor.IsZero() || t.Before(anchor) {
				anchor = t
			}
		}
	}
	if !anchor.IsZero() {
		delta := dayAlign(anchor, nowNaiveNY()) + off
		for _, rec := range recs {
			for _, key := range []string{"dateIssue", "dateValid"} {
				if ds, ok := rec[key].(string); ok {
					rec[key] = shiftNaive(ds, layoutDate, delta)
				}
			}
		}
	}
	writeJSON(w, "application/json", recs)
}

// --- control API ---

func (s *stub) handleControlSet(w http.ResponseWriter, r *http.Request) {
	source, state := r.PathValue("source"), r.PathValue("state")
	if state != "up" && state != "down" && state != "stale" {
		http.Error(w, "state must be up, down, or stale", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	if source == "all" {
		for _, src := range allSources {
			s.states[src] = state
		}
	} else if _, known := s.states[source]; known {
		s.states[source] = state
	} else {
		s.mu.Unlock()
		http.Error(w, fmt.Sprintf("unknown source %q (want one of %s, or all)",
			source, strings.Join(allSources, ", ")), http.StatusBadRequest)
		return
	}
	s.mu.Unlock()
	log.Printf("control: %s -> %s", source, state)
	s.handleControlStatus(w, r)
}

func (s *stub) handleControlScenario(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name != "live" && name != "storm" {
		http.Error(w, "scenario must be live or storm", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.scenario = name
	s.mu.Unlock()
	log.Printf("control: scenario -> %s", name)
	s.handleControlStatus(w, r)
}

func (s *stub) handleControlStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	out := struct {
		Scenario string            `json:"scenario"`
		Sources  map[string]string `json:"sources"`
	}{Scenario: s.scenario, Sources: make(map[string]string, len(s.states))}
	for k, v := range s.states {
		out.Sources[k] = v
	}
	s.mu.Unlock()
	writeJSON(w, "application/json", out)
}

// --- wiring ---

func (s *stub) routes() http.Handler {
	mux := http.NewServeMux()
	// NWS. {coords} is "x,y"; every gridpoint serves the OKX 34,45 recording.
	mux.HandleFunc("GET /gridpoints/{office}/{coords}", s.handleGridpoint)
	mux.HandleFunc("GET /gridpoints/{office}/{coords}/forecast", s.handleTextForecast)
	mux.HandleFunc("GET /alerts/active", s.handleAlerts)
	// Open-Meteo (weather and air-quality share the port here; the server
	// splits them via OPENMETEO_BASE_URL / OPENMETEO_AQ_BASE_URL).
	mux.HandleFunc("GET /v1/forecast", s.handleOMWeather)
	mux.HandleFunc("GET /v1/air-quality", s.handleOMAir)
	// AirNow new-style services. Any API_KEY is accepted.
	mux.HandleFunc("GET /aq/observation/current/ziplatlong/{$}", s.handleAirNowObs)
	mux.HandleFunc("GET /aq/forecast/current/{$}", s.handleAirNowForecast)
	// Control plane.
	mux.HandleFunc("POST /control/scenario/{name}", s.handleControlScenario)
	mux.HandleFunc("POST /control/{source}/{state}", s.handleControlSet)
	mux.HandleFunc("GET /control/status", s.handleControlStatus)
	return logRequests(mux)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		q := ""
		if r.URL.RawQuery != "" {
			q = "?" + r.URL.RawQuery
		}
		log.Printf("%s %s%s", r.Method, r.URL.Path, q)
	})
}

func main() {
	addr := flag.String("addr", ":9099", "listen address")
	fixturesRoot := flag.String("fixtures", "", "repo root containing internal/*/testdata (default: walk up to go.mod)")
	flag.Parse()

	root := *fixturesRoot
	if root == "" {
		var err error
		if root, err = findRoot(); err != nil {
			log.Fatal(err)
		}
	}
	s := newStub(loadFixtures(root))
	log.Printf("e2e stub listening on %s (fixtures: %s, scenario: live)", *addr, root)
	if err := http.ListenAndServe(*addr, s.routes()); err != nil {
		log.Fatal(err)
	}
}
