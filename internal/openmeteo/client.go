// Package openmeteo is a client for the keyless Open-Meteo weather and
// air-quality APIs. It is the sole UV source, the input source for the
// Kong–Huber WBGT fallback, and the modeled (CAMS) hourly-AQI source.
//
// Base URLs and the http.Client are injectable for tests; every request
// takes a context and is retried once on transport error or 5xx.
package openmeteo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// hourlyWeatherVars are the hourly variables requested from the weather API.
const hourlyWeatherVars = "temperature_2m,relative_humidity_2m,dew_point_2m," +
	"wind_speed_10m,wind_gusts_10m,uv_index,shortwave_radiation," +
	"direct_radiation,diffuse_radiation,precipitation_probability,cloud_cover"

// Config holds the endpoints so they can be pointed at test servers.
type Config struct {
	// WeatherURL is the full forecast endpoint URL.
	WeatherURL string
	// AirQualityURL is the full air-quality endpoint URL.
	AirQualityURL string
	// Timezone is the IANA timezone requested; hourly timestamps are
	// returned (and parsed) in this zone.
	Timezone string
	// ForecastDays bounds the horizon (we need 48 h; 3 days covers it).
	ForecastDays int
}

// DefaultConfig returns the production endpoints, verified live 2026-07-28.
func DefaultConfig() Config {
	return Config{
		WeatherURL:    "https://api.open-meteo.com/v1/forecast",
		AirQualityURL: "https://air-quality-api.open-meteo.com/v1/air-quality",
		Timezone:      "America/New_York",
		ForecastDays:  3,
	}
}

// Client calls the Open-Meteo APIs. Zero value is not usable; use New.
type Client struct {
	Config     Config
	HTTPClient *http.Client
}

// New returns a Client with the given config; a nil httpClient means a
// default client with a 15 s timeout.
func New(cfg Config, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{Config: cfg, HTTPClient: httpClient}
}

// WeatherHour is one hour of weather-API values. Pointer fields are nil
// when the API returned null for that hour.
type WeatherHour struct {
	Time          time.Time // start of hour, in Config.Timezone
	TempF         *float64
	RelHumidity   *float64 // %
	DewPointF     *float64
	WindMPH       *float64
	GustMPH       *float64
	UVIndex       *float64
	ShortwaveWm2  *float64 // shortwave radiation, W/m²
	DirectWm2     *float64
	DiffuseWm2    *float64
	PoP           *float64 // precipitation probability, %
	CloudCoverPct *float64
}

// AirQualityHour is one hour of the air-quality API's us_aqi. AQI is nil
// when the API returned null.
type AirQualityHour struct {
	Time time.Time
	AQI  *float64
}

// weatherResp mirrors the JSON envelope of both Open-Meteo APIs.
type weatherResp struct {
	Timezone string `json:"timezone"`
	Hourly   struct {
		Time        []string   `json:"time"`
		Temperature []*float64 `json:"temperature_2m"`
		RelHumidity []*float64 `json:"relative_humidity_2m"`
		DewPoint    []*float64 `json:"dew_point_2m"`
		WindSpeed   []*float64 `json:"wind_speed_10m"`
		WindGusts   []*float64 `json:"wind_gusts_10m"`
		UVIndex     []*float64 `json:"uv_index"`
		Shortwave   []*float64 `json:"shortwave_radiation"`
		Direct      []*float64 `json:"direct_radiation"`
		Diffuse     []*float64 `json:"diffuse_radiation"`
		PrecipProb  []*float64 `json:"precipitation_probability"`
		CloudCover  []*float64 `json:"cloud_cover"`
		USAQI       []*float64 `json:"us_aqi"`
	} `json:"hourly"`
}

