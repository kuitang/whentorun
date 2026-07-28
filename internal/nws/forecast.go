package nws

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// TextForecast is the official NWS narrative forecast for a grid cell:
// the 12-hour prose periods ("Tonight", "Tuesday", …) from
// /gridpoints/{office}/{x},{y}/forecast. (The name Forecast is taken by
// the expanded hourly grid result in grid.go.)
type TextForecast struct {
	Properties TextForecastProperties `json:"properties"`
}

// TextForecastProperties is the payload under "properties".
type TextForecastProperties struct {
	UpdateTime  string           `json:"updateTime"`
	GeneratedAt string           `json:"generatedAt"`
	Periods     []ForecastPeriod `json:"periods"`
}

// ForecastPeriod is one narrative period. Start/End carry the NWS-supplied
// zone offset (Eastern for OKX).
type ForecastPeriod struct {
	Number    int       `json:"number"`
	Name      string    `json:"name"` // e.g. "Tonight", "Tuesday"
	Start     time.Time `json:"startTime"`
	End       time.Time `json:"endTime"`
	IsDaytime bool      `json:"isDaytime"`
	Short     string    `json:"shortForecast"`
	Detailed  string    `json:"detailedForecast"`
}

// Covers reports whether the period covers time t.
func (p ForecastPeriod) Covers(t time.Time) bool {
	return !t.Before(p.Start) && t.Before(p.End)
}

// Forecast fetches the narrative text forecast for one grid cell,
// e.g. Forecast(ctx, "OKX", 34, 45).
func (c *Client) Forecast(ctx context.Context, office string, x, y int) (*TextForecast, error) {
	url := fmt.Sprintf("%s/gridpoints/%s/%d,%d/forecast", c.baseURL(), office, x, y)
	body, err := c.get(ctx, url)
	if err != nil {
		return nil, err
	}
	var tf TextForecast
	if err := json.Unmarshal(body, &tf); err != nil {
		return nil, fmt.Errorf("nws: decode forecast %s/%d,%d: %w", office, x, y, err)
	}
	return &tf, nil
}
