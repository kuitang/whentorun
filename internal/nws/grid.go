package nws

import (
	"fmt"
	"strings"
	"time"
)

// Gridpoint is the raw /gridpoints/{office}/{x},{y} response, limited to
// the layers we consume. Values are run-length encoded: each entry covers
// an ISO 8601 interval like "2026-07-28T18:00:00+00:00/PT3H".
type Gridpoint struct {
	Properties GridProperties `json:"properties"`
}

// GridProperties holds the forecast layers plus identifying metadata.
type GridProperties struct {
	UpdateTime string `json:"updateTime"`
	ValidTimes string `json:"validTimes"`
	GridID     string `json:"gridId"`
	GridX      int    `json:"gridX"`
	GridY      int    `json:"gridY"`

	Temperature          GridLayer    `json:"temperature"`
	Dewpoint             GridLayer    `json:"dewpoint"`
	WBGT                 GridLayer    `json:"wetBulbGlobeTemperature"`
	WindSpeed            GridLayer    `json:"windSpeed"`
	WindGust             GridLayer    `json:"windGust"`
	PoP                  GridLayer    `json:"probabilityOfPrecipitation"`
	SkyCover             GridLayer    `json:"skyCover"`
	WindChill            GridLayer    `json:"windChill"`
	ApparentTemperature  GridLayer    `json:"apparentTemperature"`
	ProbabilityOfThunder GridLayer    `json:"probabilityOfThunder"`
	IceAccumulation      GridLayer    `json:"iceAccumulation"`
	Weather              WeatherLayer `json:"weather"`
}

// GridLayer is one numeric RLE layer.
type GridLayer struct {
	UOM    string      `json:"uom"`
	Values []GridValue `json:"values"`
}

// GridValue is one RLE run: a value held constant across an interval.
// Value is a pointer because NWS emits explicit nulls.
type GridValue struct {
	ValidTime string   `json:"validTime"`
	Value     *float64 `json:"value"`
}

// WeatherLayer is the categorical "weather" layer.
type WeatherLayer struct {
	Values []WeatherValue `json:"values"`
}

// WeatherValue is one RLE run of concurrent weather conditions.
type WeatherValue struct {
	ValidTime string             `json:"validTime"`
	Value     []WeatherCondition `json:"value"`
}

// WeatherCondition is a single condition within a run (all fields nullable).
type WeatherCondition struct {
	Coverage  *string `json:"coverage"`
	Weather   *string `json:"weather"`
	Intensity *string `json:"intensity"`
}

// Opt is an optional float64; Valid=false means the grid had no value
// (null run or no run covering the hour).
type Opt struct {
	Value float64
	Valid bool
}

// HourValues is one hour of raw per-hour grid values, already converted to
// display units (°F, mph, inches; percentages as 0–100).
type HourValues struct {
	Time time.Time // start of the hour, in the requested location

	TempF       Opt
	DewPointF   Opt
	WBGTF       Opt
	ApparentF   Opt
	WindChillF  Opt
	WindMPH     Opt
	GustMPH     Opt
	PoP         Opt // %
	SkyCover    Opt // %
	ThunderProb Opt // %
	IceAccumIn  Opt // accumulation over the run's period, repeated per hour

	// From the categorical weather layer. WxThunder is true when any
	// condition for the hour mentions thunderstorms (at any coverage);
	// WxFreezingRain when any mentions freezing rain/drizzle, sleet, or
	// ice pellets. Interpretation (e.g. coverage thresholds) is the
	// caller's job.
	WxThunder      bool
	WxFreezingRain bool
}

// Forecast is the expanded per-hour view of a gridpoint.
type Forecast struct {
	UpdateTime time.Time
	Hours      []HourValues
}

// Expand converts the RLE layers into per-hour values for `hours` hours
// starting at start (truncated to the hour; hour times are produced in
// start's location). Hours not covered by any run stay invalid.
func (gp *Gridpoint) Expand(start time.Time, hours int) (*Forecast, error) {
	if hours <= 0 {
		return nil, fmt.Errorf("nws: Expand: hours must be positive, got %d", hours)
	}
	start = start.Truncate(time.Hour)
	f := &Forecast{Hours: make([]HourValues, hours)}
	for i := range f.Hours {
		f.Hours[i].Time = start.Add(time.Duration(i) * time.Hour)
	}
	if ut, err := time.Parse(time.RFC3339, gp.Properties.UpdateTime); err == nil {
		f.UpdateTime = ut
	}

	p := &gp.Properties
	numeric := []struct {
		name  string
		layer GridLayer
		conv  func(v float64, uom string) float64
		set   func(h *HourValues, o Opt)
	}{
		{"temperature", p.Temperature, tempToF, func(h *HourValues, o Opt) { h.TempF = o }},
		{"dewpoint", p.Dewpoint, tempToF, func(h *HourValues, o Opt) { h.DewPointF = o }},
		{"wetBulbGlobeTemperature", p.WBGT, tempToF, func(h *HourValues, o Opt) { h.WBGTF = o }},
		{"apparentTemperature", p.ApparentTemperature, tempToF, func(h *HourValues, o Opt) { h.ApparentF = o }},
		{"windChill", p.WindChill, tempToF, func(h *HourValues, o Opt) { h.WindChillF = o }},
		{"windSpeed", p.WindSpeed, speedToMPH, func(h *HourValues, o Opt) { h.WindMPH = o }},
		{"windGust", p.WindGust, speedToMPH, func(h *HourValues, o Opt) { h.GustMPH = o }},
		{"probabilityOfPrecipitation", p.PoP, passthrough, func(h *HourValues, o Opt) { h.PoP = o }},
		{"skyCover", p.SkyCover, passthrough, func(h *HourValues, o Opt) { h.SkyCover = o }},
		{"probabilityOfThunder", p.ProbabilityOfThunder, passthrough, func(h *HourValues, o Opt) { h.ThunderProb = o }},
		{"iceAccumulation", p.IceAccumulation, lengthToIn, func(h *HourValues, o Opt) { h.IceAccumIn = o }},
	}
	for _, nl := range numeric {
		for _, run := range nl.layer.Values {
			if run.Value == nil {
				continue
			}
			o := Opt{Value: nl.conv(*run.Value, nl.layer.UOM), Valid: true}
			if err := forEachHour(run.ValidTime, start, hours, func(i int) {
				nl.set(&f.Hours[i], o)
			}); err != nil {
				return nil, fmt.Errorf("nws: layer %s: %w", nl.name, err)
			}
		}
	}

	for _, run := range p.Weather.Values {
		var thunder, freezing bool
		for _, cond := range run.Value {
			if cond.Weather == nil {
				continue
			}
			switch w := *cond.Weather; {
			case strings.Contains(w, "thunder"):
				thunder = true
			case strings.Contains(w, "freezing"), w == "sleet", w == "ice_pellets":
				freezing = true
			}
		}
		if !thunder && !freezing {
			continue
		}
		if err := forEachHour(run.ValidTime, start, hours, func(i int) {
			f.Hours[i].WxThunder = f.Hours[i].WxThunder || thunder
			f.Hours[i].WxFreezingRain = f.Hours[i].WxFreezingRain || freezing
		}); err != nil {
			return nil, fmt.Errorf("nws: layer weather: %w", err)
		}
	}
	return f, nil
}

