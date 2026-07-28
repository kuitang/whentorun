package narrative

// predicates.go: the pure situation detectors and the small formatting
// helpers that fill their slots. Every predicate is a total function of its
// arguments — no clocks, no I/O — so each is unit-tested in isolation.

import (
	"fmt"
	"time"

	"github.com/kuitang/whentorun/internal/categories"
	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/rank"
)

// Tunables, named so the tests read as spec.
const (
	// eveningStartH..eveningEndH is the local clock window (end-exclusive)
	// scanned for evening storms.
	eveningStartH = 16
	eveningEndH   = 22
	// frontDewDropF: an overnight dew-point drop of at least this many °F
	// (evening max → next-morning min) reads as a front clearing through.
	frontDewDropF = 5.0
	// rebuildSlopeF/rebuildPeakF: morning WBGT climbing at least
	// rebuildSlopeF °F from its early-morning floor to a midday peak of at
	// least rebuildPeakF °F reads as heat rebuilding.
	rebuildSlopeF = 8.0
	rebuildPeakF  = 85.0
	// poorAirTier: minimum EPA AQI tier (categories.AQI) that triggers a
	// health-note clause: 2 = Unhealthy for Sensitive Groups.
	poorAirTier = 2
	// unhealthyAirTier: 3 = Unhealthy (everyone), not just sensitive groups.
	unhealthyAirTier = 3
	// moderateAirTier: 1 = Moderate, the sensitive-groups footnote band.
	moderateAirTier = 1
	// smokeAQIFloor: at or above this AQI with PM2.5 dominant, the copy names
	// wildfire smoke rather than generic pollution — the only NYC source that
	// pushes fine particulate this high.
	smokeAQIFloor = 150.0
	// oppressiveDewF: dew point at or above this is the top of the
	// "Oppressive" comfort band; sweat stops evaporating usefully.
	oppressiveDewF = 70.0
	// veryColdMaxF/coldMaxF: wind-chill band ceilings below bitterChillStartF
	// (categories.WindChill edges, shifted to leave the bitter band its own
	// clause).
	veryColdMaxF = 15.0
	coldMaxF     = 25.0
	// snowMaxTempF/snowPoPPct/snowMinHours: falling snow proxy — the NWS
	// weather layer we merge names freezing rain but not snow, so cold air
	// plus a wet forecast for a sustained stretch stands in.
	snowMaxTempF = 33.0
	snowPoPPct   = 50.0
	snowMinHours = 2
	// rainPoPPct: probability of precipitation that makes a window a wet run.
	rainPoPPct = 50.0
	// rainRunHours: consecutive wet hours needed before the ledger calls it
	// rain arriving rather than a passing shower.
	rainRunHours = 2
	// breezyWindMPH/breezyGustMPH: sustained wind or gusts a runner will
	// actually work against.
	breezyWindMPH = 18.0
	breezyGustMPH = 25.0
	// stormFreshHours: how long after a line clears the all-clear is still
	// news worth leading with.
	stormFreshHours = 3
	// easeStartH/easeCeilingF: after a hot day, the first evening hour whose
	// WBGT falls back under the Low/Elevated edge.
	easeStartH   = 17
	easeCeilingF = 80.0
	// bitterChillStartF: wind chill at or below this triggers the cold-side
	// safety clause (frostbite territory); below rank.VetoWindChillF it
	// also writes the day off.
	bitterChillStartF = 0.0
	// nearTermHours limits "current conditions" scans (icing, chill, AQI)
	// to roughly the next day rather than the whole 48 h horizon.
	nearTermHours = 24
)

// heatAdvisoryEvents are the NWS heat products that banner the page.
var heatAdvisoryEvents = map[string]bool{
	"Heat Advisory":          true,
	"Excessive Heat Warning": true,
	"Extreme Heat Warning":   true,
}

// heatWarningEvents are the warning-class subset: harder copy than an
// advisory, and never softened by a variant.
var heatWarningEvents = map[string]bool{
	"Excessive Heat Warning": true,
	"Extreme Heat Warning":   true,
}

// heatAdvisoryActive returns the first heat advisory/warning active at now.
func heatAdvisoryActive(alerts []domain.Alert, now time.Time) (domain.Alert, bool) {
	for _, a := range alerts {
		if heatAdvisoryEvents[a.Event] && a.ActiveAt(now) {
			return a, true
		}
	}
	return domain.Alert{}, false
}

