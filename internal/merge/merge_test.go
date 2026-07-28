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
	p.WindSpeed = layer("wmoUnit:km_h-1", 16.09344) // 10 mph
	p.WindGust = layer("wmoUnit:km_h-1", 32.18688)  // 20 mph
	p.PoP = layer("wmoUnit:percent", 30)
	p.SkyCover = layer("wmoUnit:percent", 55)
	p.ProbabilityOfThunder = layer("wmoUnit:percent", 10)
	return gp
}

func syntheticWeather(start time.Time, n int) []openmeteo.WeatherHour {
	hours := make([]openmeteo.WeatherHour, n)
	for i := range hours {
		hours[i] = openmeteo.WeatherHour{
			Time:          start.Add(time.Duration(i) * time.Hour),
			TempF:         f(80),
			RelHumidity:   f(60),
			DewPointF:     f(65),
			WindMPH:       f(5),
			GustMPH:       f(12),
			UVIndex:       f(6),
			ShortwaveWm2:  f(500),
			DirectWm2:     f(300),
			DiffuseWm2:    f(200),
			PoP:           f(20),
			CloudCoverPct: f(40),
		}
	}
	return hours
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
				if h.UVIndex.Valid {
					t.Error("UVIndex valid with Open-Meteo weather down; UV is Open-Meteo only")
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
					"WindMPH": h.WindMPH, "UVIndex": h.UVIndex,
				} {
					if m.Valid {
						t.Errorf("%s valid with both weather sources down", name)
					}
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

	src := allUp(start, now.Add(-time.Minute))
	src.Grid.Stale = true
	src.Grid.FetchedAt = gridFetched
	src.OMWeather.Stale = true
	src.OMWeather.FetchedAt = omFetched
	src.Grid.OK = true

	r := merge.Merge(centralPark, now, src)
	h := r.Hours[0]

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
	for _, src := range []string{merge.SrcNWSGrid, merge.SrcNWSAlerts, merge.SrcAirNow, merge.SrcOMWeather, merge.SrcOMAir} {
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

	// UV from Open-Meteo only.
	if !h0.UVIndex.Valid || h0.UVIndex.Source.Origin != domain.OriginOpenMeteo {
		t.Errorf("UVIndex = %+v, want valid from open-meteo", h0.UVIndex)
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
