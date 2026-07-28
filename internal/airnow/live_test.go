package airnow

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive hits the real AirNow API. It is skipped unless AIRNOW_API_KEY is
// set (never commit the key anywhere; export it from the token file).
func TestLive(t *testing.T) {
	key := os.Getenv("AIRNOW_API_KEY")
	if key == "" {
		t.Skip("AIRNOW_API_KEY not set; skipping live API test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := New(DefaultConfig(), key, nil)
	lat, lon := 40.78, -73.97 // NYC

	obs, err := c.Observation(ctx, lat, lon)
	if err != nil {
		t.Fatalf("Observation: %v", err)
	}
	if len(obs) == 0 {
		t.Fatal("Observation: no records")
	}
	aqi, pol, cat, ok := DisplayAQI(obs)
	if !ok || aqi <= 0 || pol == "" || cat == "" {
		t.Fatalf("DisplayAQI = (%d, %q, %q, %v): implausible", aqi, pol, cat, ok)
	}
	t.Logf("live observation: %d records, display AQI %d — %s (%s)", len(obs), aqi, cat, pol)

	fc, err := c.Forecast(ctx, lat, lon)
	if err != nil {
		t.Fatalf("Forecast: %v", err)
	}
	days := DisplayForecast(fc)
	if len(days) == 0 {
		t.Fatal("Forecast: no day records")
	}
	for _, d := range days {
		if d.AQI <= 0 || d.DateValid == "" {
			t.Fatalf("implausible day forecast %+v", d)
		}
		t.Logf("live forecast %s: AQI %d — %s (%s)", d.DateValid, d.AQI, d.CategoryName, d.Pollutant)
	}
}
