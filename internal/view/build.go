package view

import (
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"

	"github.com/kuitang/whentorun/internal/chart"
	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
	"github.com/kuitang/whentorun/internal/rank"
)

// BuildInput carries everything Build needs for one page render.
type BuildInput struct {
	Path    domain.Path
	Units   string // "F" (default) | "C"
	Theme   string // "" | "light" | "dark"
	Res     merge.Result
	Windows []rank.Window // rank.BestWindows output
	Now     time.Time

	// Narrative, Breaks, ChartSVG, and ChartMeta come from the sibling
	// narrative/chart packages (internal/web wires narrative.Compose and
	// chart.Render); Build supplies a deterministic fallback lede when
	// Narrative is empty.
	Narrative string
	Breaks    []BreakVM
	ChartSVG  template.HTML
	ChartMeta chart.Meta
}

// Build assembles the full page viewmodel from merged data.
func Build(in BuildInput) Page {
	units := in.Units
	if units != "C" {
		units = "F"
	}

	p := Page{
		Path:          in.Path,
		Paths:         domain.Paths,
		Units:         units,
		Theme:         in.Theme,
		AlertFeedDown: in.Res.AlertFeedDown,
		Sun:           in.Res.Sun,
		ChartSVG:      in.ChartSVG,
		ChartMeta:     in.ChartMeta,
		GeneratedAt:   in.Now,
	}

	for _, pr := range in.Res.Prose {
		p.PeriodProse = append(p.PeriodProse, ProseVM{Name: pr.Name, Text: pr.Detailed, Source: pr.Source})
	}

	for _, a := range in.Res.Alerts {
		if a.ActiveAt(in.Now) || a.Onset.After(in.Now) {
			p.Alerts = append(p.Alerts, AlertVM{Event: a.Event, Note: alertNote(a)})
		}
	}

	bw := PickWindow(in.Windows, "morning", in.Now)
	aw := PickWindow(in.Windows, "evening", in.Now)
	p.Windows = WindowsVM{
		BeforeWork: windowVM("Before work", bw, units),
		AfterWork:  windowVM("After work", aw, units),
	}
	p.Windows.Lede = windowsLede(p.Windows)

	// internal/web wires narrative.Compose into in.Narrative; the
	// deterministic windows lede is the degraded-mode fallback only.
	p.Narrative = in.Narrative
	if p.Narrative == "" {
		p.Narrative = p.Windows.Lede
	}

	if len(in.Res.Hours) > 0 {
		p.Now = nowVM(in.Res.Hours[0], in.Now, units)
		p.Clothing = clothingLine(in.Res.Hours[0])
	}

	p.Days = buildDays(in, bw, aw, units)
	p.Freshness = freshness(in.Res)
	return p
}

// sourceOrder fixes the Freshness slice order; templates index it:
// 0 grid, 1 text forecast, 2 alerts, 3 AirNow, 4 OM weather, 5 OM air.
var sourceOrder = []struct{ key, name string }{
	{merge.SrcNWSGrid, "NWS gridpoint"},
	{merge.SrcNWSForecast, "NWS text forecast"},
	{merge.SrcNWSAlerts, "NWS alerts"},
	{merge.SrcAirNow, "EPA AirNow"},
	{merge.SrcOMWeather, "Open-Meteo weather"},
	{merge.SrcOMAir, "Open-Meteo air quality"},
}

func freshness(res merge.Result) []SourceVM {
	out := make([]SourceVM, 0, len(sourceOrder))
	for _, s := range sourceOrder {
		st := res.Freshness[s.key]
		out = append(out, SourceVM{
			Name:      s.name,
			FetchedAt: st.FetchedAt,
			Fresh:     st.Available && !st.Stale,
		})
	}
	return out
}

func alertNote(a domain.Alert) string {
	if !a.Ends.IsZero() {
		return "until " + FmtClockAmPm(a.Ends) + " " + a.Ends.In(NYLoc).Format("Mon") + "."
	}
	return "in effect."
}

