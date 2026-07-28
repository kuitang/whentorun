package view

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/kuitang/whentorun/internal/categories"
	"github.com/kuitang/whentorun/internal/domain"
)

const missingDisplay = "—"

// NYLoc is the display timezone (falls back to UTC without tzdata).
var NYLoc = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// FToC converts Fahrenheit to Celsius.
func FToC(f float64) float64 { return (f - 32) * 5 / 9 }

// FmtTemp formats a canonical-°F temperature in the requested units as a
// bare rounded integer ("88"); templates append the degree mark.
func FmtTemp(f float64, units string) string {
	v := f
	if units == "C" {
		v = FToC(f)
	}
	return strconv.Itoa(int(math.Round(v)))
}

// Quality thresholds ported from the locked mockup (ledger-v3-synthesis):
// 0 = good, 1 = fair, 2 = poor.

// QualityRH: <=64 good / 65–80 fair / >80 poor.
func QualityRH(v float64) int {
	switch {
	case v <= 64:
		return 0
	case v <= 80:
		return 1
	default:
		return 2
	}
}

// QualityUV: <=2 good / 3–7 fair / >=8 poor.
func QualityUV(v float64) int {
	switch {
	case v <= 2:
		return 0
	case v < 8:
		return 1
	default:
		return 2
	}
}

// QualityAQI: <=50 good / 51–100 fair / >100 poor.
func QualityAQI(v float64) int {
	switch {
	case v <= 50:
		return 0
	case v <= 100:
		return 1
	default:
		return 2
	}
}

// QualityWind: <=10 good / 11–18 fair / >18 poor, judged on the stronger of
// sustained speed and gusts (gusts are what runners feel).
func QualityWind(windMPH, gustMPH float64) int {
	v := math.Max(windMPH, gustMPH)
	switch {
	case v <= 10:
		return 0
	case v <= 18:
		return 1
	default:
		return 2
	}
}

// QualityRain: PoP <=20 good / 21–50 fair / >50 poor.
func QualityRain(v float64) int {
	switch {
	case v <= 20:
		return 0
	case v <= 50:
		return 1
	default:
		return 2
	}
}

// TierWBGT maps a canonical-°F WBGT to its NWS heat band 0..4.
func TierWBGT(f float64) int { return categories.WBGT(f).Tier }

// WBGTPhrase is the display phrase for a WBGT band ("High heat stress").
func WBGTPhrase(f float64) string { return categories.WBGT(f).Label + " heat stress" }

// UVWord is the lowercase EPA UV band word (low/moderate/high/very high/extreme).
func UVWord(v float64) string {
	switch {
	case v < 3:
		return "low"
	case v < 6:
		return "moderate"
	case v < 8:
		return "high"
	case v < 11:
		return "very high"
	default:
		return "extreme"
	}
}

// AQIWord is the lowercase EPA AQI category word.
func AQIWord(v float64) string {
	return strings.ToLower(categories.AQI(v).Label)
}

// DewWord is the runner comfort word for a dew point in °F, matching the
// rank comparator's wording (dry/comfortable/sticky/humid/oppressive/miserable).
func DewWord(f float64) string {
	switch {
	case f < 55:
		return "dry"
	case f < 60:
		return "comfortable"
	case f < 65:
		return "sticky"
	case f < 70:
		return "humid"
	case f < 75:
		return "oppressive"
	default:
		return "miserable"
	}
}

// invalidMetric is the "—" placeholder.
func invalidMetric() MetricVM {
	return MetricVM{Display: missingDisplay, Tier: -1, Quality: -1}
}

// TempMetric formats a temperature metric (neutral: no tier, no quality).
func TempMetric(m domain.Metric, units string) MetricVM {
	if !m.Valid {
		return invalidMetric()
	}
	return MetricVM{Display: FmtTemp(m.Value, units), Tier: -1, Quality: -1, Source: m.Source}
}

// WBGTMetric formats the WBGT metric with its heat-band tier.
func WBGTMetric(m domain.Metric, units string) MetricVM {
	if !m.Valid {
		return invalidMetric()
	}
	return MetricVM{Display: FmtTemp(m.Value, units), Tier: TierWBGT(m.Value), Quality: -1, Source: m.Source}
}

// PctMetric formats a percentage metric ("61%") with the given quality fn.
func PctMetric(m domain.Metric, quality func(float64) int) MetricVM {
	if !m.Valid {
		return invalidMetric()
	}
	return MetricVM{
		Display: strconv.Itoa(int(math.Round(m.Value))) + "%",
		Tier:    -1, Quality: quality(m.Value), Source: m.Source,
	}
}

// CountMetric formats a unitless index metric ("9", "62").
func CountMetric(m domain.Metric, quality func(float64) int) MetricVM {
	if !m.Valid {
		return invalidMetric()
	}
	return MetricVM{
		Display: strconv.Itoa(int(math.Round(m.Value))),
		Tier:    -1, Quality: quality(m.Value), Source: m.Source,
	}
}

// WindMetric formats wind as "S 6" or "S 10 g22" (gust shown when it is
// meaningfully above the sustained speed or crosses the poor threshold).
func WindMetric(wind, gust, dir domain.Metric) MetricVM {
	if !wind.Valid {
		return invalidMetric()
	}
	w := int(math.Round(wind.Value))
	g := 0.0
	if gust.Valid {
		g = gust.Value
	}
	compass := ""
	if dir.Valid {
		compass = domain.CompassPoint(dir.Value) + " "
	}
	display := fmt.Sprintf("%s%d", compass, w)
	if gust.Valid && (g >= wind.Value+8 || g > 18) {
		display += fmt.Sprintf(" g%d", int(math.Round(g)))
	}
	return MetricVM{Display: display, Tier: -1, Quality: QualityWind(wind.Value, g), Source: wind.Source}
}

// FmtClock formats "5:49" style times in NY local.
func FmtClock(t time.Time) string { return t.In(NYLoc).Format("3:04") }

// FmtClockAmPm formats "8 PM" / "2:30 PM" style times in NY local.
func FmtClockAmPm(t time.Time) string {
	lt := t.In(NYLoc)
	if lt.Minute() == 0 {
		return lt.Format("3 PM")
	}
	return lt.Format("3:04 PM")
}

// FmtHourLabel is the ledger time label, "3 PM" with a non-breaking
// space so the cell never wraps between number and meridiem.
func FmtHourLabel(t time.Time) string {
	return t.In(NYLoc).Format("3") + " " + t.In(NYLoc).Format("PM")
}

// FmtFetched formats a fetch time for attribution ("2:21 PM ET"), or
// "unavailable" when zero.
func FmtFetched(t time.Time) string {
	if t.IsZero() {
		return "unavailable"
	}
	return t.In(NYLoc).Format("3:04 PM") + " ET"
}

// FmtSpan formats a window span like "Wed 5–9 AM" or "Tue 4–8 PM"; when the
// meridiem differs it spells both ("Wed 11 AM–2 PM"). End is exclusive.
func FmtSpan(start, end time.Time) string {
	s, e := start.In(NYLoc), end.In(NYLoc)
	day := s.Format("Mon")
	if s.Format("PM") == e.Format("PM") {
		return fmt.Sprintf("%s %s–%s", day, s.Format("3"), e.Format("3 PM"))
	}
	return fmt.Sprintf("%s %s–%s", day, s.Format("3 PM"), e.Format("3 PM"))
}
