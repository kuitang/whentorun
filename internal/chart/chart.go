// Package chart renders the signature merged temperature chart from the
// approved ledger-v3-synthesis mockup as a server-side inline <svg>.
//
// The emitted SVG reproduces the mockup's geometry exactly:
//
//   - x(i) = 10 + 28i px per hour from the first hour; total width 28n.
//   - The temperature plot occupies y 48–268. The y domain is recomputed
//     from the actual WBGT/temp/dew data with 4 °F padding each side;
//     gridlines stay at whole tens.
//   - Precip strip: hatched bars rising from the y=267 baseline, 39 px per
//     100 % PoP (so the mockup's axis reads 100 % at y≈228, 50 % at y≈248).
//   - Hour labels at y 288, day/sun label row at y 308, total height 316.
//
// The sticky y-axis HTML column and the date-lock chip are the server
// template's job; Meta carries the label positions it needs (Y values are
// CSS `top` offsets for 13 px text, i.e. svg y − 6).
//
// Defs contract: the chart emits its own <defs> for the three hatch
// patterns it fills with (#hatchVeto, #hatchWin, #hatchRain — mockup ids,
// classes .pv/.pw/.pr). The PAGE must provide the shared engraved symbols
// referenced by <use>: #g-bolt, #g-sunrise, #g-sunset — and #hatchFine,
// which #g-bolt's fill references. All strokes/fills are class-driven; the
// page must also carry the mockup's fig CSS (.lx-*, .cw0–.cw4, .ln-air,
// .ln-dew, .rainbar, .win-ice, .win-hatch, .veto-hatch, .sun-g, .bolt-g,
// .fx-*, .f0–.f4, .fst, .fd0–.fd4, .lx-brk, .b3, .b4).
package chart

import (
	"fmt"
	"html/template"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/kuitang/whentorun/internal/categories"
	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
)

// Geometry constants lifted from the approved mockup.
const (
	x0      = 10.0  // x of the first hour
	dxHour  = 28.0  // px per hour
	xPad    = 18.0  // right padding after the last hour
	plotTop = 48.0  // top of the temperature plot
	plotBot = 268.0 // baseline of the temperature plot (the x-axis)
	svgH    = 316   // total svg height

	precipBase  = 267.0        // rain bars rise from here
	precipScale = 39.0 / 100.0 // px per % PoP (100% ≈ y 228, 50% ≈ y 248)
	barW        = 18.0         // rain bar width, centered on the hour

	hourLabelY = 288 // hour tick labels
	dayLabelY  = 308 // day names and sun times
	sunGlyphY  = 252 // sunrise/sunset glyph top edge (16×16)

	maxDirectLabels = 6   // selective direct labels on the WBGT line
	yPadDeg         = 4.0 // °F padding above max / below min data
	thunderVetoPct  = 30  // ThunderProb >= this draws a bolt
	minBarPct       = 5.0 // PoP below this draws no bar
)

// wbgtBoundaries are the tier band edges in °F (categories.WBGT).
var wbgtBoundaries = []float64{80, 85, 88, 90}

// Band is a horizontal time span drawn over (or above) the plot.
type Band struct {
	// Kind selects the rendering: "best" (ice tint + sparse hatch +
	// small-caps annotation), "veto" (dense hatch + ✕ reason bracket),
	// "advisory" (top bracket only, no fill).
	Kind  string
	Start time.Time // inclusive left edge
	End   time.Time // exclusive right edge
	Label string    // annotation text ("before work", "THUNDERSTORMS", "HEAT ADVISORY — UNTIL 8 PM")
	Sub   string    // best only: italic range under the label ("Wed 5–8 AM")
}

// YL is one axis label: Y is the CSS top offset (px) for the server's
// sticky-axis column, Label the text.
type YL struct {
	Y     int
	Label string
}

// Meta is what the server template needs to build the sticky axis column.
type Meta struct {
	WidthPX      int
	YLabels      []YL // temperature gridline labels, e.g. "90", "80", "70"
	PrecipLabels []YL // rain axis labels: "100%", "50%"
}

