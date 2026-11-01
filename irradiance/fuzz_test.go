package irradiance_test

import (
	"errors"
	"math"
	"testing"

	"github.com/kviatkovsky/pvforecast-go/irradiance"
)

// FuzzTranspose checks that no input panics, that errors wrap a documented
// sentinel, and that physically plausible inputs give finite, non-negative
// irradiance with the beam bounded by DNI.
func FuzzTranspose(f *testing.F) {
	f.Add(35.0, 180.0, 35.0, 180.0, 800.0, 700.0, 120.0, 1361.0, 0.0, 0.25, 2)
	f.Add(90.0, 0.0, 89.99, 359.0, 30.0, 5.0, 28.0, 1400.0, 0.0, 0.2, 1)
	f.Add(0.0, 0.0, 120.0, 0.0, 0.0, 0.0, 0.0, 1361.0, 0.0, 0.0, 0)
	f.Add(60.0, 90.0, 10.0, 90.0, 1100.0, 1050.0, 1.0, 1320.0, 40.0, 1.0, 2)

	f.Fuzz(func(t *testing.T, tilt, surfAz, zenith, sunAz, ghi, dni, dhi, extra, airMass, albedo float64, model int) {
		in := irradiance.Input{
			TiltDeg: tilt, SurfaceAzimuthDeg: surfAz, ZenithDeg: zenith, SolarAzimuthDeg: sunAz,
			GHI: ghi, DNI: dni, DHI: dhi, DNIExtra: extra, AirMass: airMass, Albedo: albedo,
		}
		p, err := irradiance.Transpose(irradiance.Model(model), in)
		if err != nil {
			if !errors.Is(err, irradiance.ErrUnknownModel) && !errors.Is(err, irradiance.ErrInvalidInput) {
				t.Fatalf("undocumented error: %v", err)
			}
			return
		}
		plausible := between(tilt, 0, 180) && between(surfAz, -360, 720) &&
			between(zenith, 0, 180) && between(sunAz, -360, 720) &&
			between(ghi, -50, 2000) && between(dni, -50, 1500) && between(dhi, -50, 1000) &&
			between(extra, 1300, 1450) && (airMass == 0 || between(airMass, 1, 40)) &&
			between(albedo, 0, 1)
		if !plausible {
			return
		}
		for _, v := range []float64{p.Global, p.Beam, p.Sky.Total, p.GroundDiffuse} {
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
				t.Fatalf("implausible result %+v for %+v", p, in)
			}
		}
		if p.Beam > math.Max(dni, 0)+1e-9 {
			t.Fatalf("beam %g exceeds DNI %g", p.Beam, dni)
		}
	})
}

func between(v, lo, hi float64) bool { return v >= lo && v <= hi }
