// Package nws is a minimal client for api.weather.gov: raw gridpoint
// forecasts (run-length encoded layers, expanded to per-hour values by
// grid.go) and active alerts for a zone (alerts.go).
package nws

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

const (
	// DefaultBaseURL is the production NWS API host.
	DefaultBaseURL = "https://api.weather.gov"
	// DefaultUserAgent identifies us to NWS, which requires an
	// identifying User-Agent on every request.
	DefaultUserAgent = "whentorun.com (kuitang@gmail.com)"
	// defaultRetryWait is the backoff before the single retry.
	defaultRetryWait = 500 * time.Millisecond
)

// Client talks to the NWS API. The zero value is usable and hits
// production; tests inject HTTPClient and BaseURL.
type Client struct {
	HTTPClient *http.Client  // nil → http.DefaultClient
	BaseURL    string        // "" → DefaultBaseURL
	UserAgent  string        // "" → DefaultUserAgent
	RetryWait  time.Duration // 0 → 500ms; set small in tests
}

func (c *Client) httpClient() *http.Client {
	if c.HTTPClient != nil {
		return c.HTTPClient
	}
	return http.DefaultClient
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return DefaultBaseURL
}

func (c *Client) userAgent() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	return DefaultUserAgent
}

func (c *Client) retryWait() time.Duration {
	if c.RetryWait != 0 {
		return c.RetryWait
	}
	return defaultRetryWait
}

// Gridpoint fetches the raw gridpoint forecast for one grid cell,
// e.g. Gridpoint(ctx, "OKX", 34, 45).
func (c *Client) Gridpoint(ctx context.Context, gridID string, x, y int) (*Gridpoint, error) {
	url := fmt.Sprintf("%s/gridpoints/%s/%d,%d", c.baseURL(), gridID, x, y)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var gp Gridpoint
	if err := json.Unmarshal(body, &gp); err != nil {
		return nil, fmt.Errorf("nws: decode gridpoint %s/%d,%d: %w", gridID, x, y, err)
	}
	return &gp, nil
}

// ActiveAlerts fetches active alerts for an NWS public zone, e.g. "NYZ072".
func (c *Client) ActiveAlerts(ctx context.Context, zone string) ([]domain.Alert, error) {
	url := fmt.Sprintf("%s/alerts/active?zone=%s", c.baseURL(), zone)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	alerts, err := parseAlerts(body)
	if err != nil {
		return nil, fmt.Errorf("nws: decode alerts for %s: %w", zone, err)
	}
	return alerts, nil
}

// get performs a GET with the required headers, retrying once on a 5xx
// response or a transport/read error, after a short backoff. 4xx responses
// are not retried.
func (c *Client) get(ctx context.Context, url string) ([]byte, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(c.retryWait()):
			}
		}
		body, retryable, err := c.doOnce(ctx, url)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !retryable {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) doOnce(ctx context.Context, url string) (body []byte, retryable bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, false, fmt.Errorf("nws: build request %s: %w", url, err)
	}
	req.Header.Set("User-Agent", c.userAgent())
	req.Header.Set("Accept", "application/geo+json")

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("nws: GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	body, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, true, fmt.Errorf("nws: read %s: %w", url, err)
	}
	switch {
	case resp.StatusCode >= 500:
		return nil, true, fmt.Errorf("nws: GET %s: status %d", url, resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, false, fmt.Errorf("nws: GET %s: status %d", url, resp.StatusCode)
	}
	return body, false, nil
}
