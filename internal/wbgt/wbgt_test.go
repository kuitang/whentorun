package wbgt

import (
	"math"
	"testing"
	"time"
)

// nyc reference point (Central Park) for end-to-end tests.
const (
	nycLat = 40.7829
	nycLon = -73.9654
)

// TestEstimateAgainstReference pins the port against reference values
// generated with the Kong & Huber (2024) published implementation
// (WBGT_analytic.py, Zenodo 10802580) transcribed to pure Python with the
// same Liljegren-2008 longwave parameterization; the zenith averages are
// pinned so only the physics chain is under test. The task tolerance is
// ±1.0 °C (1.8 °F); as an exact port we hold 0.01 °F.
func TestEstimateAgainstReference(t *testing.T) {
	tests := []struct {
		name                       string
		tempF, dewF, windMPH       float64
		solar, direct, diffuse, ps float64
		cosza, coszda              float64
		wantF                      float64
	}{
		{"muggy July afternoon, light wind", 90, 72, 6, 850, 600, 250, 0, 0.85, 0.85, 88.5636},
		{"muggy night", 78, 70, 5, 0, 0, 0, 0, 0, 0, 73.6436},
		{"hot dry breezy noon", 95, 55, 12, 950, 750, 200, 0, 0.9, 0.9, 82.5805},
		{"cool overcast (all diffuse)", 55, 50, 8, 150, 0, 150, 0, 0.5, 0.5, 54.7755},
		{"freezing sunny morning", 28, 20, 10, 300, 200, 100, 0, 0.35, 0.35, 30.5482},
		{"low sun near dawn", 70, 60, 3, 120, 60, 60, 0, 0.08, 0.12, 69.6722},
		{"split absent, fdir estimated", 88, 68, 4, 800, 0, 0, 0, 0.8, 0.8, 86.7779},
		{"extreme heat, calm full sun", 100, 75, 1, 1000, 800, 200, 0, 0.95, 0.95, 103.3215},
		{"cold windy night", 20, 10, 15, 0, 0, 0, 0, 0, 0, 17.2836},
		{"non-standard pressure", 90, 72, 6, 850, 600, 250, 98000, 0.85, 0.85, 88.5525},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Inputs{
				TempF: tt.tempF, DewPointF: tt.dewF, WindMPH: tt.windMPH,
				SolarWm2: tt.solar, DirectWm2: tt.direct, DiffuseWm2: tt.diffuse,
				PressurePa: tt.ps,
			}
			got := estimateF(in, tt.cosza, tt.coszda)
			if math.Abs(got-tt.wantF) > 0.01 {
				t.Errorf("estimateF = %.4f °F, want %.4f °F", got, tt.wantF)
			}
		})
	}
}

// TestNightApproximatesNaturalWetBulb: with no solar term the globe cools
// only radiatively, so WBGT ≈ 0.7·Tnw + 0.3·Ta and Tnw tracks the
// psychrometric wet bulb (Stull). The estimate must land between the wet
// bulb and the air temperature, close to the 0.7/0.3 blend.
func TestNightApproximatesNaturalWetBulb(t *testing.T) {
	tests := []struct {
		name        string
		tempF, dewF float64
		windMPH     float64
	}{
		{"muggy night", 78, 70, 5},
		{"dry night", 60, 40, 8},
		{"hot humid still night", 85, 78, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := Inputs{TempF: tt.tempF, DewPointF: tt.dewF, WindMPH: tt.windMPH}
			got := estimateF(in, 0, 0)

			tas := fToK(tt.tempF)
			ea := esat(fToK(tt.dewF), stdPressurePa)
			rh := ea / esat(tas, stdPressurePa) * 100
			twF := kToF(stullWetBulb(tas, rh))
			blend := 0.7*twF + 0.3*tt.tempF

			if got >= tt.tempF {
				t.Errorf("night WBGT %.2f °F not below air temp %.2f °F", got, tt.tempF)
			}
			if got <= twF-3 {
				t.Errorf("night WBGT %.2f °F implausibly far below wet bulb %.2f °F", got, twF)
			}
			if math.Abs(got-blend) > 3 {
				t.Errorf("night WBGT %.2f °F should be near 0.7·Tw+0.3·Ta = %.2f °F", got, blend)
			}
		})
	}
}

