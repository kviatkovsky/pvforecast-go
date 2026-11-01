package weather_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/kviatkovsky/pvforecast-go/weather"
)

func TestRequestValidate(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		req  weather.Request
		ok   bool
	}{
		{"valid", weather.Request{Lat: 50, Lon: 20, Start: t0, End: t0.Add(time.Hour)}, true},
		{"latitude NaN", weather.Request{Lat: math.NaN(), Start: t0, End: t0.Add(time.Hour)}, false},
		{"longitude out of range", weather.Request{Lon: 181, Start: t0, End: t0.Add(time.Hour)}, false},
		{"missing start", weather.Request{End: t0}, false},
		{"end before start", weather.Request{Start: t0, End: t0.Add(-time.Hour)}, false},
		{"empty window", weather.Request{Start: t0, End: t0}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.req.Validate()
			if tt.ok != (err == nil) || (err != nil && !errors.Is(err, weather.ErrInvalidRequest)) {
				t.Errorf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}

func TestLabel(t *testing.T) {
	t.Parallel()
	tests := []struct {
		label  weather.Label
		name   string
		offset time.Duration
	}{
		{weather.IntervalEnd, "interval-end", -30 * time.Minute},
		{weather.IntervalStart, "interval-start", 30 * time.Minute},
		{weather.Instant, "instant", 0},
		{weather.Label(9), "Label(9)", 0},
	}
	for _, tt := range tests {
		if got := tt.label.String(); got != tt.name {
			t.Errorf("String() = %q, want %q", got, tt.name)
		}
		if got := tt.label.RepresentativeOffset(time.Hour); got != tt.offset {
			t.Errorf("%s: RepresentativeOffset(1h) = %v, want %v", tt.name, got, tt.offset)
		}
	}
}

func TestSeriesValidate(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	ok := []weather.Sample{{Time: t0}, {Time: t0.Add(time.Hour), GHI: 100}}
	tests := []struct {
		name string
		s    weather.Series
		ok   bool
	}{
		{"valid", weather.Series{Samples: ok, Interval: time.Hour}, true},
		{"empty is valid", weather.Series{Interval: time.Hour}, true},
		{"zero interval", weather.Series{Samples: ok}, false},
		{"unknown label", weather.Series{Samples: ok, Interval: time.Hour, Label: 7}, false},
		{"unordered", weather.Series{Samples: []weather.Sample{ok[1], ok[0]}, Interval: time.Hour}, false},
		{"duplicate time", weather.Series{Samples: []weather.Sample{ok[0], ok[0]}, Interval: time.Hour}, false},
		{"NaN value", weather.Series{Samples: []weather.Sample{{Time: t0, WindMS: math.NaN()}}, Interval: time.Hour}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.s.Validate()
			if tt.ok != (err == nil) || (err != nil && !errors.Is(err, weather.ErrInvalidSeries)) {
				t.Errorf("Validate() = %v, want ok=%v", err, tt.ok)
			}
		})
	}
}