// thunderVetoed reports whether the hour is struck out for lightning: the
// NWS weather layer names thunder, or probabilityOfThunder reaches the veto
// threshold shared with internal/rank.
func thunderVetoed(h domain.Hour) bool {
	return h.WxThunder || (h.ThunderProb.Valid && h.ThunderProb.Value >= rank.VetoThunderProbPct)
}

// eveningStormHours returns day's thunder-vetoed hours with local clock in
// [eveningStartH, eveningEndH), in order.
func eveningStormHours(hours []domain.Hour, day time.Time, loc *time.Location) []domain.Hour {
	var out []domain.Hour
	for _, h := range hours {
		lt := h.Time.In(loc)
		if sameLocalDay(lt, day) && lt.Hour() >= eveningStartH && lt.Hour() < eveningEndH && thunderVetoed(h) {
			out = append(out, h)
		}
	}
	return out
}

// stormsClearBy returns the end of day's last evening storm hour — the
// first clear moment after the line moves through.
func stormsClearBy(hours []domain.Hour, day time.Time, loc *time.Location) (time.Time, bool) {
	hs := eveningStormHours(hours, day, loc)
	if len(hs) == 0 {
		return time.Time{}, false
	}
	return hs[len(hs)-1].Time.Add(time.Hour), true
}

// phase names where now sits relative to a day's storm line.
type phase int

const (
	stormNone phase = iota
	stormApproaching
	stormActive
	stormCleared
)

// pending reports a storm line that has not finished yet — the two phases
// that write the day off.
func (p phase) pending() bool { return p == stormApproaching || p == stormActive }

// stormOutlook places now against day's evening storm line and returns the
// phase plus the line's arrival and all-clear times. stormCleared only holds
// for stormFreshHours after the line moves through; after that the storms are
// history and the copy stops mentioning them.
func stormOutlook(hours []domain.Hour, day time.Time, loc *time.Location, now time.Time) (phase, time.Time, time.Time) {
	hs := eveningStormHours(hours, day, loc)
	if len(hs) == 0 {
		return stormNone, time.Time{}, time.Time{}
	}
	in, out := hs[0].Time, hs[len(hs)-1].Time.Add(time.Hour)
	switch {
	case now.Before(in):
		return stormApproaching, in, out
	case now.Before(out):
		return stormActive, in, out
	case now.Sub(out) <= stormFreshHours*time.Hour:
		return stormCleared, in, out
	}
	return stormNone, in, out
}

// snowLikely reports cold air plus a wet forecast for snowMinHours or more
// near-term hours: falling snow, as close as the merged feeds get to naming
// it. Freezing-rain hours belong to icingAhead and are not counted here.
func snowLikely(hours []domain.Hour) bool {
	n := 0
	for i, h := range hours {
		if i >= nearTermHours {
			break
		}
		if h.WxFreezingRain || !h.TempF.Valid || !h.PoP.Valid {
			continue
		}
		if h.TempF.Value <= snowMaxTempF && h.PoP.Value >= snowPoPPct {
			n++
		}
	}
	return n >= snowMinHours
}

// rainStart returns the first hour of day's first sustained wet stretch:
// rainRunHours consecutive hours at or above rainPoPPct that are not already
// struck out for thunder (storms get their own divider).
func rainStart(hours []domain.Hour, day time.Time, loc *time.Location) (time.Time, bool) {
	run := 0
	for _, h := range hours {
		lt := h.Time.In(loc)
		wet := sameLocalDay(lt, day) && !thunderVetoed(h) && h.PoP.Valid && h.PoP.Value >= rainPoPPct
		if !wet {
			run = 0
			continue
		}
		run++
		if run == rainRunHours {
			return h.Time.Add(-time.Duration(rainRunHours-1) * time.Hour), true
		}
	}
	return time.Time{}, false
}

