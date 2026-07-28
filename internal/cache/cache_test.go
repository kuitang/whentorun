package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a settable clock for staleness tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.t
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.t = f.t.Add(d)
}

func TestStalenessTransitions(t *testing.T) {
	ttl := TTL{Fresh: 10 * time.Minute, ServeStale: 3 * time.Hour}
	tests := []struct {
		name      string
		age       time.Duration
		wantOK    bool
		wantStale bool
	}{
		{"just fetched", 0, true, false},
		{"within fresh", 9 * time.Minute, true, false},
		{"exactly fresh TTL", 10 * time.Minute, true, false},
		{"just past fresh", 10*time.Minute + time.Second, true, true},
		{"deep in stale window", 2 * time.Hour, true, true},
		{"just before serve-stale limit", 3*time.Hour - time.Second, true, true},
		{"at serve-stale limit", 3 * time.Hour, false, false},
		{"long expired", 24 * time.Hour, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := newFakeClock()
			c := New("test", ttl, func(ctx context.Context, key string) (string, error) {
				return "payload", nil
			})
			c.Now = clk.Now
			if err := c.Refresh(context.Background(), "k"); err != nil {
				t.Fatalf("Refresh: %v", err)
			}
			fetchedAt := clk.Now()
			clk.Advance(tt.age)
			v, ok := c.Get("k")
			if ok != tt.wantOK {
				t.Fatalf("Get ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if v.Stale != tt.wantStale {
				t.Errorf("Stale = %v, want %v", v.Stale, tt.wantStale)
			}
			if v.Data != "payload" {
				t.Errorf("Data = %q, want %q", v.Data, "payload")
			}
			if !v.FetchedAt.Equal(fetchedAt) {
				t.Errorf("FetchedAt = %v, want %v", v.FetchedAt, fetchedAt)
			}
		})
	}
}

func TestGetNeverFetches(t *testing.T) {
	var calls atomic.Int32
	c := New("test", TTLNWSGrid, func(ctx context.Context, key string) (int, error) {
		calls.Add(1)
		return 42, nil
	})
	if _, ok := c.Get("k"); ok {
		t.Fatal("Get on empty cache reported ok")
	}
	if n := calls.Load(); n != 0 {
		t.Fatalf("Get triggered %d upstream calls, want 0", n)
	}
}

func TestKeysAreIndependent(t *testing.T) {
	c := New("test", TTLNWSGrid, func(ctx context.Context, key string) (string, error) {
		return "value-" + key, nil
	})
	for _, k := range []string{"a", "b"} {
		if err := c.Refresh(context.Background(), k); err != nil {
			t.Fatalf("Refresh(%q): %v", k, err)
		}
	}
	for _, k := range []string{"a", "b"} {
		v, ok := c.Get(k)
		if !ok || v.Data != "value-"+k {
			t.Errorf("Get(%q) = %q, %v; want %q, true", k, v.Data, ok, "value-"+k)
		}
	}
	if _, ok := c.Get("c"); ok {
		t.Error("Get of never-fetched key reported ok")
	}
}

func TestSingleflightDeduplicates(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	c := New("test", TTLNWSGrid, func(ctx context.Context, key string) (string, error) {
		calls.Add(1)
		<-release
		return "shared", nil
	})

	const n = 10
	var wg sync.WaitGroup
	results := make([]Value[string], n)
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			results[i], errs[i] = c.GetOrFetch(context.Background(), "k")
		}(i)
	}
	// Let all goroutines pile onto the in-flight fetch, then release it.
	time.Sleep(50 * time.Millisecond)
	close(release)
	wg.Wait()

	if n := calls.Load(); n != 1 {
		t.Errorf("upstream called %d times, want 1 (singleflight)", n)
	}
	for i := 0; i < n; i++ {
		if errs[i] != nil {
			t.Fatalf("goroutine %d: %v", i, errs[i])
		}
		if results[i].Data != "shared" {
			t.Errorf("goroutine %d got %q, want %q", i, results[i].Data, "shared")
		}
	}
}

