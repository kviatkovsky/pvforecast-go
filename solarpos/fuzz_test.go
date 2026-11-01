package solarpos_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/kviatkovsky/pvforecast-go/solarpos"
)

// FuzzPosition checks that no input makes an algorithm panic or return
// out-of-range values, and that every error wraps a documented sentinel.
func FuzzPosition(f *testing.F) {
	f.Add(int64(1782036000), 50.0647, 19.9450, 219.0, 0.0, 0.0, 0.0)
	f.Add(int64(0), 90.0, 180.0, 0.0, 101325.0, 12.0, 67.0)
	f.Add(int64(-62135596800), -90.0, -180.0, -6.5e6, 5e5, 6000.0, -8000.0)
	f.Add(int64(1e12), 0.0, 0.0, 1e9, 1.0, -273.0, 8000.0)

	f.Fuzz(func(t *testing.T, sec int64, lat, lon, alt, pressure, temp, deltaT float64) {
		ts := time.Unix(sec, 0)
		site := solarpos.Site{Lat: lat, Lon: lon, AltitudeM: alt}
		if pressure != 0 {
			site.Atmosphere = &solarpos.Atmosphere{PressurePa: pressure, TempC: temp}
		}
		for _, alg := range []solarpos.Algorithm{solarpos.SPA{DeltaT: deltaT}, solarpos.NOAA{}} {
			p, err := alg.Position(ts, site)
			if err != nil {
				if !errors.Is(err, solarpos.ErrInvalidSite) && !errors.Is(err, solarpos.ErrOutOfRange) {
					t.Fatalf("%T: undocumented error %v", alg, err)
				}
				continue
			}
			for _, v := range []float64{p.Zenith, p.ApparentZenith, p.Azimuth, p.DNIExtra} {
				if math.IsNaN(v) || math.IsInf(v, 0) {
					t.Fatalf("%T: non-finite result %+v", alg, p)
				}
			}
			if p.Zenith < 0 || p.Zenith > 180 || p.Azimuth < 0 || p.Azimuth >= 360 {
				t.Fatalf("%T: angle out of range %+v", alg, p)
			}
		}
	})
}
