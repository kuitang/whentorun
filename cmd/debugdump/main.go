// Command debugdump fetches LIVE data for Central Park through the real
// upstream clients and the merge layer, then prints a readable 48 h merged
// table plus active alerts and per-source freshness. It is a verification
// tool, not part of the served app.
//
// Usage:
//
//	AIRNOW_API_KEY=… go run ./cmd/debugdump
//
// The AirNow key is read from the environment only and never printed; the
// airnow client scrubs it from any error text.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kuitang/whentorun/internal/airnow"
	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
	"github.com/kuitang/whentorun/internal/nws"
	"github.com/kuitang/whentorun/internal/openmeteo"
	"github.com/kuitang/whentorun/internal/rank"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "debugdump:", err)
		os.Exit(1)
	}
}

func run() error {
	path := domain.PathBySlug("central-park")
	if path == nil {
		return fmt.Errorf("central-park path not found")
	}

	apiKey := os.Getenv("AIRNOW_API_KEY")
	if apiKey == "" {
		fmt.Fprintln(os.Stderr, "warning: AIRNOW_API_KEY not set; AirNow layer will be unavailable")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	nwsClient := &nws.Client{}
	anClient := airnow.New(airnow.DefaultConfig(), apiKey, nil)
	omClient := openmeteo.New(openmeteo.DefaultConfig(), nil)

	now := time.Now()

	// Fetch every source; a failed source becomes OK=false so the merge
	// layer's fallback behavior is exercised exactly as in production.
	grid, gridErr := nwsClient.Gridpoint(ctx, path.GridID, path.GridX, path.GridY)
	alerts, alertsErr := nwsClient.ActiveAlerts(ctx, path.AlertZone)
	var obs []airnow.ObservationRecord
	obsErr := fmt.Errorf("skipped: no API key")
	if apiKey != "" {
		obs, obsErr = anClient.Observation(ctx, path.Lat, path.Lon)
	}
	omw, omwErr := omClient.Weather(ctx, path.Lat, path.Lon)
	oma, omaErr := omClient.AirQuality(ctx, path.Lat, path.Lon)

	for _, s := range []struct {
		name string
		err  error
	}{
		{"nws-grid", gridErr}, {"nws-alerts", alertsErr}, {"airnow", obsErr},
		{"openmeteo-weather", omwErr}, {"openmeteo-air", omaErr},
	} {
		if s.err != nil {
			fmt.Fprintf(os.Stderr, "fetch %s: %v\n", s.name, s.err)
		}
	}

	fetched := time.Now()
	src := merge.Sources{
		Grid:      merge.Input[*nws.Gridpoint]{Data: grid, FetchedAt: fetched, OK: gridErr == nil},
		Alerts:    merge.Input[[]domain.Alert]{Data: alerts, FetchedAt: fetched, OK: alertsErr == nil},
		AirNowObs: merge.Input[[]airnow.ObservationRecord]{Data: obs, FetchedAt: fetched, OK: obsErr == nil},
		OMWeather: merge.Input[[]openmeteo.WeatherHour]{Data: omw, FetchedAt: fetched, OK: omwErr == nil},
		OMAir:     merge.Input[[]openmeteo.AirQualityHour]{Data: oma, FetchedAt: fetched, OK: omaErr == nil},
	}

	res := merge.Merge(*path, now, src)
	print48h(*path, now, res)
	return nil
}

// nyLoc keeps every printed time in the display timezone regardless of the
// machine's local zone.
var nyLoc = func() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UTC
	}
	return loc
}()

