// Package merge is THE single source of truth for field→source fallback.
// It combines the cached payloads of every upstream into []domain.Hour for
// one path over the next 48 hours, plus active alerts and per-source
// freshness for /healthz.
//
// Priority per PLAN.md:
//
//	WBGT              NWS wetBulbGlobeTemperature → wbgt.EstimateF from
//	                  Open-Meteo inputs (tagged Estimated) → invalid
//	temp/dew/wind/gust/wind dir/RH/PoP/sky  NWS → Open-Meteo
//	apparent/wind chill/thunder prob/ice  NWS only (Open-Meteo has no equivalent)
//	UV                Open-Meteo only
//	sunrise/sunset    Open-Meteo daily table only
//	AQI current hour  AirNow max-pollutant (monitored) → Open-Meteo us_aqi (Modeled)
//	AQI other hours   Open-Meteo us_aqi, tagged Modeled
//	alerts            NWS only; a stale-expired alert feed sets AlertFeedDown
//	                  so the UI shows "alert feed unavailable", never a false
//	                  all-clear.
package merge

import (
	"time"

	"github.com/kuitang/whentorun/internal/airnow"
	"github.com/kuitang/whentorun/internal/cache"
	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/nws"
	"github.com/kuitang/whentorun/internal/openmeteo"
	"github.com/kuitang/whentorun/internal/wbgt"
)

// Horizon is the forecast window in hours.
const Horizon = 48

// Freshness map keys, one per upstream feed.
const (
	SrcNWSGrid     = "nws-grid"
	SrcNWSForecast = "nws-forecast"
	SrcNWSAlerts   = "nws-alerts"
	SrcAirNow      = "airnow"
	SrcOMWeather   = "openmeteo-weather"
	SrcOMAir       = "openmeteo-air"
)

// Input is one source's cached payload plus availability. OK=false means
// the source is unavailable: never fetched, or aged past its serve-stale
// TTL (the cache reports !ok and the caller passes OK=false).
type Input[T any] struct {
	Data      T
	FetchedAt time.Time
	Stale     bool
	OK        bool
}

// In adapts a cache read to an Input.
func In[T any](v cache.Value[T], ok bool) Input[T] {
	return Input[T]{Data: v.Data, FetchedAt: v.FetchedAt, Stale: v.Stale, OK: ok}
}

// Sources is every upstream payload merge needs for one path.
//
// Forecast (the narrative text forecast) has no cache-refresher wiring site
// yet; when the server phase registers it, use TTL 1 h fresh / 6 h
// serve-stale (the prose updates a few times a day, far slower than the
// grid).
type Sources struct {
	Grid      Input[*nws.Gridpoint]             // NWS gridpoint for the path's cell
	Forecast  Input[*nws.TextForecast]          // NWS narrative forecast for the path's cell
	Alerts    Input[[]domain.Alert]             // NWS active alerts for the path's zone
	AirNowObs Input[[]airnow.ObservationRecord] // AirNow current observations (city-wide)
	OMWeather Input[openmeteo.WeatherData]      // Open-Meteo weather for the path
	OMAir     Input[[]openmeteo.AirQualityHour] // Open-Meteo hourly us_aqi (CAMS)
}

// SourceStatus is one feed's freshness for /healthz.
type SourceStatus struct {
	Available bool      `json:"available"`
	Stale     bool      `json:"stale"`
	FetchedAt time.Time `json:"fetched_at"`
}

// SunTimes is one calendar day's sunrise and sunset (America/New_York).
type SunTimes struct {
	Sunrise time.Time
	Sunset  time.Time
}

// Prose is one official NWS narrative forecast period, provenance-tagged
// like every other merged value (Stale follows the feed's TTL state).
type Prose struct {
	Name     string // e.g. "Tonight", "Tuesday"
	Short    string // shortForecast
	Detailed string // detailedForecast
	Start    time.Time
	End      time.Time
	Source   domain.SourceTag
}

// Result is the merged view for one path.
type Result struct {
	Hours  []domain.Hour  // Horizon hours starting at now truncated to the hour
	Alerts []domain.Alert // active NWS alerts; meaningless when AlertFeedDown
	// AlertFeedDown is true when the alert feed is unavailable. The UI must
	// then show "alert feed unavailable" — never an implicit all-clear.
	AlertFeedDown bool
	// Sun holds sunrise/sunset for each calendar day the Horizon window
	// touches, in order (Open-Meteo daily table; empty when unavailable).
	Sun       []SunTimes
	SunSource domain.SourceTag // provenance for Sun
	// Prose holds the official NWS narrative periods that overlap the
	// Horizon window, in order (empty when the forecast feed is
	// unavailable).
	Prose     []Prose
	Freshness map[string]SourceStatus // keyed by the Src* constants
}