func nowVM(h domain.Hour, now time.Time, units string) NowVM {
	vm := NowVM{
		TimeLabel: now.In(NYLoc).Format("3:04 PM") + " ET",
		WBGT:      WBGTMetric(h.WBGTF, units),
		Temp:      TempMetric(h.TempF, units),
		Dew:       TempMetric(h.DewPointF, units),
		UV:        CountMetric(h.UVIndex, QualityUV),
		AQI:       CountMetric(h.AQI, QualityAQI),
		RH:        PctMetric(h.RH, QualityRH),
		Wind:      WindMetric(h.WindMPH, h.GustMPH, h.WindDirDeg),
		Rain:      PctMetric(h.PoP, QualityRain),

		AQIPollutant: h.AQIPollutant,
		WindDeg:      -1,
	}
	if h.WindDirDeg.Valid {
		vm.WindCompass = domain.CompassPoint(h.WindDirDeg.Value)
		vm.WindDeg = h.WindDirDeg.Value
	}
	if h.UVIndex.Valid {
		vm.UVWord = UVWord(h.UVIndex.Value)
	}
	if h.AQI.Valid {
		vm.AQIWord = AQIWord(h.AQI.Value)
	}
	if h.WBGTF.Valid {
		vm.WBGTPhrase = WBGTPhrase(h.WBGTF.Value)
	}
	if h.DewPointF.Valid {
		vm.DewNote = DewWord(h.DewPointF.Value) + " at a jog"
	}
	return vm
}

// PickWindow returns the chronologically first still-relevant window with
// the given base label ("morning" | "midday" | "evening"), preferring
// non-vetoed windows. internal/web uses it to place the chart bands with
// the same choice the page frames.
func PickWindow(ws []rank.Window, label string, now time.Time) *rank.Window {
	var best, vetoed *rank.Window
	for i := range ws {
		w := &ws[i]
		if w.Label != label || !w.End.After(now) {
			continue
		}
		if !w.Vetoed {
			if best == nil || w.Start.Before(best.Start) {
				best = w
			}
		} else if vetoed == nil || w.Start.Before(vetoed.Start) {
			vetoed = w
		}
	}
	if best != nil {
		return best
	}
	return vetoed
}

func windowVM(label string, w *rank.Window, units string) WindowVM {
	if w == nil {
		return WindowVM{Label: label}
	}
	vm := WindowVM{
		Label:  label,
		Range:  FmtSpan(w.Start, w.End),
		Vetoed: w.Vetoed,
		Reason: strings.Join(w.VetoReasons, "; "),
	}
	if !w.Vetoed {
		vm.Detail = windowDetail(w, units)
	}
	return vm
}

// windowDetail summarizes a window's hours: WBGT range with the worst band
// word, peak UV, peak AQI with its EPA word.
func windowDetail(w *rank.Window, units string) string {
	var parts []string
	loW, hiW := math.Inf(1), math.Inf(-1)
	for _, h := range w.Hours {
		if h.WBGTF.Valid {
			loW = math.Min(loW, h.WBGTF.Value)
			hiW = math.Max(hiW, h.WBGTF.Value)
		}
	}
	if hiW >= loW {
		band := strings.ToLower(strings.SplitN(WBGTPhrase(hiW), " heat", 2)[0])
		lo, hi := FmtTemp(loW, units), FmtTemp(hiW, units)
		if lo == hi {
			parts = append(parts, fmt.Sprintf("%s° %s", hi, band))
		} else {
			parts = append(parts, fmt.Sprintf("%s–%s° %s", lo, hi, band))
		}
	}
	if uv, ok := peak(w.Hours, func(h domain.Hour) domain.Metric { return h.UVIndex }); ok {
		parts = append(parts, fmt.Sprintf("UV %d", int(math.Round(uv))))
	}
	if aqi, ok := peak(w.Hours, func(h domain.Hour) domain.Metric { return h.AQI }); ok {
		word := "good"
		switch QualityAQI(aqi) {
		case 1:
			word = "moderate"
		case 2:
			word = "unhealthy"
		}
		parts = append(parts, fmt.Sprintf("AQI %d %s", int(math.Round(aqi)), word))
	}
	return strings.Join(parts, " · ")
}

