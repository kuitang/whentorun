package nws

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

// alertCollection mirrors the GeoJSON /alerts/active response, limited to
// the fields we consume.
type alertCollection struct {
	Features []struct {
		Properties struct {
			ID       string `json:"id"`
			Event    string `json:"event"`
			Severity string `json:"severity"`
			Headline string `json:"headline"`
			Onset    string `json:"onset"`
			Ends     string `json:"ends"`
			Expires  string `json:"expires"`
		} `json:"properties"`
	} `json:"features"`
}

// parseAlerts converts an /alerts/active GeoJSON body into domain alerts.
// A missing/null onset or ends yields a zero time (domain.Alert treats a
// zero Ends as open-ended). When ends is absent, expires is used instead,
// per NWS semantics for alerts without a defined end.
func parseAlerts(body []byte) ([]domain.Alert, error) {
	var coll alertCollection
	if err := json.Unmarshal(body, &coll); err != nil {
		return nil, err
	}
	alerts := make([]domain.Alert, 0, len(coll.Features))
	for i, f := range coll.Features {
		p := f.Properties
		onset, err := parseAlertTime(p.Onset)
		if err != nil {
			return nil, fmt.Errorf("alert %d (%s): onset: %w", i, p.ID, err)
		}
		endsRaw := p.Ends
		if endsRaw == "" {
			endsRaw = p.Expires
		}
		ends, err := parseAlertTime(endsRaw)
		if err != nil {
			return nil, fmt.Errorf("alert %d (%s): ends: %w", i, p.ID, err)
		}
		alerts = append(alerts, domain.Alert{
			ID:       p.ID,
			Event:    p.Event,
			Severity: p.Severity,
			Headline: p.Headline,
			Onset:    onset,
			Ends:     ends,
		})
	}
	return alerts, nil
}

func parseAlertTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	return time.Parse(time.RFC3339, s)
}
