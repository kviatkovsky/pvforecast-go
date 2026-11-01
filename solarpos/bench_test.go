package solarpos_test

import (
	"testing"
	"time"

	"github.com/kviatkovsky/pvforecast-go/solarpos"
)

// sinkPosition prevents the compiler from optimizing the benchmarked call
// away.
var sinkPosition solarpos.Position

func BenchmarkPosition(b *testing.B) {
	start := time.Date(2026, time.January, 1, 0, 30, 0, 0, time.UTC)
	for _, a := range algorithms {
		b.Run(a.name, func(b *testing.B) {
			b.ReportAllocs()
			i := 0
			for b.Loop() {
				p, err := a.alg.Position(start.Add(time.Duration(i%8760)*time.Hour), krakow)
				if err != nil {
					b.Fatal(err)
				}
				sinkPosition = p
				i++
			}
		})
	}
}
