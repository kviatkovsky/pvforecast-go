package forecast_test

import (
	"testing"
	"time"

	"github.com/kviatkovsky/pvforecast-go/forecast"
	"github.com/kviatkovsky/pvforecast-go/solarpos"
	"github.com/kviatkovsky/pvforecast-go/weather"
)

// yearSeries tiles the first fixture day over a year: 8760 hourly samples.
func yearSeries(tb testing.TB) weather.Series {
	tb.Helper()
	day := fixtureSeries(tb).Samples[:24]
	s := weather.Series{Interval: time.Hour, Label: weather.IntervalEnd}
	start := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	for d := range 365 {
		for h, smp := range day {
			smp.Time = start.Add(time.Duration(d*24+h+1) * time.Hour)
			s.Samples = append(s.Samples, smp)
		}
	}
	return s
}

// BenchmarkComputeYear runs a full year of hourly data through Compute.
// Allocations are per call (the result slices), not per sample.
func BenchmarkComputeYear(b *testing.B) {
	series := yearSeries(b)
	cases := []struct {
		name string
		sys  forecast.System
		opt  forecast.Options
	}{
		{"perez-spa-1array", southSystem(), forecast.DefaultOptions()},
		{"perez-spa-2arrays", eastWestSystem(), forecast.DefaultOptions()},
		{"perez-noaa-1array", southSystem(), forecast.Options{Transposition: forecast.DefaultOptions().Transposition, SolarPos: solarpos.NOAA{}}},
	}
	for i := range cases {
		tt := &cases[i]
		b.Run(tt.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := forecast.Compute(tt.sys, series, tt.opt); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(series.Samples)), "ns/sample")
		})
	}
}