// eveningEase returns the first evening hour whose WBGT falls back below
// easeCeilingF after a day that peaked in the heat bands. It stays quiet on
// storm days, where the ledger has more urgent things to say.
func eveningEase(hours []domain.Hour, day time.Time, loc *time.Location) (time.Time, bool) {
	peak, ok := extremeWBGT(hours, day, loc, 10, 16, true)
	if !ok || peak < rebuildPeakF || len(eveningStormHours(hours, day, loc)) > 0 {
		return time.Time{}, false
	}
	for _, h := range hours {
		lt := h.Time.In(loc)
		if sameLocalDay(lt, day) && lt.Hour() >= easeStartH && h.WBGTF.Valid && h.WBGTF.Value < easeCeilingF {
			return h.Time, true
		}
	}
	return time.Time{}, false
}

// peakDew returns the highest valid dew point over the near term.
func peakDew(hours []domain.Hour) (float64, bool) {
	var max float64
	found := false
	for i, h := range hours {
		if i >= nearTermHours {
			break
		}
		if h.DewPointF.Valid && (!found || h.DewPointF.Value > max) {
			max, found = h.DewPointF.Value, true
		}
	}
	return max, found
}

// frontClearing reports an overnight dew-point drop of at least
// frontDewDropF °F from day's evening (local 18:00–23:59 max) to the next
// morning (local 04:00–08:59 min). Returns the drop.
func frontClearing(hours []domain.Hour, day time.Time, loc *time.Location) (float64, bool) {
	next := day.AddDate(0, 0, 1)
	eveMax, eveOK := extremeDew(hours, day, loc, 18, 24, true)
	mornMin, mornOK := extremeDew(hours, next, loc, 4, 9, false)
	if !eveOK || !mornOK {
		return 0, false
	}
	drop := eveMax - mornMin
	return drop, drop >= frontDewDropF
}

// extremeDew scans day's hours with local clock in [startH, endH) and
// returns the max (wantMax) or min valid dew point.
func extremeDew(hours []domain.Hour, day time.Time, loc *time.Location, startH, endH int, wantMax bool) (float64, bool) {
	var best float64
	found := false
	for _, h := range hours {
		lt := h.Time.In(loc)
		if !sameLocalDay(lt, day) || lt.Hour() < startH || lt.Hour() >= endH || !h.DewPointF.Valid {
			continue
		}
		v := h.DewPointF.Value
		if !found || (wantMax && v > best) || (!wantMax && v < best) {
			best, found = v, true
		}
	}
	return best, found
}

// heatRebuilding reports whether day's WBGT climbs at least rebuildSlopeF
// °F from its early-morning floor (local 05:00–08:59 min) to a midday peak
// (local 10:00–15:59 max) of at least rebuildPeakF °F. Returns the peak.
func heatRebuilding(hours []domain.Hour, day time.Time, loc *time.Location) (float64, bool) {
	floor, floorOK := extremeWBGT(hours, day, loc, 5, 9, false)
	peak, peakOK := extremeWBGT(hours, day, loc, 10, 16, true)
	if !floorOK || !peakOK {
		return 0, false
	}
	return peak, peak >= rebuildPeakF && peak-floor >= rebuildSlopeF
}

// extremeWBGT mirrors extremeDew for the WBGT metric.
func extremeWBGT(hours []domain.Hour, day time.Time, loc *time.Location, startH, endH int, wantMax bool) (float64, bool) {
	var best float64
	found := false
	for _, h := range hours {
		lt := h.Time.In(loc)
		if !sameLocalDay(lt, day) || lt.Hour() < startH || lt.Hour() >= endH || !h.WBGTF.Valid {
			continue
		}
		v := h.WBGTF.Value
		if !found || (wantMax && v > best) || (!wantMax && v < best) {
			best, found = v, true
		}
	}
	return best, found
}

// firstAQI returns the first near-term hour carrying a known AQI — the
// value the page is already showing as "now".
func firstAQI(hours []domain.Hour) (domain.Hour, bool) {
	for i, h := range hours {
		if i >= nearTermHours {
			break
		}
		if h.AQI.Valid {
			return h, true
		}
	}
	return domain.Hour{}, false
}

// poorAir fires when the first known AQI in the near term reaches
// poorAirTier (Unhealthy for Sensitive Groups) or worse. Returns the value
// and its EPA category.
func poorAir(hours []domain.Hour) (float64, categories.Category, bool) {
	h, ok := firstAQI(hours)
	if !ok {
		return 0, categories.Category{}, false
	}
	cat := categories.AQI(h.AQI.Value)
	return h.AQI.Value, cat, cat.Tier >= poorAirTier
}

