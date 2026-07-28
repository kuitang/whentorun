package nws

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

const minimalGridpoint = `{"properties":{"gridId":"OKX","gridX":34,"gridY":45,
	"updateTime":"2026-07-27T18:51:39+00:00",
	"temperature":{"uom":"wmoUnit:degC","values":[
		{"validTime":"2026-07-28T00:00:00+00:00/PT2H","value":20}]}}}`

func testClient(baseURL string) *Client {
	return &Client{BaseURL: baseURL, RetryWait: time.Millisecond}
}

func TestClientHeadersAndPaths(t *testing.T) {
	var gotUA, gotAccept, gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUA = r.Header.Get("User-Agent")
		gotAccept = r.Header.Get("Accept")
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		if r.URL.Path == "/gridpoints/OKX/34,45" {
			w.Write([]byte(minimalGridpoint))
			return
		}
		w.Write([]byte(`{"features":[]}`))
	}))
	defer srv.Close()
	c := testClient(srv.URL)

	gp, err := c.Gridpoint(context.Background(), "OKX", 34, 45)
	if err != nil {
		t.Fatalf("Gridpoint: %v", err)
	}
	if gotPath != "/gridpoints/OKX/34,45" {
		t.Errorf("path = %q, want /gridpoints/OKX/34,45", gotPath)
	}
	if gotUA != "whentorun.com (kuitang@gmail.com)" {
		t.Errorf("User-Agent = %q", gotUA)
	}
	if gotAccept != "application/geo+json" {
		t.Errorf("Accept = %q", gotAccept)
	}
	if gp.Properties.GridID != "OKX" || gp.Properties.GridX != 34 {
		t.Errorf("decoded identity = %s/%d", gp.Properties.GridID, gp.Properties.GridX)
	}

	alerts, err := c.ActiveAlerts(context.Background(), "NYZ072")
	if err != nil {
		t.Fatalf("ActiveAlerts: %v", err)
	}
	if gotPath != "/alerts/active" || gotQuery != "zone=NYZ072" {
		t.Errorf("alerts URL = %q?%q, want /alerts/active?zone=NYZ072", gotPath, gotQuery)
	}
	if len(alerts) != 0 {
		t.Errorf("alerts = %v, want empty", alerts)
	}
}

func TestClientRetryOn5xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		w.Write([]byte(minimalGridpoint))
	}))
	defer srv.Close()

	gp, err := testClient(srv.URL).Gridpoint(context.Background(), "OKX", 34, 45)
	if err != nil {
		t.Fatalf("Gridpoint after one 500: %v", err)
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2", calls.Load())
	}
	if gp.Properties.GridID != "OKX" {
		t.Errorf("GridID = %q", gp.Properties.GridID)
	}
}

func TestClientRetryExhausted(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	_, err := testClient(srv.URL).Gridpoint(context.Background(), "OKX", 34, 45)
	if err == nil {
		t.Fatal("want error after two 5xx")
	}
	if calls.Load() != 2 {
		t.Errorf("calls = %d, want 2 (exactly one retry)", calls.Load())
	}
}

func TestClientNoRetryOn4xx(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "no", http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := testClient(srv.URL).Gridpoint(context.Background(), "OKX", 34, 45)
	if err == nil {
		t.Fatal("want error on 404")
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (4xx must not retry)", calls.Load())
	}
}

// failOnceTransport fails the first round trip with a network-style error,
// then delegates.
type failOnceTransport struct {
	calls atomic.Int32
	next  http.RoundTripper
}

func (tr *failOnceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if tr.calls.Add(1) == 1 {
		return nil, errors.New("simulated connection reset")
	}
	return tr.next.RoundTrip(req)
}

func TestClientRetryOnNetworkError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(minimalGridpoint))
	}))
	defer srv.Close()

	tr := &failOnceTransport{next: http.DefaultTransport}
	c := &Client{
		BaseURL:    srv.URL,
		RetryWait:  time.Millisecond,
		HTTPClient: &http.Client{Transport: tr},
	}
	gp, err := c.Gridpoint(context.Background(), "OKX", 34, 45)
	if err != nil {
		t.Fatalf("Gridpoint after one network error: %v", err)
	}
	if tr.calls.Load() != 2 {
		t.Errorf("transport calls = %d, want 2", tr.calls.Load())
	}
	if gp.Properties.GridID != "OKX" {
		t.Errorf("GridID = %q", gp.Properties.GridID)
	}
}

func TestClientContextCancelledDuringBackoff(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{BaseURL: srv.URL, RetryWait: time.Hour} // backoff would block
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	_, err := c.Gridpoint(ctx, "OKX", 34, 45)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if calls.Load() != 1 {
		t.Errorf("calls = %d, want 1 (cancelled before retry)", calls.Load())
	}
}

// TestClientGridpointFixtureEndToEnd serves the recorded fixture through
// the client and expands it, exercising the full decode path.
func TestClientGridpointFixtureEndToEnd(t *testing.T) {
	raw, err := os.ReadFile("testdata/gridpoint_okx_34_45.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/geo+json")
		w.Write(raw)
	}))
	defer srv.Close()

	gp, err := testClient(srv.URL).Gridpoint(context.Background(), "OKX", 34, 45)
	if err != nil {
		t.Fatalf("Gridpoint: %v", err)
	}
	f, err := gp.Expand(time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC), 48)
	if err != nil {
		t.Fatalf("Expand: %v", err)
	}
	if len(f.Hours) != 48 || !f.Hours[0].TempF.Valid {
		t.Errorf("expanded forecast incomplete: %d hours, h0 temp valid=%v",
			len(f.Hours), f.Hours[0].TempF.Valid)
	}
}

// TestClientAlertsFixtureEndToEnd serves the recorded alerts fixture
// through the client.
func TestClientAlertsFixtureEndToEnd(t *testing.T) {
	raw, err := os.ReadFile("testdata/alerts_nyz072.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(raw)
	}))
	defer srv.Close()

	alerts, err := testClient(srv.URL).ActiveAlerts(context.Background(), "NYZ072")
	if err != nil {
		t.Fatalf("ActiveAlerts: %v", err)
	}
	if len(alerts) != 1 || alerts[0].Event != "Flood Watch" {
		t.Errorf("alerts = %+v, want one Flood Watch", alerts)
	}
}

func TestClientDefaults(t *testing.T) {
	c := &Client{}
	if c.baseURL() != "https://api.weather.gov" {
		t.Errorf("baseURL = %q", c.baseURL())
	}
	if c.userAgent() != "whentorun.com (kuitang@gmail.com)" {
		t.Errorf("userAgent = %q", c.userAgent())
	}
	if c.retryWait() != 500*time.Millisecond {
		t.Errorf("retryWait = %v", c.retryWait())
	}
	if c.httpClient() != http.DefaultClient {
		t.Error("httpClient: want http.DefaultClient")
	}
}