func peak(hours []domain.Hour, get func(domain.Hour) domain.Metric) (float64, bool) {
	v, ok := math.Inf(-1), false
	for _, h := range hours {
		if m := get(h); m.Valid && m.Value > v {
			v, ok = m.Value, true
		}
	}
	return v, ok
}

// windowsLede is the deterministic fallback verdict sentence, used only
// when the narrative engine (narrative.Compose, wired in internal/web)
// returns no above-fold line.
func windowsLede(w WindowsVM) string {
	var parts []string
	for _, wv := range []WindowVM{w.BeforeWork, w.AfterWork} {
		if !wv.Found() {
			continue
		}
		if wv.Vetoed {
			parts = append(parts, fmt.Sprintf("%s %s is out — %s", strings.ToLower(wv.Label), wv.Range, wv.Reason))
		} else {
			parts = append(parts, fmt.Sprintf("%s %s", strings.ToLower(wv.Label), wv.Range))
		}
	}
	if len(parts) == 0 {
		return "No clear running window in the next 48 hours — every candidate hour is ruled out or missing data."
	}
	s := "Best windows: " + strings.Join(parts, "; ") + "."
	return strings.ToUpper(s[:1]) + s[1:]
}

// clothingLine is a rule-based one-liner from the current hour; its wording
// stays here (the narrative engine covers the verdict and ledger breaks).
func clothingLine(h domain.Hour) string {
	if !h.WBGTF.Valid {
		if h.TempF.Valid && h.TempF.Value < 40 {
			return "Layers: tights, long sleeves, gloves once it is near freezing."
		}
		return "Dress for the temperature and check back once WBGT returns."
	}
	switch TierWBGT(h.WBGTF.Value) {
	case 0:
		if h.TempF.Valid && h.TempF.Value < 45 {
			return "Cool kit: long sleeves to start, shorts unless the wind bites."
		}
		return "Ordinary kit: shorts and a light shirt cover it."
	case 1:
		return "Singlet and shorts; carry water past 40 minutes."
	case 2:
		return "The lightest kit you own and water every 20 minutes."
	default:
		return "The lightest kit you own, a brimmed hat, and water every 15 minutes."
	}
}