// smokeAir reports air bad enough, on fine particulate, that naming wildfire
// smoke is the honest description rather than a guess.
func smokeAir(h domain.Hour) bool {
	return h.AQI.Valid && h.AQI.Value >= smokeAQIFloor && h.AQIPollutant == "PM2.5"
}

// rainyWindow returns the window's peak probability of precipitation when it
// reaches rainPoPPct — the runner is getting wet.
func rainyWindow(w rank.Window) (float64, bool) {
	var max float64
	for _, h := range w.Hours {
		if h.PoP.Valid && h.PoP.Value > max {
			max = h.PoP.Value
		}
	}
	return max, max >= rainPoPPct
}

// breezyWindow returns the window's windiest hour (speed, gust, compass
// point) when wind is worth a sentence. All three values must be known: a
// partial wind sentence is worse than none.
func breezyWindow(w rank.Window) (float64, float64, string, bool) {
	var wind, gust float64
	var dir string
	ok := false
	for _, h := range w.Hours {
		if !h.WindMPH.Valid || !h.GustMPH.Valid || !h.WindDirDeg.Valid {
			continue
		}
		if h.WindMPH.Value < breezyWindMPH && h.GustMPH.Value < breezyGustMPH {
			continue
		}
		if ok && h.GustMPH.Value <= gust {
			continue
		}
		wind, gust, dir, ok = h.WindMPH.Value, h.GustMPH.Value, domain.CompassPoint(h.WindDirDeg.Value), true
	}
	return wind, gust, dir, ok
}

// icingAhead reports freezing rain/sleet or ice accumulation in the near
// term.
func icingAhead(hours []domain.Hour) bool {
	for i, h := range hours {
		if i >= nearTermHours {
			break
		}
		if h.WxFreezingRain || (h.IceAccumIn.Valid && h.IceAccumIn.Value > 0) {
			return true
		}
	}
	return false
}

// coldestChill returns the minimum valid wind chill over the near term.
func coldestChill(hours []domain.Hour) (float64, bool) {
	var min float64
	found := false
	for i, h := range hours {
		if i >= nearTermHours {
			break
		}
		if h.WindChillF.Valid && (!found || h.WindChillF.Value < min) {
			min, found = h.WindChillF.Value, true
		}
	}
	return min, found
}

// bestWindow returns the top-ranked non-vetoed window; with excludeToday it
// skips today's windows so "skip today" copy never contradicts itself.
func bestWindow(windows []rank.Window, excludeToday bool) (rank.Window, bool) {
	for _, w := range windows {
		if w.Vetoed {
			continue
		}
		if excludeToday && w.DayLabel == "today" {
			continue
		}
		return w, true
	}
	return rank.Window{}, false
}

// windowFor returns the non-vetoed window with the given day and span
// labels, if ranked.
func windowFor(windows []rank.Window, dayLabel, label string) (rank.Window, bool) {
	for _, w := range windows {
		if !w.Vetoed && w.DayLabel == dayLabel && w.Label == label {
			return w, true
		}
	}
	return rank.Window{}, false
}

// todayAllVetoed reports that today still had candidate windows and every
// one of them is struck out — the trigger for "the next one that works is …"
// copy. No today windows at all (late evening, say) is not the same thing.
func todayAllVetoed(windows []rank.Window) bool {
	seen := false
	for _, w := range windows {
		if w.DayLabel != "today" {
			continue
		}
		if !w.Vetoed {
			return false
		}
		seen = true
	}
	return seen
}

// siblingOut reports that the OTHER commute-side slot on the chosen day is
// struck out, so the copy can say so before naming the window that works.
// Morning is checked first: a runner who lost the dawn slot needs to know
// before they read the rest.
func siblingOut(windows []rank.Window, chosen rank.Window) (SituationKey, bool) {
	for _, sib := range []struct {
		label string
		key   SituationKey
	}{{"morning", SitBeforeWorkOut}, {"evening", SitAfterWorkOut}} {
		if sib.label == chosen.Label {
			continue
		}
		for _, w := range windows {
			if w.Vetoed && w.DayLabel == chosen.DayLabel && w.Label == sib.label {
				return sib.key, true
			}
		}
	}
	return "", false
}

