package openmeteo_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kviatkovsky/pvforecast-go/internal/testutil"
	"github.com/kviatkovsky/pvforecast-go/weather"
	"github.com/kviatkovsky/pvforecast-go/weather/openmeteo"
)

// The fixtures are synthetic responses in the Open-Meteo format (see
// tools/fixtures/gen.py), covering 2026-06-20 00:00 to 2026-06-22 23:00 UTC
// with the last three hours null.
var (
	fixtureStart = time.Date(2026, time.June, 20, 0, 0, 0, 0, time.UTC)
	fixtureNow   = func() time.Time { return fixtureStart.Add(7 * time.Hour) }
)

func fixture(tb testing.TB, name string) []byte {
	tb.Helper()
	b, err := os.ReadFile(testutil.TestdataPath("openmeteo/" + name))
	if err != nil {
		tb.Fatal(err)
	}
	return b
}

// roundTripFunc is an in-memory http.RoundTripper. Unlike httptest.Server it
// works inside a synctest bubble.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func respond(status int, body string, header ...string) *http.Response {
	h := http.Header{}
	for i := 0; i+1 < len(header); i += 2 {
		h.Set(header[i], header[i+1])
	}
	return &http.Response{StatusCode: status, Header: h, Body: io.NopCloser(strings.NewReader(body))}
}

func newClient(rt roundTripFunc, opts ...openmeteo.Option) *openmeteo.Client {
	base := []openmeteo.Option{
		openmeteo.WithHTTPClient(&http.Client{Transport: rt}),
		openmeteo.WithBaseURL("https://open-meteo.test/v1/forecast"),
		openmeteo.WithClock(fixtureNow),
	}
	return openmeteo.New(append(base, opts...)...)
}

func krakowRequest(days int) weather.Request {
	return weather.Request{
		Lat: 50.0647, Lon: 19.945,
		Start: fixtureStart, End: fixtureStart.Add(time.Duration(days) * 24 * time.Hour),
	}
}

// TestForecastOverHTTP exercises the real HTTP stack end to end.
func TestForecastOverHTTP(t *testing.T) {
	t.Parallel()
	var gotQuery atomicValue
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery.Store(r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(fixture(t, "forecast_averaged.json"))
	}))
	t.Cleanup(srv.Close)

	c := openmeteo.New(openmeteo.WithBaseURL(srv.URL), openmeteo.WithClock(fixtureNow))
	s, err := c.Forecast(t.Context(), krakowRequest(3))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(); err != nil {
		t.Fatalf("series invalid: %v", err)
	}
	if got, want := len(s.Samples), 72-3; got != want {
		t.Errorf("got %d samples, want %d (nulls dropped)", got, want)
	}
	if s.Label != weather.IntervalEnd || s.Interval != time.Hour || s.Source != "open-meteo" {
		t.Errorf("metadata = %v, %v, %q", s.Label, s.Interval, s.Source)
	}
	noon := s.Samples[12]
	if !noon.Time.Equal(fixtureStart.Add(12*time.Hour)) || noon.GHI != 904.7 || noon.DNI != 855.2 {
		t.Errorf("sample 12 = %+v", noon)
	}

	q := gotQuery.Load()
	for _, want := range []string{
		"latitude=50.0647", "longitude=19.945", "timezone=UTC", "wind_speed_unit=ms", "forecast_days=3",
		"hourly=shortwave_radiation%2Cdirect_normal_irradiance%2Cdiffuse_radiation%2Ctemperature_2m%2Cwind_speed_10m",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q lacks %q", q, want)
		}
	}
}

type atomicValue struct{ v atomic.Value }

func (a *atomicValue) Store(s string) { a.v.Store(s) }
func (a *atomicValue) Load() string   { s, _ := a.v.Load().(string); return s }

