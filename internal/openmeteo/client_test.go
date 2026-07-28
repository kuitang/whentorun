package openmeteo

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func init() { retryDelay = time.Millisecond }

// fixtureServer serves testdata/<file> and records the request query.
func fixtureServer(t *testing.T, file string, gotQuery *url.Values) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(filepath.Join("testdata", file))
	if err != nil {
		t.Fatal(err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotQuery != nil {
			*gotQuery = r.URL.Query()
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
}

func newTestClient(weatherURL, aqURL string) *Client {
	cfg := DefaultConfig()
	cfg.WeatherURL = weatherURL
	cfg.AirQualityURL = aqURL
	return New(cfg, nil)
}

func fp(v float64) *float64 { return &v }

func TestWeatherParsesFixture(t *testing.T) {
	var q url.Values
	srv := fixtureServer(t, "weather_nyc.json", &q)
	defer srv.Close()
	c := newTestClient(srv.URL, srv.URL)

	hours, err := c.Weather(context.Background(), 40.78, -73.97)
	if err != nil {
		t.Fatalf("Weather: %v", err)
	}
	if len(hours) != 72 {
		t.Fatalf("got %d hours, want 72", len(hours))
	}

	// Request params.
	wantParams := map[string]string{
		"latitude":         "40.7800",
		"longitude":        "-73.9700",
		"temperature_unit": "fahrenheit",
		"wind_speed_unit":  "mph",
		"timezone":         "America/New_York",
		"forecast_days":    "3",
		"hourly":           hourlyWeatherVars,
	}
	for k, want := range wantParams {
		if got := q.Get(k); got != want {
			t.Errorf("query %s = %q, want %q", k, got, want)
		}
	}

	ny, _ := time.LoadLocation("America/New_York")
	tests := []struct {
		idx  int
		want WeatherHour
	}{
		{0, WeatherHour{
			Time:  time.Date(2026, 7, 27, 0, 0, 0, 0, ny),
			TempF: fp(71.8), RelHumidity: fp(67), DewPointF: fp(60.2),
			WindMPH: fp(4.3), GustMPH: fp(14.5), UVIndex: fp(0),
			ShortwaveWm2: fp(0), DirectWm2: fp(0), DiffuseWm2: fp(0),
			PoP: fp(1), CloudCoverPct: fp(27),
		}},
		{13, WeatherHour{
			Time:  time.Date(2026, 7, 27, 13, 0, 0, 0, ny),
			TempF: fp(83.6), RelHumidity: fp(55), DewPointF: fp(65.7),
			WindMPH: fp(1.1), GustMPH: fp(3.6), UVIndex: fp(7.3),
			ShortwaveWm2: fp(264), DirectWm2: fp(0), DiffuseWm2: fp(264),
			PoP: fp(5), CloudCoverPct: fp(44),
		}},
	}
	for _, tc := range tests {
		got := hours[tc.idx]
		if !got.Time.Equal(tc.want.Time) {
			t.Errorf("hour %d Time = %v, want %v", tc.idx, got.Time, tc.want.Time)
		}
		cmp := []struct {
			name      string
			got, want *float64
		}{
			{"TempF", got.TempF, tc.want.TempF},
			{"RelHumidity", got.RelHumidity, tc.want.RelHumidity},
			{"DewPointF", got.DewPointF, tc.want.DewPointF},
			{"WindMPH", got.WindMPH, tc.want.WindMPH},
			{"GustMPH", got.GustMPH, tc.want.GustMPH},
			{"UVIndex", got.UVIndex, tc.want.UVIndex},
			{"ShortwaveWm2", got.ShortwaveWm2, tc.want.ShortwaveWm2},
			{"DirectWm2", got.DirectWm2, tc.want.DirectWm2},
			{"DiffuseWm2", got.DiffuseWm2, tc.want.DiffuseWm2},
			{"PoP", got.PoP, tc.want.PoP},
			{"CloudCoverPct", got.CloudCoverPct, tc.want.CloudCoverPct},
		}
		for _, f := range cmp {
			switch {
			case f.want == nil && f.got != nil:
				t.Errorf("hour %d %s = %v, want nil", tc.idx, f.name, *f.got)
			case f.want != nil && f.got == nil:
				t.Errorf("hour %d %s = nil, want %v", tc.idx, f.name, *f.want)
			case f.want != nil && *f.got != *f.want:
				t.Errorf("hour %d %s = %v, want %v", tc.idx, f.name, *f.got, *f.want)
			}
		}
	}
}

func TestAirQualityParsesFixture(t *testing.T) {
	var q url.Values
	srv := fixtureServer(t, "airquality_nyc.json", &q)
	defer srv.Close()
	c := newTestClient(srv.URL, srv.URL)

	hours, err := c.AirQuality(context.Background(), 40.78, -73.97)
	if err != nil {
		t.Fatalf("AirQuality: %v", err)
	}
	if len(hours) != 72 {
		t.Fatalf("got %d hours, want 72", len(hours))
	}
	if got := q.Get("hourly"); got != "us_aqi" {
		t.Errorf("query hourly = %q, want us_aqi", got)
	}
	ny, _ := time.LoadLocation("America/New_York")
	tests := []struct {
		idx      int
		wantTime time.Time
		wantAQI  float64
	}{
		{0, time.Date(2026, 7, 27, 0, 0, 0, 0, ny), 90},
		{71, time.Date(2026, 7, 29, 23, 0, 0, 0, ny), 44},
	}
	for _, tc := range tests {
		got := hours[tc.idx]
		if !got.Time.Equal(tc.wantTime) {
			t.Errorf("hour %d Time = %v, want %v", tc.idx, got.Time, tc.wantTime)
		}
		if got.AQI == nil || *got.AQI != tc.wantAQI {
			t.Errorf("hour %d AQI = %v, want %v", tc.idx, got.AQI, tc.wantAQI)
		}
	}
}

func TestNullsBecomeNil(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"hourly":{"time":["2026-07-27T00:00","2026-07-27T01:00"],
			"temperature_2m":[71.8,null],"us_aqi":[null,44],
			"precipitation_probability":[3]}}`))
	}))
	defer srv.Close()
	c := newTestClient(srv.URL, srv.URL)

	wh, err := c.Weather(context.Background(), 40.78, -73.97)
	if err != nil {
		t.Fatalf("Weather: %v", err)
	}
	if wh[0].TempF == nil || *wh[0].TempF != 71.8 {
		t.Errorf("hour 0 TempF = %v, want 71.8", wh[0].TempF)
	}
	if wh[1].TempF != nil {
		t.Errorf("hour 1 TempF = %v, want nil", *wh[1].TempF)
	}
	// Short array: index 1 missing entirely.
	if wh[1].PoP != nil {
		t.Errorf("hour 1 PoP = %v, want nil (short array)", *wh[1].PoP)
	}
	// Absent variable: all nil.
	if wh[0].UVIndex != nil {
		t.Errorf("hour 0 UVIndex = %v, want nil (absent)", *wh[0].UVIndex)
	}

	ah, err := c.AirQuality(context.Background(), 40.78, -73.97)
	if err != nil {
		t.Fatalf("AirQuality: %v", err)
	}
	if ah[0].AQI != nil {
		t.Errorf("hour 0 AQI = %v, want nil", *ah[0].AQI)
	}
	if ah[1].AQI == nil || *ah[1].AQI != 44 {
		t.Errorf("hour 1 AQI = %v, want 44", ah[1].AQI)
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
		{"no retry on 4xx", []int{400}, true, 1},
		{"success first try", []int{200}, false, 1},
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
				w.Write([]byte(`{"hourly":{"time":[],"temperature_2m":[]}}`))
			}))
			defer srv.Close()
			c := newTestClient(srv.URL, srv.URL)
			_, err := c.Weather(context.Background(), 40.78, -73.97)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tc.wantErr)
			}
			if hits != tc.wantHits {
				t.Errorf("hits = %d, want %d", hits, tc.wantHits)
			}
		})
	}
}

func TestContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500) // force the retry path, which must respect ctx
	}))
	defer srv.Close()
	c := newTestClient(srv.URL, srv.URL)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Weather(ctx, 40.78, -73.97); err == nil {
		t.Fatal("want error from canceled context")
	}
}
