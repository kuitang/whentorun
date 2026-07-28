package openmeteo

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestLive hits the real Open-Meteo APIs (keyless). Skipped unless
// WHENTORUN_LIVE=1 so normal test runs stay hermetic.
func TestLive(t *testing.T) {
	if os.Getenv("WHENTORUN_LIVE") == "" {
		t.Skip("WHENTORUN_LIVE not set; skipping live API test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c := New(DefaultConfig(), nil)
	lat, lon := 40.78, -73.97 // NYC

	wh, err := c.Weather(ctx, lat, lon)
	if err != nil {
		t.Fatalf("Weather: %v", err)
	}
	if len(wh) < 48 {
		t.Fatalf("Weather: %d hours, want >= 48", len(wh))
	}
	h := wh[13] // early afternoon of day 1: everything should be populated
	for name, v := range map[string]*float64{
		"TempF": h.TempF, "RelHumidity": h.RelHumidity, "DewPointF": h.DewPointF,
		"WindMPH": h.WindMPH, "GustMPH": h.GustMPH, "UVIndex": h.UVIndex,
		"ShortwaveWm2": h.ShortwaveWm2, "DirectWm2": h.DirectWm2,
		"DiffuseWm2": h.DiffuseWm2, "PoP": h.PoP, "CloudCoverPct": h.CloudCoverPct,
	} {
		if v == nil {
			t.Errorf("hour 13 %s is nil", name)
		}
	}
	if h.TempF != nil && (*h.TempF < -30 || *h.TempF > 130) {
		t.Errorf("hour 13 TempF = %v °F: implausible", *h.TempF)
	}
	t.Logf("live weather: %d hours; hour 13 %s temp=%v°F uv=%v", len(wh), h.Time.Format(time.RFC3339), *h.TempF, *h.UVIndex)

	ah, err := c.AirQuality(ctx, lat, lon)
	if err != nil {
		t.Fatalf("AirQuality: %v", err)
	}
	if len(ah) < 48 {
		t.Fatalf("AirQuality: %d hours, want >= 48", len(ah))
	}
	if ah[0].AQI == nil {
		t.Error("AirQuality: hour 0 AQI is nil")
	} else {
		t.Logf("live air quality: %d hours; hour 0 us_aqi=%v", len(ah), *ah[0].AQI)
	}
}
