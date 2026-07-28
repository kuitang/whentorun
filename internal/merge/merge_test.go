package merge_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/airnow"
	"github.com/kuitang/whentorun/internal/cache"
	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
	"github.com/kuitang/whentorun/internal/nws"
	"github.com/kuitang/whentorun/internal/openmeteo"
	"github.com/kuitang/whentorun/internal/wbgt"
)

var nyLoc = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		panic(err)
	}
	return loc
}()

func f(v float64) *float64 { return &v }

var centralPark = *domain.PathBySlug("central-park")

// --- synthetic sources for the fallback matrix ---

// syntheticGrid builds an NWS gridpoint whose every layer holds one constant
// value across [start, start+48h). Units are the NWS grid defaults (°C,
// km/h) so the conversion path is exercised too.
func syntheticGrid(start time.Time) *nws.Gridpoint {
	iv := start.UTC().Format(time.RFC3339) + "/PT48H"
	layer := func(uom string, v float64) nws.GridLayer {
		return nws.GridLayer{UOM: uom, Values: []nws.GridValue{{ValidTime: iv, Value: &v}}}
	}
	gp := &nws.Gridpoint{}
	p := &gp.Properties
	p.UpdateTime = start.UTC().Format(time.RFC3339)
	p.Temperature = layer("wmoUnit:degC", 25) // 77 °F
	p.Dewpoint = layer("wmoUnit:degC", 20)    // 68 °F
	p.WBGT = layer("wmoUnit:degC", 27)        // 80.6 °F
	p.ApparentTemperature = layer("wmoUnit:degC", 26)
	p.WindSpeed = layer("wmoUnit:km_h-1", 16.09344)        // 10 mph
	p.WindGust = layer("wmoUnit:km_h-1", 32.18688)         // 20 mph
	p.WindDirection = layer("wmoUnit:degree_(angle)", 170) // from the SSE
	p.RelativeHumidity = layer("wmoUnit:percent", 72)
	p.PoP = layer("wmoUnit:percent", 30)
	p.SkyCover = layer("wmoUnit:percent", 55)
	p.ProbabilityOfThunder = layer("wmoUnit:percent", 10)
	return gp
}

func syntheticWeather(start time.Time, n int) openmeteo.WeatherData {
	hours := make([]openmeteo.WeatherHour, n)
	for i := range hours {
		hours[i] = openmeteo.WeatherHour{
			Time:          start.Add(time.Duration(i) * time.Hour),
			TempF:         f(80),
			RelHumidity:   f(60),
			DewPointF:     f(65),
			WindMPH:       f(5),
			GustMPH:       f(12),
			WindDirDeg:    f(250),
			UVIndex:       f(6),
			ShortwaveWm2:  f(500),
			DirectWm2:     f(300),
			DiffuseWm2:    f(200),
			PoP:           f(20),
			CloudCoverPct: f(40),
		}
	}
	// Daily sunrise/sunset: one day before the window plus every day it
	// touches, so merge's day filtering is exercised.
	first := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location()).AddDate(0, 0, -1)
	days := make([]openmeteo.SunDay, 0, 5)
	for d := 0; d < 5; d++ {
		day := first.AddDate(0, 0, d)
		days = append(days, openmeteo.SunDay{
			Date:    day,
			Sunrise: day.Add(5*time.Hour + 48*time.Minute),
			Sunset:  day.Add(20*time.Hour + 15*time.Minute),
		})
	}
	return openmeteo.WeatherData{Hours: hours, Days: days}
}

// syntheticForecast builds 12-hour narrative periods around start: one
// fully-past period, five inside/overlapping the 48 h window, and two
// beyond it, so merge's overlap filtering is exercised. Periods i=1..4
// (Tonight .. Wednesday) overlap [start, start+48h).
func syntheticForecast(start time.Time) *nws.TextForecast {
	names := []string{"Yesterday", "Tonight", "Tuesday", "Tuesday Night", "Wednesday", "Beyond", "Far Beyond"}
	tf := &nws.TextForecast{}
	for i, name := range names {
		s := start.Add(time.Duration(i-1) * 12 * time.Hour)
		tf.Properties.Periods = append(tf.Properties.Periods, nws.ForecastPeriod{
			Number:    i + 1,
			Name:      name,
			Start:     s,
			End:       s.Add(12 * time.Hour),
			IsDaytime: i%2 == 0,
			Short:     "Chance Showers",
			Detailed:  name + ": a chance of showers.",
		})
	}
	return tf
}

func syntheticAir(start time.Time, n int) []openmeteo.AirQualityHour {
	hours := make([]openmeteo.AirQualityHour, n)
	for i := range hours {
		hours[i] = openmeteo.AirQualityHour{Time: start.Add(time.Duration(i) * time.Hour), AQI: f(42)}
	}
	return hours
}

