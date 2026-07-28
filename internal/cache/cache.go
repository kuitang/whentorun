// Package cache is a generic TTL cache with serve-stale-with-label
// semantics plus a background Refresher, so HTTP handlers only ever read
// the cache and never trigger per-visitor upstream calls.
//
// Each entry ages through three states:
//
//	age <= TTL.Fresh              fresh (Stale=false)
//	Fresh < age < TTL.ServeStale  served with Stale=true so the UI can label it
//	age >= TTL.ServeStale         treated as unavailable (Get reports !ok)
//
// Concurrent fetches of the same key are de-duplicated with singleflight.
package cache

import (
	"context"
	"fmt"
	"math/rand/v2"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// TTL is a serve-stale policy: fresh for Fresh, servable (labeled stale)
// until ServeStale of age, unavailable after that. Ages are measured from
// the fetch time.
type TTL struct {
	Fresh      time.Duration
	ServeStale time.Duration
}

// Per-source TTLs from PLAN.md.
var (
	TTLNWSGrid   = TTL{Fresh: 10 * time.Minute, ServeStale: 3 * time.Hour}
	TTLAlerts    = TTL{Fresh: 2 * time.Minute, ServeStale: 15 * time.Minute}
	TTLAirNow    = TTL{Fresh: 30 * time.Minute, ServeStale: 3 * time.Hour}
	TTLOpenMeteo = TTL{Fresh: 45 * time.Minute, ServeStale: 6 * time.Hour} // plan allows 30–60 min
)

// Value is a cached payload with its provenance.
type Value[T any] struct {
	Data      T
	FetchedAt time.Time
	Stale     bool // past the fresh TTL but still servable
}

// FetchFunc fetches the upstream payload for one key.
type FetchFunc[T any] func(ctx context.Context, key string) (T, error)

// Cache is a TTL cache for one source. Create with New.
type Cache[T any] struct {
	// Now is the clock; nil means time.Now. Tests inject a fake.
	Now func() time.Time

	name  string
	ttl   TTL
	fetch FetchFunc[T]

	sf      singleflight.Group
	mu      sync.Mutex
	entries map[string]Value[T]
}

// New returns a cache for one source (name is used in errors and by the
// Refresher's error callback).
func New[T any](name string, ttl TTL, fetch FetchFunc[T]) *Cache[T] {
	return &Cache[T]{name: name, ttl: ttl, fetch: fetch, entries: map[string]Value[T]{}}
}

// Name returns the source name given to New.
func (c *Cache[T]) Name() string { return c.name }

// TTL returns the cache's TTL policy.
func (c *Cache[T]) TTL() TTL { return c.ttl }

func (c *Cache[T]) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

// Get returns the cached value for key without ever calling upstream.
// ok is false when the key has never been fetched or the entry has aged
// past ServeStale; a servable entry past its fresh TTL has Stale=true.
func (c *Cache[T]) Get(key string) (v Value[T], ok bool) {
	c.mu.Lock()
	e, exists := c.entries[key]
	c.mu.Unlock()
	if !exists {
		return Value[T]{}, false
	}
	age := c.now().Sub(e.FetchedAt)
	if age >= c.ttl.ServeStale {
		return Value[T]{}, false
	}
	e.Stale = age > c.ttl.Fresh
	return e, true
}

// Refresh fetches key from upstream (de-duplicated via singleflight) and
// stores the result. On error the previous entry, if any, is kept.
func (c *Cache[T]) Refresh(ctx context.Context, key string) error {
	_, err, _ := c.sf.Do(key, func() (any, error) {
		data, err := c.fetch(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("cache %s: refresh %q: %w", c.name, key, err)
		}
		v := Value[T]{Data: data, FetchedAt: c.now()}
		c.mu.Lock()
		c.entries[key] = v
		c.mu.Unlock()
		return nil, nil
	})
	return err
}

// GetOrFetch returns a fresh value, fetching if the entry is missing or
// past its fresh TTL (de-duplicated via singleflight). If the fetch fails
// but a stale-yet-servable entry exists, that entry is returned with
// Stale=true and a nil error. Handlers should normally use Get and let the
// Refresher do the fetching; GetOrFetch exists for tools and warm-up.
func (c *Cache[T]) GetOrFetch(ctx context.Context, key string) (Value[T], error) {
	if v, ok := c.Get(key); ok && !v.Stale {
		return v, nil
	}
	err := c.Refresh(ctx, key)
	if v, ok := c.Get(key); ok {
		return v, nil // freshly stored, or servable-stale after a failed fetch
	}
	if err == nil {
		// Refresh succeeded but the entry immediately expired: only
		// possible with a pathological clock or zero TTL.
		err = fmt.Errorf("cache %s: %q expired immediately after fetch", c.name, key)
	}
	return Value[T]{}, err
}

// Refresher owns background goroutines, one per (cache, key), that refresh
// entries on the cache's fresh-TTL cadence with jitter. Register keys with
// Keep, then Start; Stop cancels every loop and waits for them to exit.
type Refresher struct {
	// OnError, if set, is called when a background refresh fails.
	OnError func(cacheName, key string, err error)
	// Jitter is the fraction of the interval used as ± jitter (default 0.1).
	Jitter float64

	mu      sync.Mutex
	started bool
	stopped bool
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	pending []refreshJob
}

type refreshJob struct {
	cacheName string
	key       string
	interval  time.Duration
	refresh   func(context.Context) error
}

// NewRefresher returns an idle Refresher.
func NewRefresher() *Refresher { return &Refresher{Jitter: 0.1} }

// Keep registers a background refresh loop for one key of c, on c's
// fresh-TTL cadence. May be called before or after Start. (A free function
// because Go methods cannot add type parameters.)
func Keep[T any](r *Refresher, c *Cache[T], key string) {
	interval := c.ttl.Fresh
	if interval <= 0 {
		interval = time.Minute
	}
	r.add(refreshJob{
		cacheName: c.name,
		key:       key,
		interval:  interval,
		refresh:   func(ctx context.Context) error { return c.Refresh(ctx, key) },
	})
}

func (r *Refresher) add(j refreshJob) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	if !r.started {
		r.pending = append(r.pending, j)
		return
	}
	r.launch(j)
}

