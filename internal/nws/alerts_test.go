package nws

import (
	"os"
	"testing"
	"time"
)

// TestParseAlertsFixture parses the recorded live alerts response for
// NYZ072 (a real Flood Watch captured 2026-07-28).
func TestParseAlertsFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/alerts_nyz072.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	alerts, err := parseAlerts(raw)
	if err != nil {
		t.Fatalf("parseAlerts: %v", err)
	}
	if len(alerts) != 1 {
		t.Fatalf("len(alerts) = %d, want 1", len(alerts))
	}
	a := alerts[0]
	if a.Event != "Flood Watch" {
		t.Errorf("Event = %q, want %q", a.Event, "Flood Watch")
	}
	if a.Severity != "Severe" {
		t.Errorf("Severity = %q, want %q", a.Severity, "Severe")
	}
	if a.ID == "" {
		t.Error("ID is empty")
	}
	if a.Headline == "" {
		t.Error("Headline is empty")
	}
	edt := time.FixedZone("EDT", -4*3600)
	if want := time.Date(2026, 7, 28, 8, 0, 0, 0, edt); !a.Onset.Equal(want) {
		t.Errorf("Onset = %v, want %v", a.Onset, want)
	}
	if want := time.Date(2026, 7, 29, 8, 0, 0, 0, edt); !a.Ends.Equal(want) {
		t.Errorf("Ends = %v, want %v", a.Ends, want)
	}
	// Sanity: the alert is active in the middle of its window.
	if !a.ActiveAt(time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)) {
		t.Error("ActiveAt(mid-window) = false, want true")
	}
}

func TestParseAlertsTable(t *testing.T) {
	tests := []struct {
		name      string
		body      string
		wantErr   bool
		wantN     int
		wantEvent string
		wantOnset time.Time
		wantEnds  time.Time
	}{
		{
			name:  "empty feed",
			body:  `{"features":[]}`,
			wantN: 0,
		},
		{
			name: "null ends falls back to expires",
			body: `{"features":[{"properties":{
				"id":"x1","event":"Special Weather Statement","severity":"Moderate",
				"headline":"h","onset":"2026-07-28T10:00:00+00:00",
				"ends":null,"expires":"2026-07-28T14:00:00+00:00"}}]}`,
			wantN:     1,
			wantEvent: "Special Weather Statement",
			wantOnset: time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC),
			wantEnds:  time.Date(2026, 7, 28, 14, 0, 0, 0, time.UTC),
		},
		{
			name: "null onset and no times → zero times (open-ended)",
			body: `{"features":[{"properties":{
				"id":"x2","event":"Tornado Warning","severity":"Extreme",
				"headline":"h","onset":null,"ends":null}}]}`,
			wantN:     1,
			wantEvent: "Tornado Warning",
		},
		{
			name:    "malformed json",
			body:    `{"features":`,
			wantErr: true,
		},
		{
			name: "bad onset time",
			body: `{"features":[{"properties":{
				"id":"x3","event":"E","onset":"not-a-time"}}]}`,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			alerts, err := parseAlerts([]byte(tt.body))
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAlerts: %v", err)
			}
			if len(alerts) != tt.wantN {
				t.Fatalf("len = %d, want %d", len(alerts), tt.wantN)
			}
			if tt.wantN == 0 {
				return
			}
			a := alerts[0]
			if a.Event != tt.wantEvent {
				t.Errorf("Event = %q, want %q", a.Event, tt.wantEvent)
			}
			if !a.Onset.Equal(tt.wantOnset) {
				t.Errorf("Onset = %v, want %v", a.Onset, tt.wantOnset)
			}
			if !a.Ends.Equal(tt.wantEnds) {
				t.Errorf("Ends = %v, want %v", a.Ends, tt.wantEnds)
			}
		})
	}
}
