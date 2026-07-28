package wbgt

import (
	"math"
	"testing"
	"time"
)

// TestCosSolarZenithSolstice: on the June solstice the maximum sun height
// at NYC corresponds to zenith = lat − 23.437°, i.e. cosz ≈ 0.9545, and
// solar noon falls near 16:57 UTC (12:57 EDT) for lon −73.9654.
func TestCosSolarZenithSolstice(t *testing.T) {
	day := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	best, bestT := -2.0, day
	for m := 0; m < 24*60; m++ {
		ts := day.Add(time.Duration(m) * time.Minute)
		if c := cosSolarZenith(nycLat, nycLon, ts); c > best {
			best, bestT = c, ts
		}
	}
	if math.Abs(best-0.95452) > 0.005 {
		t.Errorf("solstice max cosz = %.5f, want ≈0.95452", best)
	}
	noonUTC := float64(bestT.Hour()) + float64(bestT.Minute())/60
	if noonUTC < 16.6 || noonUTC > 17.3 {
		t.Errorf("solar noon at %.2f h UTC, want ≈16.95", noonUTC)
	}
}

// TestCosSolarZenithEquinox: near the March equinox the noon zenith equals
// the latitude, cosz ≈ cos(40.7829°) = 0.7572.
func TestCosSolarZenithEquinox(t *testing.T) {
	day := time.Date(2026, 3, 20, 0, 0, 0, 0, time.UTC)
	best := -2.0
	for m := 0; m < 24*60; m++ {
		if c := cosSolarZenith(nycLat, nycLon, day.Add(time.Duration(m)*time.Minute)); c > best {
			best = c
		}
	}
	if math.Abs(best-0.75719) > 0.01 {
		t.Errorf("equinox max cosz = %.5f, want ≈0.75719", best)
	}
}

func TestCosSolarZenithNight(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	for _, ts := range []time.Time{
		time.Date(2026, 7, 28, 0, 30, 0, 0, ny),
		time.Date(2026, 1, 15, 23, 0, 0, 0, ny),
		time.Date(2026, 12, 21, 3, 0, 0, 0, ny),
	} {
		if c := cosSolarZenith(nycLat, nycLon, ts); c >= 0 {
			t.Errorf("cosz at %v = %.4f, want < 0 (night)", ts, c)
		}
	}
}

func TestHourlyCosZenith(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")

	// Deep night: both averages zero.
	cosza, coszda := hourlyCosZenith(nycLat, nycLon, time.Date(2026, 7, 28, 1, 0, 0, 0, ny))
	if cosza != 0 || coszda != 0 {
		t.Errorf("night hour: got (%.4f, %.4f), want (0, 0)", cosza, coszda)
	}

	// Midday: fully sunlit, so cosza == coszda and both large.
	cosza, coszda = hourlyCosZenith(nycLat, nycLon, time.Date(2026, 7, 28, 12, 30, 0, 0, ny))
	if cosza < 0.8 || math.Abs(cosza-coszda) > 1e-12 {
		t.Errorf("midday hour: got (%.4f, %.4f), want equal and > 0.8", cosza, coszda)
	}

	// Hour containing sunrise (5:25 EDT on Jun 21): partially sunlit, so
	// the daytime-only average exceeds the all-hour average — the exact
	// property that suppresses spurious dawn WBGT spikes.
	cosza, coszda = hourlyCosZenith(nycLat, nycLon, time.Date(2026, 6, 21, 5, 0, 0, 0, ny))
	if !(coszda > cosza && cosza > 0) {
		t.Errorf("sunrise hour: got cosza %.4f, coszda %.4f, want coszda > cosza > 0", cosza, coszda)
	}
}
