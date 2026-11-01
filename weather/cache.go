package weather

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"sync"
	"time"
)

// Cache is a Provider that remembers successful forecasts for a while and
// merges concurrent identical requests into one upstream call.
//
// Requests are keyed by location rounded to 0.01° (about 1 km, finer than
// any weather model grid), window and Instant. Errors are not cached. Cache
// is safe for concurrent use.
type Cache struct {
	next       Provider
	ttl        time.Duration
	maxEntries int

	mu       sync.Mutex
	entries  map[cacheKey]cacheEntry
	inflight map[cacheKey]*call
}

var _ Provider = (*Cache)(nil)

type cacheKey struct {
	lat, lon   int64 // hundredths of a degree
	start, end int64 // Unix nanoseconds
	instant    bool
}

type cacheEntry struct {
	series  Series
	expires time.Time
}

// call is one upstream request that other callers may wait for.
type call struct {
	done   chan struct{}
	series Series
	err    error
}

// NewCache wraps next. Entries live for ttl; at most maxEntries are kept,
// evicting expired and then the oldest entries first.
func NewCache(next Provider, ttl time.Duration, maxEntries int) *Cache {
	return &Cache{
		next:       next,
		ttl:        ttl,
		maxEntries: max(maxEntries, 1),
		entries:    map[cacheKey]cacheEntry{},
		inflight:   map[cacheKey]*call{},
	}
}

func keyOf(r Request) cacheKey {
	return cacheKey{
		lat: int64(math.Round(r.Lat * 100)), lon: int64(math.Round(r.Lon * 100)),
		start: r.Start.UnixNano(), end: r.End.UnixNano(), instant: r.Instant,
	}
}

// Forecast implements Provider. The returned Series owns its Samples, so
// callers may modify them.
func (c *Cache) Forecast(ctx context.Context, req Request) (Series, error) {
	if err := req.Validate(); err != nil {
		return Series{}, err
	}
	key := keyOf(req)

	c.mu.Lock()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expires) {
		c.mu.Unlock()
		return clone(e.series), nil
	}
	if cl, ok := c.inflight[key]; ok {
		c.mu.Unlock()
		return c.wait(ctx, req, cl)
	}
	cl := &call{done: make(chan struct{})}
	c.inflight[key] = cl
	c.mu.Unlock()

	cl.series, cl.err = c.next.Forecast(ctx, req)

	c.mu.Lock()
	delete(c.inflight, key)
	if cl.err == nil {
		c.store(key, cl.series)
	}
	c.mu.Unlock()
	close(cl.done)

	if cl.err != nil {
		return Series{}, cl.err
	}
	return clone(cl.series), nil
}

// wait blocks until the in-flight call finishes or ctx ends. If the call
// failed only because its own caller gave up, this caller retries with its
// own, still valid context instead of inheriting that cancellation.
func (c *Cache) wait(ctx context.Context, req Request, cl *call) (Series, error) {
	select {
	case <-ctx.Done():
		return Series{}, fmt.Errorf("weather: waiting for cached request: %w", ctx.Err())
	case <-cl.done:
	}
	if cl.err != nil {
		if isContextErr(cl.err) && ctx.Err() == nil {
			return c.Forecast(ctx, req)
		}
		return Series{}, cl.err
	}
	return clone(cl.series), nil
}

func isContextErr(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// store adds an entry; c.mu must be held.
func (c *Cache) store(key cacheKey, s Series) {
	now := time.Now()
	if len(c.entries) >= c.maxEntries {
		for k, e := range c.entries {
			if !now.Before(e.expires) {
				delete(c.entries, k)
			}
		}
	}
	if len(c.entries) >= c.maxEntries {
		var oldest cacheKey
		var oldestExp time.Time
		for k, e := range c.entries {
			if oldestExp.IsZero() || e.expires.Before(oldestExp) {
				oldest, oldestExp = k, e.expires
			}
		}
		delete(c.entries, oldest)
	}
	c.entries[key] = cacheEntry{series: s, expires: now.Add(c.ttl)}
}

// Len returns the number of cached entries, including expired ones not yet
// evicted.
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

func clone(s Series) Series {
	s.Samples = slices.Clone(s.Samples)
	return s
}