// yScale maps °F values to plot y pixels.
type yScale struct {
	vMax float64 // value at plotTop
	perF float64 // px per °F
}

func (s yScale) y(v float64) float64 { return plotTop + (s.vMax-v)*s.perF }

// newScale computes the y domain from all valid WBGT/temp/dew values with
// yPadDeg padding each side, spanning the fixed plot band.
func newScale(hours []domain.Hour) (yScale, bool) {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, h := range hours {
		for _, m := range []domain.Metric{h.WBGTF, h.TempF, h.DewPointF} {
			if m.Valid {
				lo = math.Min(lo, m.Value)
				hi = math.Max(hi, m.Value)
			}
		}
	}
	if lo > hi {
		return yScale{}, false
	}
	vMin, vMax := lo-yPadDeg, hi+yPadDeg
	return yScale{vMax: vMax, perF: (plotBot - plotTop) / (vMax - vMin)}, true
}

// gridTens returns the whole-ten values that get gridlines: tens at least
// 2 °F inside the padded domain, top-down (mockup order 90, 80, 70).
func (s yScale) gridTens() []int {
	vMin := s.vMax - (plotBot-plotTop)/s.perF
	var tens []int
	top := int(math.Floor((s.vMax - 2) / 10))
	bot := int(math.Ceil((vMin + 2) / 10))
	for t := top; t >= bot; t-- {
		tens = append(tens, t*10)
	}
	return tens
}

// fToC converts Fahrenheit to Celsius.
func fToC(f float64) float64 { return (f - 32) * 5 / 9 }

// cToF converts Celsius to Fahrenheit.
func cToF(c float64) float64 { return c*9/5 + 32 }

// gridLine is one horizontal gridline: its y position (in the °F plot
// space) and its label in display units.
type gridLine struct {
	valF  float64
	label string
}

// gridLines returns the gridlines top-down in the display unit: whole tens
// of °F, or multiples of 5 °C when celsius — always at least 2 °F inside
// the padded domain, mirroring the mockup's 90/80/70 rhythm.
func (s yScale) gridLines(celsius bool) []gridLine {
	if !celsius {
		var gs []gridLine
		for _, t := range s.gridTens() {
			gs = append(gs, gridLine{valF: float64(t), label: strconv.Itoa(t)})
		}
		return gs
	}
	vMin := s.vMax - (plotBot-plotTop)/s.perF
	top := int(math.Floor(fToC(s.vMax-2) / 5))
	bot := int(math.Ceil(fToC(vMin+2) / 5))
	var gs []gridLine
	for c := top; c >= bot; c-- {
		gs = append(gs, gridLine{valF: cToF(float64(c * 5)), label: strconv.Itoa(c * 5)})
	}
	return gs
}

// xAt maps a time to an x pixel, given the first hour.
func xAt(first, t time.Time) float64 {
	return x0 + dxHour*t.Sub(first).Hours()
}

// fnum formats a coordinate: rounded to 2 decimals, trailing zeros trimmed.
func fnum(f float64) string {
	return strconv.FormatFloat(math.Round(f*100)/100, 'f', -1, 64)
}

// esc escapes text for inclusion in SVG text nodes / attributes.
func esc(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
	return r.Replace(s)
}

// pt is one plottable WBGT point.
type pt struct {
	x, y, v float64
}

// wseg is one tier-inked run of the WBGT polyline.
type wseg struct {
	tier int
	pts  []pt
}

func tierOf(v float64) int { return categories.WBGT(v).Tier }

// boundariesBetween returns the tier boundaries strictly between va and vb,
// ordered in the direction of travel va→vb.
func boundariesBetween(va, vb float64) []float64 {
	lo, hi := math.Min(va, vb), math.Max(va, vb)
	var cs []float64
	for _, b := range wbgtBoundaries {
		if b > lo && b < hi {
			cs = append(cs, b)
		}
	}
	if va > vb { // descending: reverse
		for i, j := 0, len(cs)-1; i < j; i, j = i+1, j-1 {
			cs[i], cs[j] = cs[j], cs[i]
		}
	}
	return cs
}

