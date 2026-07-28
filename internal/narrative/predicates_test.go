package narrative

import (
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/rank"
)

func nyT(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatalf("load America/New_York: %v", err)
	}
	return loc
}

func metric(v float64) domain.Metric {
	return domain.Val(v, domain.SourceTag{Origin: domain.OriginNWS})
}

// mkHours builds n consecutive hours from start, letting set fill each one
// (lt is the hour's local time in start's location).
func mkHours(start time.Time, n int, set func(lt time.Time, h *domain.Hour)) []domain.Hour {
	loc := start.Location()
	hs := make([]domain.Hour, n)
	for i := range hs {
		tt := start.Add(time.Duration(i) * time.Hour)
		hs[i] = domain.Hour{Time: tt}
		if set != nil {
			set(tt.In(loc), &hs[i])
		}
	}
	return hs
}

func TestHeatAdvisoryActive(t *testing.T) {
	loc := nyT(t)
	now := time.Date(2026, 7, 28, 15, 0, 0, 0, loc)
	mk := func(event string, onset, ends time.Time) domain.Alert {
		return domain.Alert{Event: event, Onset: onset, Ends: ends}
	}
	cases := []struct {
		name   string
		alerts []domain.Alert
		want   bool
	}{
		{"active advisory", []domain.Alert{mk("Heat Advisory", now.Add(-4*time.Hour), now.Add(5*time.Hour))}, true},
		{"excessive heat warning", []domain.Alert{mk("Excessive Heat Warning", time.Time{}, time.Time{})}, true},
		{"expired", []domain.Alert{mk("Heat Advisory", now.Add(-8*time.Hour), now.Add(-time.Hour))}, false},
		{"not yet onset", []domain.Alert{mk("Heat Advisory", now.Add(2*time.Hour), now.Add(9*time.Hour))}, false},
		{"wrong event", []domain.Alert{mk("Flash Flood Warning", time.Time{}, time.Time{})}, false},
		{"none", nil, false},
	}
	for _, tc := range cases {
		if _, got := heatAdvisoryActive(tc.alerts, now); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestThunderVetoed(t *testing.T) {
	cases := []struct {
		name string
		h    domain.Hour
		want bool
	}{
		{"wx layer thunder", domain.Hour{WxThunder: true}, true},
		{"prob at threshold", domain.Hour{ThunderProb: metric(rank.VetoThunderProbPct)}, true},
		{"prob below threshold", domain.Hour{ThunderProb: metric(rank.VetoThunderProbPct - 0.1)}, false},
		{"no data", domain.Hour{}, false},
	}
	for _, tc := range cases {
		if got := thunderVetoed(tc.h); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEveningStormHoursAndClearBy(t *testing.T) {
	loc := nyT(t)
	day := time.Date(2026, 7, 28, 0, 0, 0, 0, loc)
	hours := mkHours(day, 24, func(lt time.Time, h *domain.Hour) {
		switch lt.Hour() {
		case 15, 19, 21, 22: // 15 is before the window, 22 is past it
			h.ThunderProb = metric(60)
		}
	})

	hs := eveningStormHours(hours, day, loc)
	if len(hs) != 2 || hs[0].Time.Hour() != 19 || hs[1].Time.Hour() != 21 {
		t.Fatalf("eveningStormHours: got %d hours %v, want the 19h and 21h rows", len(hs), hs)
	}

	clear, ok := stormsClearBy(hours, day, loc)
	if !ok || !clear.Equal(day.Add(22*time.Hour)) {
		t.Errorf("stormsClearBy: got %v ok=%v, want %v", clear, ok, day.Add(22*time.Hour))
	}

	if _, ok := stormsClearBy(hours, day.AddDate(0, 0, 1), loc); ok {
		t.Error("stormsClearBy on a storm-free day: got ok=true")
	}
}

func TestFrontClearing(t *testing.T) {
	loc := nyT(t)
	day := time.Date(2026, 7, 28, 0, 0, 0, 0, loc)
	build := func(eveDew, mornDew float64, withMorning bool) []domain.Hour {
		return mkHours(day.Add(12*time.Hour), 30, func(lt time.Time, h *domain.Hour) {
			if sameLocalDay(lt, day) && lt.Hour() >= 18 {
				h.DewPointF = metric(eveDew)
			}
			if withMorning && !sameLocalDay(lt, day) && lt.Hour() >= 4 && lt.Hour() < 9 {
				h.DewPointF = metric(mornDew)
			}
		})
	}

	if drop, ok := frontClearing(build(74, 69, true), day, loc); !ok || drop != 5 {
		t.Errorf("drop of 5: got drop=%v ok=%v, want 5 true", drop, ok)
	}
	if _, ok := frontClearing(build(74, 69.5, true), day, loc); ok {
		t.Error("drop of 4.5: got ok=true, want false")
	}
	if _, ok := frontClearing(build(74, 0, false), day, loc); ok {
		t.Error("missing morning dew: got ok=true, want false")
	}
}

func TestHeatRebuilding(t *testing.T) {
	loc := nyT(t)
	day := time.Date(2026, 7, 29, 0, 0, 0, 0, loc)
	build := func(floor, peak float64, withFloor bool) []domain.Hour {
		return mkHours(day, 24, func(lt time.Time, h *domain.Hour) {
			if withFloor && lt.Hour() >= 5 && lt.Hour() < 9 {
				h.WBGTF = metric(floor)
			}
			if lt.Hour() >= 10 && lt.Hour() < 16 {
				h.WBGTF = metric(peak)
			}
		})
	}

	if peak, ok := heatRebuilding(build(74, 89, true), day, loc); !ok || peak != 89 {
		t.Errorf("74→89: got peak=%v ok=%v, want 89 true", peak, ok)
	}
	if _, ok := heatRebuilding(build(78, 85.5, true), day, loc); ok {
		t.Error("slope 7.5 < 8: got ok=true, want false")
	}
	if _, ok := heatRebuilding(build(70, 84.5, true), day, loc); ok {
		t.Error("peak 84.5 < 85: got ok=true, want false")
	}
	if _, ok := heatRebuilding(build(0, 89, false), day, loc); ok {
		t.Error("missing morning floor: got ok=true, want false")
	}
}

func TestStormOutlook(t *testing.T) {
	loc := nyT(t)
	day := time.Date(2026, 8, 13, 0, 0, 0, 0, loc)
	hours := mkHours(day, 48, func(lt time.Time, h *domain.Hour) {
		if lt.Day() == 13 && lt.Hour() >= 16 && lt.Hour() <= 18 {
			h.WxThunder = true
		}
	})
	at := func(h int) time.Time { return day.Add(time.Duration(h) * time.Hour) }

	cases := []struct {
		now  time.Time
		want phase
	}{
		{at(12), stormApproaching},
		{at(16), stormActive},
		{at(18), stormActive},
		{at(19), stormCleared}, // the hour it clears
		{at(22), stormCleared}, // still fresh at +3 h
		{at(23), stormNone},    // stale: the ledger stops mentioning it
	}
	for _, tc := range cases {
		got, in, out := stormOutlook(hours, day, loc, tc.now)
		if got != tc.want {
			t.Errorf("stormOutlook at %v: got phase %d, want %d", tc.now, got, tc.want)
		}
		if !in.Equal(at(16)) || !out.Equal(at(19)) {
			t.Errorf("stormOutlook at %v: got span %v–%v, want 16:00–19:00", tc.now, in, out)
		}
	}
	if got, _, _ := stormOutlook(mkHours(day, 48, nil), day, loc, at(12)); got != stormNone {
		t.Errorf("no storms: got phase %d, want stormNone", got)
	}
	if !stormApproaching.pending() || !stormActive.pending() || stormCleared.pending() {
		t.Error("pending(): want approaching and active only")
	}
}

func TestSnowLikely(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 1, 20, 6, 0, 0, 0, loc)
	build := func(temp, pop float64, n int) []domain.Hour {
		return mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
			if int(lt.Sub(start)/time.Hour) < n {
				h.TempF, h.PoP = metric(temp), metric(pop)
			}
		})
	}
	if !snowLikely(build(30, 70, 6)) {
		t.Error("cold and wet for 6 h: got false, want true")
	}
	if snowLikely(build(30, 70, 1)) {
		t.Error("one wet hour is a flurry, not a snow day: got true, want false")
	}
	if snowLikely(build(40, 70, 6)) {
		t.Error("40°F rain: got true, want false")
	}
	if snowLikely(build(30, 20, 6)) {
		t.Error("cold and dry: got true, want false")
	}
	// Freezing rain belongs to icingAhead, not here.
	fr := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		h.TempF, h.PoP, h.WxFreezingRain = metric(30), metric(80), true
	})
	if snowLikely(fr) {
		t.Error("freezing rain counted as snow: got true, want false")
	}
}

