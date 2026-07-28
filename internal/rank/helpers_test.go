package rank

import (
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

func nycLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(TZName)
	if err != nil {
		t.Fatalf("LoadLocation: %v", err)
	}
	return loc
}

func v(x float64) domain.Metric {
	return domain.Val(x, domain.SourceTag{Origin: domain.OriginNWS})
}

// pleasantWarm is a clean summer hour: low WBGT band, dry, Good AQI, low UV,
// no rain, calm — every warm key in its best bucket.
func pleasantWarm(at time.Time) domain.Hour {
	return domain.Hour{
		Time:        at,
		WBGTF:       v(70),
		TempF:       v(72),
		DewPointF:   v(50),
		ApparentF:   v(72),
		WindMPH:     v(5),
		GustMPH:     v(8),
		PoP:         v(5),
		UVIndex:     v(1),
		AQI:         v(35),
		ThunderProb: v(0),
	}
}

// pleasantCold is a clean winter hour: mild wind-chill band, no precip.
func pleasantCold(at time.Time) domain.Hour {
	return domain.Hour{
		Time:       at,
		TempF:      v(38),
		ApparentF:  v(33),
		DewPointF:  v(20),
		WindChillF: v(33),
		WindMPH:    v(5),
		GustMPH:    v(8),
		PoP:        v(5),
		UVIndex:    v(1),
		AQI:        v(35),
	}
}

// mkHours generates n consecutive hours starting at start (stepping real
// time, so DST transitions produce the true local clock sequence), applying
// mod (if non-nil) to each.
func mkHours(start time.Time, n int, base func(time.Time) domain.Hour, mod func(i int, h *domain.Hour)) []domain.Hour {
	hours := make([]domain.Hour, n)
	for i := range hours {
		hours[i] = base(start.Add(time.Duration(i) * time.Hour))
		if mod != nil {
			mod(i, &hours[i])
		}
	}
	return hours
}

// findWindow locates a window by day label and span label.
func findWindow(t *testing.T, ws []Window, day, label string) Window {
	t.Helper()
	for _, w := range ws {
		if w.DayLabel == day && w.Label == label {
			return w
		}
	}
	t.Fatalf("window %s %s not found in %v", day, label, windowNames(ws))
	return Window{}
}

func windowNames(ws []Window) []string {
	var out []string
	for _, w := range ws {
		out = append(out, w.DayLabel+" "+w.Label)
	}
	return out
}
