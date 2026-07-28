package nws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

const minimalForecast = `{"properties":{"updateTime":"2026-07-27T18:51:39+00:00","periods":[
	{"number":1,"name":"Tonight","startTime":"2026-07-27T20:00:00-04:00","endTime":"2026-07-28T06:00:00-04:00",
	 "isDaytime":false,"shortForecast":"Partly Cloudy","detailedForecast":"Partly cloudy, with a low around 71."}]}}`

func TestForecastHeadersAndPath(t *testing.T) {
	var gotUA, gotAccept, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		w.Write([]byte(minimalForecast))
	}))
	defer srv.Close()

	tf, err := testClient(srv.URL).Forecast(context.Background(), "OKX", 34, 45)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	if gotPath != "/gridpoints/OKX/34,45/forecast" {
		t.Errorf("path = %q, want /gridpoints/OKX/34,45/forecast", gotPath)
	}
	if gotUA != "whentorun.com (kuitang@gmail.com)" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if gotAccept != "application/geo+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if len(tf.Properties.Periods) != 1 {
		t.Fatalf("periods = %d, want 1", len(tf.Properties.Periods))
	}
	p := tf.Properties.Periods[0]
	if p.Name != "Tonight" || p.IsDaytime || p.Short != "Partly Cloudy" ||
		p.Detailed != "Partly cloudy, with a low around 71." {
		t.Errorf("period = %+v", p)
	}
	wantStart := time.Date(2026, 7, 27, 20, 0, 0, 0, time.FixedZone("", -4*3600))
	if !p.Start.Equal(wantStart) {
		t.Errorf("Start = %v, want %v", p.Start, wantStart)
	}
	if !p.End.Equal(wantStart.Add(10 * time.Hour)) {
		t.Errorf("End = %v, want %v", p.End, wantStart.Add(10*time.Hour))
	}
}

func TestForecastRetryOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Write([]byte(minimalForecast))
	}))
	defer srv.Close()

	tf, err := testClient(srv.URL).Forecast(context.Background(), "OKX", 34, 45)
	if err != nil {
		t.Fatalf("Forecast after one 500: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
	if len(tf.Properties.Periods) != 1 {
		t.Errorf("periods = %d, want 1", len(tf.Properties.Periods))
	}
}

func TestForecastDecodeError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"properties":{"periods":"not-a-list"}}`))
	}))
	defer srv.Close()

	if _, err := testClient(srv.URL).Forecast(context.Background(), "OKX", 34, 45); err == nil {
		t.Fatal("want decode error")
	}
}

func TestForecastPeriodCovers(t *testing.T) {
	loc := time.FixedZone("EDT", -4*3600)
	p := ForecastPeriod{
		Start: time.Date(2026, 7, 27, 20, 0, 0, 0, loc),
		End:   time.Date(2026, 7, 28, 6, 0, 0, 0, loc),
	}
	for _, tc := range []struct {
		t    time.Time
		want bool
	}{
		{p.Start.Add(-time.Second), false},
		{p.Start, true}, // inclusive start
		{p.Start.Add(5 * time.Hour), true},
		{p.End, false}, // exclusive end
	} {
		if got := p.Covers(tc.t); got != tc.want {
			t.Errorf("Covers(%v) = %v, want %v", tc.t, got, tc.want)
		}
	}
}

// TestForecastFixtureEndToEnd serves the recorded live fixture (OKX/34,45,
// captured 2026-07-27) through the client.
func TestForecastFixtureEndToEnd(t *testing.T) {
	raw, err := os.ReadFile("testdata/forecast_okx_34_45.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/geo+json")
		w.Write(raw)
	}))
	defer srv.Close()

	tf, err := testClient(srv.URL).Forecast(context.Background(), "OKX", 34, 45)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	periods := tf.Properties.Periods
	if len(periods) < 4 {
		t.Fatalf("periods = %d, want >= 4", len(periods))
	}
	p0 := periods[0]
	if p0.Number != 1 || p0.Name != "Tonight" || p0.IsDaytime {
		t.Errorf("periods[0] = %+v, want number 1 nighttime Tonight", p0)
	}
	if p0.Short == "" || p0.Detailed == "" {
		t.Error("periods[0] prose empty")
	}
	if periods[1].Name != "Tuesday" || !periods[1].IsDaytime {
		t.Errorf("periods[1] = %+v, want daytime Tuesday", periods[1])
	}
	// Periods must be contiguous: each starts when the previous ends.
	for i := 1; i < len(periods); i++ {
		if !periods[i].Start.Equal(periods[i-1].End) {
			t.Errorf("periods[%d].Start = %v, want %v (contiguous)", i, periods[i].Start, periods[i-1].End)
		}
	}
	nyLoc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	if !p0.Covers(time.Date(2026, 7, 27, 20, 30, 0, 0, nyLoc)) {
		t.Error("periods[0] should cover 2026-07-27 20:30 ET")
	}
}
