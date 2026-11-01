package weather_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kviatkovsky/pvforecast-go/weather"
)

// countingProvider returns one sample per call and counts the calls. If
// gate is non-nil, every call blocks until it is closed or ctx ends.
type countingProvider struct {
	calls atomic.Int32
	gate  chan struct{}
	err   error
}

func (p *countingProvider) Forecast(ctx context.Context, r weather.Request) (weather.Series, error) {
	p.calls.Add(1)
	if p.gate != nil {
		select {
		case <-p.gate:
		case <-ctx.Done():
			return weather.Series{}, ctx.Err()
		}
	}
	if p.err != nil {
		return weather.Series{}, p.err
	}
	return weather.Series{
		Interval: time.Hour,
		Samples:  []weather.Sample{{Time: r.Start, GHI: 500}},
	}, nil
}

func request(lat float64) weather.Request {
	t0 := time.Date(2026, time.June, 1, 0, 0, 0, 0, time.UTC)
	return weather.Request{Lat: lat, Lon: 20, Start: t0, End: t0.Add(24 * time.Hour)}
}

func TestCacheHitsAndExpiry(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p := &countingProvider{}
		c := weather.NewCache(p, 15*time.Minute, 10)
		ctx := t.Context()

		for range 3 {
			if _, err := c.Forecast(ctx, request(50.0647)); err != nil {
				t.Fatal(err)
			}
		}
		// 50.0649 rounds to the same 0.01° cell.
		if _, err := c.Forecast(ctx, request(50.0649)); err != nil {
			t.Fatal(err)
		}
		if got := p.calls.Load(); got != 1 {
			t.Fatalf("upstream calls = %d, want 1", got)
		}

		time.Sleep(16 * time.Minute)
		if _, err := c.Forecast(ctx, request(50.0647)); err != nil {
			t.Fatal(err)
		}
		if got := p.calls.Load(); got != 2 {
			t.Errorf("after expiry: upstream calls = %d, want 2", got)
		}
	})
}

func TestCacheDoesNotCacheErrors(t *testing.T) {
	t.Parallel()
	p := &countingProvider{err: weather.ErrUpstream}
	c := weather.NewCache(p, time.Hour, 10)
	for range 2 {
		if _, err := c.Forecast(t.Context(), request(50)); !errors.Is(err, weather.ErrUpstream) {
			t.Fatalf("err = %v", err)
		}
	}
	if got := p.calls.Load(); got != 2 || c.Len() != 0 {
		t.Errorf("calls = %d, entries = %d; want 2, 0", got, c.Len())
	}
}

func TestCacheMergesConcurrentRequests(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p := &countingProvider{gate: make(chan struct{})}
		c := weather.NewCache(p, time.Hour, 10)

		var wg sync.WaitGroup
		errs := make(chan error, 20)
		for range 20 {
			wg.Go(func() {
				_, err := c.Forecast(t.Context(), request(50))
				errs <- err
			})
		}
		synctest.Wait() // everyone is either the leader or waiting for it
		close(p.gate)
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		if got := p.calls.Load(); got != 1 {
			t.Errorf("upstream calls = %d, want 1", got)
		}
	})
}

func TestCacheWaiterSurvivesLeaderCancellation(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p := &countingProvider{gate: make(chan struct{})}
		c := weather.NewCache(p, time.Hour, 10)

		leaderCtx, cancelLeader := context.WithCancel(t.Context())
		leaderErr := make(chan error, 1)
		go func() {
			_, err := c.Forecast(leaderCtx, request(50))
			leaderErr <- err
		}()
		synctest.Wait()

		waiterErr := make(chan error, 1)
		go func() {
			_, err := c.Forecast(t.Context(), request(50))
			waiterErr <- err
		}()
		synctest.Wait()

		cancelLeader()
		if err := <-leaderErr; !errors.Is(err, context.Canceled) {
			t.Fatalf("leader err = %v", err)
		}
		synctest.Wait() // the waiter has retried and is now the leader
		close(p.gate)
		if err := <-waiterErr; err != nil {
			t.Errorf("waiter inherited the leader's cancellation: %v", err)
		}
		if got := p.calls.Load(); got != 2 {
			t.Errorf("upstream calls = %d, want 2", got)
		}
	})
}

func TestCacheWaiterHonorsOwnContext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p := &countingProvider{gate: make(chan struct{})}
		c := weather.NewCache(p, time.Hour, 10)
		go func() { _, _ = c.Forecast(t.Context(), request(50)) }()
		synctest.Wait()

		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if _, err := c.Forecast(ctx, request(50)); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("err = %v, want DeadlineExceeded", err)
		}
		close(p.gate)
	})
}

func TestCacheEviction(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		p := &countingProvider{}
		c := weather.NewCache(p, time.Hour, 2)
		for i, lat := range []float64{10, 20, 30} {
			if _, err := c.Forecast(t.Context(), request(lat)); err != nil {
				t.Fatal(err)
			}
			time.Sleep(time.Minute) // distinct expiry times
			if want := min(i+1, 2); c.Len() != want {
				t.Fatalf("after %d inserts: %d entries, want %d", i+1, c.Len(), want)
			}
		}
		// The oldest entry (lat 10) was evicted; lat 30 is still cached.
		if _, err := c.Forecast(t.Context(), request(30)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Forecast(t.Context(), request(10)); err != nil {
			t.Fatal(err)
		}
		if got := p.calls.Load(); got != 4 {
			t.Errorf("upstream calls = %d, want 4", got)
		}
	})
}

func TestCacheReturnsIndependentCopies(t *testing.T) {
	t.Parallel()
	c := weather.NewCache(&countingProvider{}, time.Hour, 10)
	a, _ := c.Forecast(t.Context(), request(50))
	a.Samples[0].GHI = -1
	b, _ := c.Forecast(t.Context(), request(50))
	if b.Samples[0].GHI != 500 {
		t.Errorf("a caller's modification leaked into the cache: GHI %g", b.Samples[0].GHI)
	}
	if _, err := c.Forecast(t.Context(), weather.Request{Lat: 99}); !errors.Is(err, weather.ErrInvalidRequest) {
		t.Errorf("invalid request: err = %v", err)
	}
}