func TestRainStartAndEveningEase(t *testing.T) {
	loc := nyT(t)
	day := time.Date(2026, 5, 7, 0, 0, 0, 0, loc)
	hours := mkHours(day, 48, func(lt time.Time, h *domain.Hour) {
		switch {
		case lt.Day() == 7 && lt.Hour() == 8: // a single wet hour: a shower
			h.PoP = metric(80)
		case lt.Day() == 7 && lt.Hour() >= 13 && lt.Hour() <= 17:
			h.PoP = metric(70)
		default:
			h.PoP = metric(10)
		}
	})
	got, ok := rainStart(hours, day, loc)
	if !ok || !got.Equal(day.Add(13*time.Hour)) {
		t.Errorf("rainStart: got %v ok=%v, want 13:00 true", got, ok)
	}
	if _, ok := rainStart(mkHours(day, 48, nil), day, loc); ok {
		t.Error("no PoP data: got ok=true, want false")
	}

	hot := mkHours(day, 48, func(lt time.Time, h *domain.Hour) {
		wbgt := 76.0
		if hr := lt.Hour(); hr >= 10 && hr < 18 {
			wbgt = 88
		}
		h.WBGTF = metric(wbgt)
	})
	ease, ok := eveningEase(hot, day, loc)
	if !ok || !ease.Equal(day.Add(18*time.Hour)) {
		t.Errorf("eveningEase: got %v ok=%v, want 18:00 true", ease, ok)
	}
	// A day that never got hot has nothing to ease from.
	mild := mkHours(day, 48, func(lt time.Time, h *domain.Hour) { h.WBGTF = metric(74) })
	if _, ok := eveningEase(mild, day, loc); ok {
		t.Error("mild day: got ok=true, want false")
	}
	// Storm days keep the storm dividers instead.
	stormy := mkHours(day, 48, func(lt time.Time, h *domain.Hour) {
		wbgt := 76.0
		if hr := lt.Hour(); hr >= 10 && hr < 18 {
			wbgt = 88
		}
		h.WBGTF = metric(wbgt)
		if lt.Hour() == 17 {
			h.WxThunder = true
		}
	})
	if _, ok := eveningEase(stormy, day, loc); ok {
		t.Error("storm day: got ok=true, want false")
	}
}

