// Package narrative is the rule-based narrative engine for whentorun.com.
// It turns merged hourly conditions, alerts, and window rankings into the
// above-the-fold editorial line and the italic divider rows inside the
// hourly ledger — with NO runtime LLM. Roughly twenty pure predicates
// (predicates.go) detect situations; a composer picks 1–3 clauses by
// priority (safety > windows > texture) and slot-fills templates from a
// Bank. Variant selection is deterministic: in.Now's local YearDay modulo
// the variant count, so the copy rotates day to day but never randomly.
//
// bank.go ships the authored bank: 4–5 phrasing variants per situation, in
// the register of the approved v3 mockup — plain, active, editorial; a
// knowledgeable running friend, never system-speak, never alarmist.
package narrative

import (
	"strings"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
	"github.com/kuitang/whentorun/internal/rank"
)

// Input is everything the engine reads. Loc nil falls back to
// America/New_York (the site's fixed display zone).
type Input struct {
	Hours  []domain.Hour
	Alerts []domain.Alert
	Sun    []merge.SunTimes
	Now    time.Time
	Loc    *time.Location
}

// Output is the composed narrative.
type Output struct {
	// AboveFold is the editorial line under the masthead (1–3 sentences).
	AboveFold string
	// TableBreaks are italic divider rows for the hourly ledger, sorted by
	// After ascending. Each renders after the hour row whose start equals
	// After.
	TableBreaks []Break
}

// Break is one divider row: it appears after the hour row starting at After.
type Break struct {
	After time.Time
	Text  string
}

// SituationKey names one situation the predicates can detect; it indexes
// the Bank.
type SituationKey string

// Template is display copy with {placeholder} slots.
type Template string

// Bank maps each situation to its rotation of template variants.
type Bank map[SituationKey][]Template

// Situation keys — above-the-fold clauses.
//
// Safety keys are listed in the exact order Compose tests them; that order
// IS the severity policy. At most one safety clause is ever emitted.
const (
	// Safety tier — hazards, in descending severity.
	SitIcing              SituationKey = "icing"
	SitDangerousChill     SituationKey = "dangerous-chill"
	SitBitterChill        SituationKey = "bitter-chill"
	SitHeatAdvisoryStorms SituationKey = "heat-advisory-storms"
	SitStormsActive       SituationKey = "storms-active"
	SitStormsApproaching  SituationKey = "storms-approaching"
	SitSnow               SituationKey = "snow"
	SitHeatWarning        SituationKey = "heat-warning"
	SitAirSmoke           SituationKey = "air-smoke"
	SitAirUnhealthy       SituationKey = "air-unhealthy"
	SitHeatAdvisory       SituationKey = "heat-advisory"
	SitAirSensitive       SituationKey = "air-sensitive"
	SitStormsCleared      SituationKey = "storms-cleared"
	SitVeryCold           SituationKey = "very-cold"
	SitCold               SituationKey = "cold"
	SitOppressiveDew      SituationKey = "oppressive-dew"
	SitAirModerate        SituationKey = "air-moderate"

	// Windows tier. The -best variants are reserved for windows that clear
	// cleanWindow: dry, Good air, no heat signal.
	SitBeforeWorkOut     SituationKey = "before-work-out"
	SitAfterWorkOut      SituationKey = "after-work-out"
	SitRunBeforeWorkBest SituationKey = "run-before-work-best"
	SitRunBeforeWorkGood SituationKey = "run-before-work-good"
	SitRunMidday         SituationKey = "run-midday"
	SitRunAfterWorkBest  SituationKey = "run-after-work-best"
	SitRunAfterWorkGood  SituationKey = "run-after-work-good"
	SitNextViableDay     SituationKey = "next-viable-day"
	SitNoWindow          SituationKey = "no-window"

	// Texture tier.
	SitRainLikely     SituationKey = "rain-likely"
	SitFrontClearing  SituationKey = "front-clearing"
	SitHeatRebuilding SituationKey = "heat-rebuilding"
	SitBreezy         SituationKey = "breezy"
	SitCleanMorning   SituationKey = "clean-morning"
)