// launch starts one loop goroutine; the caller must hold r.mu.
func (r *Refresher) launch(j refreshJob) {
	r.wg.Add(1)
	go r.loop(r.ctx, j)
}

// Start begins refreshing every registered key. The loops stop when ctx is
// canceled or Stop is called.
func (r *Refresher) Start(ctx context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started || r.stopped {
		return
	}
	r.ctx, r.cancel = context.WithCancel(ctx)
	r.started = true
	for _, j := range r.pending {
		r.launch(j)
	}
	r.pending = nil
}

// Stop cancels all refresh loops and blocks until they have exited. Safe to
// call more than once and before Start.
func (r *Refresher) Stop() {
	r.mu.Lock()
	r.stopped = true
	r.pending = nil
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}

func (r *Refresher) loop(ctx context.Context, j refreshJob) {
	defer r.wg.Done()
	for {
		if err := j.refresh(ctx); err != nil && ctx.Err() == nil {
			r.mu.Lock()
			onErr := r.OnError
			r.mu.Unlock()
			if onErr != nil {
				onErr(j.cacheName, j.key, err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.jittered(j.interval)):
		}
	}
}

// jittered returns interval scaled by 1 ± Jitter (uniform), so the loops
// for different keys drift apart instead of thundering together.
func (r *Refresher) jittered(interval time.Duration) time.Duration {
	frac := r.Jitter
	if frac <= 0 {
		return interval
	}
	scale := 1 + frac*(2*rand.Float64()-1)
	return time.Duration(float64(interval) * scale)
}
