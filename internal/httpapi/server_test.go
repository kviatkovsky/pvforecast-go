package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/kviatkovsky/pvforecast-go/internal/httpapi"
	"github.com/kviatkovsky/pvforecast-go/internal/report"
	"github.com/kviatkovsky/pvforecast-go/internal/testutil"
	"github.com/kviatkovsky/pvforecast-go/weather"
	"github.com/kviatkovsky/pvforecast-go/weather/openmeteo"
)

var fixtureNow = time.Date(2026, time.June, 20, 0, 0, 0, 0, time.UTC)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fixtureProvider serves the synthetic Open-Meteo response through the real
// client.
func fixtureProvider(tb testing.TB) weather.Provider {
	tb.Helper()
	body, err := os.ReadFile(testutil.TestdataPath("openmeteo/forecast_averaged.json"))
	if err != nil {
		tb.Fatal(err)
	}
	rt := roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})
	return openmeteo.New(
		openmeteo.WithHTTPClient(&http.Client{Transport: rt}),
		openmeteo.WithClock(func() time.Time { return fixtureNow }),
	)
}

type providerFunc func(context.Context, weather.Request) (weather.Series, error)

func (f providerFunc) Forecast(ctx context.Context, r weather.Request) (weather.Series, error) {
	return f(ctx, r)
}

func newServer(tb testing.TB, p weather.Provider, mod ...func(*httpapi.Config)) *httpapi.Server {
	tb.Helper()
	cfg := httpapi.Config{Provider: p, Version: "v9.9.9", Now: func() time.Time { return fixtureNow }}
	for _, m := range mod {
		m(&cfg)
	}
	return httpapi.New(cfg)
}

func do(h http.Handler, method, target, body string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

type problem struct {
	Title     string   `json:"title"`
	Status    int      `json:"status"`
	Detail    string   `json:"detail"`
	Errors    []string `json:"errors"`
	RequestID string   `json:"request_id"`
}

func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json (body %s)", ct, rec.Body)
	}
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Status != rec.Code || p.RequestID == "" {
		t.Errorf("problem %+v does not match status %d or lacks a request id", p, rec.Code)
	}
	return p
}

// TestGetMatchesCLI checks that the API and `pvforecast --format json` give
// the same forecast for the same inputs.
func TestGetMatchesCLI(t *testing.T) {
	t.Parallel()
	h := newServer(t, fixtureProvider(t))
	rec := do(h, http.MethodGet, "/v1/forecast?lat=50.0647&lon=19.945&altitude=219&kwp=10&days=2&tz=Europe/Warsaw", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Cache-Control") == "" || rec.Header().Get("X-Request-ID") == "" {
		t.Errorf("missing headers: %v", rec.Header())
	}
	var got, want report.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile("../../cmd/pvforecast/testdata/json.golden")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(golden, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("API response differs from the CLI golden output:\napi total %g kWh, %d points\ncli total %g kWh, %d points",
			got.TotalKWh, len(got.Points), want.TotalKWh, len(want.Points))
	}
}

