package rank

import (
	"fmt"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

// Veto thresholds. Proposed defaults flagged for Kui at mockup review
// (PLAN.md "Flagged for Kui" #1).
const (
	// VetoThunderProbPct vetoes an hour when NWS probabilityOfThunder
	// meets or exceeds this percentage.
	VetoThunderProbPct = 30.0
	// VetoAQI vetoes at the EPA Very Unhealthy floor (AQI 201).
	VetoAQI = 201.0
	// VetoWBGTF vetoes at the top NWS WBGT band (>= 90°F).
	VetoWBGTF = 90.0
	// VetoWindChillF vetoes below -10°F wind chill.
	VetoWindChillF = -10.0
)

// warningClassEvents are the NWS alert events that veto an hour outright.
var warningClassEvents = map[string]bool{
	"Tornado Warning":             true,
	"Severe Thunderstorm Warning": true,
	"Flash Flood Warning":         true,
	"Excessive Heat Warning":      true,
	"Ice Storm Warning":           true,
	"Winter Storm Warning":        true,
	"Blizzard Warning":            true,
}

// WarningClassAlert reports whether the NWS event name is a warning-class
// alert that vetoes overlapping hours.
func WarningClassAlert(event string) bool { return warningClassEvents[event] }

// alertOverlapsHour reports whether alert a is active at any point during
// the hour starting at start (i.e. [start, start+1h)). Zero Onset means
// "already in effect"; zero Ends means open-ended — same conventions as
// domain.Alert.ActiveAt, generalized from an instant to an interval.
func alertOverlapsHour(a domain.Alert, start time.Time) bool {
	end := start.Add(time.Hour)
	if !a.Onset.IsZero() && !a.Onset.Before(end) {
		return false // starts at or after the hour ends
	}
	if !a.Ends.IsZero() && !a.Ends.After(start) {
		return false // ended at or before the hour begins
	}
	return true
}

// HourVetoes returns the named reasons this hour is unsuitable for running,
// in a deterministic order (alerts first, then thunder, AQI, WBGT, ice,
// wind chill). An empty slice means the hour is not vetoed. Each string is
// meant for direct display in the UI.
func HourVetoes(h domain.Hour, alerts []domain.Alert) []string {
	var reasons []string
	for _, a := range alerts {
		if WarningClassAlert(a.Event) && alertOverlapsHour(a, h.Time) {
			reasons = append(reasons, a.Event)
		}
	}
	switch {
	case h.WxThunder:
		reasons = append(reasons, "thunderstorms in the forecast")
	case h.ThunderProb.Valid && h.ThunderProb.Value >= VetoThunderProbPct:
		reasons = append(reasons, fmt.Sprintf("thunder probability %.0f%%", h.ThunderProb.Value))
	}
	if h.AQI.Valid && h.AQI.Value >= VetoAQI {
		reasons = append(reasons, fmt.Sprintf("AQI %.0f — %s", h.AQI.Value, aqiBucketVal(h.AQI.Value).label))
	}
	if h.WBGTF.Valid && h.WBGTF.Value >= VetoWBGTF {
		reasons = append(reasons, fmt.Sprintf("WBGT %.0f°F — extreme heat stress", h.WBGTF.Value))
	}
	switch {
	case h.WxFreezingRain:
		reasons = append(reasons, "freezing rain or sleet")
	case h.IceAccumIn.Valid && h.IceAccumIn.Value > 0:
		reasons = append(reasons, fmt.Sprintf("ice accumulation %.2f in", h.IceAccumIn.Value))
	}
	if h.WindChillF.Valid && h.WindChillF.Value < VetoWindChillF {
		reasons = append(reasons, fmt.Sprintf("wind chill %.0f°F", h.WindChillF.Value))
	}
	return reasons
}