var syntheticObs = []airnow.ObservationRecord{
	{ParameterName: "PM2.5", NowcastAQI: 66, AQICategoryName: "Moderate"},
	{ParameterName: "OZONE", NowcastAQI: 64, AQICategoryName: "Moderate"},
}

var syntheticAlerts = []domain.Alert{{
	ID: "test-alert", Event: "Flood Watch", Severity: "Moderate", Headline: "Flood Watch in effect",
}}

func okIn[T any](data T, fetchedAt time.Time) merge.Input[T] {
	return merge.Input[T]{Data: data, FetchedAt: fetchedAt, OK: true}
}

// allUp returns Sources with every upstream healthy, fetched at `fetched`.
func allUp(start, fetched time.Time) merge.Sources {
	return merge.Sources{
		Grid:      okIn(syntheticGrid(start), fetched),
		Forecast:  okIn(syntheticForecast(start), fetched),
		Alerts:    okIn(syntheticAlerts, fetched),
		AirNowObs: okIn(syntheticObs, fetched),
		OMWeather: okIn(syntheticWeather(start, merge.Horizon), fetched),
		OMAir:     okIn(syntheticAir(start, merge.Horizon), fetched),
	}
}

func wantMetric(t *testing.T, name string, m domain.Metric, value float64, origin domain.Origin, estimated, modeled bool) {
	t.Helper()
	if !m.Valid {
		t.Fatalf("%s: invalid, want value %v from %s", name, value, origin)
	}
	if diff := m.Value - value; diff > 0.01 || diff < -0.01 {
		t.Errorf("%s: value = %v, want %v", name, m.Value, value)
	}
	if m.Source.Origin != origin {
		t.Errorf("%s: origin = %s, want %s", name, m.Source.Origin, origin)
	}
	if m.Source.Estimated != estimated {
		t.Errorf("%s: Estimated = %v, want %v", name, m.Source.Estimated, estimated)
	}
	if m.Source.Modeled != modeled {
		t.Errorf("%s: Modeled = %v, want %v", name, m.Source.Modeled, modeled)
	}
}

