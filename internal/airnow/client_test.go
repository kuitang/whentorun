package airnow

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func init() { retryDelay = time.Millisecond }

const testKey = "TEST-KEY-0000"

// fixtureServer serves testdata/<file> for the given path and records the
// last request path and query.
func fixtureServer(t *testing.T, file string, gotPath *string, gotQuery *url.Values) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotPath != nil {
			*gotPath = r.URL.Path
		}
		if gotQuery != nil {
			*gotQuery = r.URL.Query()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
}

func newTestClient(baseURL string) *Client {
	cfg := DefaultConfig()
	cfg.BaseURL = baseURL
	return New(cfg, testKey, nil)
}

func TestObservationParsesFixture(t *testing.T) {
	var path string
	var q url.Values
	srv := fixtureServer(t, "observation_nyc.json", &path, &q)
	defer srv.Close()
	c := newTestClient(srv.URL)

	recs, err := c.Observation(context.Background(), 40.78, -73.97)
	if err != nil {
		t.Fatalf("Observation: %v", err)
	}
	if path != "/aq/observation/current/ziplatlong/" {
		t.Errorf("request path = %q, want /aq/observation/current/ziplatlong/", path)
	}
	wantParams := map[string]string{
		"format":    "application/json",
		"latitude":  "40.7800",
		"longitude": "-73.9700",
		"distance":  "25",
		"API_KEY":   testKey,
	}
	for k, want := range wantParams {
		if got := q.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}
	if len(recs) != 2 {
		t.Fatalf("got %d records, want 2", len(recs))
	}
	want := ObservationRecord{
		DateObserved: "2026-07-27", HourObserved: "20:00", LocalTimeZone: "EDT",
		ReportingAreaName: "New York City Region", SiteID: "360610135", SiteName: "CCNY",
		ParameterName: "PM2.5", NowcastAQI: 66, AQICategoryName: "Moderate",
		ReportingAgency: "New York Dept. of Environmental Conservation",
	}
	if !reflect.DeepEqual(recs[0], want) {
		t.Errorf("record 0 = %+v, want %+v", recs[0], want)
	}
	if recs[1].ParameterName != "OZONE" || recs[1].NowcastAQI != 64 {
		t.Errorf("record 1 = %+v, want OZONE/64", recs[1])
	}
}

func TestForecastParsesFixture(t *testing.T) {
	var path string
	srv := fixtureServer(t, "forecast_nyc.json", &path, nil)
	defer srv.Close()
	c := newTestClient(srv.URL)

	recs, err := c.Forecast(context.Background(), 40.78, -73.97)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if path != "/aq/forecast/current/" {
		t.Errorf("request path = %q, want /aq/forecast/current/", path)
	}
	if len(recs) != 4 {
		t.Fatalf("got %d records, want 4", len(recs))
	}
	want := ForecastRecord{
		DateIssue: "2026-07-27", DateValid: "2026-07-27",
		ReportingArea: "New York City Region", StateCode: "NY",
		ParameterName: "OZONE", AQI: 97, CategoryNumber: 2, CategoryName: "Moderate",
	}
	got := recs[0]
	got.Discussion = "" // fixture has empty discussion anyway
	if !reflect.DeepEqual(got, want) {
		t.Errorf("record 0 = %+v, want %+v", recs[0], want)
	}
}

func TestDisplayAQI(t *testing.T) {
	tests := []struct {
		name          string
		recs          []ObservationRecord
		wantAQI       int
		wantPollutant string
		wantCategory  string
		wantOK        bool
	}{
		{"empty", nil, 0, "", "", false},
		{"single", []ObservationRecord{
			{ParameterName: "OZONE", NowcastAQI: 40, AQICategoryName: "Good"},
		}, 40, "OZONE", "Good", true},
		{"max wins with dominant pollutant", []ObservationRecord{
			{ParameterName: "PM2.5", NowcastAQI: 66, AQICategoryName: "Moderate"},
			{ParameterName: "OZONE", NowcastAQI: 64, AQICategoryName: "Moderate"},
		}, 66, "PM2.5", "Moderate", true},
		{"later record can dominate", []ObservationRecord{
			{ParameterName: "OZONE", NowcastAQI: 64, AQICategoryName: "Moderate"},
			{ParameterName: "PM2.5", NowcastAQI: 155, AQICategoryName: "Unhealthy"},
		}, 155, "PM2.5", "Unhealthy", true},
		{"tie keeps first", []ObservationRecord{
			{ParameterName: "PM2.5", NowcastAQI: 50, AQICategoryName: "Good"},
			{ParameterName: "OZONE", NowcastAQI: 50, AQICategoryName: "Good"},
		}, 50, "PM2.5", "Good", true},
		{"zero AQI record still valid", []ObservationRecord{
			{ParameterName: "PM2.5", NowcastAQI: 0, AQICategoryName: "Good"},
		}, 0, "PM2.5", "Good", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			aqi, pol, cat, ok := DisplayAQI(tc.recs)
			if aqi != tc.wantAQI || pol != tc.wantPollutant || cat != tc.wantCategory || ok != tc.wantOK {
				t.Errorf("DisplayAQI = (%d, %q, %q, %v), want (%d, %q, %q, %v)",
					aqi, pol, cat, ok, tc.wantAQI, tc.wantPollutant, tc.wantCategory, tc.wantOK)
			}
		})
	}
}