func TestSmokeAirAndPeakDew(t *testing.T) {
	pm := func(aqi float64, pollutant string) domain.Hour {
		return domain.Hour{AQI: metric(aqi), AQIPollutant: pollutant}
	}
	if !smokeAir(pm(168, "PM2.5")) {
		t.Error("AQI 168 on PM2.5: got false, want true")
	}
	if smokeAir(pm(168, "O3")) {
		t.Error("ozone is not smoke: got true, want false")
	}
	if smokeAir(pm(120, "PM2.5")) {
		t.Error("AQI 120 is below the smoke floor: got true, want false")
	}

	loc := nyT(t)
	start := time.Date(2026, 8, 20, 5, 0, 0, 0, loc)
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		dew := 68.0
		if int(lt.Sub(start)/time.Hour) == 6 {
			dew = 76
		}
		if lt.Sub(start) >= nearTermHours*time.Hour {
			dew = 90 // beyond the near term, must not count
		}
		h.DewPointF = metric(dew)
	})
	if dew, ok := peakDew(hours); !ok || dew != 76 {
		t.Errorf("peakDew: got %v ok=%v, want 76 true", dew, ok)
	}
	if _, ok := peakDew(mkHours(start, 48, nil)); ok {
		t.Error("no dew data: got ok=true, want false")
	}
}

