package rank

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

// TZName is the fixed timezone for all window arithmetic.
const TZName = "America/New_York"

// baseSpan is a candidate window in local clock hours, end-exclusive.
type baseSpan struct {
	label  string
	startH int
	endH   int
}

// Candidate windows: morning 5–9, midday 11–14, evening 16–20 local.
var baseSpans = []baseSpan{
	{"morning", 5, 9},
	{"midday", 11, 14},
	{"evening", 16, 20},
}

// Window is a candidate running window, either ranked or vetoed.
type Window struct {
	Label    string // "morning" | "midday" | "evening"
	DayLabel string // "today" | "tomorrow"
	Start    time.Time
	End      time.Time // exclusive
	Hours    []domain.Hour

	Vetoed      bool
	VetoReasons []string // named reasons, deduped, deterministic order

	Rank        int    // 1-based among non-vetoed windows; 0 if vetoed
	Explanation string // UI-ready explanation of its placement
}

// BestWindows builds the candidate windows for today and tomorrow (relative
// to now, in America/New_York), vetoes any window containing a vetoed hour
// (collecting the named reasons), extends the survivors across adjacent
// same-top-bucket hours, and ranks them lexicographically.
//
// Returned order: ranked windows best-first (Rank 1..n), then vetoed windows
// chronologically. Windows that already ended, or whose base hours are not
// all present in the data, are omitted.
//
// Rules, documented:
//   - Hour matching is by LOCAL calendar day + clock hour, so DST-transition
//     days work: the repeated 1 AM on fall-back and the skipped 2 AM on
//     spring-forward never intersect the 5–20 window range, and window
//     boundary instants come from time.Date in the location.
//   - Extension: a window grows one hour at a time past its base span while
//     the adjacent hour exists, is not vetoed, is not part of another base
//     window on the same day, and its primary-key bucket (WBGT tier when
//     warm, wind-chill band when cold) is the same or better than the
//     window's worst primary bucket — extension never worsens the window's
//     top bucket. Hours that have already fully elapsed are not added.
//   - A window is ranked by its WORST hour under the seasonal comparator
//     (protective framing, matching the "worst nearby monitor" AQI stance);
//     ties break toward the earlier window.
func BestWindows(hours []domain.Hour, alerts []domain.Alert, now time.Time) ([]Window, error) {
	loc, err := time.LoadLocation(TZName)
	if err != nil {
		return nil, fmt.Errorf("rank: load %s: %w", TZName, err)
	}
	season := SeasonFor(hours, loc)

	// Index hours by (local year, yday, clock hour). On the fall-back day
	// two hours share local clock 1 AM; the earlier one wins, and 1 AM is
	// outside every window span anyway.
	type hkey struct{ y, yd, h int }
	idx := make(map[hkey]int, len(hours))
	for i, h := range hours {
		lt := h.Time.In(loc)
		k := hkey{lt.Year(), lt.YearDay(), lt.Hour()}
		if _, ok := idx[k]; !ok {
			idx[k] = i
		}
	}
	lookup := func(day time.Time, clockH int) (domain.Hour, bool) {
		i, ok := idx[hkey{day.Year(), day.YearDay(), clockH}]
		if !ok {
			return domain.Hour{}, false
		}
		return hours[i], true
	}

	// Clock hours claimed by base spans (same for every day).
	inBase := map[int]bool{}
	for _, sp := range baseSpans {
		for hh := sp.startH; hh < sp.endH; hh++ {
			inBase[hh] = true
		}
	}

	nowLocal := now.In(loc)
	today := time.Date(nowLocal.Year(), nowLocal.Month(), nowLocal.Day(), 0, 0, 0, 0, loc)
	days := []struct {
		day   time.Time
		label string
	}{
		{today, "today"},
		{today.AddDate(0, 0, 1), "tomorrow"},
	}

	var ranked, vetoed []Window
	for _, d := range days {
		for _, sp := range baseSpans {
			end := time.Date(d.day.Year(), d.day.Month(), d.day.Day(), sp.endH, 0, 0, 0, loc)
			if !end.After(now) {
				continue // window fully in the past
			}
			var ws []domain.Hour
			complete := true
			for hh := sp.startH; hh < sp.endH; hh++ {
				h, ok := lookup(d.day, hh)
				if !ok {
					complete = false
					break
				}
				ws = append(ws, h)
			}
			if !complete {
				continue // data does not cover the window
			}

			w := Window{Label: sp.label, DayLabel: d.label, Hours: ws}
			seen := map[string]bool{}
			for _, h := range ws {
				for _, r := range HourVetoes(h, alerts) {
					if !seen[r] {
						seen[r] = true
						w.VetoReasons = append(w.VetoReasons, r)
					}
				}
			}
			if len(w.VetoReasons) > 0 {
				w.Vetoed = true
				w.Start = ws[0].Time
				w.End = end
				w.Explanation = "not recommended: " + strings.Join(w.VetoReasons, "; ")
				vetoed = append(vetoed, w)
				continue
			}

			// Extension across adjacent same-top-bucket hours.
			top := 0
			for _, h := range ws {
				if b := primaryBucket(season, h); b > top {
					top = b
				}
			}
			extendable := func(hh int) (domain.Hour, bool) {
				if hh < 0 || hh > 23 || inBase[hh] {
					return domain.Hour{}, false
				}
				h, ok := lookup(d.day, hh)
				if !ok || !h.Time.Add(time.Hour).After(now) {
					return domain.Hour{}, false
				}
				if len(HourVetoes(h, alerts)) > 0 || primaryBucket(season, h) > top {
					return domain.Hour{}, false
				}
				return h, true
			}
			for hh := sp.startH - 1; ; hh-- {
				h, ok := extendable(hh)
				if !ok {
					break
				}
				w.Hours = append([]domain.Hour{h}, w.Hours...)
			}
			lastH := sp.endH - 1
			for hh := sp.endH; ; hh++ {
				h, ok := extendable(hh)
				if !ok {
					break
				}
				w.Hours = append(w.Hours, h)
				lastH = hh
			}
			w.Start = w.Hours[0].Time
			// time.Date normalizes hour 24 to midnight next day.
			w.End = time.Date(d.day.Year(), d.day.Month(), d.day.Day(), lastH+1, 0, 0, 0, loc)
			ranked = append(ranked, w)
		}
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		c, _ := CompareHours(season, worstHour(season, ranked[i].Hours), worstHour(season, ranked[j].Hours))
		if c != 0 {
			return c < 0
		}
		return ranked[i].Start.Before(ranked[j].Start)
	})
	for i := range ranked {
		ranked[i].Rank = i + 1
		ranked[i].Explanation = explain(season, ranked, i)
	}

	sort.SliceStable(vetoed, func(i, j int) bool { return vetoed[i].Start.Before(vetoed[j].Start) })
	return append(ranked, vetoed...), nil
}

