package chart

import (
	"math"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
)

var ny = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return loc
}()

func tag() domain.SourceTag { return domain.SourceTag{Origin: domain.OriginNWS} }

// mkHours builds n consecutive hours from start with the given WBGT/temp/dew
// values (NaN = invalid).
func mkHours(start time.Time, wbgt, temp, dew []float64) []domain.Hour {
	n := len(wbgt)
	hs := make([]domain.Hour, n)
	for i := range hs {
		hs[i].Time = start.Add(time.Duration(i) * time.Hour)
		if !math.IsNaN(wbgt[i]) {
			hs[i].WBGTF = domain.Val(wbgt[i], tag())
		}
		if !math.IsNaN(temp[i]) {
			hs[i].TempF = domain.Val(temp[i], tag())
		}
		if !math.IsNaN(dew[i]) {
			hs[i].DewPointF = domain.Val(dew[i], tag())
		}
	}
	return hs
}

func repeat(v float64, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = v
	}
	return s
}

// --- geometry math -----------------------------------------------------

func TestXAt(t *testing.T) {
	first := time.Date(2026, 7, 28, 15, 0, 0, 0, ny)
	tests := []struct {
		name string
		t    time.Time
		want float64
	}{
		{"first hour", first, 10},
		{"one hour later", first.Add(time.Hour), 38},
		{"nine hours later (midnight)", first.Add(9 * time.Hour), 262},
		{"exact minute: sunset 8:15 PM", time.Date(2026, 7, 28, 20, 15, 0, 0, ny), 157},
		{"exact minute: sunrise 5:49 AM next day", time.Date(2026, 7, 29, 5, 49, 0, 0, ny), 424.8666666667},
	}
	for _, tc := range tests {
		if got := xAt(first, tc.t); math.Abs(got-tc.want) > 1e-6 {
			t.Errorf("%s: xAt = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestYScaleAndGridTens(t *testing.T) {
	// Data spanning 60..96 pads to 56..100: 220px / 44°F = 5 px/°F.
	start := time.Date(2026, 7, 28, 15, 0, 0, 0, ny)
	hs := mkHours(start,
		[]float64{90, 88},
		[]float64{96, 94},
		[]float64{60, 61},
	)
	sc, ok := newScale(hs)
	if !ok {
		t.Fatal("newScale: no data")
	}
	tests := []struct {
		v, want float64
	}{
		{100, 48}, {96, 68}, {90, 98}, {80, 148}, {70, 198}, {60, 248}, {56, 268},
	}
	for _, tc := range tests {
		if got := sc.y(tc.v); math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("y(%v) = %v, want %v", tc.v, got, tc.want)
		}
	}
	// Tens at least 2°F inside 56..100: 60,70,80,90 (top-down).
	got := sc.gridTens()
	want := []int{90, 80, 70, 60}
	if len(got) != len(want) {
		t.Fatalf("gridTens = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("gridTens = %v, want %v", got, want)
		}
	}
}

func TestGridTensExcludesEdgeHuggers(t *testing.T) {
	// Data 64..92 pads to 60..96; 60 is only 0°F inside the bottom edge
	// after padding — wait, it IS the edge. Tens rule: within [vMin+2, vMax-2]
	// = [62, 94] keeps 70, 80, 90 and drops 60 (mockup behavior).
	start := time.Date(2026, 7, 28, 15, 0, 0, 0, ny)
	hs := mkHours(start, []float64{88, 74}, []float64{92, 76}, []float64{64, 64})
	sc, _ := newScale(hs)
	got := sc.gridTens()
	want := []int{90, 80, 70}
	if len(got) != len(want) {
		t.Fatalf("gridTens = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("gridTens = %v, want %v", got, want)
		}
	}
}

func TestBoundariesBetween(t *testing.T) {
	tests := []struct {
		va, vb float64
		want   []float64
	}{
		{78, 91, []float64{80, 85, 88, 90}},
		{91, 78, []float64{90, 88, 85, 80}},
		{82, 84, nil},
		{87, 88, nil}, // boundary hit exactly at an endpoint: no strict crossing
		{88, 87, nil},
		{86, 89, []float64{88}},
	}
	for _, tc := range tests {
		got := boundariesBetween(tc.va, tc.vb)
		if len(got) != len(tc.want) {
			t.Errorf("boundariesBetween(%v,%v) = %v, want %v", tc.va, tc.vb, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("boundariesBetween(%v,%v) = %v, want %v", tc.va, tc.vb, got, tc.want)
			}
		}
	}
}

func TestSegmentRunInterpolatesCrossings(t *testing.T) {
	// One straight segment rising 78→82 over 28px crosses the 80 boundary
	// exactly halfway: split at x=24 with the crossing point shared.
	run := []pt{{x: 10, y: 0, v: 78}, {x: 38, y: 28, v: 82}}
	segs := segmentRun(run)
	if len(segs) != 2 {
		t.Fatalf("got %d segments, want 2: %+v", len(segs), segs)
	}
	if segs[0].tier != 0 || segs[1].tier != 1 {
		t.Fatalf("tiers = %d,%d, want 0,1", segs[0].tier, segs[1].tier)
	}
	cross := segs[0].pts[len(segs[0].pts)-1]
	if math.Abs(cross.x-24) > 1e-9 || math.Abs(cross.v-80) > 1e-9 {
		t.Errorf("crossing = %+v, want x=24 v=80", cross)
	}
	if segs[1].pts[0] != cross {
		t.Errorf("crossing point not shared: %+v vs %+v", segs[1].pts[0], cross)
	}
	// y interpolated linearly too: halfway of 0..28.
	if math.Abs(cross.y-14) > 1e-9 {
		t.Errorf("crossing y = %v, want 14", cross.y)
	}
}

func TestSegmentRunTierCounts(t *testing.T) {
	// Rise through all five tiers then fall back below 80: expect
	// tiers 0,1,2,3,4 then 3,2,1,0 — nine segments in order.
	vals := []float64{78, 82, 87, 89, 92, 89, 87, 82, 78}
	run := make([]pt, len(vals))
	for i, v := range vals {
		run[i] = pt{x: float64(10 + 28*i), v: v}
	}
	segs := segmentRun(run)
	wantTiers := []int{0, 1, 2, 3, 4, 3, 2, 1, 0}
	if len(segs) != len(wantTiers) {
		t.Fatalf("got %d segments, want %d", len(segs), len(wantTiers))
	}
	for i, s := range segs {
		if s.tier != wantTiers[i] {
			t.Errorf("segment %d tier = %d, want %d", i, s.tier, wantTiers[i])
		}
		if len(s.pts) < 2 {
			t.Errorf("segment %d has %d points", i, len(s.pts))
		}
	}
	// Consecutive segments share their boundary point.
	for i := 1; i < len(segs); i++ {
		prevEnd := segs[i-1].pts[len(segs[i-1].pts)-1]
		if segs[i].pts[0] != prevEnd {
			t.Errorf("segment %d does not start at previous end", i)
		}
	}
}

func TestSegmentRunExactBoundaryPoint(t *testing.T) {
	// A point exactly on 88 splits there: 87→88 is tier 2, 88→89 tier 3,
	// sharing the 88 point (mockup's shared-point convention).
	run := []pt{{x: 0, v: 87}, {x: 28, v: 88}, {x: 56, v: 89}}
	segs := segmentRun(run)
	if len(segs) != 2 || segs[0].tier != 2 || segs[1].tier != 3 {
		t.Fatalf("segments = %+v, want tiers 2,3", segs)
	}
	if segs[0].pts[len(segs[0].pts)-1].x != 28 || segs[1].pts[0].x != 28 {
		t.Errorf("split not at the boundary point: %+v", segs)
	}
}

func TestFindExtremaAndSelect(t *testing.T) {
	vals := []float64{88, 74, 89, 71, 87}
	run := make([]pt, len(vals))
	for i, v := range vals {
		run[i] = pt{x: float64(i), v: v}
	}
	ex := findExtrema(run)
	if len(ex) != 5 {
		t.Fatalf("got %d extrema, want 5", len(ex))
	}
	wantMax := []bool{true, false, true, false, true}
	for i, e := range ex {
		if e.isMax != wantMax[i] {
			t.Errorf("extremum %d isMax = %v, want %v", i, e.isMax, wantMax[i])
		}
	}
	// A long zigzag trims to 6.
	long := make([]pt, 20)
	for i := range long {
		v := 75.0
		if i%2 == 0 {
			v = 85 + float64(i)/10
		}
		long[i] = pt{x: float64(i), v: v}
	}
	if got := selectLabels(findExtrema(long)); len(got) != maxDirectLabels {
		t.Errorf("selectLabels kept %d, want %d", len(got), maxDirectLabels)
	}
}

// --- gap handling ------------------------------------------------------

func TestGapsBreakPolylines(t *testing.T) {
	nan := math.NaN()
	start := time.Date(2026, 7, 28, 15, 0, 0, 0, ny)
	hs := mkHours(start,
		[]float64{75, 76, nan, 76, 75},
		[]float64{80, 81, nan, 81, 80},
		[]float64{60, 60, 60, 60, 60},
	)
	svg, _, err := Render(hs, nil, nil, ny, "F")
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)
	if got := strings.Count(s, `class="ln-air"`); got != 2 {
		t.Errorf("air polylines = %d, want 2 (gap must split, not zero-fill)", got)
	}
	if got := strings.Count(s, `class="ln-dew"`); got != 1 {
		t.Errorf("dew polylines = %d, want 1", got)
	}
	if got := strings.Count(s, `class="cw0"`); got != 2 {
		t.Errorf("cw0 polylines = %d, want 2 (WBGT gap must split)", got)
	}
	if strings.Contains(s, "NaN") {
		t.Error("output contains NaN")
	}
}

func TestRenderCelsius(t *testing.T) {
	start := time.Date(2026, 7, 28, 15, 0, 0, 0, ny)
	hs := mkHours(start,
		[]float64{68, 71, 75, 78, 75, 71}, // WBGT °F: 20–25.6 °C
		[]float64{72, 75, 79, 82, 79, 75},
		[]float64{60, 60, 60, 60, 60, 60},
	)
	svg, meta, err := Render(hs, nil, nil, ny, "C")
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)
	if !strings.Contains(s, "Celsius axis") {
		t.Error("title should name the Celsius axis")
	}
	for _, yl := range meta.YLabels {
		v, err := strconv.Atoi(yl.Label)
		if err != nil {
			t.Fatalf("axis label %q not a number", yl.Label)
		}
		if v%5 != 0 || v > 40 {
			t.Errorf("celsius axis label %d: want a multiple of 5 in °C range", v)
		}
	}
	// Direct labels print converted values: the 78 °F max is 26 °C.
	if !strings.Contains(s, ">26&#176; ") {
		t.Errorf("celsius direct label 26° missing:\n%s", s)
	}
	if strings.Contains(s, ">78&#176; ") {
		t.Error("fahrenheit direct label leaked into celsius chart")
	}
}