// Situation keys — table divider rows.
const (
	BreakStormsArrive SituationKey = "break-storms-arrive"
	BreakStormsPass   SituationKey = "break-storms-pass"
	BreakRainArrives  SituationKey = "break-rain-arrives"
	BreakHeatRebuilds SituationKey = "break-heat-rebuilds"
	BreakEveningEases SituationKey = "break-evening-eases"
	BreakClearing     SituationKey = "break-clearing"
)

// maxBreaksPerDay keeps the ledger a ledger: at most this many divider rows
// land on any one local day, earliest first.
const maxBreaksPerDay = 2

// clause is one selected situation plus its filled slots.
type clause struct {
	key   SituationKey
	slots map[string]string
}

// Compose runs the predicates over in and assembles the narrative from
// bank. It is deterministic: same input, same date, same output.
func Compose(in Input, bank Bank) Output {
	loc := in.Loc
	if loc == nil {
		loc = nyLoc
	}
	now := in.Now.In(loc)
	yd := now.YearDay()
	today := localMidnight(now)
	tomorrow := today.AddDate(0, 0, 1)

	windows, err := rank.BestWindows(in.Hours, in.Alerts, in.Now)
	if err != nil {
		windows = nil
	}

	// ---- Safety clause (at most one; the slice order is the policy). ----
	advisory, advisoryOn := heatAdvisoryActive(in.Alerts, in.Now)
	phase, stormIn, stormOut := stormOutlook(in.Hours, today, loc, in.Now)
	chill, chillKnown := coldestChill(in.Hours)
	aqiVal, aqiCat, aqiPoor := poorAir(in.Hours)
	aqiHour, aqiKnown := firstAQI(in.Hours)
	dew, dewKnown := peakDew(in.Hours)

	alertSlots := map[string]string{"alert": advisory.Event, "alert_end": fmtAlertEnd(advisory, loc)}
	airSlots := map[string]string{"aqi": fmtInt(aqiVal), "aqi_category": aqiCat.Label}
	chillSlots := map[string]string{"chill": fmtDegF(chill)}

	// skip marks a hazard that writes today off entirely: the window clause
	// then looks past today, so the copy can never contradict itself.
	hazards := []struct {
		on    bool
		key   SituationKey
		slots map[string]string
		skip  bool
	}{
		{icingAhead(in.Hours), SitIcing, nil, true},
		{chillKnown && chill < rank.VetoWindChillF, SitDangerousChill, chillSlots, true},
		{chillKnown && chill <= bitterChillStartF, SitBitterChill, chillSlots, false},
		{advisoryOn && phase.pending(), SitHeatAdvisoryStorms, alertSlots, true},
		{phase == stormActive, SitStormsActive, map[string]string{"time": fmtClock(stormOut.In(loc))}, true},
		{phase == stormApproaching, SitStormsApproaching, map[string]string{
			"time":  fmtClock(stormIn.In(loc)),
			"clear": fmtClock(stormOut.In(loc)),
		}, true},
		{snowLikely(in.Hours), SitSnow, nil, false},
		{advisoryOn && heatWarningEvents[advisory.Event], SitHeatWarning, alertSlots, false},
		{aqiKnown && smokeAir(aqiHour), SitAirSmoke, airSlots, false},
		{aqiPoor && aqiCat.Tier >= unhealthyAirTier, SitAirUnhealthy, airSlots, false},
		{advisoryOn, SitHeatAdvisory, alertSlots, false},
		{aqiPoor, SitAirSensitive, airSlots, false},
		{phase == stormCleared, SitStormsCleared, map[string]string{"time": fmtClock(stormOut.In(loc))}, false},
		{chillKnown && chill <= veryColdMaxF, SitVeryCold, chillSlots, false},
		{chillKnown && chill <= coldMaxF, SitCold, chillSlots, false},
		{dewKnown && dew >= oppressiveDewF, SitOppressiveDew, map[string]string{"dew": fmtDegF(dew)}, false},
		{aqiKnown && aqiCat.Tier == moderateAirTier, SitAirModerate, map[string]string{"aqi": fmtInt(aqiVal)}, false},
	}
	var safety *clause
	todaySkipped := false
	for _, h := range hazards {
		if h.on {
			safety = &clause{key: h.key, slots: h.slots}
			todaySkipped = h.skip
			break
		}
	}

	// ---- Window clause (plus, when its sibling slot is out, a short
	// clause naming that). ----
	var window, sibling *clause
	var chosen rank.Window
	var haveChosen, windowBest bool
	var pop float64
	var popOK bool
	if w, ok := bestWindow(windows, todaySkipped); ok {
		chosen, haveChosen = w, true
		pop, popOK = rainyWindow(w)
		slots := map[string]string{"day": w.DayLabel, "range": fmtWindowRange(w, loc)}
		if !todaySkipped && w.DayLabel != "today" && todayAllVetoed(windows) {
			// Today ranked out on its own (no hazard clause said so), so the
			// window clause carries the "not today" news itself.
			slots["span"] = spanWord(w.Label)
			window = &clause{key: SitNextViableDay, slots: slots}
		} else {
			// "Best" is a claim about the whole hour, so a wet window never
			// earns it however clean the air is.
			windowBest = cleanWindow(w) && !popOK
			window = &clause{key: windowKey(w.Label, windowBest), slots: slots}
			if key, ok := siblingOut(windows, w); ok {
				sibling = &clause{key: key, slots: map[string]string{"day": w.DayLabel}}
			}
		}
	} else if len(in.Hours) > 0 {
		window = &clause{key: SitNoWindow}
	}

	// ---- Texture clause (at most one; fixed priority). ----
	var texture *clause
	wind, gust, dir, windOK := breezyWindow(chosen)
	switch {
	case haveChosen && popOK:
		texture = &clause{key: SitRainLikely, slots: map[string]string{"pop": fmtInt(pop)}}
	case boolOf(frontClearing(in.Hours, today, loc)):
		texture = &clause{key: SitFrontClearing}
	case boolOf(heatRebuilding(in.Hours, tomorrow, loc)) || boolOf(heatRebuilding(in.Hours, today, loc)):
		texture = &clause{key: SitHeatRebuilding}
	case haveChosen && windOK:
		texture = &clause{key: SitBreezy, slots: map[string]string{
			"wind": fmtInt(wind), "gust": fmtInt(gust), "dir": dir,
		}}
	case haveChosen && !windowBest && cleanWindow(chosen):
		// The -best window variants already say this; only the plainer
		// window keys need the beat spelled out.
		texture = &clause{key: SitCleanMorning}
	}

	// ---- Assemble the above-fold line. ----
	// Safety leads. When today is written off, the texture beat ("the front
	// clears overnight") bridges into the window recommendation, matching
	// the approved v3 mockup: advisory+storms → skip today → before work
	// tomorrow. Otherwise the window leads and texture trails.
	var order []*clause
	if todaySkipped {
		order = []*clause{safety, texture, sibling, window}
	} else {
		order = []*clause{safety, sibling, window, texture}
	}
	var parts []string
	for _, c := range order {
		if c == nil {
			continue
		}
		if s := render(bank, c.key, yd, c.slots); s != "" {
			parts = append(parts, s)
		}
	}

	out := Output{AboveFold: strings.Join(parts, " ")}
	out.TableBreaks = composeBreaks(in, bank, loc, yd, today, tomorrow, windows)
	return out
}