func TestRainyAndBreezyWindow(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 5, 7, 5, 0, 0, 0, loc)
	win := func(set func(i int, h *domain.Hour)) rank.Window {
		hs := mkHours(start, 4, func(lt time.Time, h *domain.Hour) {})
		for i := range hs {
			set(i, &hs[i])
		}
		return rank.Window{Hours: hs}
	}

	if pop, ok := rainyWindow(win(func(i int, h *domain.Hour) {
		h.PoP = metric(float64(20 + 20*i)) // 20, 40, 60, 80
	})); !ok || pop != 80 {
		t.Errorf("rainyWindow: got %v ok=%v, want 80 true", pop, ok)
	}
	if _, ok := rainyWindow(win(func(i int, h *domain.Hour) { h.PoP = metric(30) })); ok {
		t.Error("30%% PoP: got ok=true, want false")
	}

	wind, gust, dir, ok := breezyWindow(win(func(i int, h *domain.Hour) {
		h.WindMPH = metric(float64(10 + 4*i)) // 10, 14, 18, 22
		h.GustMPH = metric(float64(16 + 6*i)) // 16, 22, 28, 34
		h.WindDirDeg = metric(270)
	}))
	if !ok || wind != 22 || gust != 34 || dir != "W" {
		t.Errorf("breezyWindow: got %v/%v %q ok=%v, want 22/34 W true", wind, gust, dir, ok)
	}
	// A partial wind sentence is worse than none.
	if _, _, _, ok := breezyWindow(win(func(i int, h *domain.Hour) {
		h.WindMPH, h.GustMPH = metric(24), metric(38)
	})); ok {
		t.Error("no wind direction: got ok=true, want false")
	}
	if _, _, _, ok := breezyWindow(win(func(i int, h *domain.Hour) {
		h.WindMPH, h.GustMPH, h.WindDirDeg = metric(8), metric(12), metric(90)
	})); ok {
		t.Error("light air: got ok=true, want false")
	}
}

func TestTodayAllVetoedAndSiblingOut(t *testing.T) {
	all := []rank.Window{
		{Label: "morning", DayLabel: "today", Vetoed: true},
		{Label: "evening", DayLabel: "today", Vetoed: true},
		{Label: "morning", DayLabel: "tomorrow", Rank: 1},
	}
	if !todayAllVetoed(all) {
		t.Error("every today window struck out: got false, want true")
	}
	if todayAllVetoed([]rank.Window{{DayLabel: "today"}, {DayLabel: "today", Vetoed: true}}) {
		t.Error("one today window survives: got true, want false")
	}
	// No today windows at all (late evening) is not the same as all vetoed.
	if todayAllVetoed([]rank.Window{{DayLabel: "tomorrow"}}) {
		t.Error("no today windows: got true, want false")
	}

	chosen := rank.Window{Label: "evening", DayLabel: "today"}
	if key, ok := siblingOut(all, chosen); !ok || key != SitBeforeWorkOut {
		t.Errorf("siblingOut: got %q ok=%v, want before-work-out true", key, ok)
	}
	morning := rank.Window{Label: "morning", DayLabel: "today"}
	if key, ok := siblingOut(all, morning); !ok || key != SitAfterWorkOut {
		t.Errorf("siblingOut for a morning pick: got %q ok=%v, want after-work-out true", key, ok)
	}
	if _, ok := siblingOut([]rank.Window{{Label: "morning", DayLabel: "today", Rank: 1}}, chosen); ok {
		t.Error("sibling not struck out: got ok=true, want false")
	}
}

func TestWindowKeyAndSpanWord(t *testing.T) {
	cases := []struct {
		label string
		best  bool
		key   SituationKey
		span  string
	}{
		{"morning", true, SitRunBeforeWorkBest, "before work"},
		{"morning", false, SitRunBeforeWorkGood, "before work"},
		{"evening", true, SitRunAfterWorkBest, "after work"},
		{"evening", false, SitRunAfterWorkGood, "after work"},
		{"midday", true, SitRunMidday, "midday"},
	}
	for _, tc := range cases {
		if got := windowKey(tc.label, tc.best); got != tc.key {
			t.Errorf("windowKey(%q, %v): got %q, want %q", tc.label, tc.best, got, tc.key)
		}
		if got := spanWord(tc.label); got != tc.span {
			t.Errorf("spanWord(%q): got %q, want %q", tc.label, got, tc.span)
		}
	}
}