func TestForecastInstant(t *testing.T) {
	t.Parallel()
	var query string
	c := newClient(func(r *http.Request) (*http.Response, error) {
		query = r.URL.RawQuery
		return respond(http.StatusOK, string(fixture(t, "forecast_instant.json"))), nil
	})
	req := krakowRequest(1)
	req.Instant = true
	s, err := c.Forecast(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if s.Label != weather.Instant || len(s.Samples) != 24 {
		t.Errorf("label %v, %d samples", s.Label, len(s.Samples))
	}
	if !strings.Contains(query, "shortwave_radiation_instant") {
		t.Errorf("query %q does not request instant variables", query)
	}
}

func TestForecastFiltersToRequestWindow(t *testing.T) {
	t.Parallel()
	c := newClient(func(*http.Request) (*http.Response, error) {
		return respond(http.StatusOK, string(fixture(t, "forecast_averaged.json"))), nil
	})
	req := krakowRequest(1)
	req.Start = fixtureStart.Add(6 * time.Hour)
	req.End = fixtureStart.Add(9 * time.Hour)
	s, err := c.Forecast(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Samples) != 3 || !s.Samples[0].Time.Equal(req.Start) {
		t.Errorf("got %d samples starting %v, want 3 starting %v", len(s.Samples), s.Samples[0].Time, req.Start)
	}
}

func TestForecastErrors(t *testing.T) {
	t.Parallel()
	valid := string(fixture(t, "forecast_averaged.json"))
	tests := []struct {
		name       string
		req        weather.Request
		status     int
		body       string
		wantErr    error
		wantStatus int
	}{
		{
			name: "invalid request is rejected locally", req: weather.Request{Lat: 91},
			wantErr: weather.ErrInvalidRequest,
		},
		{
			name: "beyond the forecast horizon", req: krakowRequest(20),
			wantErr: weather.ErrInvalidRequest,
		},
		{
			name: "API rejects the request", req: krakowRequest(1), status: http.StatusBadRequest,
			body:    `{"error":true,"reason":"Cannot initialize WindspeedUnit from invalid String value"}`,
			wantErr: weather.ErrInvalidRequest, wantStatus: http.StatusBadRequest,
		},
		{
			name: "malformed JSON", req: krakowRequest(1), status: http.StatusOK, body: `{"hourly":`,
			wantErr: weather.ErrUpstream,
		},
		{
			name: "missing variable", req: krakowRequest(1), status: http.StatusOK,
			body:    strings.Replace(valid, `"wind_speed_10m": [`, `"wind_speed_100m": [`, 1),
			wantErr: weather.ErrUpstream,
		},
		{
			name: "wrong wind unit", req: krakowRequest(1), status: http.StatusOK,
			body:    strings.Replace(valid, `"wind_speed_10m": "m/s"`, `"wind_speed_10m": "km/h"`, 1),
			wantErr: weather.ErrUpstream,
		},
		{
			name: "non-UTC timestamps", req: krakowRequest(1), status: http.StatusOK,
			body:    strings.Replace(valid, `"utc_offset_seconds": 0`, `"utc_offset_seconds": 7200`, 1),
			wantErr: weather.ErrUpstream,
		},
		{
			name: "bad timestamp", req: krakowRequest(1), status: http.StatusOK,
			body:    strings.Replace(valid, `"2026-06-20T05:00"`, `"2026-06-20 05h"`, 1),
			wantErr: weather.ErrUpstream,
		},
		{
			name: "column length mismatch", req: krakowRequest(1), status: http.StatusOK,
			body:    strings.Replace(valid, `"temperature_2m": [`, `"temperature_2m": [1.0,`, 1),
			wantErr: weather.ErrUpstream,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := newClient(func(*http.Request) (*http.Response, error) {
				return respond(tt.status, tt.body), nil
			}, openmeteo.WithRetry(0, 0))
			_, err := c.Forecast(t.Context(), tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantStatus != 0 {
				var apiErr *openmeteo.APIError
				if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.wantStatus || apiErr.Reason == "" {
					t.Errorf("want *APIError with status %d and a reason, got %#v", tt.wantStatus, err)
				}
			}
		})
	}
}

// TestRetry runs in a synctest bubble, so the backoff sleeps take no real
// time and the elapsed virtual time can be asserted exactly.
func TestRetry(t *testing.T) {
	t.Parallel()
	ok := string(fixture(t, "forecast_averaged.json"))
	tests := []struct {
		name      string
		responses []*http.Response // nil entry means a transport error
		wantErr   error
		wantCalls int
		wantWait  time.Duration // exact when Retry-After drives the wait
	}{
		{
			name: "429 with Retry-After then success",
			responses: []*http.Response{
				respond(http.StatusTooManyRequests, `{"error":true,"reason":"Minutely API request limit exceeded"}`, "Retry-After", "2"),
				respond(http.StatusOK, ok),
			},
			wantCalls: 2, wantWait: 2 * time.Second,
		},
		{
			name:      "transport error then success",
			responses: []*http.Response{nil, respond(http.StatusOK, ok)},
			wantCalls: 2,
		},
		{
			name: "persistent 503 exhausts retries",
			responses: []*http.Response{
				respond(http.StatusServiceUnavailable, ""), respond(http.StatusServiceUnavailable, ""),
				respond(http.StatusServiceUnavailable, ""), respond(http.StatusServiceUnavailable, ""),
			},
			wantErr: weather.ErrUpstream, wantCalls: 4,
		},
		{
			name: "persistent 429 reports rate limiting",
			responses: []*http.Response{
				respond(http.StatusTooManyRequests, ""), respond(http.StatusTooManyRequests, ""),
				respond(http.StatusTooManyRequests, ""), respond(http.StatusTooManyRequests, ""),
			},
			wantErr: weather.ErrRateLimited, wantCalls: 4,
		},
		{
			name:      "4xx is not retried",
			responses: []*http.Response{respond(http.StatusBadRequest, `{"error":true,"reason":"bad"}`)},
			wantErr:   weather.ErrInvalidRequest, wantCalls: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				var calls int
				c := newClient(func(*http.Request) (*http.Response, error) {
					r := tt.responses[calls]
					calls++
					if r == nil {
						return nil, errors.New("connection reset by peer")
					}
					return r, nil
				}, openmeteo.WithRetry(3, time.Second))

				start := time.Now()
				_, err := c.Forecast(t.Context(), krakowRequest(1))
				elapsed := time.Since(start)
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}
				if calls != tt.wantCalls {
					t.Errorf("calls = %d, want %d", calls, tt.wantCalls)
				}
				if tt.wantWait > 0 && elapsed != tt.wantWait {
					t.Errorf("waited %v, want %v", elapsed, tt.wantWait)
				}
				// Backoff is capped: 3 retries from 1 s never exceed 0.5+1+2+4 s.
				if elapsed > 8*time.Second {
					t.Errorf("waited %v, longer than the backoff schedule", elapsed)
				}
			})
		})
	}
}

func TestContextCancelledDuringBackoff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		c := newClient(func(*http.Request) (*http.Response, error) {
			return respond(http.StatusServiceUnavailable, ""), nil
		}, openmeteo.WithRetry(5, 10*time.Second))

		go func() {
			time.Sleep(3 * time.Second)
			cancel()
		}()
		_, err := c.Forecast(ctx, krakowRequest(1))
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}

func TestContextCancelledDuringRequest(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	c := newClient(func(r *http.Request) (*http.Response, error) {
		cancel()
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	if _, err := c.Forecast(ctx, krakowRequest(1)); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestAPIErrorMessage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err  *openmeteo.APIError
		want string
	}{
		{&openmeteo.APIError{StatusCode: 400, Reason: "bad unit"}, "open-meteo: HTTP 400: bad unit"},
		{&openmeteo.APIError{StatusCode: 502}, "open-meteo: HTTP 502"},
	}
	for _, tt := range tests {
		if got := tt.err.Error(); got != tt.want {
			t.Errorf("Error() = %q, want %q", got, tt.want)
		}
	}
}