func print48h(path domain.Path, now time.Time, res merge.Result) {
	fmt.Printf("whentorun debug dump — %s (%.4f, %.4f) grid %s/%d,%d zone %s\n",
		path.Name, path.Lat, path.Lon, path.GridID, path.GridX, path.GridY, path.AlertZone)
	fmt.Printf("generated %s (all times America/New_York)\n\n", now.In(nyLoc).Format("2006-01-02 15:04:05 MST"))

	fmt.Println("PER-SOURCE FRESHNESS")
	for _, key := range []string{merge.SrcNWSGrid, merge.SrcNWSAlerts, merge.SrcAirNow, merge.SrcOMWeather, merge.SrcOMAir} {
		s := res.Freshness[key]
		state := "available"
		if !s.Available {
			state = "UNAVAILABLE"
		} else if s.Stale {
			state = "available (stale)"
		}
		fmt.Printf("  %-18s %-18s fetched %s\n", key, state, s.FetchedAt.In(nyLoc).Format("15:04:05 MST"))
	}
	fmt.Println()

	fmt.Println("ACTIVE ALERTS")
	switch {
	case res.AlertFeedDown:
		fmt.Println("  ALERT FEED UNAVAILABLE — cannot confirm all-clear")
	case len(res.Alerts) == 0:
		fmt.Println("  none")
	default:
		for _, a := range res.Alerts {
			fmt.Printf("  [%s] %s\n    %s\n    onset %s  ends %s\n",
				a.Severity, a.Event, a.Headline,
				fmtTime(a.Onset), fmtTime(a.Ends))
		}
	}
	fmt.Println()

	fmt.Println("48-HOUR MERGED TABLE")
	fmt.Printf("  %-15s %-11s %6s %6s %5s %-12s %10s %5s %7s  %s\n",
		"hour", "wbgt", "temp", "dew", "uv", "aqi", "wind", "pop", "thunder", "vetoes")
	fmt.Println("  " + strings.Repeat("-", 108))
	for _, h := range res.Hours {
		wind := "—"
		if h.WindMPH.Valid {
			wind = fmt.Sprintf("%.0f", h.WindMPH.Value)
			if h.GustMPH.Valid && h.GustMPH.Value > h.WindMPH.Value {
				wind += fmt.Sprintf("g%.0f", h.GustMPH.Value)
			}
			wind += "mph"
		}
		thunder := "—"
		if h.ThunderProb.Valid {
			thunder = fmt.Sprintf("%.0f%%", h.ThunderProb.Value)
		}
		if h.WxThunder {
			thunder += "*"
		}
		aqi := "—"
		if h.AQI.Valid {
			aqi = fmt.Sprintf("%.0f/%s", h.AQI.Value, srcLabel(h.AQI.Source))
			if h.AQIPollutant != "" {
				aqi += "(" + h.AQIPollutant + ")"
			}
		}
		vetoes := strings.Join(rank.HourVetoes(h, res.Alerts), "; ")
		fmt.Printf("  %-15s %-11s %6s %6s %5s %-12s %10s %5s %7s  %s\n",
			h.Time.Format("Mon 01-02 15:04"),
			metricWithSrc(h.WBGTF, "°F"),
			metric(h.TempF, "°F"),
			metric(h.DewPointF, "°F"),
			metric(h.UVIndex, ""),
			aqi,
			wind,
			metric(h.PoP, "%"),
			thunder,
			vetoes)
	}
	fmt.Println()
	fmt.Println("legend: value/src — src: nws=NWS grid, est=computed Kong–Huber (estimated),")
	fmt.Println("        an=AirNow monitor, om=Open-Meteo, cams=Open-Meteo modeled CAMS AQI")
	fmt.Println("        thunder trailing * = thunderstorms in NWS weather layer")
}

func metric(m domain.Metric, unit string) string {
	if !m.Valid {
		return "—"
	}
	return fmt.Sprintf("%.0f%s", m.Value, unit)
}

func metricWithSrc(m domain.Metric, unit string) string {
	if !m.Valid {
		return "—"
	}
	return fmt.Sprintf("%.0f%s/%s", m.Value, unit, srcLabel(m.Source))
}

// srcLabel is a compact provenance tag for the table.
func srcLabel(s domain.SourceTag) string {
	var label string
	switch s.Origin {
	case domain.OriginNWS:
		label = "nws"
	case domain.OriginAirNow:
		label = "an"
	case domain.OriginOpenMeteo:
		label = "om"
		if s.Modeled {
			label = "cams"
		}
	case domain.OriginComputed:
		label = "est"
	default:
		label = string(s.Origin)
	}
	if s.Stale {
		label += "!"
	}
	return label
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.In(nyLoc).Format("2006-01-02 15:04 MST")
}
