package weather

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
)

// Sentinel errors shared by all providers.
var (
	// ErrInvalidRequest reports a request the provider cannot serve, either
	// rejected locally or by the upstream API (HTTP 4xx other than 429).
	ErrInvalidRequest = errors.New("weather: invalid request")
	// ErrRateLimited reports that the upstream API throttled the client
	// (HTTP 429) and retries were exhausted.
	ErrRateLimited = errors.New("weather: rate limited")
	// ErrUpstream reports a server-side or transport failure that persisted
	// after retries (HTTP 5xx, connection errors, malformed responses).
	ErrUpstream = errors.New("weather: upstream failure")
	// ErrInvalidSeries reports a Series that violates its invariants.
	ErrInvalidSeries = errors.New("weather: invalid series")
)

// Provider returns weather forecasts.
//
// Implementations must honor context cancellation and be safe for
// concurrent use.
type Provider interface {
	Forecast(ctx context.Context, req Request) (Series, error)
}

// Request asks for a forecast for one location over [Start, End).
type Request struct {
	Lat, Lon   float64   // degrees
	Start, End time.Time // instants; providers return samples labeled in [Start, End)
	// Instant requests instantaneous irradiance instead of interval means.
	Instant bool
}

// Validate reports whether the request is well formed. The returned error
// wraps ErrInvalidRequest.
func (r Request) Validate() error {
	switch {
	case !(r.Lat >= -90 && r.Lat <= 90):
		return fmt.Errorf("%w: latitude %g outside [-90, 90]", ErrInvalidRequest, r.Lat)
	case !(r.Lon >= -180 && r.Lon <= 180):
		return fmt.Errorf("%w: longitude %g outside [-180, 180]", ErrInvalidRequest, r.Lon)
	case r.Start.IsZero() || r.End.IsZero():
		return fmt.Errorf("%w: start and end are required", ErrInvalidRequest)
	case !r.End.After(r.Start):
		return fmt.Errorf("%w: end %s is not after start %s", ErrInvalidRequest, r.End.Format(time.RFC3339), r.Start.Format(time.RFC3339))
	}
	return nil
}

// Label says what instant a sample's timestamp refers to.
type Label int

// Timestamp labeling conventions.
const (
	// IntervalEnd: values are means over the interval that ends at the
	// timestamp. This is the Open-Meteo default for radiation.
	IntervalEnd Label = iota
	// IntervalStart: values are means over the interval that starts at the
	// timestamp.
	IntervalStart
	// Instant: values are instantaneous at the timestamp.
	Instant
)

// String returns the label name.
func (l Label) String() string {
	switch l {
	case IntervalEnd:
		return "interval-end"
	case IntervalStart:
		return "interval-start"
	case Instant:
		return "instant"
	}
	return fmt.Sprintf("Label(%d)", int(l))
}

// RepresentativeOffset returns the shift from a sample's timestamp to the
// instant that best represents it: the midpoint of an averaging interval,
// or the timestamp itself for instantaneous values. Solar geometry for a
// sample is evaluated at Time + RepresentativeOffset.
func (l Label) RepresentativeOffset(interval time.Duration) time.Duration {
	switch l {
	case IntervalEnd:
		return -interval / 2
	case IntervalStart:
		return interval / 2
	default:
		return 0
	}
}

// Sample is the weather at one timestamp. Irradiance is in W/m², air
// temperature in °C at 2 m and wind speed in m/s at 10 m.
type Sample struct {
	Time     time.Time
	GHI      float64 // global horizontal irradiance
	DNI      float64 // direct normal irradiance
	DHI      float64 // diffuse horizontal irradiance
	AirTempC float64
	WindMS   float64
}

// Series is a time-ordered sequence of samples with the metadata needed to
// interpret them.
type Series struct {
	Samples  []Sample
	Interval time.Duration // spacing of samples; also the averaging period
	Label    Label
	// Source names the provider and model, for display and provenance.
	Source string
}

// Validate checks that samples are strictly increasing in time, the
// interval is positive and every value is finite. The returned error wraps
// ErrInvalidSeries.
func (s Series) Validate() error {
	if s.Interval <= 0 {
		return fmt.Errorf("%w: interval %s must be positive", ErrInvalidSeries, s.Interval)
	}
	if s.Label < IntervalEnd || s.Label > Instant {
		return fmt.Errorf("%w: unknown label %d", ErrInvalidSeries, int(s.Label))
	}
	for i, smp := range s.Samples {
		if i > 0 && !smp.Time.After(s.Samples[i-1].Time) {
			return fmt.Errorf("%w: sample %d at %s is not after the previous one", ErrInvalidSeries, i, smp.Time.Format(time.RFC3339))
		}
		for _, v := range [...]float64{smp.GHI, smp.DNI, smp.DHI, smp.AirTempC, smp.WindMS} {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				return fmt.Errorf("%w: sample %d at %s has a non-finite value", ErrInvalidSeries, i, smp.Time.Format(time.RFC3339))
			}
		}
	}
	return nil
}