func TestPoorAir(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 7, 28, 6, 0, 0, 0, loc)
	withAQI := func(v float64) []domain.Hour {
		return mkHours(start, 48, func(lt time.Time, h *domain.Hour) { h.AQI = metric(v) })
	}

	if aqi, cat, ok := poorAir(withAQI(150)); !ok || aqi != 150 || cat.Label != "Unhealthy for Sensitive Groups" {
		t.Errorf("AQI 150: got %v %q ok=%v, want USG true", aqi, cat.Label, ok)
	}
	if _, cat, ok := poorAir(withAQI(168)); !ok || cat.Label != "Unhealthy" {
		t.Errorf("AQI 168: got %q ok=%v, want Unhealthy true", cat.Label, ok)
	}
	if _, _, ok := poorAir(withAQI(100)); ok {
		t.Error("AQI 100 (Moderate): got ok=true, want false")
	}
	if _, _, ok := poorAir(mkHours(start, 48, nil)); ok {
		t.Error("no AQI data: got ok=true, want false")
	}
	// Only the first known near-term value counts; a spike beyond the
	// near-term horizon must not fire the clause.
	late := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		if lt.Sub(start) >= nearTermHours*time.Hour {
			h.AQI = metric(180)
		}
	})
	if _, _, ok := poorAir(late); ok {
		t.Error("AQI spike beyond near term: got ok=true, want false")
	}
}

func TestIcingAhead(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 1, 17, 6, 0, 0, 0, loc)

	fr := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		if lt.Sub(start) == 3*time.Hour {
			h.WxFreezingRain = true
		}
	})
	if !icingAhead(fr) {
		t.Error("freezing rain at +3h: got false, want true")
	}

	ice := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		if lt.Sub(start) == 5*time.Hour {
			h.IceAccumIn = metric(0.05)
		}
	})
	if !icingAhead(ice) {
		t.Error("ice accumulation at +5h: got false, want true")
	}

	far := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		if lt.Sub(start) >= 30*time.Hour {
			h.WxFreezingRain = true
		}
	})
	if icingAhead(far) {
		t.Error("icing only beyond near term: got true, want false")
	}
	if icingAhead(mkHours(start, 48, nil)) {
		t.Error("no icing: got true, want false")
	}
}

func TestColdestChill(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 1, 17, 6, 0, 0, 0, loc)
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		switch d := lt.Sub(start) / time.Hour; {
		case d == 4:
			h.WindChillF = metric(-12)
		case d < 24:
			h.WindChillF = metric(8)
		default:
			h.WindChillF = metric(-40) // beyond near term, must be ignored
		}
	})
	if min, ok := coldestChill(hours); !ok || min != -12 {
		t.Errorf("got min=%v ok=%v, want -12 true", min, ok)
	}
	if _, ok := coldestChill(mkHours(start, 48, nil)); ok {
		t.Error("no wind chill data: got ok=true, want false")
	}
}

func TestBestWindowAndWindowFor(t *testing.T) {
	ws := []rank.Window{
		{Label: "evening", DayLabel: "today", Rank: 1},
		{Label: "morning", DayLabel: "tomorrow", Rank: 2},
		{Label: "midday", DayLabel: "today", Vetoed: true},
	}

	if w, ok := bestWindow(ws, false); !ok || w.DayLabel != "today" || w.Label != "evening" {
		t.Errorf("bestWindow: got %+v ok=%v, want today evening", w, ok)
	}
	if w, ok := bestWindow(ws, true); !ok || w.DayLabel != "tomorrow" || w.Label != "morning" {
		t.Errorf("bestWindow excludeToday: got %+v ok=%v, want tomorrow morning", w, ok)
	}
	if _, ok := bestWindow([]rank.Window{{Vetoed: true}}, false); ok {
		t.Error("all vetoed: got ok=true, want false")
	}

	if _, ok := windowFor(ws, "tomorrow", "morning"); !ok {
		t.Error("windowFor tomorrow morning: got ok=false, want true")
	}
	if _, ok := windowFor(ws, "today", "midday"); ok {
		t.Error("windowFor vetoed window: got ok=true, want false")
	}
}

