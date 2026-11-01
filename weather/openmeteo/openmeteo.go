package openmeteo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/kviatkovsky/pvforecast-go/weather"
)

// DefaultBaseURL is the Open-Meteo forecast endpoint.
const DefaultBaseURL = "https://api.open-meteo.com/v1/forecast"

// MaxForecastDays is the longest horizon the forecast endpoint serves.
const MaxForecastDays = 16

const (
	defaultMaxRetries = 3
	defaultBackoff    = 500 * time.Millisecond
	maxBackoff        = 10 * time.Second
	maxResponseBytes  = 8 << 20
	timeLayout        = "2006-01-02T15:04"
)

// Hourly variables requested from the API, averaged over the preceding hour
// unless the _instant variants are used.
var (
	averagedVars = [...]string{"shortwave_radiation", "direct_normal_irradiance", "diffuse_radiation"}
	instantVars  = [...]string{"shortwave_radiation_instant", "direct_normal_irradiance_instant", "diffuse_radiation_instant"}
	weatherVars  = [...]string{"temperature_2m", "wind_speed_10m"}
)

// Units the client expects; checking them guards against silent changes in
// the API or a wrong wind_speed_unit.
const (
	unitIrradiance  = "W/m²"
	unitTemperature = "°C"
	unitWind        = "m/s"
)

// Client is a weather.Provider backed by the Open-Meteo forecast API. It
// is safe for concurrent use.
type Client struct {
	baseURL    string
	httpClient *http.Client
	maxRetries int
	backoff    time.Duration
	now        func() time.Time
}

var _ weather.Provider = (*Client)(nil)

// Option configures a Client.
type Option func(*Client)

// WithHTTPClient sets the HTTP client; nil keeps the default, a client with
// a 30 s timeout. Set per-call deadlines through the context.
func WithHTTPClient(c *http.Client) Option {
	return func(cl *Client) {
		if c != nil {
			cl.httpClient = c
		}
	}
}

// WithBaseURL overrides the endpoint, for example to use a self-hosted
// instance or a test server.
func WithBaseURL(u string) Option { return func(cl *Client) { cl.baseURL = u } }

// WithRetry sets how many times a request is retried after a 5xx, 429 or
// transport error, and the initial backoff, which doubles on each attempt
// (with jitter, capped at 10 s). A Retry-After header takes precedence.
func WithRetry(maxRetries int, initialBackoff time.Duration) Option {
	return func(cl *Client) {
		cl.maxRetries = max(0, maxRetries)
		cl.backoff = initialBackoff
	}
}

// WithClock sets the clock used to compute the forecast horizon.
func WithClock(now func() time.Time) Option { return func(cl *Client) { cl.now = now } }