// TestSolarAndWindEffects: adding sun raises WBGT, and under strong sun,
// less wind means higher WBGT.
func TestSolarAndWindEffects(t *testing.T) {
	base := Inputs{TempF: 88, DewPointF: 70}
	at := func(windMPH, solar, direct, diffuse, cosz float64) float64 {
		in := base
		in.WindMPH, in.SolarWm2, in.DirectWm2, in.DiffuseWm2 = windMPH, solar, direct, diffuse
		return estimateF(in, cosz, cosz)
	}

	night := at(3, 0, 0, 0, 0)
	sunnyCalm := at(3, 900, 650, 250, 0.9)
	sunnyWindy := at(15, 900, 650, 250, 0.9)

	if sunnyCalm <= night+3 {
		t.Errorf("full sun + low wind (%.2f °F) should exceed night (%.2f °F) by well over 3 °F", sunnyCalm, night)
	}
	if sunnyCalm <= sunnyWindy {
		t.Errorf("under sun, calm (%.2f °F) should exceed windy (%.2f °F)", sunnyCalm, sunnyWindy)
	}
	if sunnyWindy <= night {
		t.Errorf("sunny windy (%.2f °F) should still exceed night (%.2f °F)", sunnyWindy, night)
	}
}

// TestEstimateFEndToEnd runs the full path including solar position for NYC.
func TestEstimateFEndToEnd(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	july := Inputs{
		TempF: 90, DewPointF: 72, WindMPH: 6,
		SolarWm2: 850, DirectWm2: 600, DiffuseWm2: 250,
		Lat: nycLat, Lon: nycLon,
	}

	noon := july
	noon.Time = time.Date(2026, 7, 28, 13, 0, 0, 0, ny)
	midnight := july
	midnight.Time = time.Date(2026, 7, 28, 0, 0, 0, 0, ny)
	midnight.SolarWm2, midnight.DirectWm2, midnight.DiffuseWm2 = 0, 0, 0
	midnight.TempF, midnight.DewPointF = 78, 70

	gotNoon := EstimateF(noon)
	gotMidnight := EstimateF(midnight)
	if math.IsNaN(gotNoon) || math.IsNaN(gotMidnight) {
		t.Fatalf("NaN result: noon %v, midnight %v", gotNoon, gotMidnight)
	}
	// Muggy 90/72 in full July sun is dangerous heat: WBGT well into the
	// 85+ flag range but below air temp + 15.
	if gotNoon < 84 || gotNoon > 100 {
		t.Errorf("noon WBGT = %.2f °F, want in [84, 100]", gotNoon)
	}
	if gotMidnight >= gotNoon {
		t.Errorf("midnight WBGT %.2f °F should be below noon %.2f °F", gotMidnight, gotNoon)
	}
}

// TestEstimateFNeverNaN sweeps 48 hours across varied conditions: the
// estimator must always return a finite, physically plausible value —
// including through sunrise/sunset hours where the zenith terms are edgy.
func TestEstimateFNeverNaN(t *testing.T) {
	ny, _ := time.LoadLocation("America/New_York")
	start := time.Date(2026, 7, 28, 0, 0, 0, 0, ny)
	combos := []Inputs{
		{TempF: 90, DewPointF: 72, WindMPH: 6},
		{TempF: 55, DewPointF: 54, WindMPH: 0},
		{TempF: 20, DewPointF: 5, WindMPH: 25},
		{TempF: 100, DewPointF: 78, WindMPH: 1},
	}
	for h := 0; h < 48; h++ {
		ts := start.Add(time.Duration(h) * time.Hour)
		_, coszda := hourlyCosZenith(nycLat, nycLon, ts)
		for _, in := range combos {
			in.Lat, in.Lon, in.Time = nycLat, nycLon, ts
			if coszda > 0 {
				// Rough clear-sky-ish solar consistent with sun elevation.
				in.SolarWm2 = 900 * coszda
				in.DirectWm2 = 0.7 * in.SolarWm2
				in.DiffuseWm2 = 0.3 * in.SolarWm2
			}
			got := EstimateF(in)
			if math.IsNaN(got) || math.IsInf(got, 0) {
				t.Fatalf("hour %d temp %.0f: non-finite WBGT %v", h, in.TempF, got)
			}
			// Bounds are loose: in calm full sun the black globe runs
			// 30–40 °F above air temperature, so WBGT can sit well above
			// TempF (largest for the 0-mph combo).
			if got < in.TempF-40 || got > in.TempF+35 {
				t.Errorf("hour %d temp %.0f: WBGT %.1f °F implausible", h, in.TempF, got)
			}
		}
	}
}