// segmentRun splits one gapless run of WBGT points into tier-inked
// segments, inserting interpolated points at band crossings. Adjacent
// segments share their crossing point, exactly as the mockup does.
func segmentRun(run []pt) []wseg {
	if len(run) < 2 {
		return nil
	}
	var segs []wseg
	cur := wseg{tier: -1}
	piece := func(a, b pt, tier int) {
		if cur.tier != tier {
			if len(cur.pts) > 1 {
				segs = append(segs, cur)
			}
			cur = wseg{tier: tier, pts: []pt{a}}
		}
		cur.pts = append(cur.pts, b)
	}
	for k := 0; k+1 < len(run); k++ {
		a, b := run[k], run[k+1]
		prev := a
		for _, c := range boundariesBetween(a.v, b.v) {
			t := (c - a.v) / (b.v - a.v)
			cp := pt{x: a.x + t*(b.x-a.x), y: a.y + t*(b.y-a.y), v: c}
			piece(prev, cp, tierOf((prev.v+c)/2))
			prev = cp
		}
		piece(prev, b, tierOf((prev.v+b.v)/2))
	}
	if len(cur.pts) > 1 {
		segs = append(segs, cur)
	}
	return segs
}

// extremum is one direct-label candidate on the WBGT line.
type extremum struct {
	p     pt
	isMax bool
	first bool // first point of the whole series
	last  bool // last point of the whole series
}

// findExtrema returns the alternating local extrema of a run (including its
// endpoints), used for selective direct labels.
func findExtrema(run []pt) []extremum {
	if len(run) == 0 {
		return nil
	}
	if len(run) == 1 {
		return []extremum{{p: run[0], isMax: true}}
	}
	var out []extremum
	dir := 0
	for k := 1; k < len(run); k++ {
		d := 0
		if run[k].v > run[k-1].v {
			d = 1
		} else if run[k].v < run[k-1].v {
			d = -1
		}
		if d == 0 {
			continue
		}
		if dir == 0 {
			// The first point is an endpoint extremum.
			out = append(out, extremum{p: run[0], isMax: d < 0})
		} else if d != dir {
			out = append(out, extremum{p: run[k-1], isMax: dir > 0})
		}
		dir = d
	}
	if dir == 0 { // flat series
		return []extremum{{p: run[0], isMax: true}}
	}
	out = append(out, extremum{p: run[len(run)-1], isMax: dir > 0})
	return out
}

// selectLabels trims the extrema list to at most maxDirectLabels by
// prominence (value distance to neighboring extrema), preserving order.
func selectLabels(ex []extremum) []extremum {
	if len(ex) <= maxDirectLabels {
		return ex
	}
	type scored struct {
		i    int
		prom float64
	}
	sc := make([]scored, len(ex))
	for i := range ex {
		p := 0.0
		if i > 0 {
			p += math.Abs(ex[i].p.v - ex[i-1].p.v)
		}
		if i < len(ex)-1 {
			p += math.Abs(ex[i].p.v - ex[i+1].p.v)
		}
		sc[i] = scored{i, p}
	}
	// Selection sort of the top maxDirectLabels by prominence.
	keep := map[int]bool{}
	for n := 0; n < maxDirectLabels; n++ {
		best, bestP := -1, -1.0
		for _, s := range sc {
			if !keep[s.i] && s.prom > bestP {
				best, bestP = s.i, s.prom
			}
		}
		keep[best] = true
	}
	var out []extremum
	for i, e := range ex {
		if keep[i] {
			out = append(out, e)
		}
	}
	return out
}

// bar is one precip strip bar.
type bar struct {
	x, top  float64 // left edge, top edge
	pop     float64
	thunder bool
	day     int // yearday key for per-day max tags
}

func thunderAt(h domain.Hour) bool {
	return h.WxThunder || (h.ThunderProb.Valid && h.ThunderProb.Value >= thunderVetoPct)
}

// hourLabel formats a tick label the mockup way: 12A, 3A, 12P, 3P.
func hourLabel(h int) string {
	switch {
	case h == 0:
		return "12A"
	case h < 12:
		return fmt.Sprintf("%dA", h)
	case h == 12:
		return "12P"
	default:
		return fmt.Sprintf("%dP", h-12)
	}
}

