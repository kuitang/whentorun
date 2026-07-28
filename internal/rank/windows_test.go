package rank

import (
	"strings"
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

// TestBestWindowsWarmCanary is the brief's canary at window level: today's
// morning is thermally excellent (WBGT 64.4°F = 18°C) but has AQI 160;
// tomorrow's morning is WBGT-similar with clean air. Tomorrow morning must
// win, and the explanation must surface the official AQI category — the
// pollution is never averaged away.
func TestBestWindowsWarmCanary(t *testing.T) {
	loc := nycLoc(t)
	start := time.Date(2026, 7, 28, 5, 0, 0, 0, loc)
	now := time.Date(2026, 7, 28, 4, 0, 0, 0, loc)

	hours := mkHours(start, 48, pleasantWarm, func(i int, h *domain.Hour) {
		h.WBGTF = v(82) // elevated heat stress everywhere by default
		switch {
		case i <= 3: // today 5-8: thermally excellent, polluted
			h.WBGTF = v(64.4)
			h.AQI = v(160)
			h.AQIPollutant = "PM2.5"
		case i >= 24 && i <= 27: // tomorrow 5-8: WBGT-similar, clean air
			h.WBGTF = v(64.9)
			h.AQI = v(40)
		}
	})

	ws, err := BestWindows(hours, nil, now)
	if err != nil {
		t.Fatalf("BestWindows: %v", err)
	}
	if len(ws) != 6 {
		t.Fatalf("got %d windows (%v), want 6", len(ws), windowNames(ws))
	}

	best := ws[0]
	if best.DayLabel != "tomorrow" || best.Label != "morning" || best.Rank != 1 {
		t.Fatalf("best window = %s %s rank %d, want tomorrow morning rank 1", best.DayLabel, best.Label, best.Rank)
	}
	for _, sub := range []string{"air quality (AQI)", "Good", "Unhealthy"} {
		if !strings.Contains(best.Explanation, sub) {
			t.Errorf("best explanation %q missing %q (AQI category must be surfaced)", best.Explanation, sub)
		}
	}

	second := ws[1]
	if second.DayLabel != "today" || second.Label != "morning" || second.Rank != 2 {
		t.Fatalf("second window = %s %s rank %d, want today morning rank 2", second.DayLabel, second.Label, second.Rank)
	}
	if !strings.Contains(second.Explanation, "Unhealthy") {
		t.Errorf("second explanation %q must surface the Unhealthy AQI category", second.Explanation)
	}

	// Polluted-but-cool morning still beats every elevated-WBGT window, and
	// the explanation for the window below it names the WBGT key.
	third := ws[2]
	if got := wbgtBucket(worstHour(SeasonWarm, third.Hours).WBGTF).v; got != 1 {
		t.Fatalf("third window WBGT bucket = %d, want 1 (elevated)", got)
	}
	if !strings.Contains(third.Explanation, "heat stress (WBGT)") {
		t.Errorf("third explanation %q should name the WBGT key", third.Explanation)
	}

	// The four identical elevated windows tie and keep chronological order.
	wantOrder := [][2]string{
		{"today", "midday"}, {"today", "evening"},
		{"tomorrow", "midday"}, {"tomorrow", "evening"},
	}
	for i, want := range wantOrder {
		w := ws[2+i]
		if w.DayLabel != want[0] || w.Label != want[1] {
			t.Errorf("ws[%d] = %s %s, want %s %s", 2+i, w.DayLabel, w.Label, want[0], want[1])
		}
		if w.Rank != 3+i {
			t.Errorf("ws[%d].Rank = %d, want %d", 2+i, w.Rank, 3+i)
		}
	}
	if !strings.Contains(ws[3].Explanation, "tied") {
		t.Errorf("tied window explanation = %q, want tie wording", ws[3].Explanation)
	}

	// No vetoes anywhere in this fixture.
	for _, w := range ws {
		if w.Vetoed {
			t.Errorf("%s %s unexpectedly vetoed: %v", w.DayLabel, w.Label, w.VetoReasons)
		}
	}
	// Base morning window shape: 4 hours, 05:00–09:00.
	if len(best.Hours) != 4 {
		t.Errorf("best window has %d hours, want 4 (no extension into elevated hours)", len(best.Hours))
	}
	if best.Start.Hour() != 5 || best.End.Hour() != 9 {
		t.Errorf("best window %v–%v, want 05:00–09:00", best.Start, best.End)
	}
}

func TestBestWindowsVetoes(t *testing.T) {
	loc := nycLoc(t)
	start := time.Date(2026, 7, 28, 5, 0, 0, 0, loc)
	now := time.Date(2026, 7, 28, 4, 0, 0, 0, loc)

	hours := mkHours(start, 48, pleasantWarm, func(i int, h *domain.Hour) {
		if i >= 30 && i <= 32 { // tomorrow 11-13: thunder
			h.ThunderProb = v(60)
		}
	})
	// Flash Flood Warning covering part of today's evening window.
	alerts := []domain.Alert{{
		ID: "test-ffw", Event: "Flash Flood Warning", Severity: "Severe",
		Onset: time.Date(2026, 7, 28, 16, 30, 0, 0, loc),
		Ends:  time.Date(2026, 7, 28, 19, 0, 0, 0, loc),
	}}

	ws, err := BestWindows(hours, alerts, now)
	if err != nil {
		t.Fatalf("BestWindows: %v", err)
	}

	evening := findWindow(t, ws, "today", "evening")
	if !evening.Vetoed || evening.Rank != 0 {
		t.Fatalf("today evening: Vetoed=%v Rank=%d, want vetoed with rank 0", evening.Vetoed, evening.Rank)
	}
	if len(evening.VetoReasons) != 1 || evening.VetoReasons[0] != "Flash Flood Warning" {
		t.Errorf("evening veto reasons = %v, want exactly [\"Flash Flood Warning\"] (deduped across hours)", evening.VetoReasons)
	}
	if !strings.Contains(evening.Explanation, "not recommended") ||
		!strings.Contains(evening.Explanation, "Flash Flood Warning") {
		t.Errorf("evening explanation = %q, want not-recommended wording naming the warning", evening.Explanation)
	}

	midday := findWindow(t, ws, "tomorrow", "midday")
	if !midday.Vetoed {
		t.Fatal("tomorrow midday should be vetoed by thunder probability")
	}
	if len(midday.VetoReasons) != 1 || !strings.Contains(midday.VetoReasons[0], "thunder probability 60%") {
		t.Errorf("midday veto reasons = %v, want thunder probability", midday.VetoReasons)
	}

	// Ranked windows come first, vetoed windows last in chronological order.
	var seenVetoed bool
	for _, w := range ws {
		if w.Vetoed {
			seenVetoed = true
		} else if seenVetoed {
			t.Fatalf("ranked window %s %s after a vetoed one", w.DayLabel, w.Label)
		}
	}
	if n := len(ws); ws[n-2].Label != "evening" || ws[n-1].Label != "midday" {
		t.Errorf("vetoed tail = %v %v, want today evening then tomorrow midday", ws[n-2].Label, ws[n-1].Label)
	}
	for _, w := range ws[:len(ws)-2] {
		if w.Vetoed {
			t.Errorf("unexpected vetoed window %s %s", w.DayLabel, w.Label)
		}
	}
}

func TestWindowExtension(t *testing.T) {
	loc := nycLoc(t)
	start := time.Date(2026, 7, 28, 4, 0, 0, 0, loc)
	now := time.Date(2026, 7, 28, 4, 0, 0, 0, loc)

	// Hours 4-10 share the morning's low WBGT band; everything else is
	// elevated. The morning window should extend down to 04:00 and up
	// through 10:00, stopping at 11:00 only because that hour belongs to
	// the midday base window.
	lowClock := map[int]bool{4: true, 5: true, 6: true, 7: true, 8: true, 9: true, 10: true}
	hours := mkHours(start, 44, pleasantWarm, func(i int, h *domain.Hour) {
		lt := h.Time.In(loc)
		if lt.Day() == 28 && lowClock[lt.Hour()] {
			h.WBGTF = v(70)
		} else {
			h.WBGTF = v(82)
		}
	})

	ws, err := BestWindows(hours, nil, now)
	if err != nil {
		t.Fatalf("BestWindows: %v", err)
	}
	m := findWindow(t, ws, "today", "morning")
	if m.Start.Hour() != 4 || m.End.Hour() != 11 {
		t.Errorf("morning window %v–%v, want 04:00–11:00", m.Start, m.End)
	}
	if len(m.Hours) != 7 {
		t.Errorf("morning window has %d hours, want 7 (4..10)", len(m.Hours))
	}
	if m.Rank != 1 {
		t.Errorf("extended low-band morning rank = %d, want 1", m.Rank)
	}
	// Midday (elevated top bucket) may extend across equal-or-better
	// adjacent hours: down through the low-band 9-10 gap, up through the
	// elevated 14-15 gap, stopping at the evening base span.
	mid := findWindow(t, ws, "today", "midday")
	if mid.Start.Hour() != 9 {
		t.Errorf("midday start %v, want 09:00 (hours 9-10 are equal-or-better buckets)", mid.Start)
	}
	if mid.End.Hour() != 16 {
		t.Errorf("midday end %v, want 16:00 (hours 14-15 share the top bucket; 16 is evening base)", mid.End)
	}
}

func TestWindowExtensionStopsAtVetoedAndWorseHours(t *testing.T) {
	loc := nycLoc(t)
	start := time.Date(2026, 7, 28, 5, 0, 0, 0, loc)
	now := start

	hours := mkHours(start, 20, pleasantWarm, func(i int, h *domain.Hour) {
		lt := h.Time.In(loc)
		switch lt.Hour() {
		case 9: // adjacent to morning: vetoed by thunder
			h.ThunderProb = v(80)
		case 14, 15: // adjacent to midday: worse (elevated) bucket
			h.WBGTF = v(82)
		case 20: // adjacent to evening: worse bucket
			h.WBGTF = v(86)
		}
	})

	ws, err := BestWindows(hours, nil, now)
	if err != nil {
		t.Fatalf("BestWindows: %v", err)
	}
	m := findWindow(t, ws, "today", "morning")
	if m.End.Hour() != 9 {
		t.Errorf("morning end %v, want 09:00 (hour 9 vetoed, no extension)", m.End)
	}
	mid := findWindow(t, ws, "today", "midday")
	if mid.End.Hour() != 14 {
		t.Errorf("midday end %v, want 14:00 (hour 14 in a worse bucket)", mid.End)
	}
	ev := findWindow(t, ws, "today", "evening")
	if ev.End.Hour() != 20 {
		t.Errorf("evening end %v, want 20:00 (hour 20 in a worse bucket)", ev.End)
	}
	if mid.Start.Hour() != 10 {
		t.Errorf("midday start %v, want 10:00 (hours 10 extends, 9 vetoed)", mid.Start)
	}
}

func TestBestWindowsColdRanking(t *testing.T) {
	loc := nycLoc(t)
	start := time.Date(2026, 1, 15, 4, 0, 0, 0, loc)
	now := start

	// Only today is covered (20 hours), so tomorrow's windows are skipped.
	hours := mkHours(start, 20, pleasantCold, func(i int, h *domain.Hour) {
		switch hr := h.Time.In(loc).Hour(); {
		case hr >= 5 && hr <= 8: // morning: very cold
			h.WindChillF = v(5)
		case hr >= 11 && hr <= 13: // midday: mild
			h.WindChillF = v(35)
		case hr >= 16 && hr <= 19: // evening: cold
			h.WindChillF = v(15)
		default: // filler: bitter (worst non-veto band) blocks extension
			h.WindChillF = v(-5)
		}
	})

	ws, err := BestWindows(hours, nil, now)
	if err != nil {
		t.Fatalf("BestWindows: %v", err)
	}
	if len(ws) != 3 {
		t.Fatalf("got %d windows (%v), want 3 (tomorrow uncovered)", len(ws), windowNames(ws))
	}
	wantOrder := []string{"midday", "evening", "morning"}
	for i, want := range wantOrder {
		if ws[i].Label != want || ws[i].Rank != i+1 {
			t.Errorf("ws[%d] = %s rank %d, want %s rank %d", i, ws[i].Label, ws[i].Rank, want, i+1)
		}
	}
	if !strings.Contains(ws[0].Explanation, "wind chill") {
		t.Errorf("cold winner explanation = %q, want it to name wind chill", ws[0].Explanation)
	}
	if !strings.Contains(ws[0].Explanation, "mild") || !strings.Contains(ws[0].Explanation, "cold") {
		t.Errorf("cold winner explanation = %q, want band labels", ws[0].Explanation)
	}
}

func TestBestWindowsSkipsPastAndUncovered(t *testing.T) {
	loc := nycLoc(t)
	start := time.Date(2026, 7, 28, 5, 0, 0, 0, loc)
	// Noon: today's morning (ended 09:00) is past; midday (ends 14:00) is
	// still live even though it started already.
	now := time.Date(2026, 7, 28, 12, 0, 0, 0, loc)

	// Data through today 23:00 only: no tomorrow windows.
	hours := mkHours(start, 19, pleasantWarm, nil)

	ws, err := BestWindows(hours, nil, now)
	if err != nil {
		t.Fatalf("BestWindows: %v", err)
	}
	if len(ws) != 2 {
		t.Fatalf("got %d windows (%v), want 2", len(ws), windowNames(ws))
	}
	for _, w := range ws {
		if w.DayLabel != "today" {
			t.Errorf("unexpected %s window (tomorrow not covered by data)", w.DayLabel)
		}
	}
	if ws[0].Label == "morning" || ws[1].Label == "morning" {
		t.Error("morning window should be dropped once it has ended")
	}
}

// blockFiller makes every hour outside the base window spans "bitter"
// (worst non-veto wind-chill band) so windows keep their base shapes —
// the DST tests assert exact boundary instants, not extension behavior.
func blockFiller(loc *time.Location) func(int, *domain.Hour) {
	return func(_ int, h *domain.Hour) {
		hr := h.Time.In(loc).Hour()
		inSpan := (hr >= 5 && hr < 9) || (hr >= 11 && hr < 14) || (hr >= 16 && hr < 20)
		if !inSpan {
			h.WindChillF = v(-5)
		}
	}
}

// TestBestWindowsDSTFallBack: Nov 1 2026 is the fall-back day (2 AM EDT ->
// 1 AM EST; a 25-hour day with a repeated 1 AM). Windows must sit at the
// correct EST instants and keep their true 3/4-hour durations.
func TestBestWindowsDSTFallBack(t *testing.T) {
	loc := nycLoc(t)
	start := time.Date(2026, 11, 1, 0, 0, 0, 0, loc) // 00:00 EDT
	now := start.Add(30 * time.Minute)

	// Sanity: the fixture really crosses the transition (repeated 1 AM).
	hours := mkHours(start, 48, pleasantCold, blockFiller(loc))
	var ones int
	for _, h := range hours {
		lt := h.Time.In(loc)
		if lt.Day() == 1 && lt.Hour() == 1 {
			ones++
		}
	}
	if ones != 2 {
		t.Fatalf("fixture has %d local 1 AM hours on Nov 1, want 2 (fall-back)", ones)
	}

	ws, err := BestWindows(hours, nil, now)
	if err != nil {
		t.Fatalf("BestWindows: %v", err)
	}
	if len(ws) != 6 {
		t.Fatalf("got %d windows (%v), want 6", len(ws), windowNames(ws))
	}

	m := findWindow(t, ws, "today", "morning")
	wantStart := time.Date(2026, 11, 1, 5, 0, 0, 0, loc)
	if !m.Start.Equal(wantStart) {
		t.Errorf("morning start %v, want %v", m.Start, wantStart)
	}
	if _, off := m.Start.Zone(); off != -5*3600 {
		t.Errorf("morning start offset %d, want -18000 (EST after fall-back)", off)
	}
	if !m.Start.UTC().Equal(time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("morning start UTC %v, want 10:00Z", m.Start.UTC())
	}
	if got := m.End.Sub(m.Start); got != 4*time.Hour {
		t.Errorf("morning duration %v, want 4h", got)
	}
	if len(m.Hours) != 4 {
		t.Errorf("morning has %d hours, want 4", len(m.Hours))
	}
	mid := findWindow(t, ws, "today", "midday")
	if got := mid.End.Sub(mid.Start); got != 3*time.Hour {
		t.Errorf("midday duration %v, want 3h", got)
	}
	// The evening BEFORE the transition (Oct 31) was EDT; confirm today's
	// windows all sit in EST.
	for _, w := range ws {
		if w.DayLabel != "today" {
			continue
		}
		if _, off := w.Start.Zone(); off != -5*3600 {
			t.Errorf("%s start offset %d, want EST", w.Label, off)
		}
	}
}

// TestBestWindowsDSTSpringForward: Mar 8 2026 is the spring-forward day
// (2 AM EST -> 3 AM EDT; a 23-hour day with no 2 AM).
func TestBestWindowsDSTSpringForward(t *testing.T) {
	loc := nycLoc(t)
	start := time.Date(2026, 3, 8, 0, 0, 0, 0, loc) // 00:00 EST
	now := start.Add(30 * time.Minute)

	hours := mkHours(start, 48, pleasantCold, blockFiller(loc))
	for _, h := range hours {
		lt := h.Time.In(loc)
		if lt.Day() == 8 && lt.Hour() == 2 {
			t.Fatalf("fixture contains a local 2 AM on Mar 8; spring-forward should skip it")
		}
	}

	ws, err := BestWindows(hours, nil, now)
	if err != nil {
		t.Fatalf("BestWindows: %v", err)
	}
	if len(ws) != 6 {
		t.Fatalf("got %d windows (%v), want 6", len(ws), windowNames(ws))
	}

	m := findWindow(t, ws, "today", "morning")
	wantStart := time.Date(2026, 3, 8, 5, 0, 0, 0, loc)
	if !m.Start.Equal(wantStart) {
		t.Errorf("morning start %v, want %v", m.Start, wantStart)
	}
	if _, off := m.Start.Zone(); off != -4*3600 {
		t.Errorf("morning start offset %d, want -14400 (EDT after spring-forward)", off)
	}
	if !m.Start.UTC().Equal(time.Date(2026, 3, 8, 9, 0, 0, 0, time.UTC)) {
		t.Errorf("morning start UTC %v, want 09:00Z", m.Start.UTC())
	}
	if got := m.End.Sub(m.Start); got != 4*time.Hour {
		t.Errorf("morning duration %v, want 4h", got)
	}
	ev := findWindow(t, ws, "tomorrow", "evening")
	if got := ev.End.Sub(ev.Start); got != 4*time.Hour {
		t.Errorf("tomorrow evening duration %v, want 4h", got)
	}
}

func TestBestWindowsEmptyInput(t *testing.T) {
	ws, err := BestWindows(nil, nil, time.Date(2026, 7, 28, 4, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("BestWindows: %v", err)
	}
	if len(ws) != 0 {
		t.Errorf("got %d windows from empty input, want 0", len(ws))
	}
}
