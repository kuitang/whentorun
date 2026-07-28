package rank

import (
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

func TestSeasonFor(t *testing.T) {
	loc := nycLoc(t)
	day := time.Date(2026, 7, 28, 0, 0, 0, 0, loc)

	// hoursAt builds one hour per (clockHour, apparentF) pair.
	hoursAt := func(pairs ...[2]float64) []domain.Hour {
		var hs []domain.Hour
		for _, p := range pairs {
			hs = append(hs, domain.Hour{
				Time:      day.Add(time.Duration(p[0]) * time.Hour),
				ApparentF: v(p[1]),
			})
		}
		return hs
	}

	tests := []struct {
		name  string
		hours []domain.Hour
		want  Season
	}{
		{
			name:  "hot july daytime is warm",
			hours: hoursAt([2]float64{9, 85}, [2]float64{12, 95}, [2]float64{18, 88}),
			want:  SeasonWarm,
		},
		{
			name:  "january daytime is cold",
			hours: hoursAt([2]float64{9, 25}, [2]float64{12, 33}, [2]float64{18, 28}),
			want:  SeasonCold,
		},
		{
			name:  "mean exactly at 60F threshold is warm",
			hours: hoursAt([2]float64{10, 55}, [2]float64{14, 65}),
			want:  SeasonWarm,
		},
		{
			name:  "mean just below threshold is cold",
			hours: hoursAt([2]float64{10, 55}, [2]float64{14, 64.9}),
			want:  SeasonCold,
		},
		{
			name: "nighttime hours excluded from daytime mean",
			// Daytime (8-19) hours are cold; freakishly warm night hours
			// must not flip the branch.
			hours: hoursAt(
				[2]float64{2, 80}, [2]float64{3, 80}, [2]float64{22, 80},
				[2]float64{10, 40}, [2]float64{14, 45},
			),
			want: SeasonCold,
		},
		{
			name: "daytime boundary hours 8 included and 20 excluded",
			// Hour 8 (cold 40) counts; hour 20 (warm 100) does not:
			// mean = 40 -> cold. If 20:00 were included mean would be 70.
			hours: hoursAt([2]float64{8, 40}, [2]float64{20, 100}),
			want:  SeasonCold,
		},
		{
			name: "falls back to TempF when apparent invalid",
			hours: []domain.Hour{
				{Time: day.Add(12 * time.Hour), TempF: v(80)},
			},
			want: SeasonWarm,
		},
		{
			name: "night-only data falls back to all-hours mean",
			// No daytime samples at all: mean over everything (30F) -> cold.
			hours: hoursAt([2]float64{1, 30}, [2]float64{23, 30}),
			want:  SeasonCold,
		},
		{
			name:  "no usable data defaults to warm",
			hours: []domain.Hour{{Time: day.Add(12 * time.Hour)}},
			want:  SeasonWarm,
		},
		{
			name: "empty slice defaults to warm",
			want: SeasonWarm,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SeasonFor(tt.hours, loc); got != tt.want {
				t.Errorf("SeasonFor = %v, want %v", got, tt.want)
			}
		})
	}
}