func TestGetOrFetchServesStaleOnFetchError(t *testing.T) {
	clk := newFakeClock()
	upstreamErr := errors.New("upstream down")
	fail := false
	var calls atomic.Int32
	c := New("test", TTL{Fresh: 10 * time.Minute, ServeStale: 3 * time.Hour},
		func(ctx context.Context, key string) (string, error) {
			calls.Add(1)
			if fail {
				return "", upstreamErr
			}
			return "good", nil
		})
	c.Now = clk.Now

	if _, err := c.GetOrFetch(context.Background(), "k"); err != nil {
		t.Fatalf("initial GetOrFetch: %v", err)
	}
	fail = true

	// Fresh: no upstream call at all.
	calls.Store(0)
	v, err := c.GetOrFetch(context.Background(), "k")
	if err != nil || v.Stale {
		t.Fatalf("fresh GetOrFetch = (stale=%v, err=%v), want fresh, nil", v.Stale, err)
	}
	if calls.Load() != 0 {
		t.Fatal("fresh GetOrFetch hit upstream")
	}

	// Stale but servable: failed refetch falls back to the stale copy.
	clk.Advance(30 * time.Minute)
	v, err = c.GetOrFetch(context.Background(), "k")
	if err != nil {
		t.Fatalf("stale GetOrFetch: %v", err)
	}
	if !v.Stale || v.Data != "good" {
		t.Errorf("stale GetOrFetch = (%q, stale=%v), want (%q, true)", v.Data, v.Stale, "good")
	}

	// Past ServeStale: unavailable, error surfaces.
	clk.Advance(4 * time.Hour)
	if _, err = c.GetOrFetch(context.Background(), "k"); !errors.Is(err, upstreamErr) {
		t.Errorf("expired GetOrFetch err = %v, want wrapping %v", err, upstreamErr)
	}

	// Upstream recovers: fetch resumes and entry is fresh again.
	fail = false
	v, err = c.GetOrFetch(context.Background(), "k")
	if err != nil || v.Stale || v.Data != "good" {
		t.Errorf("recovered GetOrFetch = (%q, stale=%v, err=%v), want (%q, false, nil)", v.Data, v.Stale, err, "good")
	}
}

func TestRefreshErrorKeepsOldEntry(t *testing.T) {
	fail := false
	c := New("test", TTLNWSGrid, func(ctx context.Context, key string) (string, error) {
		if fail {
			return "", errors.New("boom")
		}
		return "original", nil
	})
	if err := c.Refresh(context.Background(), "k"); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	fail = true
	if err := c.Refresh(context.Background(), "k"); err == nil {
		t.Fatal("Refresh with failing upstream returned nil error")
	}
	v, ok := c.Get("k")
	if !ok || v.Data != "original" {
		t.Errorf("after failed refresh Get = (%q, %v), want (%q, true)", v.Data, ok, "original")
	}
}

func TestRefresherRefreshesOnCadenceAndStopsCleanly(t *testing.T) {
	var calls atomic.Int32
	c := New("test", TTL{Fresh: 20 * time.Millisecond, ServeStale: time.Hour},
		func(ctx context.Context, key string) (string, error) {
			calls.Add(1)
			return "v", nil
		})

	r := NewRefresher()
	Keep(r, c, "k")
	r.Start(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := calls.Load(); n < 3 {
		t.Fatalf("refresher made %d calls in 2s, want >= 3", n)
	}
	if _, ok := c.Get("k"); !ok {
		t.Fatal("cache not populated by refresher")
	}

	r.Stop() // must block until the loop goroutine has exited
	after := calls.Load()
	time.Sleep(60 * time.Millisecond)
	if n := calls.Load(); n != after {
		t.Errorf("refresher kept fetching after Stop: %d -> %d", after, n)
	}
}

func TestRefresherContextCancelStopsLoops(t *testing.T) {
	var calls atomic.Int32
	c := New("test", TTL{Fresh: 10 * time.Millisecond, ServeStale: time.Hour},
		func(ctx context.Context, key string) (string, error) {
			calls.Add(1)
			return "v", nil
		})
	r := NewRefresher()
	Keep(r, c, "k")
	ctx, cancel := context.WithCancel(context.Background())
	r.Start(ctx)
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 1 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	cancel()
	r.Stop() // Stop after external cancel must still return promptly
	after := calls.Load()
	time.Sleep(40 * time.Millisecond)
	if n := calls.Load(); n != after {
		t.Errorf("loops survived context cancel: %d -> %d", after, n)
	}
}

func TestRefresherReportsErrors(t *testing.T) {
	c := New("mysource", TTL{Fresh: 10 * time.Millisecond, ServeStale: time.Hour},
		func(ctx context.Context, key string) (string, error) {
			return "", errors.New("boom")
		})
	type report struct{ name, key string }
	got := make(chan report, 1)
	r := NewRefresher()
	r.OnError = func(cacheName, key string, err error) {
		select {
		case got <- report{cacheName, key}:
		default:
		}
	}
	Keep(r, c, "mykey")
	r.Start(context.Background())
	defer r.Stop()
	select {
	case rep := <-got:
		if rep.name != "mysource" || rep.key != "mykey" {
			t.Errorf("OnError got (%q, %q), want (mysource, mykey)", rep.name, rep.key)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnError never called for failing refresh")
	}
}

func TestKeepAfterStartLaunchesImmediately(t *testing.T) {
	var calls atomic.Int32
	c := New("test", TTL{Fresh: time.Hour, ServeStale: 2 * time.Hour},
		func(ctx context.Context, key string) (string, error) {
			calls.Add(1)
			return "v", nil
		})
	r := NewRefresher()
	r.Start(context.Background())
	defer r.Stop()
	Keep(r, c, "k")
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(2 * time.Millisecond)
	}
	if calls.Load() == 0 {
		t.Fatal("Keep after Start never refreshed")
	}
}
