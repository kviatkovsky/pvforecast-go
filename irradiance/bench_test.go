package irradiance_test

import (
	"testing"

	"github.com/kviatkovsky/pvforecast-go/irradiance"
)

var sinkPOA irradiance.POA

func BenchmarkTranspose(b *testing.B) {
	in := clearNoon()
	for _, m := range models {
		b.Run(m.String(), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				p, err := irradiance.Transpose(m, in)
				if err != nil {
					b.Fatal(err)
				}
				sinkPOA = p
			}
		})
	}
}
