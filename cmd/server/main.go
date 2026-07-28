// Command server runs the whentorun.com HTTP server: it wires the upstream
// clients into TTL caches kept warm by a background Refresher, hands
// internal/web a cache-backed merge closure, and serves with graceful
// shutdown. Handlers never call upstreams directly.
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kuitang/whentorun/internal/airnow"
	"github.com/kuitang/whentorun/internal/cache"
	"github.com/kuitang/whentorun/internal/domain"
	"github.com/kuitang/whentorun/internal/merge"
	"github.com/kuitang/whentorun/internal/nws"
	"github.com/kuitang/whentorun/internal/openmeteo"
	webserver "github.com/kuitang/whentorun/internal/web"
	webassets "github.com/kuitang/whentorun/web"
)

// airnowKey is the single AirNow cache key: one NYC fetch serves all paths.
const airnowKey = "nyc"

// ttlNWSTextForecast: the narrative forecast updates a few times a day
// (see the note on merge.Sources.Forecast).
var ttlNWSTextForecast = cache.TTL{Fresh: time.Hour, ServeStale: 6 * time.Hour}

// caches bundles every per-source cache the merger reads.
type caches struct {
	grid     *cache.Cache[*nws.Gridpoint]
	forecast *cache.Cache[*nws.TextForecast]
	alerts   *cache.Cache[[]domain.Alert]
	airnow   *cache.Cache[[]airnow.ObservationRecord]
	omw      *cache.Cache[openmeteo.WeatherData]
	oma      *cache.Cache[[]openmeteo.AirQualityHour]
}

func newCaches() *caches {
	nwsClient := &nws.Client{}
	om := openmeteo.New(openmeteo.DefaultConfig(), nil)

	// The AirNow key lives ONLY in the environment (Fly secret); with no
	// key the fetch fails, the cache stays empty, and merge honestly
	// reports the source unavailable (falling back to modeled CAMS AQI).
	airnowAPIKey := os.Getenv("AIRNOW_API_KEY")
	an := airnow.New(airnow.DefaultConfig(), airnowAPIKey, nil)

	mustPath := func(slug string) domain.Path {
		p := domain.PathBySlug(slug)
		if p == nil {
			panic("unknown path slug " + slug)
		}
		return *p
	}

	return &caches{
		grid: cache.New("nws-grid", cache.TTLNWSGrid,
			func(ctx context.Context, slug string) (*nws.Gridpoint, error) {
				p := mustPath(slug)
				return nwsClient.Gridpoint(ctx, p.GridID, p.GridX, p.GridY)
			}),
		forecast: cache.New("nws-forecast", ttlNWSTextForecast,
			func(ctx context.Context, slug string) (*nws.TextForecast, error) {
				p := mustPath(slug)
				return nwsClient.Forecast(ctx, p.GridID, p.GridX, p.GridY)
			}),
		alerts: cache.New("nws-alerts", cache.TTLAlerts,
			func(ctx context.Context, zone string) ([]domain.Alert, error) {
				return nwsClient.ActiveAlerts(ctx, zone)
			}),
		airnow: cache.New("airnow", cache.TTLAirNow,
			func(ctx context.Context, _ string) ([]airnow.ObservationRecord, error) {
				if airnowAPIKey == "" {
					return nil, errors.New("AIRNOW_API_KEY not set")
				}
				cp := mustPath(webserver.DefaultSlug)
				return an.Observation(ctx, cp.Lat, cp.Lon)
			}),
		omw: cache.New("openmeteo-weather", cache.TTLOpenMeteo,
			func(ctx context.Context, slug string) (openmeteo.WeatherData, error) {
				p := mustPath(slug)
				return om.Weather(ctx, p.Lat, p.Lon)
			}),
		oma: cache.New("openmeteo-air", cache.TTLOpenMeteo,
			func(ctx context.Context, slug string) ([]openmeteo.AirQualityHour, error) {
				p := mustPath(slug)
				return om.AirQuality(ctx, p.Lat, p.Lon)
			}),
	}
}

// keepAll registers every source×key refresh loop (~24 keys: 4 per-path
// caches × 5 paths + 3 alert zones + 1 AirNow).
func (c *caches) keepAll(r *cache.Refresher, haveAirNowKey bool) {
	for _, p := range domain.Paths {
		cache.Keep(r, c.grid, p.Slug)
		cache.Keep(r, c.forecast, p.Slug)
		cache.Keep(r, c.omw, p.Slug)
		cache.Keep(r, c.oma, p.Slug)
	}
	for _, z := range domain.AlertZones() {
		cache.Keep(r, c.alerts, z)
	}
	if haveAirNowKey {
		cache.Keep(r, c.airnow, airnowKey)
	}
}

// merger builds the cache-read-only merge closure handlers use.
func (c *caches) merger() func(ctx context.Context, p domain.Path) (merge.Result, error) {
	return func(ctx context.Context, p domain.Path) (merge.Result, error) {
		gv, gok := c.grid.Get(p.Slug)
		fv, fok := c.forecast.Get(p.Slug)
		av, aok := c.alerts.Get(p.AlertZone)
		nv, nok := c.airnow.Get(airnowKey)
		wv, wok := c.omw.Get(p.Slug)
		qv, qok := c.oma.Get(p.Slug)
		src := merge.Sources{
			Grid:      merge.In(gv, gok),
			Forecast:  merge.In(fv, fok),
			Alerts:    merge.In(av, aok),
			AirNowObs: merge.In(nv, nok),
			OMWeather: merge.In(wv, wok),
			OMAir:     merge.In(qv, qok),
		}
		return merge.Merge(p, time.Now(), src), nil
	}
}

func main() {
	dev := flag.Bool("dev", false, "serve web assets and templates from disk instead of the embedded copies (run from the repo root)")
	flag.Parse()

	var assets fs.FS = webassets.Files
	var templates fs.FS
	if *dev {
		assets = os.DirFS("web")
		templates = os.DirFS("internal/web/templates")
	}

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cs := newCaches()
	refresher := cache.NewRefresher()
	refresher.OnError = func(cacheName, key string, err error) {
		log.Printf("refresh %s[%s]: %v", cacheName, key, err)
	}
	cs.keepAll(refresher, os.Getenv("AIRNOW_API_KEY") != "")
	refresher.Start(ctx)
	defer refresher.Stop()

	handler := webserver.NewServer(webserver.Options{
		Merger:    cs.merger(),
		Assets:    assets,
		Templates: templates,
		Dev:       *dev,
	})

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}

	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Printf("listening on :%s (dev=%v)", port, *dev)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("serve: %v", err)
		}
	case <-ctx.Done():
		stop() // restore default signal handling: a second ^C kills immediately
		log.Printf("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
	}
}
