package view

import (
	"strings"
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
	"github.com/kuitang/whentorun/internal/rank"
)

func TestQualityThresholds(t *testing.T) {
	cases := []struct {
		name string
		fn   func(float64) int
		v    float64
		want int
	}{
		{"rh", QualityRH, 64, 0}, {"rh", QualityRH, 65, 1}, {"rh", QualityRH, 80, 1}, {"rh", QualityRH, 81, 2},
		{"uv", QualityUV, 2, 0}, {"uv", QualityUV, 3, 1}, {"uv", QualityUV, 7, 1}, {"uv", QualityUV, 8, 2},
		{"aqi", QualityAQI, 50, 0}, {"aqi", QualityAQI, 51, 1}, {"aqi", QualityAQI, 100, 1}, {"aqi", QualityAQI, 101, 2},
		{"rain", QualityRain, 20, 0}, {"rain", QualityRain, 21, 1}, {"rain", QualityRain, 50, 1}, {"rain", QualityRain, 51, 2},
	}
	for _, c := range cases {
		if got := c.fn(c.v); got != c.want {
			t.Errorf("%s(%v) = %d, want %d", c.name, c.v, got, c.want)
		}
	}
	if got := QualityWind(10, 0); got != 0 {
		t.Errorf("wind 10 = %d, want 0", got)
	}
	if got := QualityWind(11, 0); got != 1 {
		t.Errorf("wind 11 = %d, want 1", got)
	}
	if got := QualityWind(6, 22); got != 2 {
		t.Errorf("wind 6 g22 = %d, want 2 (gusts count)", got)
	}
	if got := QualityWind(18, 0); got != 1 {
		t.Errorf("wind 18 = %d, want 1", got)
	}
	if got := QualityWind(19, 0); got != 2 {
		t.Errorf("wind 19 = %d, want 2", got)
	}
}

func TestFmtTemp(t *testing.T) {
	if got := FmtTemp(87.4, "F"); got != "87" {
		t.Errorf("F: got %q", got)
	}
	if got := FmtTemp(87.6, ""); got != "88" {
		t.Errorf("default F: got %q", got)
	}
	if got := FmtTemp(88, "C"); got != "31" {
		t.Errorf("C: got %q, want 31", got)
	}
}

func TestWindMetric(t *testing.T) {
	tag := domain.SourceTag{Origin: domain.OriginNWS}
	m := WindMetric(domain.Val(6, tag), domain.Val(22, tag), domain.Val(180, tag))
	if m.Display != "S 6 g22" {
		t.Errorf("Display = %q, want %q", m.Display, "S 6 g22")
	}
	if m.Quality != 2 {
		t.Errorf("Quality = %d, want 2", m.Quality)
	}
	m = WindMetric(domain.Val(6, tag), domain.Val(9, tag), domain.Val(180, tag))
	if m.Display != "S 6" {
		t.Errorf("Display = %q, want %q (small gust hidden)", m.Display, "S 6")
	}
}

func TestFmtSpan(t *testing.T) {
	start := time.Date(2026, 7, 29, 5, 0, 0, 0, NYLoc)
	if got := FmtSpan(start, start.Add(4*time.Hour)); got != "Wed 5–9 AM" {
		t.Errorf("got %q", got)
	}
	if got := FmtSpan(start.Add(11*time.Hour), start.Add(15*time.Hour)); got != "Wed 4–8 PM" {
		t.Errorf("got %q", got)
	}
	if got := FmtSpan(start.Add(6*time.Hour), start.Add(9*time.Hour)); got != "Wed 11 AM–2 PM" {
		t.Errorf("got %q", got)
	}
}

// syntheticResult builds 48 pleasant hours starting at now's hour.
func syntheticResult(now time.Time) merge.Result {
	tag := domain.SourceTag{Origin: domain.OriginNWS, FetchedAt: now}
	res := merge.Result{Freshness: map[string]merge.SourceStatus{}}
	start := now.In(NYLoc).Truncate(time.Hour)
	for i := 0; i < merge.Horizon; i++ {
		ts := start.Add(time.Duration(i) * time.Hour)
		res.Hours = append(res.Hours, domain.Hour{
			Time:       ts,
			WBGTF:      domain.Val(76, tag),
			TempF:      domain.Val(80, tag),
			DewPointF:  domain.Val(63, tag),
			RH:         domain.Val(60, tag),
			UVIndex:    domain.Val(4, tag),
			AQI:        domain.Val(42, tag),
			WindMPH:    domain.Val(6, tag),
			GustMPH:    domain.Val(9, tag),
			WindDirDeg: domain.Val(180, tag),
			PoP:        domain.Val(10, tag),
			SkyCover:   domain.Val(30, tag),
		})
	}
	for d := 0; d < 3; d++ {
		day := start.AddDate(0, 0, d)
		rise := time.Date(day.Year(), day.Month(), day.Day(), 5, 49, 0, 0, NYLoc)
		res.Sun = append(res.Sun, merge.SunTimes{Sunrise: rise, Sunset: rise.Add(14*time.Hour + 25*time.Minute)})
	}
	for _, s := range []string{merge.SrcNWSGrid, merge.SrcNWSAlerts, merge.SrcAirNow, merge.SrcOMWeather, merge.SrcOMAir} {
		res.Freshness[s] = merge.SourceStatus{Available: true, FetchedAt: now}
	}
	return res
}