func TestDisplayForecast(t *testing.T) {
	tests := []struct {
		name string
		recs []ForecastRecord
		want []DayForecast
	}{
		{"empty", nil, []DayForecast{}},
		{"nyc fixture shape", []ForecastRecord{
			{DateValid: "2026-07-27", ParameterName: "OZONE", AQI: 97, CategoryNumber: 2, CategoryName: "Moderate"},
			{DateValid: "2026-07-27", ParameterName: "PM2.5", AQI: 58, CategoryNumber: 2, CategoryName: "Moderate"},
			{DateValid: "2026-07-28", ParameterName: "PM2.5", AQI: 58, CategoryNumber: 2, CategoryName: "Moderate"},
			{DateValid: "2026-07-28", ParameterName: "OZONE", AQI: 46, CategoryNumber: 1, CategoryName: "Good"},
		}, []DayForecast{
			{DateValid: "2026-07-27", AQI: 97, Pollutant: "OZONE", CategoryNumber: 2, CategoryName: "Moderate"},
			{DateValid: "2026-07-28", AQI: 58, Pollutant: "PM2.5", CategoryNumber: 2, CategoryName: "Moderate"},
		}},
		{"out-of-order dates sorted, action day sticks", []ForecastRecord{
			{DateValid: "2026-07-29", ParameterName: "OZONE", AQI: 101, CategoryNumber: 3, CategoryName: "Unhealthy for Sensitive Groups", ActionDay: true},
			{DateValid: "2026-07-28", ParameterName: "PM2.5", AQI: 40, CategoryNumber: 1, CategoryName: "Good"},
			{DateValid: "2026-07-29", ParameterName: "PM2.5", AQI: 55, CategoryNumber: 2, CategoryName: "Moderate"},
		}, []DayForecast{
			{DateValid: "2026-07-28", AQI: 40, Pollutant: "PM2.5", CategoryNumber: 1, CategoryName: "Good"},
			{DateValid: "2026-07-29", AQI: 101, Pollutant: "OZONE", CategoryNumber: 3, CategoryName: "Unhealthy for Sensitive Groups", ActionDay: true},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DisplayForecast(tc.recs)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("DisplayForecast = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRetry(t *testing.T) {
	tests := []struct {
		name     string
		statuses []int
		wantErr  bool
		wantHits int
	}{
		{"retries once after 500 then succeeds", []int{500, 200}, false, 2},
		{"fails after two 5xx", []int{503, 503}, true, 2},
		{"no retry on 4xx", []int{401}, true, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			hits := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				st := tc.statuses[min(hits, len(tc.statuses)-1)]
				hits++
				if st != 200 {
					w.WriteHeader(st)
					return
				}
				w.Write([]byte(`[]`))
			}))
			defer srv.Close()
			c := newTestClient(srv.URL)
			_, err := c.Observation(context.Background(), 40.78, -73.97)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if hits != tc.wantHits {
				t.Errorf("hits = %d, want %d", hits, tc.wantHits)
			}
		})
	}
}

// TestErrorsNeverContainAPIKey covers both the transport-error path (the
// url.Error embeds the full request URL, key included) and the HTTP-status
// path (a malicious/echoing body could contain the key).
func TestErrorsNeverContainAPIKey(t *testing.T) {
	t.Run("transport error scrubbed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
		srv.Close() // connection refused → url.Error with full URL
		c := newTestClient(srv.URL)
		_, err := c.Observation(context.Background(), 40.78, -73.97)
		if err == nil {
			t.Fatal("want error")
		}
		if strings.Contains(err.Error(), testKey) {
			t.Fatalf("error leaks API key: %v", err)
		}
	})
	t.Run("status error body scrubbed", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(400)
			w.Write([]byte("bad key: " + r.URL.Query().Get("API_KEY")))
		}))
		defer srv.Close()
		c := newTestClient(srv.URL)
		_, err := c.Observation(context.Background(), 40.78, -73.97)
		if err == nil {
			t.Fatal("want error")
		}
		if strings.Contains(err.Error(), testKey) {
			t.Fatalf("error leaks API key: %v", err)
		}
		if !strings.Contains(err.Error(), "REDACTED") {
			t.Errorf("expected REDACTED marker in %v", err)
		}
	})
}

func TestConfigPathsAdjustable(t *testing.T) {
	var path string
	srv := fixtureServer(t, "observation_nyc.json", &path, nil)
	defer srv.Close()
	cfg := DefaultConfig()
	cfg.BaseURL = srv.URL + "/" // trailing slash must not double up
	cfg.ObservationPath = "/aq/observation/v3/latlong/"
	c := New(cfg, testKey, nil)
	if _, err := c.Observation(context.Background(), 40.78, -73.97); err != nil {
		t.Fatalf("Observation: %v", err)
	}
	if path != "/aq/observation/v3/latlong/" {
		t.Errorf("request path = %q, want configured /aq/observation/v3/latlong/", path)
	}
}