func TestMergeFallbackMatrix(t *testing.T) {
	now := time.Date(2026, 7, 28, 6, 30, 0, 0, nyLoc)
	start := now.Truncate(time.Hour)
	fetched := now.Add(-time.Minute)

	// The expected computed-WBGT value when NWS is down: same inputs the
	// merge hands to wbgt.EstimateF for hour 0.
	estWBGT := wbgt.EstimateF(wbgt.Inputs{
		TempF: 80, DewPointF: 65, WindMPH: 5,
		SolarWm2: 500, DirectWm2: 300, DiffuseWm2: 200,
		Lat: centralPark.Lat, Lon: centralPark.Lon, Time: start,
	})

	tests := []struct {
		name string
		kill func(*merge.Sources)
		want func(*testing.T, merge.Result)
	}{
		{
			name: "all sources up",
			kill: func(s *merge.Sources) {},
			want: func(t *testing.T, r merge.Result) {
				h := r.Hours[0]
				wantMetric(t, "WBGTF", h.WBGTF, 80.6, domain.OriginNWS, false, false)
				wantMetric(t, "TempF", h.TempF, 77, domain.OriginNWS, false, false)
				wantMetric(t, "DewPointF", h.DewPointF, 68, domain.OriginNWS, false, false)
				wantMetric(t, "WindMPH", h.WindMPH, 10, domain.OriginNWS, false, false)
				wantMetric(t, "GustMPH", h.GustMPH, 20, domain.OriginNWS, false, false)
				wantMetric(t, "WindDirDeg", h.WindDirDeg, 170, domain.OriginNWS, false, false)
				wantMetric(t, "RH", h.RH, 72, domain.OriginNWS, false, false)
				wantMetric(t, "PoP", h.PoP, 30, domain.OriginNWS, false, false)
				wantMetric(t, "SkyCover", h.SkyCover, 55, domain.OriginNWS, false, false)
				wantMetric(t, "ApparentF", h.ApparentF, 78.8, domain.OriginNWS, false, false)
				wantMetric(t, "ThunderProb", h.ThunderProb, 10, domain.OriginNWS, false, false)
				wantMetric(t, "UVIndex", h.UVIndex, 6, domain.OriginOpenMeteo, false, false)
				wantMetric(t, "AQI[0]", h.AQI, 66, domain.OriginAirNow, false, false)
				if h.AQIPollutant != "PM2.5" {
					t.Errorf("AQIPollutant = %q, want PM2.5", h.AQIPollutant)
				}
				wantMetric(t, "AQI[1]", r.Hours[1].AQI, 42, domain.OriginOpenMeteo, false, true)
				if r.AlertFeedDown {
					t.Error("AlertFeedDown = true with healthy alert feed")
				}
				if len(r.Alerts) != 1 || r.Alerts[0].Event != "Flood Watch" {
					t.Errorf("Alerts = %+v, want one Flood Watch", r.Alerts)
				}
				// Sun: only the 3 calendar days the 48 h window touches
				// (07-28 .. 07-30), from Open-Meteo.
				if len(r.Sun) != 3 {
					t.Fatalf("Sun = %d days, want 3", len(r.Sun))
				}
				wantRise := time.Date(2026, 7, 28, 5, 48, 0, 0, nyLoc)
				wantSet := time.Date(2026, 7, 28, 20, 15, 0, 0, nyLoc)
				if !r.Sun[0].Sunrise.Equal(wantRise) || !r.Sun[0].Sunset.Equal(wantSet) {
					t.Errorf("Sun[0] = %+v, want sunrise %v sunset %v", r.Sun[0], wantRise, wantSet)
				}
				if !r.Sun[2].Sunrise.Equal(wantRise.AddDate(0, 0, 2)) {
					t.Errorf("Sun[2].Sunrise = %v, want %v", r.Sun[2].Sunrise, wantRise.AddDate(0, 0, 2))
				}
				if r.SunSource.Origin != domain.OriginOpenMeteo {
					t.Errorf("SunSource.Origin = %s, want open-meteo", r.SunSource.Origin)
				}
				// Prose: only the synthetic periods overlapping the 48 h
				// window ("Yesterday", "Beyond", "Far Beyond" filtered out).
				wantProse := []string{"Tonight", "Tuesday", "Tuesday Night", "Wednesday"}
				if len(r.Prose) != len(wantProse) {
					t.Fatalf("Prose = %d periods, want %d", len(r.Prose), len(wantProse))
				}
				for i, name := range wantProse {
					if r.Prose[i].Name != name {
						t.Errorf("Prose[%d].Name = %q, want %q", i, r.Prose[i].Name, name)
					}
				}
				p0 := r.Prose[0]
				if p0.Short != "Chance Showers" || p0.Detailed != "Tonight: a chance of showers." {
					t.Errorf("Prose[0] text = %q / %q", p0.Short, p0.Detailed)
				}
				if !p0.Start.Equal(start) || !p0.End.Equal(start.Add(12*time.Hour)) {
					t.Errorf("Prose[0] span = %v..%v, want %v..%v", p0.Start, p0.End, start, start.Add(12*time.Hour))
				}
				if p0.Source.Origin != domain.OriginNWS || p0.Source.Stale {
					t.Errorf("Prose[0].Source = %+v, want fresh nws", p0.Source)
				}
				for src, st := range r.Freshness {
					if !st.Available || st.Stale {
						t.Errorf("freshness[%s] = %+v, want available and not stale", src, st)
					}
				}
			},
		},
		{
			name: "NWS grid down: WBGT estimated, weather falls to Open-Meteo",
			kill: func(s *merge.Sources) { s.Grid.OK = false },
			want: func(t *testing.T, r merge.Result) {
				h := r.Hours[0]
				wantMetric(t, "WBGTF", h.WBGTF, estWBGT, domain.OriginComputed, true, false)
				wantMetric(t, "TempF", h.TempF, 80, domain.OriginOpenMeteo, false, false)
				wantMetric(t, "DewPointF", h.DewPointF, 65, domain.OriginOpenMeteo, false, false)
				wantMetric(t, "WindMPH", h.WindMPH, 5, domain.OriginOpenMeteo, false, false)
				wantMetric(t, "GustMPH", h.GustMPH, 12, domain.OriginOpenMeteo, false, false)
				wantMetric(t, "WindDirDeg", h.WindDirDeg, 250, domain.OriginOpenMeteo, false, false)
				wantMetric(t, "RH", h.RH, 60, domain.OriginOpenMeteo, false, false)
				wantMetric(t, "PoP", h.PoP, 20, domain.OriginOpenMeteo, false, false)
				wantMetric(t, "SkyCover", h.SkyCover, 40, domain.OriginOpenMeteo, false, false)
				if h.ApparentF.Valid || h.ThunderProb.Valid || h.WindChillF.Valid || h.IceAccumIn.Valid {
					t.Error("NWS-only fields must be invalid when the grid is down")
				}
				if st := r.Freshness[merge.SrcNWSGrid]; st.Available {
					t.Error("freshness[nws-grid].Available = true, want false")
				}
			},
		},
		{
			name: "Open-Meteo weather down: NWS carries weather, UV unavailable",
			kill: func(s *merge.Sources) { s.OMWeather.OK = false },
			want: func(t *testing.T, r merge.Result) {
				h := r.Hours[0]
				wantMetric(t, "WBGTF", h.WBGTF, 80.6, domain.OriginNWS, false, false)
				wantMetric(t, "TempF", h.TempF, 77, domain.OriginNWS, false, false)
				wantMetric(t, "WindDirDeg", h.WindDirDeg, 170, domain.OriginNWS, false, false)
				wantMetric(t, "RH", h.RH, 72, domain.OriginNWS, false, false)
				if h.UVIndex.Valid {
					t.Error("UVIndex valid with Open-Meteo weather down; UV is Open-Meteo only")
				}
				if len(r.Sun) != 0 {
					t.Errorf("Sun = %+v, want empty with Open-Meteo weather down", r.Sun)
				}
				if st := r.Freshness[merge.SrcOMWeather]; st.Available {
					t.Error("freshness[openmeteo-weather].Available = true, want false")
				}
			},
		},
		{
			name: "NWS and Open-Meteo weather both down: weather fields invalid",
			kill: func(s *merge.Sources) { s.Grid.OK = false; s.OMWeather.OK = false },
			want: func(t *testing.T, r merge.Result) {
				h := r.Hours[0]
				for name, m := range map[string]domain.Metric{
					"WBGTF": h.WBGTF, "TempF": h.TempF, "DewPointF": h.DewPointF,
					"WindMPH": h.WindMPH, "WindDirDeg": h.WindDirDeg, "RH": h.RH,
					"UVIndex": h.UVIndex,
				} {
					if m.Valid {
						t.Errorf("%s valid with both weather sources down", name)
					}
				}
				if len(r.Sun) != 0 {
					t.Errorf("Sun = %+v, want empty with both weather sources down", r.Sun)
				}
				// AQI still flows from AirNow + Open-Meteo air.
				wantMetric(t, "AQI[0]", h.AQI, 66, domain.OriginAirNow, false, false)
			},
		},
		{
			name: "AirNow down: current-hour AQI falls to modeled CAMS",
			kill: func(s *merge.Sources) { s.AirNowObs.OK = false },
			want: func(t *testing.T, r merge.Result) {
				h := r.Hours[0]
				wantMetric(t, "AQI[0]", h.AQI, 42, domain.OriginOpenMeteo, false, true)
				if h.AQIPollutant != "" {
					t.Errorf("AQIPollutant = %q, want empty without AirNow", h.AQIPollutant)
				}
				if st := r.Freshness[merge.SrcAirNow]; st.Available {
					t.Error("freshness[airnow].Available = true, want false")
				}
			},
		},
		{
			name: "AirNow returns no records: treated like AirNow down for AQI",
			kill: func(s *merge.Sources) { s.AirNowObs.Data = nil },
			want: func(t *testing.T, r merge.Result) {
				wantMetric(t, "AQI[0]", r.Hours[0].AQI, 42, domain.OriginOpenMeteo, false, true)
				if st := r.Freshness[merge.SrcAirNow]; st.Available {
					t.Error("freshness[airnow].Available = true for empty observation set")
				}
			},
		},
		{
			name: "Open-Meteo air down: current hour stays AirNow, later hours invalid",
			kill: func(s *merge.Sources) { s.OMAir.OK = false },
			want: func(t *testing.T, r merge.Result) {
				wantMetric(t, "AQI[0]", r.Hours[0].AQI, 66, domain.OriginAirNow, false, false)
				if r.Hours[1].AQI.Valid {
					t.Error("AQI[1] valid with Open-Meteo air down")
				}
			},
		},
		{
			name: "both AQI sources down: AQI invalid everywhere",
			kill: func(s *merge.Sources) { s.AirNowObs.OK = false; s.OMAir.OK = false },
			want: func(t *testing.T, r merge.Result) {
				if r.Hours[0].AQI.Valid || r.Hours[1].AQI.Valid {
					t.Error("AQI valid with both AQI sources down")
				}
			},
		},
		{
			name: "forecast feed down: no prose, everything else unaffected",
			kill: func(s *merge.Sources) { s.Forecast.OK = false },
			want: func(t *testing.T, r merge.Result) {
				if len(r.Prose) != 0 {
					t.Errorf("Prose = %+v, want empty with forecast feed down", r.Prose)
				}
				if st := r.Freshness[merge.SrcNWSForecast]; st.Available {
					t.Error("freshness[nws-forecast].Available = true, want false")
				}
				// Numeric fields are untouched by the prose feed.
				wantMetric(t, "WBGTF", r.Hours[0].WBGTF, 80.6, domain.OriginNWS, false, false)
			},
		},
		{
			name: "forecast with zero periods: treated as unavailable",
			kill: func(s *merge.Sources) { s.Forecast.Data = &nws.TextForecast{} },
			want: func(t *testing.T, r merge.Result) {
				if len(r.Prose) != 0 {
					t.Errorf("Prose = %+v, want empty for empty period list", r.Prose)
				}
				if st := r.Freshness[merge.SrcNWSForecast]; st.Available {
					t.Error("freshness[nws-forecast].Available = true for zero periods")
				}
			},
		},
		{
			name: "alert feed down: AlertFeedDown, never a false all-clear",
			kill: func(s *merge.Sources) { s.Alerts.OK = false; s.Alerts.Data = nil },
			want: func(t *testing.T, r merge.Result) {
				if !r.AlertFeedDown {
					t.Error("AlertFeedDown = false with alert feed down")
				}
				if len(r.Alerts) != 0 {
					t.Errorf("Alerts = %+v, want none exposed when feed is down", r.Alerts)
				}
				if st := r.Freshness[merge.SrcNWSAlerts]; st.Available {
					t.Error("freshness[nws-alerts].Available = true, want false")
				}
			},
		},
		{
			name: "everything down: all metrics invalid, alert feed flagged",
			kill: func(s *merge.Sources) {
				s.Grid.OK = false
				s.Forecast.OK = false
				s.Alerts.OK = false
				s.AirNowObs.OK = false
				s.OMWeather.OK = false
				s.OMAir.OK = false
			},
			want: func(t *testing.T, r merge.Result) {
				if len(r.Hours) != merge.Horizon {
					t.Fatalf("len(Hours) = %d, want %d", len(r.Hours), merge.Horizon)
				}
				h := r.Hours[0]
				for name, m := range map[string]domain.Metric{
					"WBGTF": h.WBGTF, "TempF": h.TempF, "UVIndex": h.UVIndex, "AQI": h.AQI,
				} {
					if m.Valid {
						t.Errorf("%s valid with every source down", name)
					}
				}
				if !r.AlertFeedDown {
					t.Error("AlertFeedDown = false with every source down")
				}
				if len(r.Prose) != 0 {
					t.Errorf("Prose = %+v, want empty with every source down", r.Prose)
				}
				for src, st := range r.Freshness {
					if st.Available {
						t.Errorf("freshness[%s].Available = true, want false", src)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := allUp(start, fetched)
			tt.kill(&src)
			r := merge.Merge(centralPark, now, src)
			if len(r.Hours) != merge.Horizon {
				t.Fatalf("len(Hours) = %d, want %d", len(r.Hours), merge.Horizon)
			}
			if !r.Hours[0].Time.Equal(start) {
				t.Fatalf("Hours[0].Time = %v, want %v", r.Hours[0].Time, start)
			}
			for i, h := range r.Hours {
				want := start.Add(time.Duration(i) * time.Hour)
				if !h.Time.Equal(want) {
					t.Fatalf("Hours[%d].Time = %v, want %v", i, h.Time, want)
				}
			}
			tt.want(t, r)
		})
	}
}

func TestMergeStaleTagsPropagate(t *testing.T) {
	now := time.Date(2026, 7, 28, 6, 30, 0, 0, nyLoc)
	start := now.Truncate(time.Hour)
	gridFetched := now.Add(-45 * time.Minute)
	omFetched := now.Add(-2 * time.Hour)

	fcFetched := now.Add(-90 * time.Minute)

	src := allUp(start, now.Add(-time.Minute))
	src.Grid.Stale = true
	src.Grid.FetchedAt = gridFetched
	src.OMWeather.Stale = true
	src.OMWeather.FetchedAt = omFetched
	src.Forecast.Stale = true
	src.Forecast.FetchedAt = fcFetched
	src.Grid.OK = true

	r := merge.Merge(centralPark, now, src)
	h := r.Hours[0]

	// Stale prose is still served, labeled stale — same semantics as every
	// other source past its fresh TTL.
	if len(r.Prose) == 0 {
		t.Fatal("Prose empty; stale forecast must still be served")
	}
	if tag := r.Prose[0].Source; tag.Origin != domain.OriginNWS || !tag.Stale || !tag.FetchedAt.Equal(fcFetched) {
		t.Errorf("Prose[0].Source = %+v, want stale nws fetched %v", tag, fcFetched)
	}
	if st := r.Freshness[merge.SrcNWSForecast]; !st.Available || !st.Stale || !st.FetchedAt.Equal(fcFetched) {
		t.Errorf("freshness[nws-forecast] = %+v, want available, stale, fetched %v", st, fcFetched)
	}

	if tag := h.TempF.Source; !tag.Stale || !tag.FetchedAt.Equal(gridFetched) {
		t.Errorf("TempF tag = %+v, want Stale=true FetchedAt=%v", tag, gridFetched)
	}
	if tag := h.UVIndex.Source; !tag.Stale || !tag.FetchedAt.Equal(omFetched) {
		t.Errorf("UVIndex tag = %+v, want Stale=true FetchedAt=%v", tag, omFetched)
	}
	if tag := h.AQI.Source; tag.Stale {
		t.Errorf("AQI tag stale = true, want false (AirNow was fresh)")
	}
	if st := r.Freshness[merge.SrcNWSGrid]; !st.Available || !st.Stale || !st.FetchedAt.Equal(gridFetched) {
		t.Errorf("freshness[nws-grid] = %+v, want available, stale, fetched %v", st, gridFetched)
	}

	// A stale NWS grid still wins over fresh Open-Meteo: fallback is by
	// availability, not by staleness.
	if h.TempF.Source.Origin != domain.OriginNWS {
		t.Errorf("TempF origin = %s, want nws (stale NWS still outranks Open-Meteo)", h.TempF.Source.Origin)
	}

	// Estimated WBGT inherits the freshness of its Open-Meteo inputs.
	src.Grid.OK = false
	r = merge.Merge(centralPark, now, src)
	tag := r.Hours[0].WBGTF.Source
	if tag.Origin != domain.OriginComputed || !tag.Estimated || !tag.Stale || !tag.FetchedAt.Equal(omFetched) {
		t.Errorf("estimated WBGT tag = %+v, want computed, Estimated, Stale, FetchedAt=%v", tag, omFetched)
	}
}

func TestInAdaptsCacheValue(t *testing.T) {
	fetched := time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)
	v := cache.Value[string]{Data: "x", FetchedAt: fetched, Stale: true}
	in := merge.In(v, true)
	if in.Data != "x" || !in.FetchedAt.Equal(fetched) || !in.Stale || !in.OK {
		t.Errorf("In(%+v, true) = %+v", v, in)
	}
	in = merge.In(cache.Value[string]{}, false)
	if in.OK {
		t.Error("In(zero, false).OK = true")
	}
}

// --- fixture-driven test: merge over recorded upstream testdata ---

// loadFixtures replays recorded upstream responses through each client
// package (via httptest upstreams) and assembles merge.Sources exactly as
// the app would. The fixtures in testdata/ are this package's own copies
// of the recordings (originally captured for the client packages), so
// merge stays decoupled from other packages' testdata directories.
func loadFixtures(t *testing.T, fetched time.Time) merge.Sources {
	t.Helper()
	ctx := context.Background()

	// NWS gridpoint: the raw JSON decodes directly into the exported type.
	var gp nws.Gridpoint
	mustDecode(t, "testdata/gridpoint_okx_34_45.json", &gp)

	// NWS alerts through the nws client.
	alertsSrv := fixtureServer(t, "testdata/alerts_nyz072.json")
	nwsClient := &nws.Client{BaseURL: alertsSrv.URL, RetryWait: time.Millisecond}
	alerts, err := nwsClient.ActiveAlerts(ctx, "NYZ072")
	if err != nil {
		t.Fatalf("ActiveAlerts over fixture: %v", err)
	}

	// NWS narrative forecast through the nws client.
	fcSrv := fixtureServer(t, "testdata/forecast_okx_34_45.json")
	fcClient := &nws.Client{BaseURL: fcSrv.URL, RetryWait: time.Millisecond}
	fc, err := fcClient.Forecast(ctx, "OKX", 34, 45)
	if err != nil {
		t.Fatalf("Forecast over fixture: %v", err)
	}

	// Open-Meteo weather and air quality through the openmeteo client.
	weatherSrv := fixtureServer(t, "testdata/weather_nyc.json")
	airSrv := fixtureServer(t, "testdata/airquality_nyc.json")
	om := openmeteo.New(openmeteo.Config{
		WeatherURL:    weatherSrv.URL,
		AirQualityURL: airSrv.URL,
		Timezone:      "America/New_York",
		ForecastDays:  3,
	}, nil)
	omWeather, err := om.Weather(ctx, centralPark.Lat, centralPark.Lon)
	if err != nil {
		t.Fatalf("openmeteo Weather over fixture: %v", err)
	}
	omAir, err := om.AirQuality(ctx, centralPark.Lat, centralPark.Lon)
	if err != nil {
		t.Fatalf("openmeteo AirQuality over fixture: %v", err)
	}

	// AirNow observations through the airnow client (dummy test key only).
	airnowSrv := fixtureServer(t, "testdata/observation_nyc.json")
	cfg := airnow.DefaultConfig()
	cfg.BaseURL = airnowSrv.URL
	an := airnow.New(cfg, "test-key-not-real", nil)
	obs, err := an.Observation(ctx, centralPark.Lat, centralPark.Lon)
	if err != nil {
		t.Fatalf("airnow Observation over fixture: %v", err)
	}

	return merge.Sources{
		Grid:      okIn(&gp, fetched),
		Forecast:  okIn(fc, fetched),
		Alerts:    okIn(alerts, fetched),
		AirNowObs: okIn(obs, fetched),
		OMWeather: okIn(omWeather, fetched),
		OMAir:     okIn(omAir, fetched),
	}
}

func fixtureServer(t *testing.T, path string) *httptest.Server {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func mustDecode(t *testing.T, path string, out any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	if err := json.Unmarshal(body, out); err != nil {
		t.Fatalf("decode fixture %s: %v", path, err)
	}
}

func TestMergeOverRecordedFixtures(t *testing.T) {
	// The fixtures were recorded 2026-07-27 evening ET; pick a "now" inside
	// every fixture's coverage window (Open-Meteo fixtures span 07-27 00:00
	// through 07-29 23:00 ET; the NWS grid spans 07-27 12:00Z onward).
	now := time.Date(2026, 7, 27, 20, 30, 0, 0, nyLoc)
	fetched := now.Add(-2 * time.Minute)
	src := loadFixtures(t, fetched)

	r := merge.Merge(centralPark, now, src)

	if len(r.Hours) != merge.Horizon {
		t.Fatalf("len(Hours) = %d, want %d", len(r.Hours), merge.Horizon)
	}
	wantStart := time.Date(2026, 7, 27, 20, 0, 0, 0, nyLoc)
	if !r.Hours[0].Time.Equal(wantStart) {
		t.Fatalf("Hours[0].Time = %v, want %v", r.Hours[0].Time, wantStart)
	}

	// Every source healthy per /healthz.
	for _, src := range []string{merge.SrcNWSGrid, merge.SrcNWSForecast, merge.SrcNWSAlerts, merge.SrcAirNow, merge.SrcOMWeather, merge.SrcOMAir} {
		st, ok := r.Freshness[src]
		if !ok || !st.Available || st.Stale || !st.FetchedAt.Equal(fetched) {
			t.Errorf("freshness[%s] = %+v (present=%v), want available, fresh, fetched %v", src, st, ok, fetched)
		}
	}

	// WBGT comes from the NWS grid layer (never estimated when NWS is up).
	h0 := r.Hours[0]
	if !h0.WBGTF.Valid || h0.WBGTF.Source.Origin != domain.OriginNWS || h0.WBGTF.Source.Estimated {
		t.Errorf("WBGTF = %+v, want valid from nws, not estimated", h0.WBGTF)
	}
	if h0.WBGTF.Value < 40 || h0.WBGTF.Value > 120 {
		t.Errorf("WBGTF = %v °F, implausible for the July fixture", h0.WBGTF.Value)
	}
	for _, m := range []struct {
		name string
		m    domain.Metric
	}{
		{"TempF", h0.TempF}, {"DewPointF", h0.DewPointF}, {"WindMPH", h0.WindMPH},
		{"PoP", h0.PoP}, {"SkyCover", h0.SkyCover}, {"ApparentF", h0.ApparentF},
	} {
		if !m.m.Valid || m.m.Source.Origin != domain.OriginNWS {
			t.Errorf("%s = %+v, want valid from nws", m.name, m.m)
		}
	}

	// Wind direction and RH from the NWS grid (hand-computed from the
	// fixture's RLE runs at 2026-07-28T00:00Z): from the south at 170°, 67%.
	wantMetric(t, "WindDirDeg", h0.WindDirDeg, 170, domain.OriginNWS, false, false)
	wantMetric(t, "RH", h0.RH, 67, domain.OriginNWS, false, false)
	if got := domain.CompassPoint(h0.WindDirDeg.Value); got != "S" {
		t.Errorf("CompassPoint(%v) = %q, want S", h0.WindDirDeg.Value, got)
	}

	// UV from Open-Meteo only.
	if !h0.UVIndex.Valid || h0.UVIndex.Source.Origin != domain.OriginOpenMeteo {
		t.Errorf("UVIndex = %+v, want valid from open-meteo", h0.UVIndex)
	}

	// Sunrise/sunset from the Open-Meteo daily table: the window (07-27
	// 20:00 ET + 48 h) touches all three recorded days.
	if len(r.Sun) != 3 {
		t.Fatalf("Sun = %d days, want 3", len(r.Sun))
	}
	wantSun := []merge.SunTimes{
		{Sunrise: time.Date(2026, 7, 27, 5, 47, 0, 0, nyLoc), Sunset: time.Date(2026, 7, 27, 20, 16, 0, 0, nyLoc)},
		{Sunrise: time.Date(2026, 7, 28, 5, 48, 0, 0, nyLoc), Sunset: time.Date(2026, 7, 28, 20, 15, 0, 0, nyLoc)},
		{Sunrise: time.Date(2026, 7, 29, 5, 49, 0, 0, nyLoc), Sunset: time.Date(2026, 7, 29, 20, 14, 0, 0, nyLoc)},
	}
	for i, want := range wantSun {
		if !r.Sun[i].Sunrise.Equal(want.Sunrise) || !r.Sun[i].Sunset.Equal(want.Sunset) {
			t.Errorf("Sun[%d] = %+v, want %+v", i, r.Sun[i], want)
		}
	}
	if r.SunSource.Origin != domain.OriginOpenMeteo || r.SunSource.Stale {
		t.Errorf("SunSource = %+v, want fresh open-meteo", r.SunSource)
	}

	// Current-hour AQI is the AirNow max-pollutant monitor reading
	// (fixture: PM2.5 66 beats OZONE 64), not Modeled.
	if !h0.AQI.Valid || h0.AQI.Value != 66 || h0.AQI.Source.Origin != domain.OriginAirNow || h0.AQI.Source.Modeled {
		t.Errorf("AQI[0] = %+v, want 66 from airnow, monitored", h0.AQI)
	}
	if h0.AQIPollutant != "PM2.5" {
		t.Errorf("AQIPollutant = %q, want PM2.5", h0.AQIPollutant)
	}

	// Later hours: modeled CAMS AQI from Open-Meteo.
	h1 := r.Hours[1]
	if !h1.AQI.Valid || h1.AQI.Source.Origin != domain.OriginOpenMeteo || !h1.AQI.Source.Modeled {
		t.Errorf("AQI[1] = %+v, want valid from open-meteo, Modeled", h1.AQI)
	}

	// Narrative prose: the recorded forecast has 6 periods starting
	// "Tonight" (07-27 20:00 ET); the 48 h window (ending 07-29 20:00 ET)
	// overlaps the first 5 — "Thursday" (starts 07-30 06:00) is filtered.
	wantProse := []string{"Tonight", "Tuesday", "Tuesday Night", "Wednesday", "Wednesday Night"}
	if len(r.Prose) != len(wantProse) {
		t.Fatalf("Prose = %d periods, want %d", len(r.Prose), len(wantProse))
	}
	for i, name := range wantProse {
		if r.Prose[i].Name != name {
			t.Errorf("Prose[%d].Name = %q, want %q", i, r.Prose[i].Name, name)
		}
		if r.Prose[i].Short == "" || r.Prose[i].Detailed == "" {
			t.Errorf("Prose[%d] (%s): empty prose text", i, name)
		}
		if tag := r.Prose[i].Source; tag.Origin != domain.OriginNWS || tag.Stale {
			t.Errorf("Prose[%d].Source = %+v, want fresh nws", i, tag)
		}
	}
	if got := r.Prose[0].Short; got != "Partly Cloudy then Chance Showers And Thunderstorms" {
		t.Errorf("Prose[0].Short = %q", got)
	}
	if !r.Prose[0].Start.Equal(wantStart) {
		t.Errorf("Prose[0].Start = %v, want %v", r.Prose[0].Start, wantStart)
	}

	// The recorded alert fixture carries an active Flood Watch.
	if r.AlertFeedDown {
		t.Error("AlertFeedDown = true with a healthy fixture feed")
	}
	if len(r.Alerts) != 1 || r.Alerts[0].Event != "Flood Watch" {
		t.Fatalf("Alerts = %+v, want exactly the recorded Flood Watch", r.Alerts)
	}
	if !r.Alerts[0].ActiveAt(time.Date(2026, 7, 28, 12, 0, 0, 0, nyLoc)) {
		t.Error("recorded Flood Watch should be active midday 07-28")
	}

	// The 48 h window must be materially covered: every hour has a valid
	// temperature and WBGT from some source.
	for i, h := range r.Hours {
		if !h.TempF.Valid {
			t.Errorf("Hours[%d] (%v): TempF invalid over full fixtures", i, h.Time)
		}
		if !h.WBGTF.Valid {
			t.Errorf("Hours[%d] (%v): WBGTF invalid over full fixtures", i, h.Time)
		}
		if !h.WindDirDeg.Valid || !h.RH.Valid {
			t.Errorf("Hours[%d] (%v): wind dir/RH invalid over full fixtures (dir=%v rh=%v)",
				i, h.Time, h.WindDirDeg.Valid, h.RH.Valid)
		}
	}
}

// TestMergeFixturesNWSDownEstimatesWBGT kills only NWS over the recorded
// fixtures: WBGT must switch to the Kong–Huber estimate driven by the
// recorded Open-Meteo radiation inputs, tagged Estimated.
func TestMergeFixturesNWSDownEstimatesWBGT(t *testing.T) {
	now := time.Date(2026, 7, 27, 20, 30, 0, 0, nyLoc)
	src := loadFixtures(t, now.Add(-2*time.Minute))
	src.Grid.OK = false

	r := merge.Merge(centralPark, now, src)
	for i, h := range r.Hours {
		if !h.WBGTF.Valid {
			t.Fatalf("Hours[%d] (%v): WBGT invalid; estimate should cover the window", i, h.Time)
		}
		tag := h.WBGTF.Source
		if tag.Origin != domain.OriginComputed || !tag.Estimated {
			t.Fatalf("Hours[%d]: WBGT tag = %+v, want computed+Estimated", i, tag)
		}
		if h.WBGTF.Value < 30 || h.WBGTF.Value > 120 {
			t.Errorf("Hours[%d]: estimated WBGT = %v °F, implausible", i, h.WBGTF.Value)
		}
		if !h.TempF.Valid || h.TempF.Source.Origin != domain.OriginOpenMeteo {
			t.Errorf("Hours[%d]: TempF = %+v, want open-meteo", i, h.TempF)
		}
	}
}
