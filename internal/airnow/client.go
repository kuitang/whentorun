// Package airnow is a client for the EPA AirNow API's NEW services
// (the legacy /aq/observation/latLong/* endpoints retire 2026-09-30):
//
//	observation: /aq/observation/current/ziplatlong/
//	forecast:    /aq/forecast/current/
//
// Both were live-verified 2026-07-28 for NYC (40.78,-73.97) with params
// latitude/longitude/distance=25&format=application/json&API_KEY=… .
// The new services return lowerCamelCase JSON (nowcastAQI, parameterName,
// aqiCategoryName) unlike the legacy PascalCase schema.
//
// Display AQI is the max across per-pollutant records (protective framing),
// keeping the dominant pollutant's name. The API key comes from the caller
// (env AIRNOW_API_KEY at the call site) and is scrubbed from every error.
package airnow

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Config holds the base URL and the URL paths of the new-style services so
// they can be adjusted without code changes if AirNow moves them again.
type Config struct {
	BaseURL         string // e.g. "https://www.airnowapi.org"
	ObservationPath string // current observations by lat/long
	ForecastPath    string // current forecast by lat/long
	DistanceMiles   int    // monitor search radius
}

// DefaultConfig returns the endpoints verified live on 2026-07-28.
func DefaultConfig() Config {
	return Config{
		BaseURL:         "https://www.airnowapi.org",
		ObservationPath: "/aq/observation/current/ziplatlong/",
		ForecastPath:    "/aq/forecast/current/",
		DistanceMiles:   25,
	}
}

// Client calls the AirNow API. APIKey is required for live use.
type Client struct {
	Config     Config
	APIKey     string
	HTTPClient *http.Client
}

// New returns a Client; a nil httpClient means a default client with a
// 15 s timeout.
func New(cfg Config, apiKey string, httpClient *http.Client) *Client {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{Config: cfg, APIKey: apiKey, HTTPClient: httpClient}
}

// ObservationRecord is one per-pollutant current-observation record from
// the new ziplatlong service.
type ObservationRecord struct {
	DateObserved      string `json:"dateObserved"` // "2026-07-27"
	HourObserved      string `json:"hourObserved"` // "20:00"
	LocalTimeZone     string `json:"localTimeZone"`
	ReportingAreaName string `json:"reportingAreaName"`
	SiteID            string `json:"siteID"`
	SiteName          string `json:"siteName"`
	ParameterName     string `json:"parameterName"` // "PM2.5", "OZONE", …
	NowcastAQI        int    `json:"nowcastAQI"`
	AQICategoryName   string `json:"aqiCategoryName"`
	ReportingAgency   string `json:"reportingAgency"`
}

// ForecastRecord is one per-pollutant, per-day forecast record from the
// new forecast service.
type ForecastRecord struct {
	DateIssue      string `json:"dateIssue"` // "2026-07-27"
	DateValid      string `json:"dateValid"`
	ReportingArea  string `json:"reportingArea"`
	StateCode      string `json:"stateCode"`
	ParameterName  string `json:"parameterName"`
	AQI            int    `json:"aqi"`
	CategoryNumber int    `json:"categoryNumber"`
	CategoryName   string `json:"categoryName"`
	ActionDay      bool   `json:"actionDay"`
	Discussion     string `json:"discussion"`
}

// Observation fetches current per-pollutant observations near a point.
func (c *Client) Observation(ctx context.Context, lat, lon float64) ([]ObservationRecord, error) {
	var recs []ObservationRecord
	if err := c.getJSON(ctx, c.Config.ObservationPath, lat, lon, &recs); err != nil {
		return nil, fmt.Errorf("airnow observation: %w", err)
	}
	return recs, nil
}

// Forecast fetches the current per-pollutant, per-day AQI forecast near a
// point. Records are daily granularity (dateValid), typically today and
// tomorrow, one record per pollutant per day.
func (c *Client) Forecast(ctx context.Context, lat, lon float64) ([]ForecastRecord, error) {
	var recs []ForecastRecord
	if err := c.getJSON(ctx, c.Config.ForecastPath, lat, lon, &recs); err != nil {
		return nil, fmt.Errorf("airnow forecast: %w", err)
	}
	return recs, nil
}