func TestCleanWindow(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 10, 6, 5, 0, 0, 0, loc)
	build := func(set func(h *domain.Hour)) rank.Window {
		hs := mkHours(start, 4, func(lt time.Time, h *domain.Hour) {
			h.DewPointF = metric(48)
			h.AQI = metric(35)
			set(h)
		})
		return rank.Window{Hours: hs}
	}

	if !cleanWindow(build(func(h *domain.Hour) {})) {
		t.Error("dry, clean air, no WBGT: got false, want true")
	}
	if cleanWindow(build(func(h *domain.Hour) { h.DewPointF = metric(63) })) {
		t.Error("sticky dew point: got true, want false")
	}
	if cleanWindow(build(func(h *domain.Hour) { h.AQI = domain.Metric{} })) {
		t.Error("unknown AQI: got true, want false")
	}
	if cleanWindow(build(func(h *domain.Hour) { h.WBGTF = metric(82) })) {
		t.Error("elevated WBGT: got true, want false")
	}
	if cleanWindow(rank.Window{}) {
		t.Error("empty window: got true, want false")
	}
}

func TestClockFormatting(t *testing.T) {
	loc := nyT(t)
	at := func(h, m int) time.Time { return time.Date(2026, 7, 28, h, m, 0, 0, loc) }

	clocks := []struct {
		t    time.Time
		want string
	}{
		{at(20, 0), "8 PM"}, {at(0, 0), "12 AM"}, {at(12, 0), "12 PM"},
		{at(5, 0), "5 AM"}, {at(20, 30), "8:30 PM"},
	}
	for _, tc := range clocks {
		if got := fmtClock(tc.t); got != tc.want {
			t.Errorf("fmtClock(%v): got %q, want %q", tc.t, got, tc.want)
		}
	}

	ranges := []struct {
		s, e time.Time
		want string
	}{
		{at(5, 0), at(9, 0), "5–8 AM"},
		{at(11, 0), at(14, 0), "11 AM–1 PM"},
		{at(16, 0), at(0, 0).AddDate(0, 0, 1), "4–11 PM"},
	}
	for _, tc := range ranges {
		if got := fmtClockRange(tc.s, tc.e, loc); got != tc.want {
			t.Errorf("fmtClockRange(%v, %v): got %q, want %q", tc.s, tc.e, got, tc.want)
		}
	}

	// A morning window extended into the small hours clamps to 5 AM.
	w := rank.Window{Label: "morning", Start: at(2, 0), End: at(9, 0)}
	if got := fmtWindowRange(w, loc); got != "5–8 AM" {
		t.Errorf("fmtWindowRange clamp: got %q, want %q", got, "5–8 AM")
	}

	if got := fmtAlertEnd(domain.Alert{}, loc); got != "further notice" {
		t.Errorf("fmtAlertEnd zero end: got %q, want %q", got, "further notice")
	}
	if got := fmtAlertEnd(domain.Alert{Ends: at(20, 0)}, loc); got != "8 PM" {
		t.Errorf("fmtAlertEnd: got %q, want %q", got, "8 PM")
	}
}

func TestRenderAndFill(t *testing.T) {
	bank := Bank{SitNoWindow: {"alpha", "beta", "gamma"}}

	// Deterministic rotation: yearDay % variant count.
	for yd, want := range map[int]string{0: "alpha", 1: "beta", 2: "gamma", 3: "alpha", 209: "gamma"} {
		if got := render(bank, SitNoWindow, yd, nil); got != want {
			t.Errorf("render yd=%d: got %q, want %q", yd, got, want)
		}
	}

	if got := render(bank, SitIcing, 0, nil); got != "" {
		t.Errorf("render missing key: got %q, want empty", got)
	}

	got := fill("run {day}, {range}; {unknown}", map[string]string{"day": "today", "range": "5–8 AM"})
	if want := "run today, 5–8 AM; {unknown}"; got != want {
		t.Errorf("fill: got %q, want %q", got, want)
	}
}