// Weather fetches the hourly weather forecast for a point, in °F and mph.
func (c *Client) Weather(ctx context.Context, lat, lon float64) ([]WeatherHour, error) {
	q := url.Values{
		"latitude":         {fmt.Sprintf("%.4f", lat)},
		"longitude":        {fmt.Sprintf("%.4f", lon)},
		"hourly":           {hourlyWeatherVars},
		"temperature_unit": {"fahrenheit"},
		"wind_speed_unit":  {"mph"},
		"timezone":         {c.Config.Timezone},
		"forecast_days":    {fmt.Sprintf("%d", c.forecastDays())},
	}
	var resp weatherResp
	if err := c.getJSON(ctx, c.Config.WeatherURL, q, &resp); err != nil {
		return nil, fmt.Errorf("openmeteo weather: %w", err)
	}
	loc, err := time.LoadLocation(c.Config.Timezone)
	if err != nil {
		return nil, fmt.Errorf("openmeteo weather: bad timezone %q: %w", c.Config.Timezone, err)
	}
	h := resp.Hourly
	hours := make([]WeatherHour, 0, len(h.Time))
	for i, ts := range h.Time {
		t, err := time.ParseInLocation("2006-01-02T15:04", ts, loc)
		if err != nil {
			return nil, fmt.Errorf("openmeteo weather: bad hourly time %q: %w", ts, err)
		}
		hours = append(hours, WeatherHour{
			Time:          t,
			TempF:         at(h.Temperature, i),
			RelHumidity:   at(h.RelHumidity, i),
			DewPointF:     at(h.DewPoint, i),
			WindMPH:       at(h.WindSpeed, i),
			GustMPH:       at(h.WindGusts, i),
			UVIndex:       at(h.UVIndex, i),
			ShortwaveWm2:  at(h.Shortwave, i),
			DirectWm2:     at(h.Direct, i),
			DiffuseWm2:    at(h.Diffuse, i),
			PoP:           at(h.PrecipProb, i),
			CloudCoverPct: at(h.CloudCover, i),
		})
	}
	return hours, nil
}

// AirQuality fetches hourly modeled US AQI (CAMS) for a point.
func (c *Client) AirQuality(ctx context.Context, lat, lon float64) ([]AirQualityHour, error) {
	q := url.Values{
		"latitude":      {fmt.Sprintf("%.4f", lat)},
		"longitude":     {fmt.Sprintf("%.4f", lon)},
		"hourly":        {"us_aqi"},
		"timezone":      {c.Config.Timezone},
		"forecast_days": {fmt.Sprintf("%d", c.forecastDays())},
	}
	var resp weatherResp
	if err := c.getJSON(ctx, c.Config.AirQualityURL, q, &resp); err != nil {
		return nil, fmt.Errorf("openmeteo air-quality: %w", err)
	}
	loc, err := time.LoadLocation(c.Config.Timezone)
	if err != nil {
		return nil, fmt.Errorf("openmeteo air-quality: bad timezone %q: %w", c.Config.Timezone, err)
	}
	h := resp.Hourly
	hours := make([]AirQualityHour, 0, len(h.Time))
	for i, ts := range h.Time {
		t, err := time.ParseInLocation("2006-01-02T15:04", ts, loc)
		if err != nil {
			return nil, fmt.Errorf("openmeteo air-quality: bad hourly time %q: %w", ts, err)
		}
		hours = append(hours, AirQualityHour{Time: t, AQI: at(h.USAQI, i)})
	}
	return hours, nil
}

func (c *Client) forecastDays() int {
	if c.Config.ForecastDays <= 0 {
		return 3
	}
	return c.Config.ForecastDays
}

// at returns s[i] if present, else nil, tolerating short arrays.
func at(s []*float64, i int) *float64 {
	if i < len(s) {
		return s[i]
	}
	return nil
}

// retryDelay between the first attempt and the single retry; a variable so
// tests can shrink it.
var retryDelay = 500 * time.Millisecond

// getJSON GETs base?query and decodes JSON into out, retrying once on
// transport error or 5xx status.
func (c *Client) getJSON(ctx context.Context, base string, q url.Values, out any) error {
	fullURL := base + "?" + q.Encode()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return err
		}
		resp, err := c.HTTPClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("status %d: %s", resp.StatusCode, truncate(body, 200))
		}
		if err := json.Unmarshal(body, out); err != nil {
			return fmt.Errorf("decoding response: %w", err)
		}
		return nil
	}
	return lastErr
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
