package rank

import (
	"strings"
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

func TestBucketBoundaries(t *testing.T) {
	type c struct {
		val       float64
		wantV     int
		wantLabel string
	}
	tests := []struct {
		name  string
		f     func(domain.Metric) bucket
		cases []c
	}{
		{
			name: "wbgt",
			f:    wbgtBucket,
			cases: []c{
				{64.4, 0, "low heat stress"},
				{79.9, 0, "low heat stress"},
				{80, 1, "elevated heat stress"},
				{84.9, 1, "elevated heat stress"},
				{85, 2, "high heat stress"},
				{88, 3, "very high heat stress"},
				{90, 4, "extreme heat stress"},
				{101, 4, "extreme heat stress"},
			},
		},
		{
			name: "dew point",
			f:    dewBucket,
			cases: []c{
				{40, 0, "dry"},
				{54.9, 0, "dry"},
				{55, 1, "comfortable"},
				{60, 2, "sticky"},
				{65, 3, "humid"},
				{70, 4, "oppressive"},
				{75, 5, "miserable"},
				{80, 5, "miserable"},
			},
		},
		{
			name: "aqi",
			f:    aqiBucket,
			cases: []c{
				{0, 0, "Good"},
				{50, 0, "Good"},
				{51, 1, "Moderate"},
				{100, 1, "Moderate"},
				{101, 2, "Unhealthy for Sensitive Groups"},
				{150, 2, "Unhealthy for Sensitive Groups"},
				{151, 3, "Unhealthy"},
				{160, 3, "Unhealthy"},
				{200, 3, "Unhealthy"},
				{201, 4, "Very Unhealthy"},
				{300, 4, "Very Unhealthy"},
				{301, 5, "Hazardous"},
				{500, 5, "Hazardous"},
			},
		},
		{
			name: "uv",
			f:    uvBucket,
			cases: []c{
				{0, 0, "low"},
				{2.9, 0, "low"},
				{3, 1, "moderate"},
				{5.9, 1, "moderate"},
				{6, 2, "high"},
				{8, 3, "very high"},
				{11, 4, "extreme"},
			},
		},
		{
			name: "pop",
			f:    popBucket,
			cases: []c{
				{0, 0, "unlikely"},
				{19, 0, "unlikely"},
				{20, 1, "possible"},
				{49, 1, "possible"},
				{50, 2, "likely"},
				{70, 3, "very likely"},
				{100, 3, "very likely"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, cc := range tt.cases {
				got := tt.f(v(cc.val))
				if got.v != cc.wantV || got.label != cc.wantLabel {
					t.Errorf("%s(%v) = (%d, %q), want (%d, %q)",
						tt.name, cc.val, got.v, got.label, cc.wantV, cc.wantLabel)
				}
			}
			if got := tt.f(domain.Metric{}); got.v != unknownBucketVal || got.label != "unknown" {
				t.Errorf("%s(invalid) = (%d, %q), want unknown", tt.name, got.v, got.label)
			}
		})
	}
}

func TestWindBucket(t *testing.T) {
	tests := []struct {
		name       string
		wind, gust domain.Metric
		wantV      int
		wantLabel  string
	}{
		{"uses gust when valid", v(5), v(25), 2, "windy"},
		{"falls back to sustained wind", v(5), domain.Metric{}, 0, "calm"},
		{"boundary 10", domain.Metric{}, v(10), 1, "breezy"},
		{"boundary 20", domain.Metric{}, v(20), 2, "windy"},
		{"boundary 30", domain.Metric{}, v(30), 3, "very windy"},
		{"both invalid is unknown", domain.Metric{}, domain.Metric{}, unknownBucketVal, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := windBucket(domain.Hour{WindMPH: tt.wind, GustMPH: tt.gust})
			if got.v != tt.wantV || got.label != tt.wantLabel {
				t.Errorf("windBucket = (%d, %q), want (%d, %q)", got.v, got.label, tt.wantV, tt.wantLabel)
			}
		})
	}
}

func TestChillBucket(t *testing.T) {
	tests := []struct {
		name        string
		chill, temp domain.Metric
		wantV       int
		wantLabel   string
	}{
		{"mild at 32", v(32), domain.Metric{}, 0, "mild"},
		{"chilly at 20", v(20), domain.Metric{}, 1, "chilly"},
		{"cold at 10", v(10), domain.Metric{}, 2, "cold"},
		{"very cold at 0", v(0), domain.Metric{}, 3, "very cold"},
		{"bitter below 0", v(-5), domain.Metric{}, 4, "bitter"},
		{"falls back to temperature", domain.Metric{}, v(40), 0, "mild"},
		{"both invalid is unknown", domain.Metric{}, domain.Metric{}, unknownBucketVal, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chillBucket(domain.Hour{WindChillF: tt.chill, TempF: tt.temp})
			if got.v != tt.wantV || got.label != tt.wantLabel {
				t.Errorf("chillBucket = (%d, %q), want (%d, %q)", got.v, got.label, tt.wantV, tt.wantLabel)
			}
		})
	}
}