// nyLoc is the display timezone; falls back to UTC if tzdata is missing.
var nyLoc = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UTC
	}
	return loc
}()

// Merge produces the merged 48 h view for path as of now.
func Merge(path domain.Path, now time.Time, src Sources) Result {
	start := now.In(nyLoc).Truncate(time.Hour)

	res := Result{
		Hours:     make([]domain.Hour, Horizon),
		Freshness: map[string]SourceStatus{},
	}

	// NWS gridpoint: expand RLE layers to per-hour values aligned to start.
	var nwsHours []nws.HourValues
	gridOK := src.Grid.OK && src.Grid.Data != nil
	if gridOK {
		f, err := src.Grid.Data.Expand(start, Horizon)
		if err != nil {
			gridOK = false
		} else {
			nwsHours = f.Hours
		}
	}
	res.Freshness[SrcNWSGrid] = status(src.Grid, gridOK)

	// NWS narrative forecast: keep the periods that overlap the Horizon
	// window. Like AirNow, the feed counts as available only when it yields
	// usable prose — a payload with zero periods cannot serve any.
	fcOK := src.Forecast.OK && src.Forecast.Data != nil && len(src.Forecast.Data.Properties.Periods) > 0
	if fcOK {
		fcTag := tag(domain.OriginNWS, src.Forecast)
		end := start.Add(Horizon * time.Hour)
		for _, p := range src.Forecast.Data.Properties.Periods {
			if p.End.After(start) && p.Start.Before(end) {
				res.Prose = append(res.Prose, Prose{
					Name: p.Name, Short: p.Short, Detailed: p.Detailed,
					Start: p.Start, End: p.End, Source: fcTag,
				})
			}
		}
	}
	res.Freshness[SrcNWSForecast] = status(src.Forecast, fcOK)

	omwOK := src.OMWeather.OK
	omWeather := indexWeather(src.OMWeather.Data.Hours)
	res.Freshness[SrcOMWeather] = status(src.OMWeather, omwOK)

	omaOK := src.OMAir.OK
	omAir := indexAir(src.OMAir.Data)
	res.Freshness[SrcOMAir] = status(src.OMAir, omaOK)

	// AirNow counts as available only when it yields a usable reading: a
	// reachable feed with zero records cannot serve an AQI.
	anAQI, anPollutant, _, anHas := airnow.DisplayAQI(src.AirNowObs.Data)
	airnowUsable := src.AirNowObs.OK && anHas
	res.Freshness[SrcAirNow] = status(src.AirNowObs, airnowUsable)

	nwsTag := tag(domain.OriginNWS, src.Grid)
	omwTag := tag(domain.OriginOpenMeteo, src.OMWeather)
	omaTag := tag(domain.OriginOpenMeteo, src.OMAir)
	omaTag.Modeled = true // CAMS model output, not a monitor reading
	anTag := tag(domain.OriginAirNow, src.AirNowObs)
	estTag := tag(domain.OriginComputed, src.OMWeather) // estimate inherits its inputs' freshness
	estTag.Estimated = true

	for i := range res.Hours {
		t := start.Add(time.Duration(i) * time.Hour)
		h := &res.Hours[i]
		h.Time = t

		var nh nws.HourValues
		if gridOK {
			nh = nwsHours[i]
			h.WxThunder = nh.WxThunder
			h.WxFreezingRain = nh.WxFreezingRain
		}
		var ow openmeteo.WeatherHour
		var owFound bool
		if omwOK {
			ow, owFound = omWeather[t.Unix()]
		}

		// NWS → Open-Meteo fields.
		h.TempF = pick(gridOK, nh.TempF, nwsTag, owFound, ow.TempF, omwTag)
		h.DewPointF = pick(gridOK, nh.DewPointF, nwsTag, owFound, ow.DewPointF, omwTag)
		h.WindMPH = pick(gridOK, nh.WindMPH, nwsTag, owFound, ow.WindMPH, omwTag)
		h.GustMPH = pick(gridOK, nh.GustMPH, nwsTag, owFound, ow.GustMPH, omwTag)
		h.WindDirDeg = pick(gridOK, nh.WindDirDeg, nwsTag, owFound, ow.WindDirDeg, omwTag)
		h.RH = pick(gridOK, nh.RH, nwsTag, owFound, ow.RelHumidity, omwTag)
		h.PoP = pick(gridOK, nh.PoP, nwsTag, owFound, ow.PoP, omwTag)
		h.SkyCover = pick(gridOK, nh.SkyCover, nwsTag, owFound, ow.CloudCoverPct, omwTag)

		// NWS-only fields (no Open-Meteo equivalent).
		h.ApparentF = pick(gridOK, nh.ApparentF, nwsTag, false, nil, omwTag)
		h.WindChillF = pick(gridOK, nh.WindChillF, nwsTag, false, nil, omwTag)
		h.ThunderProb = pick(gridOK, nh.ThunderProb, nwsTag, false, nil, omwTag)
		h.IceAccumIn = pick(gridOK, nh.IceAccumIn, nwsTag, false, nil, omwTag)

		// WBGT: NWS grid layer → Kong–Huber estimate from Open-Meteo inputs.
		switch {
		case gridOK && nh.WBGTF.Valid:
			h.WBGTF = domain.Val(nh.WBGTF.Value, nwsTag)
		case owFound && ow.TempF != nil && ow.DewPointF != nil && ow.WindMPH != nil && ow.ShortwaveWm2 != nil:
			est := wbgt.EstimateF(wbgt.Inputs{
				TempF:      *ow.TempF,
				DewPointF:  *ow.DewPointF,
				WindMPH:    *ow.WindMPH,
				SolarWm2:   *ow.ShortwaveWm2,
				DirectWm2:  deref(ow.DirectWm2),
				DiffuseWm2: deref(ow.DiffuseWm2),
				Lat:        path.Lat,
				Lon:        path.Lon,
				Time:       t,
			})
			h.WBGTF = domain.Val(est, estTag)
		}

		// UV: Open-Meteo only.
		if owFound && ow.UVIndex != nil {
			h.UVIndex = domain.Val(*ow.UVIndex, omwTag)
		}

		// AQI: current hour prefers the AirNow monitor reading; every other
		// hour (and the AirNow-down case) uses modeled CAMS us_aqi.
		if i == 0 && airnowUsable {
			h.AQI = domain.Val(float64(anAQI), anTag)
			h.AQIPollutant = anPollutant
		} else if omaOK {
			if aq, found := omAir[t.Unix()]; found && aq.AQI != nil {
				h.AQI = domain.Val(*aq.AQI, omaTag)
			}
		}
	}

	// Sunrise/sunset: Open-Meteo daily table, filtered to the calendar days
	// the Horizon window touches.
	if omwOK {
		end := start.Add(Horizon * time.Hour)
		for _, d := range src.OMWeather.Data.Days {
			dayStart := d.Date
			if dayStart.Before(end) && start.Before(dayStart.Add(24*time.Hour)) {
				res.Sun = append(res.Sun, SunTimes{Sunrise: d.Sunrise, Sunset: d.Sunset})
			}
		}
		res.SunSource = omwTag
	}

	// Alerts: NWS only. A down feed must surface as down, never as "no alerts".
	if src.Alerts.OK {
		res.Alerts = src.Alerts.Data
	} else {
		res.AlertFeedDown = true
	}
	res.Freshness[SrcNWSAlerts] = status(src.Alerts, src.Alerts.OK)

	return res
}