// TestDewPointClamped: a (bad-input) dew point above air temperature must
// clamp to saturation, matching a dew point equal to air temperature.
func TestDewPointClamped(t *testing.T) {
	a := estimateF(Inputs{TempF: 70, DewPointF: 80, WindMPH: 5}, 0, 0)
	b := estimateF(Inputs{TempF: 70, DewPointF: 70, WindMPH: 5}, 0, 0)
	if math.Abs(a-b) > 1e-9 {
		t.Errorf("dew point above temp: got %.4f, want %.4f", a, b)
	}
}

func TestDirectFraction(t *testing.T) {
	tests := []struct {
		name                string
		cosza, coszda, rsds float64
		direct, diffuse     float64
		want, tol           float64
	}{
		{"night", 0, 0, 0, 0, 0, 0, 0},
		{"sun below 0.5 deg", 0.001, 0.001, 50, 40, 10, 0, 0},
		{"measured split", 0.8, 0.8, 800, 600, 200, 0.75, 1e-12},
		{"measured split capped at 0.9", 0.8, 0.8, 800, 780, 20, 0.9, 1e-12},
		{"all diffuse", 0.5, 0.5, 150, 0, 150, 0, 1e-12},
		// Estimated: s* = 800/(1367·0.8) = 0.7315 → f = e^(3−1.34·s*−1.65/s*)
		{"estimated clear sky", 0.8, 0.8, 800, 0, 0, 0.78994, 0.0001},
		{"estimated overcast", 0.5, 0.5, 100, 0, 0, 0.000209, 0.00002},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := directFraction(tt.cosza, tt.coszda, tt.rsds, tt.direct, tt.diffuse)
			if math.Abs(got-tt.want) > tt.tol {
				t.Errorf("directFraction = %.5f, want %.5f", got, tt.want)
			}
		})
	}
}

func TestWind2m(t *testing.T) {
	tests := []struct {
		name                string
		wind10m, cosz, rsds float64
		wantExp             float64
	}{
		{"strong sun light wind (unstable)", 3, 0.9, 950, 0.15},
		{"strong sun strong wind", 6, 0.9, 950, 0.2},
		{"moderate sun moderate wind", 3, 0.7, 400, 0.2},
		{"weak sun", 3, 0.3, 100, 0.25},
		{"windy night", 5, 0, 0, 0.25},
		{"calm night (stable)", 1, 0, 0, 0.3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			want := tt.wind10m * math.Pow(0.2, tt.wantExp)
			if want < minWind2m {
				want = minWind2m
			}
			if got := wind2m(tt.wind10m, tt.cosz, tt.rsds); math.Abs(got-want) > 1e-12 {
				t.Errorf("wind2m = %.4f, want %.4f (exp %.2f)", got, want, tt.wantExp)
			}
		})
	}
	if got := wind2m(0, 0, 0); got != minWind2m {
		t.Errorf("calm floor: wind2m(0) = %.3f, want %.2f", got, minWind2m)
	}
}

// TestEsat sanity-checks the saturation vapor pressure against standard
// values (Buck 1981): ~3169 Pa at 25 °C, ~611 Pa at 0 °C over ice.
func TestEsat(t *testing.T) {
	if got := esat(298.15, stdPressurePa); math.Abs(got-3180) > 20 {
		t.Errorf("esat(25°C) = %.1f Pa, want ≈3180", got)
	}
	if got := esat(273.15, stdPressurePa); math.Abs(got-611) > 3 {
		t.Errorf("esat(0°C) = %.1f Pa, want ≈611", got)
	}
}