// New returns a Client with the given options.
func New(opts ...Option) *Client {
	c := &Client{
		baseURL:    DefaultBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		maxRetries: defaultMaxRetries,
		backoff:    defaultBackoff,
		now:        time.Now,
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

// APIError is an error response from the API. It wraps the matching
// weather sentinel, so callers can use errors.Is(err, weather.ErrRateLimited)
// or errors.As(err, &apiErr) for details.
type APIError struct {
	StatusCode int
	Reason     string // the API's "reason" field, if any
}

// Error implements error.
func (e *APIError) Error() string {
	if e.Reason != "" {
		return fmt.Sprintf("open-meteo: HTTP %d: %s", e.StatusCode, e.Reason)
	}
	return fmt.Sprintf("open-meteo: HTTP %d", e.StatusCode)
}

// Unwrap maps the status code to a weather sentinel error.
func (e *APIError) Unwrap() error {
	switch {
	case e.StatusCode == http.StatusTooManyRequests:
		return weather.ErrRateLimited
	case e.StatusCode >= 500:
		return weather.ErrUpstream
	default:
		return weather.ErrInvalidRequest
	}
}

func (e *APIError) retryable() bool {
	return e.StatusCode == http.StatusTooManyRequests || e.StatusCode >= 500
}

// Forecast implements weather.Provider. It returns hourly samples labeled
// in [req.Start, req.End). Averaged values are labeled at the end of their
// hour (weather.IntervalEnd); with req.Instant they are instantaneous.
// Hours for which the API has no data (null) are omitted.
func (c *Client) Forecast(ctx context.Context, req weather.Request) (weather.Series, error) {
	if err := req.Validate(); err != nil {
		return weather.Series{}, err
	}
	days, err := c.forecastDays(req.End)
	if err != nil {
		return weather.Series{}, err
	}
	u, err := c.url(req, days)
	if err != nil {
		return weather.Series{}, err
	}
	body, err := c.getWithRetry(ctx, u)
	if err != nil {
		return weather.Series{}, err
	}
	return decode(body, req)
}

// forecastDays returns the forecast_days value that covers end, counted in
// UTC calendar days from today.
func (c *Client) forecastDays(end time.Time) (int, error) {
	today := c.now().UTC().Truncate(24 * time.Hour)
	days := int(math.Ceil(end.Sub(today).Hours() / 24))
	if days > MaxForecastDays {
		return 0, fmt.Errorf("%w: end %s is beyond the %d-day forecast horizon",
			weather.ErrInvalidRequest, end.UTC().Format(time.RFC3339), MaxForecastDays)
	}
	return max(days, 1), nil
}

func (c *Client) url(req weather.Request, days int) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("open-meteo: base URL: %w", err)
	}
	vars := averagedVars[:]
	if req.Instant {
		vars = instantVars[:]
	}
	hourly := make([]string, 0, len(vars)+len(weatherVars))
	hourly = append(hourly, vars...)
	hourly = append(hourly, weatherVars[:]...)

	q := base.Query()
	q.Set("latitude", strconv.FormatFloat(req.Lat, 'f', -1, 64))
	q.Set("longitude", strconv.FormatFloat(req.Lon, 'f', -1, 64))
	q.Set("hourly", strings.Join(hourly, ","))
	q.Set("wind_speed_unit", "ms")
	q.Set("timezone", "UTC")
	q.Set("forecast_days", strconv.Itoa(days))
	base.RawQuery = q.Encode()
	return base.String(), nil
}

func (c *Client) getWithRetry(ctx context.Context, u string) ([]byte, error) {
	for attempt := 0; ; attempt++ {
		body, retryAfter, err := c.get(ctx, u)
		if err == nil {
			return body, nil
		}
		if ctx.Err() != nil || attempt >= c.maxRetries || !isRetryable(err) {
			return nil, err
		}
		wait := retryAfter
		if wait <= 0 {
			wait = c.backoffFor(attempt)
		}
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, fmt.Errorf("open-meteo: waiting to retry: %w", ctx.Err())
		case <-t.C:
		}
	}
}

// backoffFor returns the exponential backoff for an attempt with "equal
// jitter": half fixed, half random, so concurrent clients spread out.
func (c *Client) backoffFor(attempt int) time.Duration {
	if c.backoff <= 0 {
		return 0
	}
	// Double until the cap; shifting blindly would overflow after ~35
	// attempts and turn the wait into zero.
	d := c.backoff
	for range attempt {
		if d >= maxBackoff/2 {
			d = maxBackoff
			break
		}
		d *= 2
	}
	d = min(d, maxBackoff)
	half := d / 2
	return half + rand.N(half+1) //nolint:gosec // jitter does not need a CSPRNG
}

func isRetryable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.retryable()
	}
	// Transport errors and truncated bodies are worth another try.
	return errors.Is(err, weather.ErrUpstream)
}

// get performs one request. On a retryable API error it also returns the
// Retry-After delay, if the server sent one.
func (c *Client) get(ctx context.Context, u string) (body []byte, retryAfter time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return nil, 0, fmt.Errorf("open-meteo: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, fmt.Errorf("open-meteo: %w", ctx.Err())
		}
		return nil, 0, fmt.Errorf("%w: open-meteo: %w", weather.ErrUpstream, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err = io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, 0, fmt.Errorf("%w: open-meteo: read body: %w", weather.ErrUpstream, err)
	}
	if resp.StatusCode != http.StatusOK {
		apiErr := &APIError{StatusCode: resp.StatusCode}
		var e struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(body, &e) == nil {
			apiErr.Reason = e.Reason
		}
		return nil, parseRetryAfter(resp.Header.Get("Retry-After")), apiErr
	}
	return body, 0, nil
}