// buildDays groups the 48 hours into calendar-day ledgers with sun rows and
// window shading.
func buildDays(in BuildInput, bw, aw *rank.Window, units string) []DayVM {
	if len(in.Res.Hours) == 0 {
		return nil
	}
	nowDay := in.Now.In(NYLoc).YearDay()

	// Index sun times by local calendar day.
	type sunDay struct{ rise, set time.Time }
	sun := map[int]sunDay{}
	for _, s := range in.Res.Sun {
		if !s.Sunrise.IsZero() {
			sun[s.Sunrise.In(NYLoc).YearDay()] = sunDay{rise: s.Sunrise, set: s.Sunset}
		}
	}

	inWindow := func(t time.Time, w *rank.Window) bool {
		return w != nil && !w.Vetoed && !t.Before(w.Start) && t.Before(w.End)
	}
	annotated := map[*rank.Window]bool{}

	// Narrative divider rows, indexed by the hour they follow.
	breaks := map[int64][]string{}
	for _, b := range in.Breaks {
		k := b.After.Unix()
		breaks[k] = append(breaks[k], b.Text)
	}

	var days []DayVM
	var cur *DayVM
	curYD := -1

	for _, h := range in.Res.Hours {
		lt := h.Time.In(NYLoc)
		if lt.YearDay() != curYD {
			curYD = lt.YearDay()
			label := lt.Format("Monday January 2")
			if lt.YearDay() == nowDay && lt.Year() == in.Now.In(NYLoc).Year() {
				label += " · today"
			}
			sl := ""
			if sd, ok := sun[curYD]; ok {
				sl = "rises " + FmtClock(sd.rise) + " · sets " + FmtClock(sd.set)
			}
			days = append(days, DayVM{Label: label, SunLabel: sl})
			cur = &days[len(days)-1]
		}

		sd, hasSun := sun[curYD]
		isDay := lt.Hour() >= 6 && lt.Hour() < 20
		if hasSun {
			isDay = !h.Time.Before(sd.rise.Truncate(time.Hour)) && h.Time.Before(sd.set)
		}

		row := RowVM{
			Kind:      "hour",
			Time:      h.Time,
			TimeLabel: FmtHourLabel(h.Time),
			WBGT:      WBGTMetric(h.WBGTF, units),
			Temp:      TempMetric(h.TempF, units),
			Dew:       TempMetric(h.DewPointF, units),
			RH:        PctMetric(h.RH, QualityRH),
			UV:        CountMetric(h.UVIndex, QualityUV),
			AQI:       CountMetric(h.AQI, QualityAQI),
			Wind:      WindMetric(h.WindMPH, h.GustMPH, h.WindDirDeg),
			Rain:      PctMetric(h.PoP, QualityRain),
			WindDeg:   -1,
			Thunder:   h.WxThunder || (h.ThunderProb.Valid && h.ThunderProb.Value >= rank.VetoThunderProbPct),
		}
		if h.WindDirDeg.Valid {
			row.WindDeg = h.WindDirDeg.Value
		}
		row.Veto = strings.Join(rank.HourVetoes(h, in.Res.Alerts), "; ")
		row.Glyph = glyphFor(h, isDay, hasSun, sd.rise, h.Time)
		switch {
		case inWindow(h.Time, bw):
			row.Window = "before-work"
			if !annotated[bw] {
				annotated[bw] = true
				row.Text = "before work"
			}
		case inWindow(h.Time, aw):
			row.Window = "after-work"
			if !annotated[aw] {
				annotated[aw] = true
				row.Text = "after work"
			}
		}
		cur.Rows = append(cur.Rows, row)

		// Sun divider rows land after the hour that contains the event.
		if hasSun {
			hourEnd := h.Time.Add(time.Hour)
			if !sd.rise.Before(h.Time) && sd.rise.Before(hourEnd) {
				cur.Rows = append(cur.Rows, RowVM{
					Kind: "sun", Time: sd.rise, Glyph: "sunrise",
					Text: "sunrise " + FmtClock(sd.rise),
				})
			}
			if !sd.set.Before(h.Time) && sd.set.Before(hourEnd) {
				cur.Rows = append(cur.Rows, RowVM{
					Kind: "sun", Time: sd.set, Glyph: "sunset",
					Text: "sunset " + FmtClock(sd.set),
				})
			}
		}
		for _, txt := range breaks[h.Time.Unix()] {
			cur.Rows = append(cur.Rows, RowVM{Kind: "narrative", Time: h.Time, Text: txt})
		}
	}
	return days
}

// glyphFor picks the engraved glyph for an hour row.
func glyphFor(h domain.Hour, isDay, hasSun bool, sunrise, t time.Time) string {
	thunder := h.WxThunder || (h.ThunderProb.Valid && h.ThunderProb.Value >= rank.VetoThunderProbPct)
	switch {
	case thunder:
		return "storm"
	case h.PoP.Valid && h.PoP.Value >= 25:
		return "rain"
	case h.SkyCover.Valid && h.SkyCover.Value >= 60:
		return "cloud"
	case hasSun && !sunrise.Before(t) && sunrise.Before(t.Add(time.Hour)):
		return "sunrise"
	case isDay:
		return "sun"
	default:
		return "moon"
	}
}
