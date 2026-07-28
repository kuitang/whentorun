package rank

import (
	"strings"
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

func TestWarningClassAlert(t *testing.T) {
	tests := []struct {
		event string
		want  bool
	}{
		{"Tornado Warning", true},
		{"Severe Thunderstorm Warning", true},
		{"Flash Flood Warning", true},
		{"Excessive Heat Warning", true},
		{"Ice Storm Warning", true},
		{"Winter Storm Warning", true},
		{"Blizzard Warning", true},
		{"Heat Advisory", false},
		{"Flood Watch", false},
		{"Severe Thunderstorm Watch", false},
		{"Special Weather Statement", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := WarningClassAlert(tt.event); got != tt.want {
			t.Errorf("WarningClassAlert(%q) = %v, want %v", tt.event, got, tt.want)
		}
	}
}

func TestHourVetoes(t *testing.T) {
	loc := nycLoc(t)
	at := time.Date(2026, 7, 28, 17, 0, 0, 0, loc)

	tests := []struct {
		name   string
		mutate func(*domain.Hour)
		alerts []domain.Alert
		want   []string // substrings, one per expected reason, in order
	}{
		{
			name: "clean hour has no vetoes",
		},
		{
			// The brief's canary companion: a recorded-style Flash Flood
			// Warning fixture must produce a veto carrying that exact name.
			name: "flash flood warning overlapping hour",
			alerts: []domain.Alert{{
				ID: "urn:oid:2.49.0.1.840.0.test", Event: "Flash Flood Warning",
				Severity: "Severe", Headline: "Flash Flood Warning until 7 PM",
				Onset: at.Add(-30 * time.Minute), Ends: at.Add(2 * time.Hour),
			}},
			want: []string{"Flash Flood Warning"},
		},
		{
			name: "warning starting mid-hour overlaps",
			alerts: []domain.Alert{{
				Event: "Tornado Warning",
				Onset: at.Add(30 * time.Minute), Ends: at.Add(90 * time.Minute),
			}},
			want: []string{"Tornado Warning"},
		},
		{
			name: "warning ended before hour does not veto",
			alerts: []domain.Alert{{
				Event: "Flash Flood Warning",
				Onset: at.Add(-3 * time.Hour), Ends: at.Add(-time.Minute),
			}},
		},
		{
			name: "warning ending exactly at hour start does not veto",
			alerts: []domain.Alert{{
				Event: "Flash Flood Warning",
				Onset: at.Add(-3 * time.Hour), Ends: at,
			}},
		},
		{
			name: "warning starting exactly at hour end does not veto",
			alerts: []domain.Alert{{
				Event: "Flash Flood Warning",
				Onset: at.Add(time.Hour), Ends: at.Add(3 * time.Hour),
			}},
		},
		{
			name: "open-ended warning with zero times vetoes",
			alerts: []domain.Alert{{
				Event: "Excessive Heat Warning",
			}},
			want: []string{"Excessive Heat Warning"},
		},
		{
			name: "advisory-class alert does not veto",
			alerts: []domain.Alert{{
				Event: "Heat Advisory",
				Onset: at.Add(-time.Hour), Ends: at.Add(4 * time.Hour),
			}},
		},
		{
			name:   "thunder probability at threshold",
			mutate: func(h *domain.Hour) { h.ThunderProb = v(30) },
			want:   []string{"thunder probability 30%"},
		},
		{
			name:   "thunder probability just below threshold",
			mutate: func(h *domain.Hour) { h.ThunderProb = v(29) },
		},
		{
			name:   "thunder in weather layer without probability",
			mutate: func(h *domain.Hour) { h.WxThunder = true },
			want:   []string{"thunderstorms in the forecast"},
		},
		{
			name: "AQI at 201 vetoes with category name",
			mutate: func(h *domain.Hour) {
				h.AQI = v(201)
				h.AQIPollutant = "PM2.5"
			},
			want: []string{"AQI 201 — Very Unhealthy"},
		},
		{
			name:   "AQI 200 does not veto",
			mutate: func(h *domain.Hour) { h.AQI = v(200) },
		},
		{
			name:   "AQI 350 names Hazardous",
			mutate: func(h *domain.Hour) { h.AQI = v(350) },
			want:   []string{"AQI 350 — Hazardous"},
		},
		{
			name:   "WBGT at 90F vetoes",
			mutate: func(h *domain.Hour) { h.WBGTF = v(90) },
			want:   []string{"WBGT 90°F — extreme heat stress"},
		},
		{
			name:   "WBGT just below 90F does not veto",
			mutate: func(h *domain.Hour) { h.WBGTF = v(89.9) },
		},
		{
			name:   "freezing rain vetoes",
			mutate: func(h *domain.Hour) { h.WxFreezingRain = true },
			want:   []string{"freezing rain or sleet"},
		},
		{
			name:   "ice accumulation vetoes",
			mutate: func(h *domain.Hour) { h.IceAccumIn = v(0.05) },
			want:   []string{"ice accumulation 0.05 in"},
		},
		{
			name:   "zero ice accumulation does not veto",
			mutate: func(h *domain.Hour) { h.IceAccumIn = v(0) },
		},
		{
			name:   "wind chill below -10F vetoes",
			mutate: func(h *domain.Hour) { h.WindChillF = v(-11) },
			want:   []string{"wind chill -11°F"},
		},
		{
			name:   "wind chill exactly -10F does not veto",
			mutate: func(h *domain.Hour) { h.WindChillF = v(-10) },
		},
		{
			name: "invalid metrics never veto",
			mutate: func(h *domain.Hour) {
				h.AQI = domain.Metric{}
				h.WBGTF = domain.Metric{}
				h.ThunderProb = domain.Metric{}
				h.WindChillF = domain.Metric{}
				h.IceAccumIn = domain.Metric{}
			},
		},
		{
			name: "multiple vetoes in deterministic order",
			mutate: func(h *domain.Hour) {
				h.ThunderProb = v(45)
				h.AQI = v(250)
				h.WBGTF = v(92)
			},
			alerts: []domain.Alert{{
				Event: "Severe Thunderstorm Warning",
				Onset: at.Add(-time.Hour), Ends: at.Add(2 * time.Hour),
			}},
			want: []string{
				"Severe Thunderstorm Warning",
				"thunder probability 45%",
				"AQI 250 — Very Unhealthy",
				"WBGT 92°F — extreme heat stress",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := pleasantWarm(at)
			if tt.mutate != nil {
				tt.mutate(&h)
			}
			got := HourVetoes(h, tt.alerts)
			if len(got) != len(tt.want) {
				t.Fatalf("HourVetoes = %q, want %d reasons matching %q", got, len(tt.want), tt.want)
			}
			for i, sub := range tt.want {
				if !strings.Contains(got[i], sub) {
					t.Errorf("reason[%d] = %q, want substring %q", i, got[i], sub)
				}
			}
		})
	}
}