func TestPopIceBucket(t *testing.T) {
	tests := []struct {
		name string
		h    domain.Hour
		want string
	}{
		{"freezing rain is icy", domain.Hour{WxFreezingRain: true, PoP: v(0)}, "icy precipitation"},
		{"ice accumulation is icy", domain.Hour{IceAccumIn: v(0.1), PoP: v(0)}, "icy precipitation"},
		{"otherwise pop band", domain.Hour{PoP: v(60)}, "likely"},
	}
	for _, tt := range tests {
		if got := popIceBucket(tt.h); got.label != tt.want {
			t.Errorf("%s: popIceBucket label = %q, want %q", tt.name, got.label, tt.want)
		}
	}
}

// TestCanaryAQINeverAveragedAway is the brief's canary: a thermally
// excellent hour (WBGT 64.4°F = 18°C) with AQI 160 must rank BELOW a
// WBGT-similar hour with clean air, and the comparator must surface the
// official AQI category — the pollution can never be averaged into an
// overall score.
func TestCanaryAQINeverAveragedAway(t *testing.T) {
	loc := nycLoc(t)
	at := time.Date(2026, 7, 28, 6, 0, 0, 0, loc)

	polluted := pleasantWarm(at)
	polluted.WBGTF = v(64.4) // 18°C — thermally excellent
	polluted.AQI = v(160)    // Unhealthy
	polluted.AQIPollutant = "PM2.5"

	clean := pleasantWarm(at.Add(time.Hour))
	clean.WBGTF = v(64.9) // WBGT-similar: same "low heat stress" band
	clean.AQI = v(40)     // Good

	cmp, diff := CompareHours(SeasonWarm, polluted, clean)
	if cmp != 1 {
		t.Fatalf("polluted vs clean = %d, want 1 (polluted must rank below)", cmp)
	}
	if !strings.Contains(diff.Key, "AQI") {
		t.Errorf("differing key = %q, want it to name AQI", diff.Key)
	}
	if diff.A != "Unhealthy" || diff.B != "Good" {
		t.Errorf("labels = (%q, %q), want official EPA categories (\"Unhealthy\", \"Good\")", diff.A, diff.B)
	}
	// Symmetric check.
	cmp, diff = CompareHours(SeasonWarm, clean, polluted)
	if cmp != -1 || diff.A != "Good" || diff.B != "Unhealthy" {
		t.Errorf("clean vs polluted = (%d, %+v), want (-1, Good vs Unhealthy)", cmp, diff)
	}
}

