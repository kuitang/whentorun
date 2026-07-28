package nws

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

func fptr(v float64) *float64 { return &v }
func sptr(s string) *string   { return &s }

func approx(t *testing.T, name string, got Opt, want float64) {
	t.Helper()
	if !got.Valid {
		t.Errorf("%s: got invalid, want %v", name, want)
		return
	}
	if math.Abs(got.Value-want) > 0.01 {
		t.Errorf("%s: got %v, want %v", name, got.Value, want)
	}
}

func TestParseISODuration(t *testing.T) {
	tests := []struct {
		in      string
		want    time.Duration
		wantErr bool
	}{
		{in: "PT1H", want: time.Hour},
		{in: "PT3H", want: 3 * time.Hour},
		{in: "PT12H", want: 12 * time.Hour},
		{in: "P1D", want: 24 * time.Hour},
		{in: "P1DT18H", want: 42 * time.Hour},
		{in: "P7DT13H", want: 7*24*time.Hour + 13*time.Hour},
		{in: "P6DT13H", want: 6*24*time.Hour + 13*time.Hour},
		{in: "PT30M", want: 30 * time.Minute},
		{in: "PT1H30M", want: 90 * time.Minute},
		{in: "P2W", want: 14 * 24 * time.Hour},
		{in: "PT90S", want: 90 * time.Second},
		{in: "", wantErr: true},
		{in: "P", wantErr: true},
		{in: "3H", wantErr: true},
		{in: "PT", wantErr: true},
		{in: "P1H", wantErr: true},  // H outside time part
		{in: "PT1D", wantErr: true}, // D inside time part
		{in: "P1", wantErr: true},   // trailing number
		{in: "PTH", wantErr: true},  // designator without number
	}
	for _, tt := range tests {
		got, err := parseISODuration(tt.in)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseISODuration(%q): want error, got %v", tt.in, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseISODuration(%q): unexpected error: %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("parseISODuration(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestParseInterval(t *testing.T) {
	start, dur, err := parseInterval("2026-07-28T18:00:00+00:00/PT3H")
	if err != nil {
		t.Fatalf("parseInterval: %v", err)
	}
	wantStart := time.Date(2026, 7, 28, 18, 0, 0, 0, time.UTC)
	if !start.Equal(wantStart) {
		t.Errorf("start = %v, want %v", start, wantStart)
	}
	if dur != 3*time.Hour {
		t.Errorf("dur = %v, want 3h", dur)
	}
	for _, bad := range []string{"2026-07-28T18:00:00+00:00", "notatime/PT3H", "2026-07-28T18:00:00+00:00/3H"} {
		if _, _, err := parseInterval(bad); err == nil {
			t.Errorf("parseInterval(%q): want error", bad)
		}
	}
}

// TestExpandHandComputed checks RLE expansion against hand-computed
// intervals, including unit conversion, defensive uom handling, null runs,
// runs straddling the window edge, and weather-layer flags.
func TestExpandHandComputed(t *testing.T) {
	gp := &Gridpoint{Properties: GridProperties{
		UpdateTime: "2026-07-27T18:51:39+00:00",
		Temperature: GridLayer{UOM: "wmoUnit:degC", Values: []GridValue{
			// Starts before the window: only its last hour (index 0) lands inside.
			{ValidTime: "2026-07-27T22:00:00+00:00/PT3H", Value: fptr(10)},
			{ValidTime: "2026-07-28T01:00:00+00:00/PT2H", Value: fptr(20)},
			// Null run: hour 3 must stay invalid.
			{ValidTime: "2026-07-28T03:00:00+00:00/PT1H", Value: nil},
		}},
		// Defensive: already °F, must pass through.
		Dewpoint: GridLayer{UOM: "wmoUnit:degF", Values: []GridValue{
			{ValidTime: "2026-07-28T00:00:00+00:00/PT4H", Value: fptr(55)},
		}},
		WBGT: GridLayer{UOM: "wmoUnit:degC", Values: []GridValue{
			{ValidTime: "2026-07-28T02:00:00+00:00/PT1H", Value: fptr(30)},
		}},
		WindSpeed: GridLayer{UOM: "wmoUnit:km_h-1", Values: []GridValue{
			// P1D covers the whole window; 16.09344 km/h = exactly 10 mph.
			{ValidTime: "2026-07-28T00:00:00+00:00/P1D", Value: fptr(16.09344)},
		}},
		// Defensive: already mph, must pass through.
		WindGust: GridLayer{UOM: "wmoUnit:mph", Values: []GridValue{
			{ValidTime: "2026-07-28T00:00:00+00:00/PT4H", Value: fptr(25)},
		}},
		PoP: GridLayer{UOM: "wmoUnit:percent", Values: []GridValue{
			{ValidTime: "2026-07-28T00:00:00+00:00/PT4H", Value: fptr(30)},
		}},
		// Real API omits uom on this layer; values are percent.
		ProbabilityOfThunder: GridLayer{Values: []GridValue{
			{ValidTime: "2026-07-28T01:00:00+00:00/PT3H", Value: fptr(40)},
		}},
		IceAccumulation: GridLayer{UOM: "wmoUnit:mm", Values: []GridValue{
			{ValidTime: "2026-07-28T00:00:00+00:00/PT6H", Value: fptr(2.54)},
		}},
		Weather: WeatherLayer{Values: []WeatherValue{
			{ValidTime: "2026-07-28T00:00:00+00:00/PT1H", Value: []WeatherCondition{
				{Coverage: sptr("chance"), Weather: sptr("thunderstorms")},
				{Coverage: sptr("likely"), Weather: sptr("rain_showers"), Intensity: sptr("light")},
			}},
			{ValidTime: "2026-07-28T02:00:00+00:00/PT1H", Value: []WeatherCondition{
				{Coverage: sptr("likely"), Weather: sptr("freezing_rain")},
			}},
			{ValidTime: "2026-07-28T03:00:00+00:00/PT1H", Value: []WeatherCondition{
				{Coverage: sptr("chance"), Weather: sptr("sleet")},
				{Coverage: sptr("chance"), Weather: sptr("thunderstorms")},
			}},
			// Plain rain: no flags.
			{ValidTime: "2026-07-28T01:00:00+00:00/PT1H", Value: []WeatherCondition{
				{Coverage: sptr("definite"), Weather: sptr("rain_showers"), Intensity: sptr("moderate")},
			}},
		}},
	}}

	start := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	f, err := gp.Expand(start, 4)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(f.Hours) != 4 {
		t.Fatalf("len(Hours) = %d, want 4", len(f.Hours))
	}
	if want := time.Date(2026, 7, 27, 18, 51, 39, 0, time.UTC); !f.UpdateTime.Equal(want) {
		t.Errorf("UpdateTime = %v, want %v", f.UpdateTime, want)
	}
	for i, h := range f.Hours {
		if want := start.Add(time.Duration(i) * time.Hour); !h.Time.Equal(want) {
			t.Errorf("hour %d Time = %v, want %v", i, h.Time, want)
		}
	}

	// Temperature: 10°C=50°F (run straddling window start), 20°C=68°F ×2, then invalid.
	approx(t, "h0 TempF", f.Hours[0].TempF, 50)
	approx(t, "h1 TempF", f.Hours[1].TempF, 68)
	approx(t, "h2 TempF", f.Hours[2].TempF, 68)
	if f.Hours[3].TempF.Valid {
		t.Errorf("h3 TempF: want invalid (null run), got %v", f.Hours[3].TempF.Value)
	}
	// Dewpoint already °F.
	for i := 0; i < 4; i++ {
		approx(t, "DewPointF", f.Hours[i].DewPointF, 55)
	}
	// WBGT only hour 2: 30°C = 86°F.
	if f.Hours[0].WBGTF.Valid || f.Hours[1].WBGTF.Valid || f.Hours[3].WBGTF.Valid {
		t.Error("WBGTF: want invalid outside hour 2")
	}
	approx(t, "h2 WBGTF", f.Hours[2].WBGTF, 86)
	// Wind conversions.
	approx(t, "h0 WindMPH", f.Hours[0].WindMPH, 10)
	approx(t, "h0 GustMPH", f.Hours[0].GustMPH, 25)
	// Percent layers.
	approx(t, "h0 PoP", f.Hours[0].PoP, 30)
	if f.Hours[0].ThunderProb.Valid {
		t.Errorf("h0 ThunderProb: want invalid, got %v", f.Hours[0].ThunderProb.Value)
	}
	approx(t, "h1 ThunderProb", f.Hours[1].ThunderProb, 40)
	approx(t, "h3 ThunderProb", f.Hours[3].ThunderProb, 40)
	// Ice: 2.54 mm = 0.1 in.
	approx(t, "h0 IceAccumIn", f.Hours[0].IceAccumIn, 0.1)
	// Weather flags.
	wantWx := []struct{ thunder, freezing bool }{
		{true, false},  // h0: thunderstorms
		{false, false}, // h1: plain rain
		{false, true},  // h2: freezing_rain
		{true, true},   // h3: sleet + thunderstorms
	}
	for i, want := range wantWx {
		if f.Hours[i].WxThunder != want.thunder || f.Hours[i].WxFreezingRain != want.freezing {
			t.Errorf("h%d weather flags = (thunder=%v, freezing=%v), want (%v, %v)",
				i, f.Hours[i].WxThunder, f.Hours[i].WxFreezingRain, want.thunder, want.freezing)
		}
	}

	// Non-hour start truncates; non-UTC location is preserved in Hour times.
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	f2, err := gp.Expand(time.Date(2026, 7, 27, 20, 25, 3, 0, ny), 2)
	if err != nil {
		t.Fatalf("Expand (NY): %v", err)
	}
	if want := time.Date(2026, 7, 27, 20, 0, 0, 0, ny); !f2.Hours[0].Time.Equal(want) {
		t.Errorf("NY h0 Time = %v, want %v", f2.Hours[0].Time, want)
	}
	// 2026-07-27 20:00 EDT = 2026-07-28 00:00 UTC → same first value.
	approx(t, "NY h0 TempF", f2.Hours[0].TempF, 50)

	if _, err := gp.Expand(start, 0); err == nil {
		t.Error("Expand with 0 hours: want error")
	}
}

func TestExpandBadValidTime(t *testing.T) {
	gp := &Gridpoint{Properties: GridProperties{
		Temperature: GridLayer{UOM: "wmoUnit:degC", Values: []GridValue{
			{ValidTime: "garbage", Value: fptr(1)},
		}},
	}}
	if _, err := gp.Expand(time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC), 2); err == nil {
		t.Error("want error for malformed validTime")
	}
}

// TestExpandFixture expands the recorded live gridpoint for OKX/34,45 over
// a 48 h window and checks spot values hand-computed from the fixture's
// RLE runs (see testdata/gridpoint_okx_34_45.json).
func TestExpandFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/gridpoint_okx_34_45.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var gp Gridpoint
	if err := json.Unmarshal(raw, &gp); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}
	if gp.Properties.GridID != "OKX" || gp.Properties.GridX != 34 || gp.Properties.GridY != 45 {
		t.Fatalf("fixture identity = %s/%d,%d, want OKX/34,45",
			gp.Properties.GridID, gp.Properties.GridX, gp.Properties.GridY)
	}

	start := time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)
	f, err := gp.Expand(start, 48)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(f.Hours) != 48 {
		t.Fatalf("len(Hours) = %d, want 48", len(f.Hours))
	}

	// Hour 0 (2026-07-28T00:00Z): temp 25.556°C, dew 18.889°C, WBGT
	// 22.778°C, wind 5.556 km/h, gust 18.52 km/h, PoP 13, sky 47.
	approx(t, "h0 TempF", f.Hours[0].TempF, 78)
	approx(t, "h0 DewPointF", f.Hours[0].DewPointF, 66)
	approx(t, "h0 WBGTF", f.Hours[0].WBGTF, 73)
	approx(t, "h0 ApparentF", f.Hours[0].ApparentF, 78)
	approx(t, "h0 WindMPH", f.Hours[0].WindMPH, 3.452)
	approx(t, "h0 GustMPH", f.Hours[0].GustMPH, 11.508)
	approx(t, "h0 PoP", f.Hours[0].PoP, 13)
	approx(t, "h0 SkyCover", f.Hours[0].SkyCover, 47)
	approx(t, "h0 ThunderProb", f.Hours[0].ThunderProb, 0)
	approx(t, "h0 IceAccumIn", f.Hours[0].IceAccumIn, 0)
	// Hour 18: temp 25°C = 77°F, thunder probability 40%.
	approx(t, "h18 TempF", f.Hours[18].TempF, 77)
	approx(t, "h18 ThunderProb", f.Hours[18].ThunderProb, 40)
	// Hour 47: temp 23.889°C = 75°F.
	approx(t, "h47 TempF", f.Hours[47].TempF, 75)

	// July: windChill layer is one long null run → invalid everywhere.
	// Core layers have full 48 h coverage in the recorded window.
	for i, h := range f.Hours {
		if h.WindChillF.Valid {
			t.Errorf("h%d WindChillF: want invalid in July, got %v", i, h.WindChillF.Value)
		}
		if !h.TempF.Valid || !h.WBGTF.Valid || !h.DewPointF.Valid {
			t.Errorf("h%d: temp/WBGT/dewpoint should all be valid (temp=%v wbgt=%v dew=%v)",
				i, h.TempF.Valid, h.WBGTF.Valid, h.DewPointF.Valid)
		}
		if h.WxFreezingRain {
			t.Errorf("h%d: WxFreezingRain in July fixture", i)
		}
	}

	// Thunderstorm mentions in the weather layer: hours 6–35 and 39–47
	// (hand-derived from the fixture's weather runs).
	for i, h := range f.Hours {
		want := (i >= 6 && i <= 35) || (i >= 39 && i <= 47)
		if h.WxThunder != want {
			t.Errorf("h%d WxThunder = %v, want %v", i, h.WxThunder, want)
		}
	}

	if want := time.Date(2026, 7, 27, 18, 51, 39, 0, time.UTC); !f.UpdateTime.Equal(want) {
		t.Errorf("UpdateTime = %v, want %v", f.UpdateTime, want)
	}
}

func TestUOMConversionDefensive(t *testing.T) {
	tests := []struct {
		name string
		fn   func(float64, string) float64
		v    float64
		uom  string
		want float64
	}{
		{"degC to F", tempToF, 0, "wmoUnit:degC", 32},
		{"degC to F boiling", tempToF, 100, "wmoUnit:degC", 212},
		{"degF passthrough", tempToF, 72, "wmoUnit:degF", 72},
		{"empty uom assumes degC", tempToF, 10, "", 50},
		{"km/h to mph", speedToMPH, 16.09344, "wmoUnit:km_h-1", 10},
		{"mph passthrough", speedToMPH, 12, "wmoUnit:mph", 12},
		{"empty uom assumes km/h", speedToMPH, 16.09344, "", 10},
		{"mm to in", lengthToIn, 25.4, "wmoUnit:mm", 1},
		{"in passthrough", lengthToIn, 0.5, "wmoUnit:in", 0.5},
	}
	for _, tt := range tests {
		if got := tt.fn(tt.v, tt.uom); math.Abs(got-tt.want) > 1e-9 {
			t.Errorf("%s: got %v, want %v", tt.name, got, tt.want)
		}
	}
}