// windowKey maps a ranked window's span to its bank key; best picks the
// stronger phrasing reserved for windows that clear cleanWindow.
func windowKey(label string, best bool) SituationKey {
	switch label {
	case "morning":
		if best {
			return SitRunBeforeWorkBest
		}
		return SitRunBeforeWorkGood
	case "evening":
		if best {
			return SitRunAfterWorkBest
		}
		return SitRunAfterWorkGood
	default:
		return SitRunMidday
	}
}

// spanWord names a window span the way a runner would say it.
func spanWord(label string) string {
	switch label {
	case "morning":
		return "before work"
	case "evening":
		return "after work"
	default:
		return "midday"
	}
}

// cleanWindow reports "as good as it gets" conditions across a window's
// hours: every dew point known and under 60 °F, every AQI known and Good,
// and no heat signal (WBGT absent or under 80 °F).
func cleanWindow(w rank.Window) bool {
	if len(w.Hours) == 0 {
		return false
	}
	for _, h := range w.Hours {
		if !h.DewPointF.Valid || categories.DewPoint(h.DewPointF.Value).Tier > 1 {
			return false
		}
		if !h.AQI.Valid || categories.AQI(h.AQI.Value).Tier > 0 {
			return false
		}
		if h.WBGTF.Valid && categories.WBGT(h.WBGTF.Value).Tier > 0 {
			return false
		}
	}
	return true
}

// ---- calendar + formatting helpers ----

// localMidnight truncates t to its local calendar day.
func localMidnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// sameLocalDay reports whether lt (already in local time) falls on the
// calendar day of day (a local midnight).
func sameLocalDay(lt, day time.Time) bool {
	return lt.Year() == day.Year() && lt.Month() == day.Month() && lt.Day() == day.Day()
}

// baseStartHour mirrors internal/rank's candidate spans: window range copy
// clamps to these so an extension that reaches back into the small hours
// still reads as the before/after-work slot it names.
var baseStartHour = map[string]int{"morning": 5, "midday": 11, "evening": 16}

// fmtClock renders a local time as "5 AM" / "8 PM" / "12 PM" ("8:30 PM"
// when minutes are nonzero).
func fmtClock(t time.Time) string {
	h12, mer := clock12(t.Hour())
	if m := t.Minute(); m != 0 {
		return fmt.Sprintf("%d:%02d %s", h12, m, mer)
	}
	return fmt.Sprintf("%d %s", h12, mer)
}

func clock12(h int) (int, string) {
	mer := "AM"
	if h >= 12 {
		mer = "PM"
	}
	h %= 12
	if h == 0 {
		h = 12
	}
	return h, mer
}

// fmtClockRange renders [start, endExclusive) as "5–8 AM" (shared
// meridiem) or "11 AM–1 PM". The displayed end is the last startable hour.
func fmtClockRange(start, endExclusive time.Time, loc *time.Location) string {
	s := start.In(loc)
	e := endExclusive.Add(-time.Hour).In(loc)
	sh, sm := clock12(s.Hour())
	eh, em := clock12(e.Hour())
	if sm == em {
		return fmt.Sprintf("%d–%d %s", sh, eh, sm)
	}
	return fmt.Sprintf("%d %s–%d %s", sh, sm, eh, em)
}

// fmtWindowRange renders a ranked window's span, clamping the start to its
// span's base hour (a morning window extended back to 2 AM still reads
// "5–8 AM").
func fmtWindowRange(w rank.Window, loc *time.Location) string {
	start := w.Start.In(loc)
	if base, ok := baseStartHour[w.Label]; ok && start.Hour() < base {
		start = time.Date(start.Year(), start.Month(), start.Day(), base, 0, 0, 0, loc)
	}
	return fmtClockRange(start, w.End, loc)
}

// fmtAlertEnd renders an alert's end for "{alert} holds until …" copy; an
// open-ended alert reads "further notice".
func fmtAlertEnd(a domain.Alert, loc *time.Location) string {
	if a.Ends.IsZero() {
		return "further notice"
	}
	return fmtClock(a.Ends.In(loc))
}

// fmtDegF renders a whole-degree Fahrenheit value, e.g. "-12°F".
func fmtDegF(v float64) string { return fmt.Sprintf("%.0f°F", v) }

// fmtInt renders a whole number, e.g. an AQI value.
func fmtInt(v float64) string { return fmt.Sprintf("%.0f", v) }