func TestCompareHoursWarm(t *testing.T) {
	loc := nycLoc(t)
	at := time.Date(2026, 7, 28, 6, 0, 0, 0, loc)
	mk := func(mod func(*domain.Hour)) domain.Hour {
		h := pleasantWarm(at)
		mod(&h)
		return h
	}

	tests := []struct {
		name    string
		a, b    domain.Hour
		wantCmp int
		wantKey string
		wantA   string
		wantB   string
	}{
		{
			name:    "WBGT category dominates everything below it",
			a:       mk(func(h *domain.Hour) { h.WBGTF = v(82); h.AQI = v(20); h.DewPointF = v(40) }),
			b:       mk(func(h *domain.Hour) { h.WBGTF = v(78); h.AQI = v(160); h.DewPointF = v(74) }),
			wantCmp: 1, wantKey: "heat stress (WBGT)", wantA: "elevated heat stress", wantB: "low heat stress",
		},
		{
			name:    "same WBGT band falls through to dew point",
			a:       mk(func(h *domain.Hour) { h.WBGTF = v(70); h.DewPointF = v(50) }),
			b:       mk(func(h *domain.Hour) { h.WBGTF = v(75); h.DewPointF = v(66) }),
			wantCmp: -1, wantKey: "dew point", wantA: "dry", wantB: "humid",
		},
		{
			name:    "AQI ties fall through to UV",
			a:       mk(func(h *domain.Hour) { h.UVIndex = v(9) }),
			b:       mk(func(h *domain.Hour) { h.UVIndex = v(1) }),
			wantCmp: 1, wantKey: "UV", wantA: "very high", wantB: "low",
		},
		{
			name:    "UV ties fall through to rain chance",
			a:       mk(func(h *domain.Hour) { h.PoP = v(10) }),
			b:       mk(func(h *domain.Hour) { h.PoP = v(55) }),
			wantCmp: -1, wantKey: "rain chance", wantA: "unlikely", wantB: "likely",
		},
		{
			name:    "last key is wind",
			a:       mk(func(h *domain.Hour) { h.GustMPH = v(25) }),
			b:       mk(func(h *domain.Hour) { h.GustMPH = v(5) }),
			wantCmp: 1, wantKey: "wind", wantA: "windy", wantB: "calm",
		},
		{
			name:    "small in-band differences tie (coarse bucketing)",
			a:       mk(func(h *domain.Hour) { h.WBGTF = v(72.1); h.DewPointF = v(51); h.AQI = v(38) }),
			b:       mk(func(h *domain.Hour) { h.WBGTF = v(72.3); h.DewPointF = v(53); h.AQI = v(45) }),
			wantCmp: 0,
		},
		{
			name:    "unknown AQI ranks below known Good air",
			a:       mk(func(h *domain.Hour) { h.AQI = domain.Metric{} }),
			b:       mk(func(h *domain.Hour) { h.AQI = v(45) }),
			wantCmp: 1, wantKey: "air quality (AQI)", wantA: "unknown", wantB: "Good",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmp, d := CompareHours(SeasonWarm, tt.a, tt.b)
			if cmp != tt.wantCmp {
				t.Fatalf("cmp = %d, want %d (diff %+v)", cmp, tt.wantCmp, d)
			}
			if tt.wantCmp == 0 {
				if d != (KeyDiff{}) {
					t.Errorf("diff = %+v, want zero on tie", d)
				}
				return
			}
			if d.Key != tt.wantKey || d.A != tt.wantA || d.B != tt.wantB {
				t.Errorf("diff = %+v, want {%q %q %q}", d, tt.wantKey, tt.wantA, tt.wantB)
			}
		})
	}
}

func TestCompareHoursCold(t *testing.T) {
	loc := nycLoc(t)
	at := time.Date(2026, 1, 15, 7, 0, 0, 0, loc)
	mk := func(mod func(*domain.Hour)) domain.Hour {
		h := pleasantCold(at)
		mod(&h)
		return h
	}

	tests := []struct {
		name    string
		a, b    domain.Hour
		wantCmp int
		wantKey string
	}{
		{
			name:    "wind chill band first",
			a:       mk(func(h *domain.Hour) { h.WindChillF = v(15) }),
			b:       mk(func(h *domain.Hour) { h.WindChillF = v(35) }),
			wantCmp: 1, wantKey: "wind chill",
		},
		{
			name:    "icy precipitation loses to plain rain chance",
			a:       mk(func(h *domain.Hour) { h.WxFreezingRain = true }),
			b:       mk(func(h *domain.Hour) { h.PoP = v(75) }),
			wantCmp: 1, wantKey: "precipitation",
		},
		{
			name:    "chill and precip tie falls through to AQI",
			a:       mk(func(h *domain.Hour) { h.AQI = v(120) }),
			b:       mk(func(h *domain.Hour) { h.AQI = v(30) }),
			wantCmp: 1, wantKey: "air quality (AQI)",
		},
		{
			name:    "last cold key is wind",
			a:       mk(func(h *domain.Hour) { h.GustMPH = v(4) }),
			b:       mk(func(h *domain.Hour) { h.GustMPH = v(35) }),
			wantCmp: -1, wantKey: "wind",
		},
		{
			name:    "cold comparator ignores WBGT",
			a:       mk(func(h *domain.Hour) { h.WBGTF = v(85) }),
			b:       mk(func(h *domain.Hour) { h.WBGTF = v(40) }),
			wantCmp: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmp, d := CompareHours(SeasonCold, tt.a, tt.b)
			if cmp != tt.wantCmp {
				t.Fatalf("cmp = %d, want %d (diff %+v)", cmp, tt.wantCmp, d)
			}
			if tt.wantCmp != 0 && d.Key != tt.wantKey {
				t.Errorf("diff key = %q, want %q", d.Key, tt.wantKey)
			}
		})
	}
}