// pick implements the NWS → Open-Meteo fallback for one numeric field.
func pick(nwsOK bool, nv nws.Opt, nwsTag domain.SourceTag, omOK bool, ov *float64, omTag domain.SourceTag) domain.Metric {
	if nwsOK && nv.Valid {
		return domain.Val(nv.Value, nwsTag)
	}
	if omOK && ov != nil {
		return domain.Val(*ov, omTag)
	}
	return domain.Metric{}
}

func tag[T any](origin domain.Origin, in Input[T]) domain.SourceTag {
	return domain.SourceTag{Origin: origin, FetchedAt: in.FetchedAt, Stale: in.Stale}
}

func status[T any](in Input[T], usable bool) SourceStatus {
	return SourceStatus{Available: usable, Stale: usable && in.Stale, FetchedAt: in.FetchedAt}
}

func indexWeather(hours []openmeteo.WeatherHour) map[int64]openmeteo.WeatherHour {
	m := make(map[int64]openmeteo.WeatherHour, len(hours))
	for _, h := range hours {
		m[h.Time.Unix()] = h
	}
	return m
}

func indexAir(hours []openmeteo.AirQualityHour) map[int64]openmeteo.AirQualityHour {
	m := make(map[int64]openmeteo.AirQualityHour, len(hours))
	for _, h := range hours {
		m[h.Time.Unix()] = h
	}
	return m
}

func deref(p *float64) float64 {
	if p == nil {
		return 0
	}
	return *p
}
