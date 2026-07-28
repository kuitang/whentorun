package rank

import (
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

// Season selects which lexicographic comparator applies.
type Season int

const (
	SeasonWarm Season = iota
	SeasonCold
)

func (s Season) String() string {
	if s == SeasonCold {
		return "cold"
	}
	return "warm"
}

// WarmApparentThresholdF is the warm/cold branch point for the mean daytime
// apparent temperature.
const WarmApparentThresholdF = 60.0

// daytime bounds (local clock hours, half-open): 08:00–19:59.
const daytimeStartH, daytimeEndH = 8, 20

// SeasonFor picks the seasonal comparator for a set of hours.
//
// Rule (documented per PLAN.md): take the mean apparent temperature over
// DAYTIME hours — local clock 08:00 through 19:59 — across the whole slice
// (typically the 48 h horizon). Mean >= 60°F → warm branch (WBGT-led),
// otherwise cold branch (wind-chill-led). Per hour, ApparentF is preferred
// and TempF is the fallback when apparent is missing.
//
// Edge cases: if no daytime hour carries either metric, the mean is taken
// over all hours instead; with no usable data at all the season defaults to
// warm, the product's primary case (safety vetoes apply regardless of
// season, so the default only affects tie-break ordering).
func SeasonFor(hours []domain.Hour, loc *time.Location) Season {
	mean, ok := meanApparent(hours, loc, true)
	if !ok {
		mean, ok = meanApparent(hours, loc, false)
	}
	if !ok || mean >= WarmApparentThresholdF {
		return SeasonWarm
	}
	return SeasonCold
}

// meanApparent averages apparent (fallback: air) temperature; daytimeOnly
// restricts to local 08:00–19:59.
func meanApparent(hours []domain.Hour, loc *time.Location, daytimeOnly bool) (float64, bool) {
	var sum float64
	var n int
	for _, h := range hours {
		if daytimeOnly {
			lh := h.Time.In(loc).Hour()
			if lh < daytimeStartH || lh >= daytimeEndH {
				continue
			}
		}
		m := h.ApparentF
		if !m.Valid {
			m = h.TempF
		}
		if !m.Valid {
			continue
		}
		sum += m.Value
		n++
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}