// composeBreaks builds the divider rows, anchored to predicate hour
// boundaries and clipped to the hours actually in the table.
func composeBreaks(in Input, bank Bank, loc *time.Location, yd int, today, tomorrow time.Time, windows []rank.Window) []Break {
	if len(in.Hours) == 0 {
		return nil
	}
	first, last := in.Hours[0].Time, in.Hours[len(in.Hours)-1].Time
	var breaks []Break
	add := func(after time.Time, key SituationKey, slots map[string]string) {
		if after.Before(first) || after.After(last) {
			return
		}
		for _, b := range breaks {
			if b.After.Equal(after) {
				return // one divider per row, first writer wins
			}
		}
		if s := render(bank, key, yd, slots); s != "" {
			breaks = append(breaks, Break{After: after, Text: s})
		}
	}

	days := []struct {
		day   time.Time
		label string
	}{{today, "today"}, {tomorrow, "tomorrow"}}

	for _, d := range days {
		// Storms: a divider ahead of the first thunder-vetoed evening hour
		// and another after the last one.
		if hs := eveningStormHours(in.Hours, d.day, loc); len(hs) > 0 {
			firstStorm, lastStorm := hs[0].Time, hs[len(hs)-1].Time
			add(firstStorm.Add(-time.Hour), BreakStormsArrive, map[string]string{
				"time": fmtClock(firstStorm.In(loc)),
			})
			add(lastStorm, BreakStormsPass, map[string]string{
				"time": fmtClock(lastStorm.Add(time.Hour).In(loc)),
			})
		}
		// Rain: a divider ahead of the first hour of a sustained wet stretch.
		if t, ok := rainStart(in.Hours, d.day, loc); ok {
			add(t.Add(-time.Hour), BreakRainArrives, map[string]string{"time": fmtClock(t.In(loc))})
		}
		// Heat rebuilds: after the before-work window's last hour when one
		// exists, else after the 8 AM row.
		if boolOf(heatRebuilding(in.Hours, d.day, loc)) {
			after := d.day.Add(8 * time.Hour)
			if w, ok := windowFor(windows, d.label, "morning"); ok {
				after = w.End.Add(-time.Hour)
			}
			add(after, BreakHeatRebuilds, nil)
		}
		// Evening ease: after a hot day, the row where WBGT drops back out
		// of the heat bands.
		if t, ok := eveningEase(in.Hours, d.day, loc); ok {
			add(t.Add(-time.Hour), BreakEveningEases, map[string]string{"time": fmtClock(t.In(loc))})
		}
	}

	// Clearing: tonight's front → divider ahead of tomorrow's after-work
	// window (the first drier stretch runners will feel).
	if boolOf(frontClearing(in.Hours, today, loc)) {
		if w, ok := windowFor(windows, "tomorrow", "evening"); ok {
			start := w.Start
			if base := tomorrow.Add(time.Duration(baseStartHour["evening"]) * time.Hour); start.Before(base) {
				start = base
			}
			add(start.Add(-time.Hour), BreakClearing, nil)
		}
	}

	sortBreaks(breaks)
	return capPerDay(breaks, loc)
}