func TestBuild(t *testing.T) {
	now := time.Date(2026, 7, 28, 14, 30, 0, 0, NYLoc)
	res := syntheticResult(now)
	windows, err := rank.BestWindows(res.Hours, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	p := Build(BuildInput{
		Path:    *domain.PathBySlug("central-park"),
		Units:   "F",
		Res:     res,
		Windows: windows,
		Now:     now,
	})

	if p.Now.WBGT.Display != "76" || p.Now.WBGT.Tier != 0 {
		t.Errorf("Now.WBGT = %+v, want 76 tier 0", p.Now.WBGT)
	}
	if p.Now.WBGTPhrase != "Low heat stress" {
		t.Errorf("WBGTPhrase = %q", p.Now.WBGTPhrase)
	}
	if p.Now.Temp.Quality != -1 || p.Now.Temp.Tier != -1 {
		t.Errorf("temp must be neutral: %+v", p.Now.Temp)
	}
	if !p.Windows.BeforeWork.Found() || p.Windows.BeforeWork.Vetoed {
		t.Errorf("BeforeWork = %+v, want a found, non-vetoed morning window", p.Windows.BeforeWork)
	}
	if !p.Windows.AfterWork.Found() {
		t.Errorf("AfterWork = %+v, want found", p.Windows.AfterWork)
	}
	if len(p.Days) < 2 || len(p.Days) > 3 {
		t.Fatalf("len(Days) = %d, want 2..3", len(p.Days))
	}
	if !strings.HasSuffix(p.Days[0].Label, "· today") {
		t.Errorf("first day label %q missing today suffix", p.Days[0].Label)
	}
	if p.Days[0].SunLabel != "rises 5:49 · sets 8:14" {
		t.Errorf("SunLabel = %q", p.Days[0].SunLabel)
	}

	// Rows: hour count + sun rows must cover all 48 hours.
	var hourRows, sunRows, annotated, shaded int
	for _, d := range p.Days {
		for _, r := range d.Rows {
			switch r.Kind {
			case "hour":
				hourRows++
				if r.Window != "" {
					shaded++
				}
				if r.Text != "" {
					annotated++
				}
			case "sun":
				sunRows++
			}
		}
	}
	if hourRows != merge.Horizon {
		t.Errorf("hour rows = %d, want %d", hourRows, merge.Horizon)
	}
	if sunRows == 0 {
		t.Error("no sun rows")
	}
	if shaded == 0 || annotated == 0 {
		t.Errorf("window shading rows = %d, annotations = %d, want > 0", shaded, annotated)
	}
	if len(p.Freshness) != 6 {
		t.Fatalf("freshness = %d entries, want 6", len(p.Freshness))
	}
	for i, s := range p.Freshness {
		if s.Name == "NWS text forecast" {
			continue // not in the synthetic fixture
		}
		if !s.Fresh {
			t.Errorf("source %d %s not fresh", i, s.Name)
		}
	}
}

func TestBuildVetoedEvening(t *testing.T) {
	now := time.Date(2026, 7, 28, 14, 30, 0, 0, NYLoc)
	res := syntheticResult(now)
	// Thunder over today's whole evening window (16–20 local).
	for i := range res.Hours {
		hh := res.Hours[i].Time.In(NYLoc)
		if hh.YearDay() == now.YearDay() && hh.Hour() >= 16 && hh.Hour() < 20 {
			res.Hours[i].WxThunder = true
		}
	}
	windows, err := rank.BestWindows(res.Hours, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	p := Build(BuildInput{Path: *domain.PathBySlug("central-park"), Res: res, Windows: windows, Now: now})
	// Tomorrow's evening is clean, so AfterWork should skip today's vetoed one.
	if p.Windows.AfterWork.Vetoed {
		t.Errorf("AfterWork picked the vetoed window: %+v", p.Windows.AfterWork)
	}
	if !strings.HasPrefix(p.Windows.AfterWork.Range, "Wed") {
		t.Errorf("AfterWork.Range = %q, want tomorrow (Wed)", p.Windows.AfterWork.Range)
	}
	// The vetoed hours must carry named reasons in their rows.
	var vetoRows int
	for _, d := range p.Days {
		for _, r := range d.Rows {
			if r.Kind == "hour" && r.Veto != "" {
				vetoRows++
				if !strings.Contains(r.Veto, "thunder") {
					t.Errorf("veto text %q missing thunder", r.Veto)
				}
			}
		}
	}
	if vetoRows != 4 {
		t.Errorf("veto rows = %d, want 4", vetoRows)
	}
}
