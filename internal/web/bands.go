package web

import (
	"strings"
	"time"

	"github.com/kuitang/whentorun/internal/chart"
	"github.com/kuitang/whentorun/internal/merge"
	"github.com/kuitang/whentorun/internal/rank"
	"github.com/kuitang/whentorun/internal/view"
)

// chartBands assembles the chart's overlay bands from the same choices the
// page frames: the picked before/after-work windows (ice tint), contiguous
// vetoed spans (dense hatch + named reason), and advisory-class alerts
// (top bracket only — a banner, never a veto).
func chartBands(res merge.Result, windows []rank.Window, now time.Time) []chart.Band {
	var bands []chart.Band

	for _, pick := range []struct {
		label string
		w     *rank.Window
	}{
		{"before work", view.PickWindow(windows, "morning", now)},
		{"after work", view.PickWindow(windows, "evening", now)},
	} {
		if pick.w == nil || pick.w.Vetoed {
			continue
		}
		bands = append(bands, chart.Band{
			Kind:  "best",
			Start: pick.w.Start,
			End:   pick.w.End,
			Label: pick.label,
			Sub:   view.FmtSpan(pick.w.Start, pick.w.End),
		})
	}

	// Contiguous vetoed hours become one hatched span labeled with the
	// first hour's leading reason.
	var runStart time.Time
	var runEnd time.Time
	var runLabel string
	flush := func() {
		if !runStart.IsZero() {
			bands = append(bands, chart.Band{Kind: "veto", Start: runStart, End: runEnd, Label: runLabel})
			runStart = time.Time{}
		}
	}
	for _, h := range res.Hours {
		reasons := rank.HourVetoes(h, res.Alerts)
		if len(reasons) == 0 {
			flush()
			continue
		}
		if runStart.IsZero() {
			runStart = h.Time
			runLabel = shortReason(reasons[0])
		}
		runEnd = h.Time.Add(time.Hour)
	}
	flush()

	// Advisory-class alerts (warning-class ones already veto hours above).
	if len(res.Hours) > 0 {
		first := res.Hours[0].Time
		last := res.Hours[len(res.Hours)-1].Time.Add(time.Hour)
		for _, a := range res.Alerts {
			if rank.WarningClassAlert(a.Event) {
				continue
			}
			start, end := first, last
			if a.Onset.After(start) {
				start = a.Onset
			}
			if !a.Ends.IsZero() && a.Ends.Before(end) {
				end = a.Ends
			}
			if !end.After(start) {
				continue
			}
			label := a.Event
			if !a.Ends.IsZero() {
				label += " — until " + view.FmtClockAmPm(a.Ends)
			}
			bands = append(bands, chart.Band{Kind: "advisory", Start: start, End: end, Label: label})
		}
	}
	return bands
}

// shortReason compresses a veto reason to a chart-annotation label.
func shortReason(r string) string {
	lower := strings.ToLower(r)
	if strings.Contains(lower, "thunder") && !strings.Contains(lower, "warning") {
		return "thunderstorms"
	}
	if i := strings.Index(r, " — "); i > 0 {
		return r[i+len(" — "):]
	}
	if i := strings.Index(r, " ("); i > 0 {
		return r[:i]
	}
	return r
}
