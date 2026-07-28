// Package domain holds the core types shared by every other package:
// hourly conditions, metric values with source provenance, and NWS alerts.
// All temperatures are °F, wind speeds mph, canonical internally; the UI
// converts for the °C toggle.
package domain

import (
	"math"
	"time"
)

// Origin identifies which upstream produced a value.
type Origin string

const (
	OriginNWS       Origin = "nws"
	OriginAirNow    Origin = "airnow"
	OriginOpenMeteo Origin = "open-meteo"
	OriginComputed  Origin = "computed" // e.g. Kong–Huber WBGT estimate
)

// SourceTag records provenance for a metric so the UI can label
// measured vs forecast vs estimated vs stale data honestly.
type SourceTag struct {
	Origin    Origin
	FetchedAt time.Time
	Stale     bool // served past its fresh TTL
	Estimated bool // computed locally, not from an authority
	Modeled   bool // model output (e.g. CAMS AQI) vs monitored observation
}

// Metric is a single numeric value plus provenance. Valid=false renders "—".
type Metric struct {
	Value  float64
	Valid  bool
	Source SourceTag
}

// Val returns a valid Metric.
func Val(v float64, s SourceTag) Metric { return Metric{Value: v, Valid: true, Source: s} }

// Hour is one hour of merged conditions for a path.
type Hour struct {
	Time time.Time // start of the hour, America/New_York

	WBGTF       Metric // wet-bulb globe temperature, °F
	TempF       Metric
	DewPointF   Metric
	ApparentF   Metric // apparent temperature (drives warm/cold season branch)
	WindChillF  Metric
	WindMPH     Metric
	GustMPH     Metric
	WindDirDeg  Metric // meteorological degrees: direction the wind comes FROM
	RH          Metric // relative humidity, %
	PoP         Metric // probability of precipitation, %
	SkyCover    Metric // %
	UVIndex     Metric
	AQI         Metric // display AQI = max across pollutants
	ThunderProb Metric // %
	IceAccumIn  Metric // inches

	AQIPollutant string // dominant pollutant, e.g. "PM2.5" (empty if unknown)

	// From the NWS weather layer for this hour.
	WxThunder      bool
	WxFreezingRain bool // freezing rain or sleet
}

// compassPoints are the 16-point compass names, clockwise from north.
var compassPoints = [16]string{
	"N", "NNE", "NE", "ENE", "E", "ESE", "SE", "SSE",
	"S", "SSW", "SW", "WSW", "W", "WNW", "NW", "NNW",
}

// CompassPoint maps meteorological degrees (direction the wind comes FROM,
// 0 = north, clockwise) to the nearest 16-point compass name. Out-of-range
// inputs are normalized modulo 360.
func CompassPoint(deg float64) string {
	deg = math.Mod(deg, 360)
	if deg < 0 {
		deg += 360
	}
	// Each point spans 22.5°; +11.25 centers the sectors on the points.
	i := int((deg+11.25)/22.5) % 16
	return compassPoints[i]
}

// Alert is an active NWS alert for a path's zone.
type Alert struct {
	ID       string
	Event    string // e.g. "Flash Flood Warning"
	Severity string // NWS severity: Extreme, Severe, Moderate, Minor, Unknown
	Headline string
	Onset    time.Time
	Ends     time.Time
}

// ActiveAt reports whether the alert covers time t (zero Ends = open-ended).
func (a Alert) ActiveAt(t time.Time) bool {
	if !a.Onset.IsZero() && t.Before(a.Onset) {
		return false
	}
	return a.Ends.IsZero() || t.Before(a.Ends)
}