// Render produces the inner chart <svg> plus the Meta the server template
// needs for the sticky axis column. hours must be consecutive and start at
// the chart origin; windows carries best-window, veto and advisory bands;
// sun lists sunrise/sunset per day; loc is the display timezone; unit is
// "F" or "C" — geometry is always the mockup's °F space, but axis labels
// and direct point labels print in the display unit.
func Render(hours []domain.Hour, windows []Band, sun []merge.SunTimes, loc *time.Location, unit string) (template.HTML, Meta, error) {
	if len(hours) == 0 {
		return "", Meta{}, fmt.Errorf("chart: no hours")
	}
	if loc == nil {
		loc = time.UTC
	}
	celsius := unit == "C"
	display := func(f float64) int {
		if celsius {
			return int(math.Round(fToC(f)))
		}
		return int(math.Round(f))
	}
	sc, ok := newScale(hours)
	if !ok {
		return "", Meta{}, fmt.Errorf("chart: no plottable temperature data")
	}
	first := hours[0].Time
	n := len(hours)
	width := int(math.Round(x0 + dxHour*float64(n-1) + xPad))
	lastT := hours[n-1].Time

	meta := Meta{WidthPX: width}
	grid := sc.gridLines(celsius)
	for _, g := range grid {
		meta.YLabels = append(meta.YLabels, YL{Y: int(math.Round(sc.y(g.valF))) - 6, Label: g.label})
	}
	meta.PrecipLabels = []YL{
		{Y: int(math.Round(precipBase-100*precipScale)) - 6, Label: "100%"},
		{Y: int(math.Round(precipBase-50*precipScale)) - 6, Label: "50%"},
	}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg width="%d" height="%d" viewBox="0 0 %d %d" role="img" aria-labelledby="figtitle figdesc">`+"\n", width, svgH, width, svgH)
	axisName := "Fahrenheit"
	if celsius {
		axisName = "Celsius"
	}
	fmt.Fprintf(&b, `<title id="figtitle">WBGT, temperature and dew point for the next 48 hours on one shared %s axis</title>`+"\n", axisName)
	fmt.Fprintf(&b, `<desc id="figdesc">Line chart from %s to %s. WBGT is drawn in its heat-band ink, temperature as a dashed neutral line, dew point dotted. Hatched bars along the base count the chance of rain, with bolt marks at thunder hours.</desc>`+"\n",
		esc(first.In(loc).Format("3 PM Monday")), esc(lastT.In(loc).Format("3 PM Monday")))

	// Hatch patterns the chart itself fills with (mockup ids).
	b.WriteString("<defs>\n")
	b.WriteString(`<pattern id="hatchVeto" width="3.5" height="3.5" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line class="pv" x1="0" y1="0" x2="0" y2="3.5"/></pattern>` + "\n")
	b.WriteString(`<pattern id="hatchWin" width="9" height="9" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line class="pw" x1="0" y1="0" x2="0" y2="9"/></pattern>` + "\n")
	b.WriteString(`<pattern id="hatchRain" width="3" height="3" patternUnits="userSpaceOnUse" patternTransform="rotate(45)"><line class="pr" x1="0" y1="0" x2="0" y2="3"/></pattern>` + "\n")
	b.WriteString("</defs>\n")

	bandX := func(bd Band) (float64, float64) {
		xa := xAt(first, bd.Start)
		xb := xAt(first, bd.End)
		return xa, xb
	}

	// Best-window fills first, then veto fills — under everything else.
	for _, bd := range windows {
		if bd.Kind != "best" {
			continue
		}
		xa, xb := bandX(bd)
		fmt.Fprintf(&b, `<g data-window="best">`+"\n")
		fmt.Fprintf(&b, `  <rect class="win-ice" x="%s" y="%s" width="%s" height="%s"/>`+"\n", fnum(xa), fnum(plotTop), fnum(xb-xa), fnum(plotBot-plotTop))
		fmt.Fprintf(&b, `  <rect class="win-hatch" x="%s" y="%s" width="%s" height="%s"/>`+"\n", fnum(xa), fnum(plotTop), fnum(xb-xa), fnum(plotBot-plotTop))
		b.WriteString("</g>\n")
	}
	for _, bd := range windows {
		if bd.Kind != "veto" {
			continue
		}
		xa, xb := bandX(bd)
		fmt.Fprintf(&b, `<rect class="veto-hatch" x="%s" y="%s" width="%s" height="%s"/>`+"\n", fnum(xa), fnum(plotTop), fnum(xb-xa), fnum(plotBot-plotTop))
	}

	// Gridlines at whole display-unit steps.
	for _, g := range grid {
		y := fnum(sc.y(g.valF))
		fmt.Fprintf(&b, `<line class="lx-grid" x1="0" y1="%s" x2="%d" y2="%s"/>`+"\n", y, width, y)
	}

	// Midnight rules through the plot.
	var midnights []float64
	for i, h := range hours {
		if h.Time.In(loc).Hour() == 0 && i > 0 {
			midnights = append(midnights, xAt(first, h.Time))
		}
	}
	for _, x := range midnights {
		fmt.Fprintf(&b, `<line class="lx-day" x1="%s" y1="%s" x2="%s" y2="%s"/>`+"\n", fnum(x), fnum(plotTop), fnum(x), fnum(plotBot))
	}

	// Top annotations: advisory bracket, veto reason brackets, window names.
	for _, bd := range windows {
		if bd.Kind != "advisory" {
			continue
		}
		xa, xb := bandX(bd)
		fmt.Fprintf(&b, `<text class="fx-ann f3" x="%s" y="16">%s</text>`+"\n", fnum(xa), esc(strings.ToUpper(bd.Label)))
		fmt.Fprintf(&b, `<path class="lx-brk b3" d="M %s 26 V 22 H %s V 26"/>`+"\n", fnum(xa), fnum(xb))
	}
	for _, bd := range windows {
		if bd.Kind != "veto" {
			continue
		}
		xa, xb := bandX(bd)
		fmt.Fprintf(&b, `<text class="fx-ann f4" x="%s" y="40">&#10005; %s</text>`+"\n", fnum(xa), esc(strings.ToUpper(bd.Label)))
		fmt.Fprintf(&b, `<path class="lx-brk b4" d="M %s 44 V 48 M %s 44 H %s M %s 44 V 48"/>`+"\n", fnum(xa), fnum(xa), fnum(xb), fnum(xb))
	}
	for _, bd := range windows {
		if bd.Kind != "best" {
			continue
		}
		xa, xb := bandX(bd)
		mid := fnum((xa + xb) / 2)
		fmt.Fprintf(&b, `<text class="fx-winlab" x="%s" y="66" text-anchor="middle">%s</text>`+"\n", mid, esc(bd.Label))
		if bd.Sub != "" {
			fmt.Fprintf(&b, `<text class="fx-wint" x="%s" y="82" text-anchor="middle">%s</text>`+"\n", mid, esc(bd.Sub))
		}
	}

	// X axis, hour ticks, day ticks, labels.
	fmt.Fprintf(&b, `<line class="lx-ax" x1="0" y1="%s" x2="%d" y2="%s"/>`+"\n", fnum(plotBot), width, fnum(plotBot))
	var minor, labeled, dayTicks strings.Builder
	type hourLab struct {
		x float64
		s string
	}
	var hourLabs []hourLab
	type dayLab struct {
		x float64
		s string
	}
	var dayLabs []dayLab
	for i, h := range hours {
		lt := h.Time.In(loc)
		x := xAt(first, h.Time)
		switch {
		case lt.Hour() == 0 && i > 0:
			fmt.Fprintf(&dayTicks, "M%s %sv12", fnum(x), fnum(plotBot))
			hourLabs = append(hourLabs, hourLab{x, "12A"})
			dayLabs = append(dayLabs, dayLab{x + 6, strings.ToUpper(lt.Format("Mon Jan 2"))})
		case lt.Hour()%3 == 0:
			fmt.Fprintf(&labeled, "M%s %sv7", fnum(x), fnum(plotBot))
			hourLabs = append(hourLabs, hourLab{x, hourLabel(lt.Hour())})
		default:
			fmt.Fprintf(&minor, "M%s %sv4", fnum(x), fnum(plotBot))
		}
		if i == 0 {
			dayLabs = append(dayLabs, dayLab{x, strings.ToUpper(lt.Format("Mon Jan 2"))})
		}
	}
	if minor.Len() > 0 {
		fmt.Fprintf(&b, `<path class="lx-tick" d="%s"/>`+"\n", minor.String())
	}
	if labeled.Len() > 0 {
		fmt.Fprintf(&b, `<path class="lx-tick" d="%s"/>`+"\n", labeled.String())
	}
	if dayTicks.Len() > 0 {
		fmt.Fprintf(&b, `<path class="lx-day" d="%s"/>`+"\n", dayTicks.String())
	}
	for _, hl := range hourLabs {
		fmt.Fprintf(&b, `<text class="fx-hr" x="%s" y="%d" text-anchor="middle">%s</text>`+"\n", fnum(hl.x), hourLabelY, hl.s)
	}
	for _, dl := range dayLabs {
		fmt.Fprintf(&b, `<text class="fx-day" x="%s" y="%d">%s</text>`+"\n", fnum(dl.x), dayLabelY, esc(dl.s))
	}

	// Sunrise / sunset glyphs at the exact minute.
	inRange := func(t time.Time) bool {
		return !t.IsZero() && !t.Before(first) && !t.After(lastT)
	}
	for _, s := range sun {
		if inRange(s.Sunrise) {
			x := xAt(first, s.Sunrise)
			fmt.Fprintf(&b, `<use href="#g-sunrise" data-glyph="sunrise" x="%s" y="%d" width="16" height="16" class="sun-g"/>`+"\n", fnum(x-8), sunGlyphY)
			fmt.Fprintf(&b, `<text class="fx-sun" x="%s" y="%d" text-anchor="middle">rises %s</text>`+"\n", fnum(x), dayLabelY, s.Sunrise.In(loc).Format("3:04"))
		}
		if inRange(s.Sunset) {
			x := xAt(first, s.Sunset)
			fmt.Fprintf(&b, `<use href="#g-sunset" data-glyph="sunset" x="%s" y="%d" width="16" height="16" class="sun-g"/>`+"\n", fnum(x-8), sunGlyphY)
			fmt.Fprintf(&b, `<text class="fx-sun" x="%s" y="%d" text-anchor="middle">sets %s</text>`+"\n", fnum(x), dayLabelY, s.Sunset.In(loc).Format("3:04"))
		}
	}

	// Precip strip: hatched bars, bolts at thunder hours, per-day max tags.
	var bars []bar
	for _, h := range hours {
		if !h.PoP.Valid || h.PoP.Value < minBarPct {
			continue
		}
		x := xAt(first, h.Time)
		hgt := math.Round(h.PoP.Value * precipScale)
		bars = append(bars, bar{
			x: x - barW/2, top: precipBase - hgt,
			pop: h.PoP.Value, thunder: thunderAt(h),
			day: h.Time.In(loc).YearDay(),
		})
	}
	for _, br := range bars {
		fmt.Fprintf(&b, `<rect class="rainbar" x="%s" y="%s" width="%s" height="%s"/>`+"\n", fnum(br.x), fnum(br.top), fnum(barW), fnum(precipBase-br.top))
	}
	for _, br := range bars {
		if br.thunder {
			fmt.Fprintf(&b, `<use href="#g-bolt" x="%s" y="%s" width="10" height="12.8" class="bolt-g"/>`+"\n", fnum(br.x+barW/2-5), fnum(br.top-15))
		}
	}
	// Tag set: each day's max bar, plus every thunder bar.
	dayMax := map[int]int{}
	for i, br := range bars {
		if j, ok := dayMax[br.day]; !ok || br.pop > bars[j].pop {
			dayMax[br.day] = i
		}
	}
	tagged := map[int]bool{}
	for _, i := range dayMax {
		tagged[i] = true
	}
	for i, br := range bars {
		if br.thunder {
			tagged[i] = true
		}
	}
	prevTagX := math.Inf(-1)
	for i, br := range bars {
		if !tagged[i] {
			continue
		}
		txt := fmt.Sprintf("%d%%", int(math.Round(br.pop)))
		cls, anchor := "fst", "middle"
		x, y := br.x+barW/2, br.top-6
		if br.thunder {
			cls = "f4"
			y = br.top - 5
			if br.x-prevTagX < 2*dxHour {
				anchor, x = "start", br.x+barW+1
			} else {
				anchor, x = "end", br.x-1
			}
		}
		fmt.Fprintf(&b, `<text class="fx-pt %s" data-precip-tag x="%s" y="%s" text-anchor="%s">%s</text>`+"\n", cls, fnum(x), fnum(y), anchor, txt)
		prevTagX = br.x
	}

	// Neutral series: dew point dotted, air temperature dashed. Invalid
	// hours break the polyline into separate runs — never drawn as zeros.
	writeRuns := func(class string, get func(domain.Hour) domain.Metric) {
		var run []pt
		flush := func() {
			if len(run) >= 2 {
				var ps []string
				for _, p := range run {
					ps = append(ps, fnum(p.x)+","+fnum(p.y))
				}
				fmt.Fprintf(&b, `<polyline class="%s" points="%s"/>`+"\n", class, strings.Join(ps, " "))
			}
			run = run[:0]
		}
		for _, h := range hours {
			m := get(h)
			if !m.Valid {
				flush()
				continue
			}
			run = append(run, pt{x: xAt(first, h.Time), y: sc.y(m.Value), v: m.Value})
		}
		flush()
	}
	writeRuns("ln-dew", func(h domain.Hour) domain.Metric { return h.DewPointF })
	writeRuns("ln-air", func(h domain.Hour) domain.Metric { return h.TempF })

	// WBGT: tier-ink segmented polyline, split at band crossings, broken at
	// gaps. Collect the valid runs once for both segmentation and labels.
	var wbgtRuns [][]pt
	var cur []pt
	for _, h := range hours {
		if !h.WBGTF.Valid {
			if len(cur) > 0 {
				wbgtRuns = append(wbgtRuns, cur)
				cur = nil
			}
			continue
		}
		cur = append(cur, pt{x: xAt(first, h.Time), y: sc.y(h.WBGTF.Value), v: h.WBGTF.Value})
	}
	if len(cur) > 0 {
		wbgtRuns = append(wbgtRuns, cur)
	}
	for _, run := range wbgtRuns {
		for _, seg := range segmentRun(run) {
			var ps []string
			for _, p := range seg.pts {
				ps = append(ps, fnum(p.x)+","+fnum(p.y))
			}
			fmt.Fprintf(&b, `<polyline class="cw%d" points="%s"/>`+"\n", seg.tier, strings.Join(ps, " "))
		}
	}

	// Selective direct labels at inflection extremes, max 6 across all runs.
	var ex []extremum
	for _, run := range wbgtRuns {
		ex = append(ex, findExtrema(run)...)
	}
	if len(ex) > 0 {
		ex[0].first = true
		ex[len(ex)-1].last = true
	}
	for _, e := range selectLabels(ex) {
		tier := tierOf(e.p.v)
		word := strings.ToLower(categories.WBGT(e.p.v).Label)
		fmt.Fprintf(&b, `<circle class="fd%d" cx="%s" cy="%s" r="1.8"/>`+"\n", tier, fnum(e.p.x), fnum(e.p.y))
		var x, y float64
		anchor := "middle"
		if e.isMax {
			switch {
			case e.first:
				x, y, anchor = e.p.x+4, e.p.y-8, "start"
			case e.last:
				x, y, anchor = e.p.x-4, e.p.y-8, "end"
			default:
				x, y, anchor = e.p.x-8, e.p.y-10, "end"
			}
		} else {
			x, y = e.p.x, e.p.y+16
		}
		fmt.Fprintf(&b, `<text class="fx-pt f%d" x="%s" y="%s" text-anchor="%s">%d&#176; %s</text>`+"\n", tier, fnum(x), fnum(y), anchor, display(e.p.v), esc(word))
	}

	b.WriteString("</svg>")
	return template.HTML(b.String()), meta, nil
}