// capPerDay keeps at most maxBreaksPerDay dividers on each local day,
// earliest first, so a busy forecast never turns the ledger into prose.
func capPerDay(breaks []Break, loc *time.Location) []Break {
	count := map[int]int{}
	var out []Break
	for _, b := range breaks {
		lt := b.After.In(loc)
		key := lt.Year()*1000 + lt.YearDay()
		if count[key] >= maxBreaksPerDay {
			continue
		}
		count[key]++
		out = append(out, b)
	}
	return out
}

// sortBreaks orders by After ascending (stable insertion; the slice is tiny).
func sortBreaks(b []Break) {
	for i := 1; i < len(b); i++ {
		for j := i; j > 0 && b[j].After.Before(b[j-1].After); j-- {
			b[j], b[j-1] = b[j-1], b[j]
		}
	}
}

// render picks the day's variant for key and fills its slots. Missing keys
// or empty variant lists render as "" (the clause is skipped) so a partial
// bank degrades to shorter copy, never to a panic.
func render(bank Bank, key SituationKey, yearDay int, slots map[string]string) string {
	variants := bank[key]
	if len(variants) == 0 {
		return ""
	}
	return fill(variants[yearDay%len(variants)], slots)
}

// fill replaces every {name} in t with slots[name]. Unknown placeholders
// are left intact so a template/slot mismatch is visible, not silent.
func fill(t Template, slots map[string]string) string {
	s := string(t)
	for k, v := range slots {
		s = strings.ReplaceAll(s, "{"+k+"}", v)
	}
	return s
}

func boolOf(_ float64, ok bool) bool { return ok }

// nyLoc mirrors merge's fallback: America/New_York, else UTC.
var nyLoc = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UTC
	}
	return loc
}()