// parseRetryAfter supports the delay-seconds form of Retry-After, capped
// at maxBackoff.
func parseRetryAfter(v string) time.Duration {
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return 0
	}
	return min(time.Duration(secs)*time.Second, maxBackoff)
}

// response is the subset of the forecast response this client reads.
// Hourly columns are decoded individually because their values can be null
// where the API has no data.
type response struct {
	UTCOffsetSeconds int                        `json:"utc_offset_seconds"`
	HourlyUnits      map[string]string          `json:"hourly_units"`
	Hourly           map[string]json.RawMessage `json:"hourly"`
}

func decode(body []byte, req weather.Request) (weather.Series, error) {
	var raw response
	if err := json.Unmarshal(body, &raw); err != nil {
		return weather.Series{}, fmt.Errorf("%w: open-meteo: decode response: %w", weather.ErrUpstream, err)
	}
	if raw.UTCOffsetSeconds != 0 {
		return weather.Series{}, fmt.Errorf("%w: open-meteo: expected UTC timestamps, got offset %d s", weather.ErrUpstream, raw.UTCOffsetSeconds)
	}

	var times []string
	if err := decodeField(raw.Hourly, "time", &times); err != nil {
		return weather.Series{}, err
	}
	radVars := averagedVars
	label := weather.IntervalEnd
	if req.Instant {
		radVars, label = instantVars, weather.Instant
	}
	var cols [5][]*float64
	names := [5]string{radVars[0], radVars[1], radVars[2], weatherVars[0], weatherVars[1]}
	units := [5]string{unitIrradiance, unitIrradiance, unitIrradiance, unitTemperature, unitWind}
	for i, name := range names {
		if got := raw.HourlyUnits[name]; got != units[i] {
			return weather.Series{}, fmt.Errorf("%w: open-meteo: %s in %q, want %q", weather.ErrUpstream, name, got, units[i])
		}
		if err := decodeField(raw.Hourly, name, &cols[i]); err != nil {
			return weather.Series{}, err
		}
		if len(cols[i]) != len(times) {
			return weather.Series{}, fmt.Errorf("%w: open-meteo: %s has %d values for %d timestamps", weather.ErrUpstream, name, len(cols[i]), len(times))
		}
	}

	samples := make([]weather.Sample, 0, len(times))
	for i, ts := range times {
		t, err := time.ParseInLocation(timeLayout, ts, time.UTC)
		if err != nil {
			return weather.Series{}, fmt.Errorf("%w: open-meteo: timestamp %q: %w", weather.ErrUpstream, ts, err)
		}
		if t.Before(req.Start) || !t.Before(req.End) {
			continue
		}
		var v [5]float64
		complete := true
		for j := range cols {
			if cols[j][i] == nil {
				complete = false
				break
			}
			v[j] = *cols[j][i]
		}
		if !complete {
			continue
		}
		samples = append(samples, weather.Sample{
			Time: t, GHI: v[0], DNI: v[1], DHI: v[2], AirTempC: v[3], WindMS: v[4],
		})
	}
	return weather.Series{
		Samples:  samples,
		Interval: time.Hour,
		Label:    label,
		Source:   "open-meteo",
	}, nil
}

func decodeField(hourly map[string]json.RawMessage, name string, dst any) error {
	msg, ok := hourly[name]
	if !ok {
		return fmt.Errorf("%w: open-meteo: response has no hourly %q", weather.ErrUpstream, name)
	}
	if err := json.Unmarshal(msg, dst); err != nil {
		return fmt.Errorf("%w: open-meteo: decode %q: %w", weather.ErrUpstream, name, err)
	}
	return nil
}