// DisplayAQI reduces per-pollutant observation records to the display AQI:
// the max across pollutants, with the dominant pollutant's name and EPA
// category. ok is false when there are no records.
func DisplayAQI(recs []ObservationRecord) (aqi int, pollutant, category string, ok bool) {
	for _, r := range recs {
		if !ok || r.NowcastAQI > aqi {
			aqi, pollutant, category, ok = r.NowcastAQI, r.ParameterName, r.AQICategoryName, true
		}
	}
	return aqi, pollutant, category, ok
}

// DayForecast is the display forecast for one valid date: max AQI across
// pollutants with the dominant pollutant and its EPA category.
type DayForecast struct {
	DateValid      string // "2026-07-28"
	AQI            int
	Pollutant      string
	CategoryNumber int
	CategoryName   string
	ActionDay      bool // true if any pollutant record flags an action day
}

// DisplayForecast reduces per-pollutant forecast records to one DayForecast
// per valid date (max across pollutants), sorted by date ascending.
func DisplayForecast(recs []ForecastRecord) []DayForecast {
	byDate := map[string]*DayForecast{}
	var dates []string
	for _, r := range recs {
		d, seen := byDate[r.DateValid]
		if !seen {
			d = &DayForecast{DateValid: r.DateValid, AQI: r.AQI, Pollutant: r.ParameterName,
				CategoryNumber: r.CategoryNumber, CategoryName: r.CategoryName}
			byDate[r.DateValid] = d
			dates = append(dates, r.DateValid)
		} else if r.AQI > d.AQI {
			d.AQI, d.Pollutant = r.AQI, r.ParameterName
			d.CategoryNumber, d.CategoryName = r.CategoryNumber, r.CategoryName
		}
		if r.ActionDay {
			d.ActionDay = true
		}
	}
	// ISO dates sort lexicographically; insertion order is already API
	// order, so a simple sort keeps it deterministic.
	for i := 1; i < len(dates); i++ {
		for j := i; j > 0 && dates[j] < dates[j-1]; j-- {
			dates[j], dates[j-1] = dates[j-1], dates[j]
		}
	}
	out := make([]DayForecast, 0, len(dates))
	for _, d := range dates {
		out = append(out, *byDate[d])
	}
	return out
}

// retryDelay between the first attempt and the single retry; a variable so
// tests can shrink it.
var retryDelay = 500 * time.Millisecond

// getJSON GETs BaseURL+path with the standard params and decodes JSON,
// retrying once on transport error or 5xx. The API key never appears in a
// returned error.
func (c *Client) getJSON(ctx context.Context, path string, lat, lon float64, out any) error {
	dist := c.Config.DistanceMiles
	if dist <= 0 {
		dist = 25
	}
	q := url.Values{
		"format":    {"application/json"},
		"latitude":  {fmt.Sprintf("%.4f", lat)},
		"longitude": {fmt.Sprintf("%.4f", lon)},
		"distance":  {fmt.Sprintf("%d", dist)},
		"API_KEY":   {c.APIKey},
	}
	fullURL := strings.TrimSuffix(c.Config.BaseURL, "/") + path + "?" + q.Encode()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return c.scrub(ctx.Err())
			case <-time.After(retryDelay):
			}
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
		if err != nil {
			return c.scrub(err)
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
			return c.scrub(fmt.Errorf("status %d: %s", resp.StatusCode, truncate(body, 200)))
		}
		if err := json.Unmarshal(body, out); err != nil {
			return c.scrub(fmt.Errorf("decoding response: %w", err))
		}
		return nil
	}
	return c.scrub(lastErr)
}

// scrub removes the API key from an error's text (transport errors embed
// the full request URL) so the key can never reach logs.
func (c *Client) scrub(err error) error {
	if err == nil || c.APIKey == "" {
		return err
	}
	msg := err.Error()
	if !strings.Contains(msg, c.APIKey) {
		return err
	}
	return fmt.Errorf("%s", strings.ReplaceAll(msg, c.APIKey, "REDACTED"))
}

func truncate(b []byte, n int) string {
	if len(b) > n {
		return string(b[:n]) + "…"
	}
	return string(b)
}