// worstHour returns the worst hour of the window under the seasonal
// comparator (ties keep the earlier hour).
func worstHour(season Season, hs []domain.Hour) domain.Hour {
	w := hs[0]
	for _, h := range hs[1:] {
		if c, _ := CompareHours(season, h, w); c > 0 {
			w = h
		}
	}
	return w
}

// explain builds the UI explanation for ranked[i], naming the first
// lexicographic key that separated it from its neighbor.
func explain(season Season, ranked []Window, i int) string {
	name := func(w Window) string { return w.DayLabel + " " + w.Label }
	if i == 0 {
		if len(ranked) == 1 {
			return "the only candidate window without a safety veto"
		}
		c, d := CompareHours(season, worstHour(season, ranked[0].Hours), worstHour(season, ranked[1].Hours))
		if c == 0 {
			return fmt.Sprintf("tied with %s on every ranked category; earlier window listed first", name(ranked[1]))
		}
		return fmt.Sprintf("chosen because %s is a category better than %s (%s vs %s)",
			d.Key, name(ranked[1]), d.A, d.B)
	}
	c, d := CompareHours(season, worstHour(season, ranked[i].Hours), worstHour(season, ranked[i-1].Hours))
	if c == 0 {
		return fmt.Sprintf("tied with %s on every ranked category; later window listed after", name(ranked[i-1]))
	}
	return fmt.Sprintf("ranked below %s: %s is a category worse (%s vs %s)",
		name(ranked[i-1]), d.Key, d.A, d.B)
}
