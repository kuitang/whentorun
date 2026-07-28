// Package categories maps raw metric values to their official categorical
// interpretations — words, never a composite score. Every table is data
// (ordered bands with explicit tiers) so the UI can render the band label
// plus tier ticks, and tests can walk every boundary.
//
// Sources:
//   - WBGT category names (Low/Elevated/Moderate/High/Extreme): NWS Springfield,
//     https://www.weather.gov/media/sgf/wrn/NWSHeatTools_2.pdf
//     Band edges <80 / 80–85 / 85–88 / 88–90 / >=90 °F follow the general NWS
//     WBGT guidance table (e.g. NWS Tulsa, https://www.weather.gov/tsa/wbgt);
//     the SGF Missouri map legend uses state-tuned edges (78.3/82/86/90) but
//     the same five category names.
//   - UV Index: EPA UV Index scale, https://www.epa.gov/sunsafety/uv-index-scale-0
//   - AQI: EPA AQI basics, https://www.airnow.gov/aqi/aqi-basics/
//   - Wind chill formula and validity range: NWS,
//     https://www.weather.gov/safety/cold-wind-chill-chart
//
// Dew point bands are the conventional US comfort bands with
// runner-appropriate wording; band edges are exact per PLAN.md.
package categories

import "math"

// Category is one categorical interpretation of a metric value.
type Category struct {
	Label string // band label, e.g. "Elevated", "Sticky"
	Tier  int    // 0 = most benign; higher = more concern; drives UI tier ticks
}

// band is one row of a category table: a value v matches the first band with
// v < upper. The final band uses +Inf. Tiers are stored explicitly so tables
// whose concern increases as the value *decreases* (wind chill) read the same
// way as ascending ones.
type band struct {
	upper float64
	cat   Category
}

func lookup(bands []band, v float64) Category {
	for _, b := range bands {
		if v < b.upper {
			return b.cat
		}
	}
	// Unreachable: the last band's upper is +Inf.
	return bands[len(bands)-1].cat
}

// wbgtBands: NWS WBGT heat-stress categories, °F.
// <80 Low / 80–85 Elevated / 85–88 Moderate / 88–90 High / >=90 Extreme.
var wbgtBands = []band{
	{80, Category{"Low", 0}},
	{85, Category{"Elevated", 1}},
	{88, Category{"Moderate", 2}},
	{90, Category{"High", 3}},
	{math.Inf(1), Category{"Extreme", 4}},
}

// WBGT categorizes a wet-bulb globe temperature in °F per NWS guidance.
func WBGT(f float64) Category { return lookup(wbgtBands, f) }

// dewPointBands: runner comfort bands for dew point, °F.
// <55 / 55–60 / 60–65 / 65–70 / 70–75 / >=75.
var dewPointBands = []band{
	{55, Category{"Pleasant", 0}},
	{60, Category{"Comfortable", 1}},
	{65, Category{"Sticky", 2}},
	{70, Category{"Uncomfortable", 3}},
	{75, Category{"Oppressive", 4}},
	{math.Inf(1), Category{"Miserable", 5}},
}

// DewPoint categorizes a dew point in °F into runner comfort bands.
func DewPoint(f float64) Category { return lookup(dewPointBands, f) }

// uvBands: EPA UV Index exposure categories.
// 0–2 Low / 3–5 Moderate / 6–7 High / 8–10 Very High / 11+ Extreme.
// The index is reported in whole numbers; fractional forecast values fall in
// the band of their integer range (e.g. 2.9 is still Low).
var uvBands = []band{
	{3, Category{"Low", 0}},
	{6, Category{"Moderate", 1}},
	{8, Category{"High", 2}},
	{11, Category{"Very High", 3}},
	{math.Inf(1), Category{"Extreme", 4}},
}

// UVIndex categorizes a UV index value per the EPA scale.
func UVIndex(v float64) Category { return lookup(uvBands, v) }

// aqiBands: EPA Air Quality Index categories.
// 0–50 Good / 51–100 Moderate / 101–150 Unhealthy for Sensitive Groups /
// 151–200 Unhealthy / 201–300 Very Unhealthy / 301+ Hazardous.
// AQI values are integers; the exclusive upper bounds sit at the next
// category's first integer.
var aqiBands = []band{
	{51, Category{"Good", 0}},
	{101, Category{"Moderate", 1}},
	{151, Category{"Unhealthy for Sensitive Groups", 2}},
	{201, Category{"Unhealthy", 3}},
	{301, Category{"Very Unhealthy", 4}},
	{math.Inf(1), Category{"Hazardous", 5}},
}

// AQI categorizes an EPA Air Quality Index value.
func AQI(v float64) Category { return lookup(aqiBands, v) }

// windChillBands: runner bands for NWS wind chill, °F. Concern rises as the
// value falls: >=25 Manageable / 10–25 Cold / -10–10 Very Cold / <-10 Dangerous.
var windChillBands = []band{
	{-10, Category{"Dangerous", 3}},
	{10, Category{"Very Cold", 2}},
	{25, Category{"Cold", 1}},
	{math.Inf(1), Category{"Manageable", 0}},
}

// WindChill categorizes a wind-chill temperature in °F into runner bands.
func WindChill(f float64) Category { return lookup(windChillBands, f) }

// ComputeWindChill returns the NWS wind chill in °F for an air temperature
// tempF (°F) and wind speed windMPH (mph):
//
//	WC = 35.74 + 0.6215·T − 35.75·V^0.16 + 0.4275·T·V^0.16
//
// The formula is defined only for T <= 50 °F and wind > 3 mph
// (https://www.weather.gov/safety/cold-wind-chill-chart); outside that range
// ok is false and the air temperature is returned unchanged.
func ComputeWindChill(tempF, windMPH float64) (wc float64, ok bool) {
	if tempF > 50 || windMPH <= 3 {
		return tempF, false
	}
	v16 := math.Pow(windMPH, 0.16)
	return 35.74 + 0.6215*tempF - 35.75*v16 + 0.4275*tempF*v16, true
}