func TestPostMultiArray(t *testing.T) {
	t.Parallel()
	cfg, err := os.ReadFile("../../examples/system.json")
	if err != nil {
		t.Fatal(err)
	}
	body := strings.Replace(string(cfg), `"name":`, `"days": 2, "tz": "Europe/Warsaw", "solarpos": "noaa", "name":`, 1)
	rec := do(newServer(t, fixtureProvider(t)), http.MethodPost, "/v1/forecast", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	var r report.Report
	if err := json.Unmarshal(rec.Body.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if r.KWp != 48 || len(r.Daily) != 2 || r.TotalKWh <= 0 || r.TimeZone != "Europe/Warsaw" {
		t.Errorf("unexpected report: kWp %g, %d days, %g kWh, tz %s", r.KWp, len(r.Daily), r.TotalKWh, r.TimeZone)
	}
}

func TestBadRequests(t *testing.T) {
	t.Parallel()
	h := newServer(t, providerFunc(func(context.Context, weather.Request) (weather.Series, error) {
		t.Error("provider called for an invalid request")
		return weather.Series{}, nil
	}))
	const ok = "/v1/forecast?lat=50&lon=20&kwp=5"
	tests := []struct {
		name, method, target, body string
		status                     int
		wantInBody                 []string
	}{
		{"all missing fields reported at once", "GET", "/v1/forecast?tilt=x", "", 400, []string{"lat: required", "lon: required", "kwp: required", `tilt: \"x\" is not a number`}},
		{"invalid system lists fields", "GET", "/v1/forecast?lat=95&lon=20&kwp=5&tilt=120", "", 400, []string{"lat: 95 is outside", "arrays[0].tilt: 120 is outside"}},
		{"days out of range", "GET", ok + "&days=9", "", 400, []string{"days 9"}},
		{"bad time zone", "GET", ok + "&tz=Mars/Olympus", "", 400, []string{"Mars/Olympus"}},
		{"bad model", "GET", ok + "&model=klucher", "", 400, []string{"unknown transposition model"}},
		{"bad solarpos", "GET", ok + "&solarpos=ephem", "", 400, []string{"solarpos"}},
		{"bad dcac", "GET", ok + "&dcac=0", "", 400, []string{"dcac"}},
		{"bad bool", "GET", ok + "&instant=maybe", "", 400, []string{"instant"}},
		{"NaN latitude", "GET", "/v1/forecast?lat=NaN&lon=20&kwp=5", "", 400, []string{"lat:"}},
		{"unknown JSON field", "POST", "/v1/forecast", `{"lat":1,"lon":2,"tilt_deg":5}`, 400, []string{"unknown field"}},
		{"malformed JSON", "POST", "/v1/forecast", `{"lat":`, 400, []string{"Invalid JSON"}},
		{"conflicting losses", "POST", "/v1/forecast", `{"lat":1,"lon":2,"arrays":[{"kwp":1}],"losses":{},"losses_pct":3}`, 400, []string{"either losses or losses_pct"}},
		{"empty system", "POST", "/v1/forecast", `{"lat":1,"lon":2}`, 400, []string{"arrays: at least one array"}},
		{"body too large", "POST", "/v1/forecast", `{"name":"` + strings.Repeat("x", 70<<10) + `"}`, 413, []string{"exceeds"}},
		{"unknown route", "GET", "/v2/forecast", "", 404, []string{"no route"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := do(h, tt.method, tt.target, tt.body)
			if rec.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
			decodeProblem(t, rec)
			for _, s := range tt.wantInBody {
				if !strings.Contains(rec.Body.String(), s) {
					t.Errorf("body lacks %q: %s", s, rec.Body)
				}
			}
		})
	}
	rec := do(h, http.MethodDelete, "/v1/forecast", "")
	if rec.Code != http.StatusMethodNotAllowed || !strings.Contains(rec.Header().Get("Allow"), "POST") {
		t.Errorf("DELETE: status %d, Allow %q; want 405 listing GET and POST", rec.Code, rec.Header().Get("Allow"))
	}
	decodeProblem(t, rec)
}

func TestUpstreamErrors(t *testing.T) {
	t.Parallel()
	const target = "/v1/forecast?lat=50&lon=20&kwp=5"
	tests := []struct {
		name       string
		err        error
		status     int
		retryAfter string
	}{
		{"provider down", weather.ErrUpstream, 502, ""},
		{"provider rate limited", weather.ErrRateLimited, 503, "60"},
		{"no data", nil, 502, ""}, // empty series
		{"unexpected", io.ErrUnexpectedEOF, 500, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			h := newServer(t, providerFunc(func(context.Context, weather.Request) (weather.Series, error) {
				return weather.Series{Interval: time.Hour}, tt.err
			}), func(c *httpapi.Config) { c.Logger = slog.New(slog.NewTextHandler(&logs, nil)) })
			rec := do(h, http.MethodGet, target, "")
			if rec.Code != tt.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tt.status, rec.Body)
			}
			p := decodeProblem(t, rec)
			if got := rec.Header().Get("Retry-After"); got != tt.retryAfter {
				t.Errorf("Retry-After = %q, want %q", got, tt.retryAfter)
			}
			if tt.status == 500 && (p.Detail != "" || !strings.Contains(logs.String(), "forecast failed")) {
				t.Errorf("internal error leaked detail %q or was not logged: %s", p.Detail, logs.String())
			}
		})
	}
}

func TestTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		h := newServer(t, providerFunc(func(ctx context.Context, _ weather.Request) (weather.Series, error) {
			<-ctx.Done()
			return weather.Series{}, ctx.Err()
		}), func(c *httpapi.Config) { c.RequestTimeout = 2 * time.Second })
		start := time.Now()
		rec := do(h, http.MethodGet, "/v1/forecast?lat=50&lon=20&kwp=5", "")
		if rec.Code != http.StatusGatewayTimeout || time.Since(start) != 2*time.Second {
			t.Errorf("status %d after %v, want 504 after 2s", rec.Code, time.Since(start))
		}
	})
}

// TestLoadShedding fills every slot, then checks that the next request is
// rejected immediately instead of queuing.
func TestLoadShedding(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		h := newServer(t, providerFunc(func(ctx context.Context, _ weather.Request) (weather.Series, error) {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return weather.Series{}, weather.ErrUpstream
		}), func(c *httpapi.Config) { c.MaxInFlight = 2 })

		var wg sync.WaitGroup
		for range 2 {
			wg.Go(func() { do(h, http.MethodGet, "/v1/forecast?lat=50&lon=20&kwp=5", "") })
		}
		synctest.Wait()
		rec := do(h, http.MethodGet, "/v1/forecast?lat=50&lon=20&kwp=5", "")
		if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "1" {
			t.Errorf("status %d, Retry-After %q; want 503, 1", rec.Code, rec.Header().Get("Retry-After"))
		}
		// Health checks are never shed.
		if rec := do(h, http.MethodGet, "/healthz", ""); rec.Code != http.StatusOK {
			t.Errorf("healthz under load: %d", rec.Code)
		}
		close(release)
		wg.Wait()
	})
}

func TestPanicRecovery(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	h := newServer(t, providerFunc(func(context.Context, weather.Request) (weather.Series, error) {
		panic("boom")
	}), func(c *httpapi.Config) { c.Logger = slog.New(slog.NewTextHandler(&logs, nil)) })
	rec := do(h, http.MethodGet, "/v1/forecast?lat=50&lon=20&kwp=5", "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rec.Code)
	}
	decodeProblem(t, rec)
	if !strings.Contains(logs.String(), "panic") || !strings.Contains(logs.String(), "boom") {
		t.Errorf("panic not logged: %s", logs.String())
	}
}

func TestOperationalEndpoints(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	h := newServer(t, fixtureProvider(t), func(c *httpapi.Config) { c.Logger = slog.New(slog.NewJSONHandler(&logs, nil)) })
	tests := []struct {
		path, contentType, want string
	}{
		{"/healthz", "application/json", `"status":"ok"`},
		{"/version", "application/json", `"version":"v9.9.9"`},
		{"/openapi.yaml", "application/yaml", "openapi: 3.1.0"},
	}
	for _, tt := range tests {
		rec := do(h, http.MethodGet, tt.path, "", "X-Request-ID", "trace-123")
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != tt.contentType || !strings.Contains(rec.Body.String(), tt.want) {
			t.Errorf("%s: %d %q %s", tt.path, rec.Code, rec.Header().Get("Content-Type"), rec.Body)
		}
		if got := rec.Header().Get("X-Request-ID"); got != "trace-123" {
			t.Errorf("%s: X-Request-ID = %q, want the caller's", tt.path, got)
		}
	}
	if !strings.Contains(logs.String(), `"path":"/healthz"`) || !strings.Contains(logs.String(), `"request_id":"trace-123"`) {
		t.Errorf("access log incomplete: %s", logs.String())
	}
	// A zero Version falls back to build info, then "dev".
	rec := do(httpapi.New(httpapi.Config{Provider: fixtureProvider(t)}), http.MethodGet, "/version", "")
	if !strings.Contains(rec.Body.String(), `"version":`) {
		t.Errorf("default version: %s", rec.Body)
	}
}