// forEachHour parses an ISO interval and calls fn with the index (relative
// to start) of every whole hour the run covers inside [start, start+hours).
func forEachHour(validTime string, start time.Time, hours int, fn func(i int)) error {
	runStart, dur, err := parseInterval(validTime)
	if err != nil {
		return err
	}
	runEnd := runStart.Add(dur)
	for t := runStart.Truncate(time.Hour); t.Before(runEnd); t = t.Add(time.Hour) {
		i := int(t.Sub(start) / time.Hour)
		if i >= 0 && i < hours {
			fn(i)
		}
	}
	return nil
}

// parseInterval parses "2026-07-28T18:00:00+00:00/PT3H" into a start time
// and duration.
func parseInterval(s string) (time.Time, time.Duration, error) {
	tPart, dPart, ok := strings.Cut(s, "/")
	if !ok {
		return time.Time{}, 0, fmt.Errorf("validTime %q: missing '/'", s)
	}
	start, err := time.Parse(time.RFC3339, tPart)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("validTime %q: %w", s, err)
	}
	dur, err := parseISODuration(dPart)
	if err != nil {
		return time.Time{}, 0, fmt.Errorf("validTime %q: %w", s, err)
	}
	return start, dur, nil
}

// parseISODuration parses the subset of ISO 8601 durations NWS emits:
// P[nW][nD][T[nH][nM][nS]], e.g. PT3H, P1D, P7DT13H.
func parseISODuration(s string) (time.Duration, error) {
	if len(s) < 2 || s[0] != 'P' {
		return 0, fmt.Errorf("duration %q: must start with P", s)
	}
	var d time.Duration
	num := 0
	haveNum := false
	inTime := false
	for i := 1; i < len(s); i++ {
		c := s[i]
		switch {
		case c == 'T':
			if inTime {
				return 0, fmt.Errorf("duration %q: repeated T", s)
			}
			inTime = true
		case c >= '0' && c <= '9':
			num = num*10 + int(c-'0')
			haveNum = true
		default:
			if !haveNum {
				return 0, fmt.Errorf("duration %q: designator %c without number", s, c)
			}
			var unit time.Duration
			switch {
			case c == 'W' && !inTime:
				unit = 7 * 24 * time.Hour
			case c == 'D' && !inTime:
				unit = 24 * time.Hour
			case c == 'H' && inTime:
				unit = time.Hour
			case c == 'M' && inTime:
				unit = time.Minute
			case c == 'S' && inTime:
				unit = time.Second
			default:
				return 0, fmt.Errorf("duration %q: unsupported designator %c", s, c)
			}
			d += time.Duration(num) * unit
			num, haveNum = 0, false
		}
	}
	if haveNum {
		return 0, fmt.Errorf("duration %q: trailing number without designator", s)
	}
	if d == 0 {
		return 0, fmt.Errorf("duration %q: zero duration", s)
	}
	return d, nil
}

// Unit conversions. NWS grids normally use °C, km/h, and mm, but we check
// the uom field and pass through values already in display units.

func tempToF(v float64, uom string) float64 {
	if uomIs(uom, "degF") {
		return v
	}
	return v*9/5 + 32 // assume °C (wmoUnit:degC is the grid default)
}

func speedToMPH(v float64, uom string) float64 {
	if uomIs(uom, "mph", "mi_h-1") {
		return v
	}
	return v / 1.609344 // assume km/h (wmoUnit:km_h-1 is the grid default)
}

func lengthToIn(v float64, uom string) float64 {
	if uomIs(uom, "in", "inch") {
		return v
	}
	return v / 25.4 // assume mm (wmoUnit:mm is the grid default)
}

func passthrough(v float64, _ string) float64 { return v }

// uomIs reports whether the uom's unit part (after any namespace prefix
// like "wmoUnit:") matches one of the given names.
func uomIs(uom string, names ...string) bool {
	if i := strings.LastIndexByte(uom, ':'); i >= 0 {
		uom = uom[i+1:]
	}
	for _, n := range names {
		if uom == n {
			return true
		}
	}
	return false
}