func TestRenderErrors(t *testing.T) {
	if _, _, err := Render(nil, nil, nil, ny, "F"); err == nil {
		t.Error("want error for empty hours")
	}
	// Hours present but no plottable temperature data.
	hs := make([]domain.Hour, 3)
	for i := range hs {
		hs[i].Time = time.Date(2026, 7, 28, 15+i, 0, 0, 0, ny)
	}
	if _, _, err := Render(hs, nil, nil, ny, "F"); err == nil {
		t.Error("want error for no plottable data")
	}
}

// --- golden substrings -------------------------------------------------

func TestRenderGolden(t *testing.T) {
	start := time.Date(2026, 7, 28, 15, 0, 0, 0, ny)
	n := 12
	wbgt := []float64{91, 88, 86, 83, 79, 76, 75, 74, 74, 73, 72, 72}
	temp := []float64{96, 94, 91, 87, 82, 79, 78, 77, 76, 75, 74, 74}
	dew := repeat(60, n)
	hs := mkHours(start, wbgt, temp, dew)
	// 6 PM: 60% PoP with thunder; 7 PM: 20% PoP.
	hs[3].PoP = domain.Val(60, tag())
	hs[3].ThunderProb = domain.Val(60, tag())
	hs[4].PoP = domain.Val(20, tag())

	windows := []Band{
		{Kind: "best", Start: start.Add(6 * time.Hour), End: start.Add(8 * time.Hour), Label: "after work", Sub: "Tue 9–11 PM"},
		{Kind: "veto", Start: start.Add(3 * time.Hour), End: start.Add(5 * time.Hour), Label: "thunderstorms"},
		{Kind: "advisory", Start: start, End: start.Add(5 * time.Hour), Label: "Heat Advisory — until 8 PM"},
	}
	sun := []merge.SunTimes{{
		Sunrise: time.Date(2026, 7, 28, 5, 48, 0, 0, ny),
		Sunset:  time.Date(2026, 7, 28, 20, 15, 0, 0, ny),
	}}

	svg, meta, err := Render(hs, windows, sun, ny, "F")
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)

	if want := 10 + 28*(n-1) + 18; meta.WidthPX != want {
		t.Errorf("WidthPX = %d, want %d", meta.WidthPX, want)
	}
	// Data 60..96 → domain 56..100 → 5 px/°F; labels at svg y − 6.
	wantY := []YL{{92, "90"}, {142, "80"}, {192, "70"}, {242, "60"}}
	if len(meta.YLabels) != len(wantY) {
		t.Fatalf("YLabels = %+v, want %+v", meta.YLabels, wantY)
	}
	for i, w := range wantY {
		if meta.YLabels[i] != w {
			t.Errorf("YLabels[%d] = %+v, want %+v", i, meta.YLabels[i], w)
		}
	}
	wantP := []YL{{222, "100%"}, {242, "50%"}}
	for i, w := range wantP {
		if meta.PrecipLabels[i] != w {
			t.Errorf("PrecipLabels[%d] = %+v, want %+v", i, meta.PrecipLabels[i], w)
		}
	}

	for _, want := range []string{
		`<svg width="336" height="316" viewBox="0 0 336 316" role="img" aria-labelledby="figtitle figdesc">`,
		`<pattern id="hatchVeto"`,
		`<pattern id="hatchWin"`,
		`<pattern id="hatchRain"`,
		// Best window: 9 PM (x=178) to 11 PM (x=234), full plot height.
		`<g data-window="best">`,
		`<rect class="win-ice" x="178" y="48" width="56" height="220"/>`,
		`<rect class="win-hatch" x="178" y="48" width="56" height="220"/>`,
		`<text class="fx-winlab" x="206" y="66" text-anchor="middle">after work</text>`,
		`<text class="fx-wint" x="206" y="82" text-anchor="middle">Tue 9–11 PM</text>`,
		// Veto: 6 PM (x=94) to 8 PM (x=150), dense hatch + reason bracket.
		`<rect class="veto-hatch" x="94" y="48" width="56" height="220"/>`,
		`<text class="fx-ann f4" x="94" y="40">&#10005; THUNDERSTORMS</text>`,
		`<path class="lx-brk b4" d="M 94 44 V 48 M 94 44 H 150 M 150 44 V 48"/>`,
		// Advisory bracket across 3–8 PM.
		`<text class="fx-ann f3" x="10" y="16">HEAT ADVISORY — UNTIL 8 PM</text>`,
		`<path class="lx-brk b3" d="M 10 26 V 22 H 150 V 26"/>`,
		// Gridline at 90°F: y = 48 + (100−90)·5 = 98.
		`<line class="lx-grid" x1="0" y1="98" x2="336" y2="98"/>`,
		// Axis, ticks, labels.
		`<line class="lx-ax" x1="0" y1="268" x2="336" y2="268"/>`,
		`<text class="fx-hr" x="10" y="288" text-anchor="middle">3P</text>`,
		`<text class="fx-hr" x="94" y="288" text-anchor="middle">6P</text>`,
		`<text class="fx-hr" x="262" y="288" text-anchor="middle">12A</text>`,
		`<text class="fx-day" x="10" y="308">TUE JUL 28</text>`,
		`<text class="fx-day" x="268" y="308">WED JUL 29</text>`,
		// Midnight rule at x=262 plus its long tick.
		`<line class="lx-day" x1="262" y1="48" x2="262" y2="268"/>`,
		`<path class="lx-day" d="M262 268v12"/>`,
		// Sunset 8:15 PM at the exact minute: x = 10 + 28·5.25 = 157.
		`<use href="#g-sunset" data-glyph="sunset" x="149" y="252" width="16" height="16" class="sun-g"/>`,
		`<text class="fx-sun" x="157" y="308" text-anchor="middle">sets 8:15</text>`,
		// 60% rain bar at 6 PM: height round(60·0.39)=23, top 244, x 94−9=85.
		`<rect class="rainbar" x="85" y="244" width="18" height="23"/>`,
		`<use href="#g-bolt" x="89" y="229" width="10" height="12.8" class="bolt-g"/>`,
		`<text class="fx-pt f4" data-precip-tag x="84" y="239" text-anchor="end">60%</text>`,
		// WBGT direct label at the first point (max 91 → extreme, tier 4);
		// y(91) = 48 + (100−91)·5 = 93.
		`<circle class="fd4" cx="10" cy="93" r="1.8"/>`,
		`>91&#176; extreme</text>`,
		`</svg>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("output missing %q", want)
		}
	}

	// Sunrise 5:48 AM is before the chart's first hour: must not render.
	if strings.Contains(s, "rises 5:48") {
		t.Error("out-of-range sunrise rendered")
	}
	// WBGT series 90..72 descends through tiers 4,3,2,1,0: one polyline each.
	for _, cls := range []string{`class="cw4"`, `class="cw3"`, `class="cw2"`, `class="cw1"`, `class="cw0"`} {
		if got := strings.Count(s, cls); got != 1 {
			t.Errorf("count(%s) = %d, want 1", cls, got)
		}
	}
	// No category words in the series polylines themselves, and temps neutral.
	if strings.Contains(s, `class="ln-air"`) == false || strings.Contains(s, `class="ln-dew"`) == false {
		t.Error("missing neutral air/dew series")
	}
}

// TestRender48hMockupScale renders a full 48-hour series and checks the
// mockup-scale invariants: 1344 px wide, one hour label per 3-hour tick,
// midnight day rules, sun marks at exact minutes. Set CHART_DUMP=/path to
// write the SVG out for eyeballing against the mockup.
func TestRender48hMockupScale(t *testing.T) {
	start := time.Date(2026, 7, 28, 15, 0, 0, 0, ny)
	tg := tag()
	hs := make([]domain.Hour, 48)
	for i := range hs {
		ts := start.Add(time.Duration(i) * time.Hour)
		hs[i].Time = ts
		phase := float64(i) / 24 * 2 * math.Pi
		w := math.Round(81 + 8*math.Cos(phase-0.5))
		hs[i].WBGTF = domain.Val(w, tg)
		hs[i].TempF = domain.Val(w+3, tg)
		hs[i].DewPointF = domain.Val(70, tg)
		if i >= 3 && i <= 8 {
			hs[i].PoP = domain.Val(float64(60-8*(i-3)), tg)
		}
		if i == 4 {
			hs[i].WxThunder = true
		}
	}
	windows := []Band{
		{Kind: "best", Start: start.Add(14 * time.Hour), End: start.Add(17 * time.Hour), Label: "before work", Sub: "Wed 5–8 AM"},
		{Kind: "veto", Start: start.Add(4 * time.Hour), End: start.Add(6 * time.Hour), Label: "thunderstorms"},
		{Kind: "advisory", Start: start, End: start.Add(5 * time.Hour), Label: "Heat Advisory — until 8 PM"},
	}
	sun := []merge.SunTimes{
		{Sunrise: time.Date(2026, 7, 28, 5, 48, 0, 0, ny), Sunset: time.Date(2026, 7, 28, 20, 15, 0, 0, ny)},
		{Sunrise: time.Date(2026, 7, 29, 5, 49, 0, 0, ny), Sunset: time.Date(2026, 7, 29, 20, 14, 0, 0, ny)},
		{Sunrise: time.Date(2026, 7, 30, 5, 50, 0, 0, ny), Sunset: time.Date(2026, 7, 30, 20, 13, 0, 0, ny)},
	}
	svg, meta, err := Render(hs, windows, sun, ny, "F")
	if err != nil {
		t.Fatal(err)
	}
	s := string(svg)
	if meta.WidthPX != 1344 {
		t.Errorf("WidthPX = %d, want 1344 (mockup)", meta.WidthPX)
	}
	// 16 labeled ticks (every 3 h incl. 12A midnights) over 48 hours.
	if got := strings.Count(s, `class="fx-hr"`); got != 16 {
		t.Errorf("hour labels = %d, want 16", got)
	}
	// 3 day labels (first hour + two midnights) and 2 midnight rules.
	if got := strings.Count(s, `class="fx-day"`); got != 3 {
		t.Errorf("day labels = %d, want 3", got)
	}
	if got := strings.Count(s, `<line class="lx-day"`); got != 2 {
		t.Errorf("midnight rules = %d, want 2", got)
	}
	// Sun marks inside the 48 h span: Tue sunset, Wed rise+set, Thu rise = 4.
	if got := strings.Count(s, `data-glyph=`); got != 4 {
		t.Errorf("sun glyphs = %d, want 4", got)
	}
	// Wed sunrise 5:49 AM = 14h49m from 3 PM: x = 10 + 28·14.8167 = 424.87.
	if !strings.Contains(s, `<text class="fx-sun" x="424.87" y="308" text-anchor="middle">rises 5:49</text>`) {
		t.Error("Wed sunrise mark not at the exact minute")
	}
	if dump := os.Getenv("CHART_DUMP"); dump != "" {
		if err := os.WriteFile(dump, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
